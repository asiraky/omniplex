package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/procgroup"
)

// This file runs the sign-ins of command-line tools that keep their own
// credentials (one config folder per account). Omniplex never sees those
// credentials: it runs the tool's status command to learn whether an account
// is signed in, and drives its sign-in command so the browser step can happen
// on a phone, by relaying the address the browser lands on back to the
// tool's localhost listener.

// Account statuses.
const (
	AccountSignedIn  = "signed_in"
	AccountSignedOut = "signed_out"
	AccountFailed    = "failed"
)

// Tunables; tests shorten them.
var (
	cliStatusTimeout  = 20 * time.Second
	cliPrepareTimeout = 2 * time.Minute
	// cliURLWait bounds how long the sign-in command may take to print the
	// address to open.
	cliURLWait = time.Minute
	// cliExitWait bounds how long the sign-in command may take to finish
	// once its localhost listener has been answered.
	cliExitWait = 2 * time.Minute
	// cliWaitDelay is how long a cancelled command's pipes may stay open
	// (held by a grandchild) before Wait gives up on them.
	cliWaitDelay = 2 * time.Second
)

const cliOutputCap = 64 << 10

// CheckAccount runs the CLI's status command for one account. Status is
// signed_in, signed_out or failed; detail is one scrubbed line of the
// command's output when the account is not signed in.
func CheckAccount(ctx context.Context, cli CLI, account Account) (status, detail string) {
	if strings.TrimSpace(cli.StatusCommand) == "" {
		return AccountFailed, "no status command"
	}
	var pattern *regexp.Regexp
	if cli.SignedInPattern != "" {
		re, err := regexp.Compile(cli.SignedInPattern)
		if err != nil {
			return AccountFailed, "the signed-in pattern is not a valid regular expression"
		}
		pattern = re
	}
	out, code, err := runCaptured(ctx, cli.StatusCommand, accountEnv(account), cliStatusTimeout, "cli-"+cli.ID+"-status")
	if err != nil {
		return AccountFailed, "the status check " + runFailure(err)
	}
	if code == 126 || code == 127 {
		return AccountFailed, orDefault(detailLine(out), "the status command was not found")
	}
	if pattern != nil {
		if pattern.MatchString(out) {
			return AccountSignedIn, ""
		}
		return AccountSignedOut, ""
	}
	if code == 0 {
		return AccountSignedIn, ""
	}
	// Signed out is an answer, not a failure: what the command printed
	// saying so is no use to anyone.
	return AccountSignedOut, ""
}

// SignInAccount signs one account in: it runs the prepare command, starts
// the sign-in command, narrates the first https address it prints, takes the
// address the browser ended up on (pasted, since the browser may be on
// another device) and replays it to the command's localhost listener, then
// checks the account's status. It returns nil only when the account ends up
// signed in.
func SignInAccount(ctx context.Context, ia adapter.AuthInteraction, cli CLI, account Account) error {
	if strings.TrimSpace(cli.SignInCommand) == "" {
		return fmt.Errorf("%s has no sign-in command", cli.Name)
	}
	env := accountEnv(account)
	if strings.TrimSpace(cli.PrepareCommand) != "" {
		out, code, err := runCaptured(ctx, cli.PrepareCommand, env, cliPrepareTimeout, "cli-"+cli.ID+"-prepare")
		if err != nil {
			return errors.New("the prepare command " + runFailure(err))
		}
		if code != 0 {
			return fmt.Errorf("the prepare command failed (exit %d)%s", code, suffix(detailLine(out)))
		}
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, tree := shellCommand(cctx, cli.SignInCommand, env, "cli-"+cli.ID+"-signin")
	defer tree.Kill()
	w := &urlWatcher{found: make(chan string, 1)}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		return errors.New("the sign-in command couldn't start")
	}
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		w.finish()
		exited <- err
	}()

	var authURL string
	var exitErr error
	hasExited := false
	urlTimer := time.NewTimer(cliURLWait)
	defer urlTimer.Stop()
	select {
	case authURL = <-w.found:
	case exitErr = <-exited:
		hasExited = true
		select {
		case authURL = <-w.found:
		default:
			return fmt.Errorf("the sign-in command ended without an address to open%s", suffix(detailLine(w.output())))
		}
	case <-urlTimer.C:
		return errors.New("the sign-in command printed no address to open")
	case <-ctx.Done():
		return ctx.Err()
	}

	port := loopbackRedirectPort(authURL)
	ia.Notify(adapter.AuthEvent{
		Type:    adapter.AuthEventURL,
		URL:     authURL,
		Message: "Open the link to sign in " + account.Name + ".",
	})

	if !hasExited {
		var err error
		exitErr, err = relayBrowser(ctx, ia, port, exited)
		if err != nil {
			return err
		}
	}

	status, detail := CheckAccount(ctx, cli, account)
	if status == AccountSignedIn {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(exitErr, &ee) {
		return fmt.Errorf("the sign-in command failed (exit %d)%s", ee.ExitCode(), suffix(orDefault(detail, detailLine(w.output()))))
	}
	return fmt.Errorf("still not signed in%s", suffix(detail))
}

