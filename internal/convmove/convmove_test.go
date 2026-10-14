package convmove

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// crossDevice makes the first rename fail the way a rename between two
// filesystems does.
func crossDevice(t *testing.T) {
	t.Helper()
	calls := 0
	rename = func(src, dst string) error {
		calls++
		if calls == 1 {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: syscall.EXDEV}
		}
		return RenameNoReplace(src, dst)
	}
	t.Cleanup(func() { rename = RenameNoReplace })
}

func TestMoveRenamesAndRefusesAnExistingDestination(t *testing.T) {
	top := t.TempDir()
	src, dst := filepath.Join(top, "a", "conv"), filepath.Join(top, "b", "deep", "conv")
	write(t, filepath.Join(src, "sub.jsonl"), "sub")
	copied, err := Move(src, dst, true)
	if err != nil || copied {
		t.Fatalf("Move = %v, %v", copied, err)
	}
	if read(t, filepath.Join(dst, "sub.jsonl")) != "sub" {
		t.Error("folder did not arrive")
	}
	if _, err := os.Lstat(src); !errors.Is(err, os.ErrNotExist) {
		t.Error("source left behind after a rename")
	}

	write(t, filepath.Join(top, "x.jsonl"), "mine")
	write(t, filepath.Join(top, "y.jsonl"), "theirs")
	if _, err := Move(filepath.Join(top, "x.jsonl"), filepath.Join(top, "y.jsonl"), true); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite not refused: %v", err)
	}
	if read(t, filepath.Join(top, "y.jsonl")) != "theirs" || read(t, filepath.Join(top, "x.jsonl")) != "mine" {
		t.Error("a refused move changed files")
	}
}

// Across filesystems the copy lands whole and the source stays, renamed
// aside, when asked to keep it; otherwise it goes.
func TestMoveAcrossFilesystemsKeepsOrRemovesTheAside(t *testing.T) {
	for _, keep := range []bool{true, false} {
		top := t.TempDir()
		src, dst := filepath.Join(top, "a", "conv.jsonl"), filepath.Join(top, "b", "conv.jsonl")
		write(t, src, "transcript")
		crossDevice(t)
		copied, err := Move(src, dst, keep)
		if err != nil || !copied {
			t.Fatalf("keep=%v: Move = %v, %v", keep, copied, err)
		}
		if read(t, dst) != "transcript" {
			t.Errorf("keep=%v: copy is wrong", keep)
		}
		if _, err := os.Lstat(src); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("keep=%v: source still under the name the harness reads", keep)
		}
		_, err = os.Lstat(Aside(src))
		if keep && err != nil {
			t.Errorf("aside removed although kept: %v", err)
		}
		if !keep && err == nil {
			t.Error("aside left although not kept")
		}
	}
}

// A leftover aside from an earlier copy may be the only copy; a second move
// refuses rather than replacing it, and leaves the source where it is.
func TestMoveNeverReplacesAnEarlierAside(t *testing.T) {
	top := t.TempDir()
	src, dst := filepath.Join(top, "a", "conv.jsonl"), filepath.Join(top, "b", "conv.jsonl")
	write(t, src, "new")
	write(t, Aside(src), "old")
	crossDevice(t)
	if _, err := Move(src, dst, true); err == nil {
		t.Fatal("moved over an earlier aside")
	}
	if read(t, Aside(src)) != "old" || read(t, src) != "new" {
		t.Error("files changed by a refused move")
	}
}

// A plain rename replaces an empty folder (and any file) at the destination;
// the no-replace rename refuses both, so a destination that appears after
// Move's first look is never replaced.
func TestRenameNoReplaceRefusesWhatIsThere(t *testing.T) {
	top := t.TempDir()
	src, dst := filepath.Join(top, "conv"), filepath.Join(top, "theirs")
	write(t, filepath.Join(src, "a.jsonl"), "mine")
	if err := os.Mkdir(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RenameNoReplace(src, dst); !errors.Is(err, ErrExists) {
		t.Fatalf("rename over an empty folder = %v", err)
	}
	if read(t, filepath.Join(src, "a.jsonl")) != "mine" {
		t.Error("the source changed")
	}
	if entries, err := os.ReadDir(dst); err != nil || len(entries) != 0 {
		t.Errorf("the destination changed: %v %v", entries, err)
	}

	write(t, filepath.Join(top, "x.jsonl"), "mine")
	write(t, filepath.Join(top, "y.jsonl"), "theirs")
	if err := RenameNoReplace(filepath.Join(top, "x.jsonl"), filepath.Join(top, "y.jsonl")); !errors.Is(err, ErrExists) {
		t.Fatalf("rename over a file = %v", err)
	}
	if read(t, filepath.Join(top, "y.jsonl")) != "theirs" {
		t.Error("the destination file was replaced")
	}
}

// A destination that appears between Move's check and its copy is a
// conflict: nothing of it is removed or written, and the source goes back.
func TestACopyNeverRemovesADestinationItDidNotMake(t *testing.T) {
	for _, dir := range []bool{false, true} {
		top := t.TempDir()
		src, dst := filepath.Join(top, "a", "conv"), filepath.Join(top, "b", "conv")
		if dir {
			write(t, filepath.Join(src, "sub.jsonl"), "mine")
		} else {
			write(t, src, "mine")
		}
		calls := 0
		rename = func(from, to string) error {
			calls++
			if calls == 1 {
				// Someone else's conversation lands at dst just now.
				write(t, filepath.Join(dst, "theirs.jsonl"), "theirs")
				if !dir {
					if err := os.RemoveAll(dst); err != nil {
						t.Fatal(err)
					}
					write(t, dst, "theirs")
				}
				return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EXDEV}
			}
			return RenameNoReplace(from, to)
		}
		t.Cleanup(func() { rename = RenameNoReplace })
		if _, err := Move(src, dst, true); !errors.Is(err, ErrExists) {
			t.Fatalf("dir=%v: Move = %v, want ErrExists", dir, err)
		}
		if dir {
			if read(t, filepath.Join(dst, "theirs.jsonl")) != "theirs" || read(t, filepath.Join(src, "sub.jsonl")) != "mine" {
				t.Errorf("dir=%v: files changed", dir)
			}
		} else if read(t, dst) != "theirs" || read(t, src) != "mine" {
			t.Errorf("dir=%v: files changed", dir)
		}
		if _, err := os.Lstat(Aside(src)); err == nil {
			t.Errorf("dir=%v: the source was left aside", dir)
		}
	}
}

// A copy that fails part way removes only what it made and puts the source
// back whole.
func TestAFailedCopyPutsTheSourceBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	top := t.TempDir()
	src, dst := filepath.Join(top, "a", "conv"), filepath.Join(top, "b", "conv")
	write(t, filepath.Join(src, "a.jsonl"), "first")
	write(t, filepath.Join(src, "z.jsonl"), "second")
	if err := os.Chmod(filepath.Join(src, "z.jsonl"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(src, "z.jsonl"), 0o600) })
	crossDevice(t)
	copied, err := Move(src, dst, true)
	if err == nil || !copied {
		t.Fatalf("Move = %v, %v; want a failed copy", copied, err)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the partial copy is still there: %v", err)
	}
	if read(t, filepath.Join(src, "a.jsonl")) != "first" {
		t.Error("the source did not go back")
	}
	if _, err := os.Lstat(Aside(src)); err == nil {
		t.Error("the source was left aside")
	}
}
