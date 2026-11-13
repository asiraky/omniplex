package server

import (
	"context"
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