// relayBrowser waits for either the command to finish on its own (the
// browser reached its listener directly) or a pasted address, which it
// replays to the listener before waiting for the command to finish. It
// returns the command's exit error.
func relayBrowser(ctx context.Context, ia adapter.AuthInteraction, port int, exited <-chan error) (exitErr, err error) {
	prompt := adapter.AuthPrompt{Message: "Paste the address your browser ended up on", Placeholder: "http://localhost/..."}
	if port != 0 {
		prompt.Placeholder = "http://localhost:" + strconv.Itoa(port) + "/..."
	}
	for {
		pctx, cancel := context.WithCancel(ctx)
		answers := make(chan promptAnswer, 1)
		go func() {
			v, err := ia.Prompt(pctx, prompt)
			answers <- promptAnswer{v, err}
		}()
		select {
		case err := <-exited:
			cancel()
			return err, nil
		case <-ctx.Done():
			cancel()
			return nil, ctx.Err()
		case a := <-answers:
			cancel()
			if a.err != nil {
				return nil, a.err
			}
			u, problem := pastedLoopbackURL(a.value, port)
			if problem != "" {
				ia.Notify(adapter.AuthEvent{Type: adapter.AuthEventInfo, Message: problem})
				continue
			}
			if err := replay(ctx, u); err != nil {
				return nil, err
			}
			t := time.NewTimer(cliExitWait)
			defer t.Stop()
			select {
			case err := <-exited:
				return err, nil
			case <-t.C:
				return nil, errors.New("the sign-in command did not finish")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
}

// pastedLoopbackURL accepts only an http address on this machine and, when
// the sign-in named one, the port the command listens on. problem says what
// is wrong with anything else, for the person to read.
func pastedLoopbackURL(s string, port int) (u *url.URL, problem string) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "http" || u.User != nil || u.Host == "" {
		return nil, "Paste the whole address, starting with http://localhost."
	}
	host := u.Hostname()
	if !strings.EqualFold(host, "localhost") && host != "127.0.0.1" && host != "::1" {
		return nil, "That address isn't on localhost."
	}
	if port != 0 && u.Port() != strconv.Itoa(port) {
		return nil, "That address is for a different sign-in."
	}
	u.Fragment = ""
	return u, ""
}

// replay requests the pasted address on this machine, as the browser would
// have, without following any redirect it answers with.
func replay(ctx context.Context, u *url.URL) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("invalid address")
	}
	resp, err := client.Do(req)
	if err != nil {
		// The URL carries the code; report only the transport failure.
		return fmt.Errorf("couldn't reach the sign-in command: %v", netErr(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, cliOutputCap))
	resp.Body.Close()
	return nil
}

// loopbackRedirectPort is the port of the auth URL's redirect_uri when that
// points at this machine, else 0.
func loopbackRedirectPort(authURL string) int {
	u, err := url.Parse(authURL)
	if err != nil {
		return 0
	}
	ru, err := url.Parse(u.Query().Get("redirect_uri"))
	if err != nil {
		return 0
	}
	host := ru.Hostname()
	if !strings.EqualFold(host, "localhost") && host != "127.0.0.1" && host != "::1" {
		return 0
	}
	p, err := strconv.Atoi(ru.Port())
	if err != nil || p <= 0 || p > 65535 {
		return 0
	}
	return p
}

// ---- Running commands ----

var homeRef = regexp.MustCompile(`\$\{HOME\}|\$HOME\b`)

// accountEnv is the server's environment with the account's env on top,
// "~" and $HOME in the account's values expanded to the server's home.
func accountEnv(account Account) []string {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	env := os.Environ()
	keys := make([]string, 0, len(account.Env))
	for k := range account.Env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v := account.Env[k]
		if v == "~" {
			v = home
		} else if strings.HasPrefix(v, "~/") {
			v = home + v[1:]
		}
		v = homeRef.ReplaceAllLiteralString(v, home)
		// exec keeps the last value of a duplicated key.
		env = append(env, k+"="+v)
	}
	return env
}

