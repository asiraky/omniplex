package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CloneTimeout bounds a clone. A big repo on a slow line is normal; a clone
// that has sat there for ten minutes is a prompt nobody can answer, and the
// websocket call it is blocking deserves an ending.
const CloneTimeout = 10 * time.Minute

// logLimit bounds how much git output reaches the server log. git can be
// chatty on failure and the log is not the place to paste a whole transfer.
const logLimit = 4000

// shorthand is the strict `owner/repo` form. No scheme, no spaces, no extra
// path segments: anything richer than this is a URL and passes through
// untouched, so we never rewrite something the operator typed deliberately.
var shorthand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// GitHubProtocol is the protocol gh is configured to use for git operations
// on github.com: "ssh", "https", or "" when gh is missing or says nothing.
func GitHubProtocol(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "config", "get", "git_protocol", "-h", "github.com").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// NormalizeRemote turns what an operator pasted into something git can clone.
// The only rewrite is the GitHub shorthand, expanded over SSH when protocol
// (gh's git_protocol) is "ssh" and over HTTPS otherwise. Everything else:
// https://, git://, ssh://, scp-style git@host:owner/repo.git, and local
// paths and file:// URLs, are git's business, not ours, and is passed
// through unchanged.
func NormalizeRemote(input, protocol string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", errors.New("enter a repository URL")
	}
	// A leading dash would be read by git as an option rather than a remote.
	if strings.HasPrefix(s, "-") {
		return "", errors.New("that does not look like a repository URL")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("that does not look like a repository URL")
		}
	}
	if !strings.Contains(s, "://") && shorthand.MatchString(s) {
		owner, repo, _ := strings.Cut(s, "/")
		path := owner + "/" + strings.TrimSuffix(repo, ".git") + ".git"
		if protocol == "ssh" {
			return "git@github.com:" + path, nil
		}
		return "https://github.com/" + path, nil
	}
	return s, nil
}

// githubHTTPS is an HTTPS github.com remote without credentials in it.
var githubHTTPS = regexp.MustCompile(`^https://(?:www\.)?github\.com/([A-Za-z0-9][A-Za-z0-9_.-]*)/([A-Za-z0-9][A-Za-z0-9_.-]*?)(?:\.git)?/?$`)

// sshAlternative is the SSH form of an HTTPS github.com remote, or "" when
// url is anything else.
func sshAlternative(url string) string {
	m := githubHTTPS.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return ""
	}
	return "git@github.com:" + m[1] + "/" + m[2] + ".git"
}

// DirectoryName reports the folder `git clone <url>` would create, or "" when
// the URL does not name one (it stops at the host, say). The UI uses it to
// prefill a destination, so a wrong guess is worse than no guess.
func DirectoryName(url string) string {
	s := strings.TrimSpace(url)
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	var path string
	switch i := strings.Index(s, "://"); {
	case i >= 0:
		// scheme://[user@]host[:port]/path — the path starts at the first
		// slash after the authority, so a URL with no slash names no folder.
		rest := s[i+3:]
		j := strings.Index(rest, "/")
		if j < 0 {
			return ""
		}
		path = rest[j+1:]
	default:
		// scp-style host:path, but only when the colon precedes any slash;
		// otherwise it is an ordinary local path.
		if c := strings.Index(s, ":"); c >= 0 && !strings.Contains(s[:c], "/") {
			path = s[c+1:]
		} else {
			path = s
		}
	}
	path = strings.TrimRight(path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		path = path[i+1:]
	}
	path = strings.TrimSuffix(path, ".git")
	if path == "" || path == "." || path == ".." {
		return ""
	}
	return path
}

// Clone runs `git clone url dest` non-interactively into dest, an absolute
// path that is missing or empty. The returned error is always a message
// written for the operator: git's stderr can carry a token embedded in the
// remote URL, so it goes to logf and never to the client.
func Clone(ctx context.Context, url, dest string, logf func(string, ...any)) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("enter a repository URL")
	}
	if !filepath.IsAbs(dest) {
		return fmt.Errorf("clone destination %q is not absolute", dest)
	}
	abs := filepath.Clean(dest)
	// An empty directory is a fine target — that is what a UI that just made
	// the folder hands us — but anything with content in it is not ours.
	created := false
	switch st, err := os.Stat(abs); {
	case err == nil && !st.IsDir():
		return errors.New("destination already exists and is not a directory")
	case err == nil:
		entries, err := os.ReadDir(abs)
		if err != nil {
			return fmt.Errorf("destination is not readable: %s", abs)
		}
		if len(entries) > 0 {
			return errors.New("destination already exists and is not empty")
		}
	case os.IsNotExist(err):
		created = true
	default:
		return err
	}

	parent, leaf := filepath.Dir(abs), filepath.Base(abs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("could not create %s", parent)
	}

	ctx, cancel := context.WithTimeout(ctx, CloneTimeout)
	defer cancel()
	// The leaf goes as a bare argument with cmd.Dir set to the parent, so a
	// destination that looks like an option or a URL cannot change the command.
	cmd := exec.CommandContext(ctx, "git", "clone", "--", url, leaf)
	cmd.Dir = parent
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"SSH_ASKPASS_REQUIRE=never",
		"GCM_INTERACTIVE=never",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}

	// Undo a directory the clone itself made. A directory the operator pointed
	// us at is left alone even when empty: it was not ours to remove.
	if created {
		os.RemoveAll(abs)
	}
	raw := out.String()
	if logf != nil {
		logf("clone %s failed: %v: %s", leaf, runErr, truncate(raw, logLimit))
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("clone timed out after %s", CloneTimeout)
	}
	if ctx.Err() != nil {
		return errors.New("clone cancelled")
	}
	return classify(url, raw)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// classify turns git's stderr into something worth showing. It deliberately
// returns a fixed message per case rather than any part of the raw output;
// the only thing it may name is an SSH form of an HTTPS GitHub url, which
// carries nothing the operator did not type.
func classify(url, raw string) error {
	low := strings.ToLower(raw)
	has := func(needles ...string) bool {
		for _, n := range needles {
			if strings.Contains(low, strings.ToLower(n)) {
				return true
			}
		}
		return false
	}
	switch {
	case has("authentication failed", "could not read Username", "could not read Password",
		"Permission denied (publickey)", "terminal prompts disabled", "Authentication failed"):
		if alt := sshAlternative(url); alt != "" {
			return fmt.Errorf("could not sign in to github.com over HTTPS. Try %s, or run gh auth setup-git on this machine", alt)
		}
		return errors.New("could not sign in to that remote. Set up git credentials for it on this machine (gh auth setup-git does it for GitHub), or use an SSH URL with a key git already has")
	case has("not found", "does not exist", "Repository not found", "repository does not exist"):
		return errors.New("repository not found. Check the URL, and that this machine's git credentials can see it")
	}
	return errors.New("git clone failed. The server log has the details")
}
