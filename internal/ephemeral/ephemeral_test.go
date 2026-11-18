package ephemeral

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	dir, err := Init(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestThreadOfNamesOnlyAPrivateThreadsFolder(t *testing.T) {
	setup(t)
	id := NewID()
	for path, want := range map[string]string{
		ThreadDir(id):                          id,
		filepath.Join(ThreadDir(id), "a", "b"): id,
		ThreadDir(id) + "/../" + id:            id,
		filepath.Join(Dir(), "threads"):        "",
		filepath.Join(Dir(), "threads", "x"):   "",
		filepath.Join(Dir(), "state", id):      "",
		"/home/me/" + id:                       "",
		"threads/" + id:                        "",
		ThreadDir(id) + "/../../threads-" + id: "",
	} {
		if got := ThreadOf(path); got != want {
			t.Errorf("ThreadOf(%s) = %q, want %q", path, got, want)
		}
	}
}

// Forgetting a thread takes its folder, its state and the links made for it,
// and removes a link without touching whatever else shares its folder.
func TestForgetRemovesLinksStateAndFolder(t *testing.T) {
	setup(t)
	id := NewID()
	outside := t.TempDir()
	link := filepath.Join(outside, "projects", "key")
	target := StateDir(id, "transcripts")
	if err := os.MkdirAll(ThreadDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Link(id, link, target); err != nil {
		t.Fatal(err)
	}
	// Linking again to the same place is a no-op, not an error.
	if err := Link(id, link, target); err != nil {
		t.Fatalf("relink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(link, "c.jsonl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "c.jsonl")); err != nil {
		t.Fatalf("written through the link: %v", err)
	}
	neighbour := filepath.Join(outside, "projects", "other")
	if err := os.MkdirAll(neighbour, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := Forget(id); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{link, target, ThreadDir(id), filepath.Join(Dir(), "state", id)} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after forget: %v, want gone", p, err)
		}
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Errorf("neighbour after forget: %v", err)
	}
}

// Something already at the link's place is never replaced, and forgetting
// the thread leaves it be.
func TestLinkLeavesWhatIsThereAlone(t *testing.T) {
	setup(t)
	id := NewID()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Link(id, path, StateDir(id, "t")); err == nil {
		t.Fatal("linked over a real folder")
	}
	if err := Forget(id); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		t.Fatalf("folder after forget: %v", err)
	}
}