// shellCommand prepares `sh -c script` in its own process tree, killed as a
// whole when ctx ends.
func shellCommand(ctx context.Context, script string, env []string, name string) (*exec.Cmd, procgroup.Group) {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Env = env
	tree := procgroup.Attach(cmd, name)
	cmd.Cancel = func() error {
		tree.Kill()
		return nil
	}
	cmd.WaitDelay = cliWaitDelay
	return cmd, tree
}

// runCaptured runs a script to completion under a timeout and returns its
// combined output (capped) and exit code. err is set only when the command
// could not run or was stopped (ctx's error then).
func runCaptured(ctx context.Context, script string, env []string, timeout time.Duration, name string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd, tree := shellCommand(ctx, script, env, name)
	defer tree.Kill()
	var out cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), -1, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return out.String(), -1, err
	}
	return out.String(), 0, nil
}

func runFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "was cancelled"
	}
	return "couldn't run"
}

type cappedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := cliOutputCap - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// urlWatcher collects a command's output and reports the first https
// address in it, once the address is complete (followed by anything, or the
// end of output).
type urlWatcher struct {
	cappedBuffer
	sent  bool
	found chan string
}

var firstHTTPS = regexp.MustCompile(`https://[^\s"'<>\x1b]+`)

func (w *urlWatcher) Write(p []byte) (int, error) {
	n, _ := w.cappedBuffer.Write(p)
	w.scan(false)
	return n, nil
}

func (w *urlWatcher) finish() { w.scan(true) }

func (w *urlWatcher) scan(final bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sent {
		return
	}
	loc := firstHTTPS.FindIndex(w.buf)
	if loc == nil || (!final && loc[1] == len(w.buf)) {
		return
	}
	w.sent = true
	w.found <- strings.TrimRight(string(w.buf[loc[0]:loc[1]]), ".,;:")
}

func (w *urlWatcher) output() string { return w.String() }

// ---- Detail lines ----

var (
	ansiEscape  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	urlInText   = regexp.MustCompile(`https?://[^\s"'<>]+`)
	secretPair  = regexp.MustCompile(`(?i)\b([a-z_-]*(?:token|secret|password|passwd|key)[a-z_-]*)("?\s*[=:]\s*)("[^"]*"|\S+)`)
	bearerValue = regexp.MustCompile(`(?i)\b(bearer|basic)\s+\S+`)
	longRun     = regexp.MustCompile(`[A-Za-z0-9_+=-]{24,}`)
	hasDigit    = regexp.MustCompile(`[0-9]`)
	hasLetter   = regexp.MustCompile(`[A-Za-z]`)
)

// detailLine picks the last non-empty line of output and scrubs anything
// that looks like a credential out of it.
func detailLine(out string) string {
	out = ansiEscape.ReplaceAllString(out, "")
	lines := strings.Split(strings.ReplaceAll(out, "\r", "\n"), "\n")
	line := ""
	for i := len(lines) - 1; i >= 0; i-- {
		// Skip lines with nothing to read, like the closing brace of JSON.
		if l := strings.TrimSpace(lines[i]); hasLetter.MatchString(l) {
			line = l
			break
		}
	}
	line = scrubTokens(line)
	if utf8.RuneCountInString(line) > 160 {
		r := []rune(line)
		line = string(r[:157]) + "..."
	}
	return line
}

// scrubTokens removes token-like text: URL queries, key=value credentials,
// bearer values and long opaque strings.
func scrubTokens(s string) string {
	s = urlInText.ReplaceAllStringFunc(s, func(u string) string {
		if i := strings.IndexAny(u, "?#"); i >= 0 {
			return u[:i]
		}
		return u
	})
	s = secretPair.ReplaceAllString(s, "${1}${2}[redacted]")
	s = bearerValue.ReplaceAllString(s, "${1} [redacted]")
	s = longRun.ReplaceAllStringFunc(s, func(m string) string {
		if hasDigit.MatchString(m) && hasLetter.MatchString(m) {
			return "[redacted]"
		}
		return m
	})
	return s
}

func suffix(detail string) string {
	if detail == "" {
		return ""
	}
	return ": " + detail
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
