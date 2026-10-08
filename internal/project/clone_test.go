package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRepo is a real repository on disk, so the clone tests exercise git
// itself without ever touching the network.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "init")
	return root
}

func TestCloneBringsDownTheRepo(t *testing.T) {
	src := fixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")
	if err := Clone(context.Background(), src, dest, nil); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Fatalf("cloned tree has no README: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("cloned tree is not a repo: %v", err)
	}
}

// The UI may well have made the folder before asking us to fill it.
func TestCloneAcceptsAnExistingEmptyDirectory(t *testing.T) {
	src := fixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Clone(context.Background(), "file://"+src, dest, nil); err != nil {
		t.Fatalf("clone into an empty directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Fatalf("cloned tree has no README: %v", err)
	}
}

func TestCloneRefusesANonEmptyDestination(t *testing.T) {
	src := fixtureRepo(t)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "keep"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Clone(context.Background(), src, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("error is %v, want one about a non-empty destination", err)
	}
	// Nothing that was there may be touched.
	if _, err := os.Stat(filepath.Join(dest, "keep")); err != nil {
		t.Fatalf("a refused clone removed existing content: %v", err)
	}
}

func TestCloneRefusesAFileAsDestination(t *testing.T) {
	src := fixtureRepo(t)
	dest := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(dest, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Clone(context.Background(), src, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error is %v, want one about the destination not being a directory", err)
	}
}

// A clone that fails leaves nothing behind, and says something the operator
// can act on rather than echoing git — whose output can carry a token.
func TestCloneOnABadURLCleansUpAndExplains(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "checkout")
	src := filepath.Join(t.TempDir(), "no-such-repo")
	err := Clone(context.Background(), src, dest, nil)
	if err == nil {
		t.Fatal("cloning a path that is not a repo succeeded")
	}
	if strings.Contains(err.Error(), src) {
		t.Fatalf("error %q repeats raw git output", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("a failed clone left %s behind", dest)
	}
}

func TestCloneLogsRawOutputServerSide(t *testing.T) {
	var logged []string
	err := Clone(context.Background(),
		filepath.Join(t.TempDir(), "no-such-repo"),
		filepath.Join(t.TempDir(), "checkout"),
		func(f string, a ...any) { logged = append(logged, f) })
	if err == nil {
		t.Fatal("cloning a path that is not a repo succeeded")
	}
	if len(logged) == 0 {
		t.Fatal("a failed clone logged nothing server-side")
	}
}

func TestNormalizeRemote(t *testing.T) {
	// The shorthand follows gh's git_protocol; an unset protocol is HTTPS.
	shorthand := []struct{ in, protocol, want string }{
		{"asiraky/omniplex", "", "https://github.com/asiraky/omniplex.git"},
		{"asiraky/omniplex", "https", "https://github.com/asiraky/omniplex.git"},
		{"asiraky/omniplex", "ssh", "git@github.com:asiraky/omniplex.git"},
		{"  asiraky/omniplex  ", "https", "https://github.com/asiraky/omniplex.git"},
		{"  asiraky/omniplex  ", "ssh", "git@github.com:asiraky/omniplex.git"},
		{"asiraky/omniplex.git", "https", "https://github.com/asiraky/omniplex.git"},
		{"asiraky/omniplex.git", "ssh", "git@github.com:asiraky/omniplex.git"},
	}
	for _, c := range shorthand {
		got, err := NormalizeRemote(c.in, c.protocol)
		if err != nil {
			t.Errorf("NormalizeRemote(%q, %q): %v", c.in, c.protocol, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeRemote(%q, %q) = %q, want %q", c.in, c.protocol, got, c.want)
		}
	}
	for _, protocol := range []string{"https", "ssh"} {
		bad := []string{"", "   ", "--upload-pack=x", "-x", "o/r\nrm -rf /", "a\x00b"}
		for _, in := range bad {
			if got, err := NormalizeRemote(in, protocol); err == nil {
				t.Errorf("NormalizeRemote(%q, %q) = %q, want an error", in, protocol, got)
			}
		}
		// Anything that is not the strict shorthand is git's business, not
		// ours: it goes through untouched whatever gh prefers, so a pasted
		// HTTPS URL stays HTTPS on an SSH machine.
		passed := []string{
			"https://github.com/asiraky/omniplex.git",
			"https://github.com/asiraky/omniplex",
			"git://example.com/o/r.git",
			"ssh://git@example.com:22/o/r.git",
			"git@github.com:asiraky/omniplex.git",
			"file:///srv/repos/r.git",
			"/srv/repos/r",
			"a/b/c", "own er/repo", "./local/repo",
		}
		for _, in := range passed {
			if got, err := NormalizeRemote(in, protocol); err != nil || got != in {
				t.Errorf("NormalizeRemote(%q, %q) = %q, %v; want it passed through", in, protocol, got, err)
			}
		}
	}
}

// An HTTPS github.com clone that cannot sign in names the SSH URL to try;
// any other remote gets the generic advice and names nothing.
func TestClassifySuggestsSSHForGitHubHTTPS(t *testing.T) {
	const noUser = "fatal: could not read Username for 'https://github.com': terminal prompts disabled"
	for _, url := range []string{
		"https://github.com/dvandort/nidaHR",
		"https://github.com/dvandort/nidaHR.git",
		"https://github.com/dvandort/nidaHR/",
		"https://www.github.com/dvandort/nidaHR",
	} {
		err := classify(url, noUser)
		if err == nil || !strings.Contains(err.Error(), "git@github.com:dvandort/nidaHR.git") {
			t.Errorf("classify(%q) = %v, want it to suggest git@github.com:dvandort/nidaHR.git", url, err)
		}
	}
	for _, url := range []string{
		"https://gitlab.com/dvandort/nidaHR",
		"https://token@github.com/dvandort/nidaHR",
		"https://github.com/dvandort/nidaHR/tree/main",
		"git@github.com:dvandort/nidaHR.git",
	} {
		err := classify(url, noUser)
		if err == nil || !strings.Contains(err.Error(), "could not sign in") || strings.Contains(err.Error(), "git@github.com:") {
			t.Errorf("classify(%q) = %v, want the generic sign-in advice", url, err)
		}
	}
	// A repository that is not there is not a sign-in problem.
	err := classify("https://github.com/dvandort/nidaHR", "remote: Repository not found.")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("classify on a missing repo = %v, want not found", err)
	}
}

func TestDirectoryName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/asiraky/omniplex.git", "omniplex"},
		{"https://github.com/asiraky/omniplex", "omniplex"},
		{"https://github.com/asiraky/omniplex/", "omniplex"},
		{"https://github.com/asiraky/omniplex.git/", "omniplex"},
		{"git@github.com:asiraky/omniplex.git", "omniplex"},
		{"ssh://git@github.com:22/asiraky/omniplex", "omniplex"},
		{"git://example.com/o/deep/r.git", "r"},
		{"file:///srv/repos/r.git", "r"},
		{"/srv/repos/r", "r"},
		{"https://example.com/o/r.git?ref=main", "r"},
		{"https://example.com/o/r.git#frag", "r"},
		{"https://github.com", ""},
		{"https://github.com/", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := DirectoryName(c.in); got != c.want {
			t.Errorf("DirectoryName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
