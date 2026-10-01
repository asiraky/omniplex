package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// cliHome points HOME at a temp dir so "~" in account env lands there.
func cliHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestCheckAccountExitStatus(t *testing.T) {
	home := cliHome(t)
	cli := CLI{ID: "gws", StatusCommand: `test -f "$GWS_DIR/creds"`}
	acct := Account{Name: "work", Env: map[string]string{"GWS_DIR": "~/gws-work"}}

	if status, _ := CheckAccount(context.Background(), cli, acct); status != AccountSignedOut {
		t.Fatalf("no creds: status %q", status)
	}
	os.MkdirAll(filepath.Join(home, "gws-work"), 0o700)
	os.WriteFile(filepath.Join(home, "gws-work", "creds"), nil, 0o600)
	if status, detail := CheckAccount(context.Background(), cli, acct); status != AccountSignedIn {
		t.Fatalf("with creds: status %q (%s)", status, detail)
	}
}

func TestCheckAccountPattern(t *testing.T) {
	home := cliHome(t)
	cli := CLI{
		ID:              "gws",
		StatusCommand:   `echo "dir=$GWS_DIR"; echo "$STATE"; exit 3`,
		SignedInPattern: `(?m)^signed in as \S+@`,
	}
	acct := Account{Name: "a", Env: map[string]string{"GWS_DIR": "$HOME/x", "STATE": "signed in as me@example.com"}}
	// The pattern decides, whatever the exit status.
	if status, detail := CheckAccount(context.Background(), cli, acct); status != AccountSignedIn {
		t.Fatalf("status %q (%s)", status, detail)
	}

	acct.Env["STATE"] = "not signed in: refresh_token=1//0gAbCdEfGhIjKlMnOpQrStUvWxYz0123 rejected"
	status, detail := CheckAccount(context.Background(), cli, acct)
	if status != AccountSignedOut {
		t.Fatalf("status %q", status)
	}
	if strings.Contains(detail, "0gAbCdEf") || strings.Contains(detail, "\n") {
		t.Errorf("detail not scrubbed to one line: %q", detail)
	}

	// $HOME in an env value is the server's home.
	cli.StatusCommand = `echo "$GWS_DIR"`
	cli.SignedInPattern = "^" + home + "/x\n$"
	if status, _ := CheckAccount(context.Background(), cli, acct); status != AccountSignedIn {
		t.Errorf("$HOME not expanded: %q", status)
	}
}

func TestCheckAccountFailures(t *testing.T) {
	cliHome(t)
	acct := Account{Name: "a"}
	cases := map[string]CLI{
		"missing command": {ID: "x", StatusCommand: "omniplex-no-such-command-xyz"},
		"empty command":   {ID: "x"},
		"bad pattern":     {ID: "x", StatusCommand: "true", SignedInPattern: "("},
	}
	for name, cli := range cases {
		if status, _ := CheckAccount(context.Background(), cli, acct); status != AccountFailed {
			t.Errorf("%s: status %q", name, status)
		}
	}
}

// waitDead polls until pid is gone.
func waitDead(t *testing.T, pidFile string) {
	t.Helper()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(pidFile)
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			pid = p
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no pid written")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("background process %d outlived its command", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCheckAccountTimeoutKillsTree(t *testing.T) {
	cliHome(t)
	old := cliStatusTimeout
	cliStatusTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cliStatusTimeout = old })

	pidFile := filepath.Join(t.TempDir(), "pid")
	cli := CLI{ID: "slow", StatusCommand: `sleep 30 & echo $! > "$PIDFILE"; wait`}
	start := time.Now()
	status, _ := CheckAccount(context.Background(), cli, Account{Name: "a", Env: map[string]string{"PIDFILE": pidFile}})
	if status != AccountFailed {
		t.Fatalf("status %q", status)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("timeout took %v", time.Since(start))
	}
	waitDead(t, pidFile)
}

// cliListener stands in for the sign-in command's localhost listener: when
// the browser's address is replayed to it, it drops a marker file the fake
// command waits for.
type cliListener struct {
	srv      *httptest.Server
	port     int
	mark     string
	mu       sync.Mutex
	got      url.Values
	followed bool
}

func newCLIListener(t *testing.T) *cliListener {
	l := &cliListener{mark: filepath.Join(t.TempDir(), "mark")}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if r.URL.Path == "/followed" {
			l.followed = true
			return
		}
		l.got = r.URL.Query()
		os.WriteFile(l.mark, nil, 0o600)
		// A redirect the relay must not follow.
		http.Redirect(w, r, "/followed", http.StatusFound)
	}))
	t.Cleanup(l.srv.Close)
	u, _ := url.Parse(l.srv.URL)
	l.port, _ = strconv.Atoi(u.Port())
	return l
}

// fakeSignIn prints an auth URL whose redirect_uri names the listener's
// port, waits for the marker, then writes the account's credentials when
// WRITE is set.
const fakeSignIn = `echo "Open this URL in your browser:"
echo "https://accounts.example/o/oauth2/auth?client_id=c&redirect_uri=http%3A%2F%2Flocalhost%3A$PORT%2Fcallback&state=s"
while [ ! -f "$MARK" ]; do sleep 0.05; done
if [ -n "$WRITE" ]; then mkdir -p "$DIR" && touch "$DIR/creds"; fi`

func signInCLI(l *cliListener, dir string, write bool) (CLI, Account) {
	env := map[string]string{"PORT": strconv.Itoa(l.port), "MARK": l.mark, "DIR": dir}
	if write {
		env["WRITE"] = "1"
	}
	return CLI{
		ID:             "gws",
		Name:           "Google Workspace",
		StatusCommand:  `test -f "$DIR/creds" && test -f "$DIR/prepared"`,
		PrepareCommand: `mkdir -p "$DIR" && touch "$DIR/prepared"`,
		SignInCommand:  fakeSignIn,
	}, Account{Name: "work", Env: env}
}

