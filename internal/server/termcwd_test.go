package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTermProcessCwdReadsTheProcesssDirectory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no cwd lookup on " + runtime.GOOS)
	}
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	// macOS's temp dir sits behind the /var -> /private/var link, and the
	// lookup reports where the process really is.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := processCwd(context.Background(), pid); got != want {
		t.Fatalf("cwd %q, want %q", got, want)
	}

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if got := processCwd(context.Background(), pid); got != "" {
		t.Fatalf("cwd of an exited process %q, want empty", got)
	}
}

func TestTermCwdIsSpelledAsTheShellStarted(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ dir, want string }{
		{real, link},
		{filepath.Join(real, "web", "src"), filepath.Join(link, "web", "src")},
		// Outside the checkout, or a sibling sharing its prefix: left alone.
		{"/etc", "/etc"},
		{real + "-old", real + "-old"},
		{"", ""},
	}
	for _, c := range cases {
		if got := asStartedIn(c.dir, link); got != c.want {
			t.Errorf("asStartedIn(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
	// No symlink in the way: the kernel's answer is already the right one.
	if got := asStartedIn(filepath.Join(real, "x"), real); got != filepath.Join(real, "x") {
		t.Errorf("unlinked start: got %q", got)
	}
}
