package thread

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A file the base snapshot left out because it was ignored then is not one
// the turn created, even after the turn stops ignoring it: the restore puts
// the ignore rules back and leaves the file, and still deletes what is new.
func TestRestoreCheckpointKeepsFilesTheBaseIgnored(t *testing.T) {
	ctx := context.Background()
	_, wt, _ := gitRepo(t)
	write(t, wt, ".gitignore", ".env\n*.log\n")
	write(t, wt, "nested/.gitignore", "secret.txt\n")
	gitRun(t, wt, "add", ".")
	gitRun(t, wt, "commit", "-m", "ignore rules")
	write(t, wt, ".env", "TOKEN=hunter2\n")
	write(t, wt, "nested/secret.txt", "shh\n")

	if _, err := captureCheckpoint(ctx, wt, checkpointRef("th", "t1", "base")); err != nil {
		t.Fatal(err)
	}

	// The turn stops ignoring both, and adds a file of its own.
	write(t, wt, ".gitignore", "*.log\n")
	write(t, wt, "nested/.gitignore", "")
	write(t, wt, "new.txt", "the turn's\n")

	if err := restoreCheckpoint(ctx, wt, "th", "t1"); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		".env": "TOKEN=hunter2\n", "nested/secret.txt": "shh\n",
		".gitignore": ".env\n*.log\n", "nested/.gitignore": "secret.txt\n",
	} {
		if got, err := os.ReadFile(filepath.Join(wt, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, "new.txt")); !os.IsNotExist(err) {
		t.Errorf("new.txt is still there: %v", err)
	}
}