func TestSignInAccountRelaysPastedAddress(t *testing.T) {
	cliHome(t)
	l := newCLIListener(t)
	cli, acct := signInCLI(l, filepath.Join(t.TempDir(), "acct"), true)
	ia := newOAIA()
	done := make(chan error, 1)
	go func() { done <- SignInAccount(context.Background(), ia, cli, acct) }()

	authURL := ia.authURL(t)
	if loopbackRedirectPort(authURL) != l.port {
		t.Fatalf("auth url %q", authURL)
	}
	port := strconv.Itoa(l.port)
	// Not on this machine, not http, a different port: each refused and
	// asked again, without reaching the listener.
	for _, bad := range []string{
		"http://example.com:" + port + "/callback?code=x",
		"https://localhost:" + port + "/callback?code=x",
		"http://localhost:1/callback?code=x",
		"http://user@localhost:" + port + "/callback?code=x",
	} {
		ia.prompt(t).answer <- bad
	}
	ia.prompt(t).answer <- "http://127.0.0.1:" + port + "/callback?code=the-code&state=s"

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in never finished")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.got.Get("code") != "the-code" {
		t.Errorf("listener got %v", l.got)
	}
	if l.followed {
		t.Error("the relay followed the listener's redirect")
	}
}

func TestSignInAccountCommandFinishesOnItsOwn(t *testing.T) {
	cliHome(t)
	l := newCLIListener(t)
	cli, acct := signInCLI(l, filepath.Join(t.TempDir(), "acct"), true)
	ia := newOAIA()
	done := make(chan error, 1)
	go func() { done <- SignInAccount(context.Background(), ia, cli, acct) }()
	ia.authURL(t)
	ia.prompt(t)
	// The browser reached the command directly; nobody pastes anything.
	http.Get(l.srv.URL + "/callback?code=direct")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in never finished")
	}
}

func TestSignInAccountStillSignedOut(t *testing.T) {
	cliHome(t)
	l := newCLIListener(t)
	cli, acct := signInCLI(l, filepath.Join(t.TempDir(), "acct"), false)
	ia := newOAIA()
	done := make(chan error, 1)
	go func() { done <- SignInAccount(context.Background(), ia, cli, acct) }()
	ia.authURL(t)
	ia.prompt(t).answer <- "http://localhost:" + strconv.Itoa(l.port) + "/callback?code=c"
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a sign-in that leaves the account signed out must fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in never finished")
	}
}

func TestSignInAccountNoAddress(t *testing.T) {
	cliHome(t)
	cli := CLI{ID: "x", StatusCommand: "true", SignInCommand: `echo "client_secret.json not found"; exit 1`}
	if err := SignInAccount(context.Background(), newOAIA(), cli, Account{Name: "a"}); err == nil {
		t.Fatal("a command that prints no address must fail")
	}

	cli.SignInCommand = "true"
	cli.PrepareCommand = "exit 4"
	if err := SignInAccount(context.Background(), newOAIA(), cli, Account{Name: "a"}); err == nil {
		t.Fatal("a failing prepare command must fail the sign-in")
	}
}

func TestSignInAccountURLTimeout(t *testing.T) {
	cliHome(t)
	old := cliURLWait
	cliURLWait = 200 * time.Millisecond
	t.Cleanup(func() { cliURLWait = old })
	pidFile := filepath.Join(t.TempDir(), "pid")
	cli := CLI{ID: "x", StatusCommand: "true", SignInCommand: `sleep 30 & echo $! > "$PIDFILE"; wait`}
	if err := SignInAccount(context.Background(), newOAIA(), cli, Account{Name: "a", Env: map[string]string{"PIDFILE": pidFile}}); err == nil {
		t.Fatal("a command that never prints an address must fail")
	}
	waitDead(t, pidFile)
}

func TestSignInAccountCancelKillsCommand(t *testing.T) {
	cliHome(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	cli := CLI{ID: "x", StatusCommand: "true",
		SignInCommand: `sleep 30 & echo $! > "$PIDFILE"; echo "https://accounts.example/auth?x=1"; wait`}
	ia := newOAIA()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- SignInAccount(ctx, ia, cli, Account{Name: "a", Env: map[string]string{"PIDFILE": pidFile}})
	}()
	ia.authURL(t)
	ia.prompt(t)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled sign-in must fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the sign-in")
	}
	waitDead(t, pidFile)
}

func TestScrubTokens(t *testing.T) {
	secrets := []string{"ya29.a0AfB_byC1234567890abcdefXYZ", "s3cr3tvalue", "eyJhbGciOiJIUzI1NiJ9", "qwerty"}
	in := []string{
		"auth failed for ya29.a0AfB_byC1234567890abcdefXYZ",
		"client_secret=s3cr3tvalue",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.e30.abc",
		"see https://example.com/cb?code=qwerty&state=1",
	}
	for i, s := range in {
		got := scrubTokens(s)
		if strings.Contains(got, secrets[i]) {
			t.Errorf("scrubTokens(%q) = %q", s, got)
		}
	}
	// Ordinary prose and paths survive.
	plain := "no credentials in /home/me/.config/gws-work"
	if got := scrubTokens(plain); got != plain {
		t.Errorf("scrubTokens(%q) = %q", plain, got)
	}
}

var _ adapter.AuthInteraction = (*oaIA)(nil)
