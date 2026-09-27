package session

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// filesFixture is a session in a git worktree holding a committed file, an
// untracked file, and an ignored one — the three visibilities the tree has to
// decide between — plus a secret outside the workspace for the escape tests.
func filesFixture(t *testing.T) (mgr *Manager, id, worktree, outside string) {
	t.Helper()
	root, worktree, _ := gitRepo(t)
	write(t, root, "tracked.txt", "one\n")
	write(t, root, ".gitignore", "ignored.log\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	gitRun(t, worktree, "merge", "main")

	write(t, worktree, "untracked.txt", "two\n")
	write(t, worktree, "ignored.log", "noise\n")
	write(t, worktree, "sub/deep.txt", "three\n")

	outside = t.TempDir()
	write(t, outside, "secret.txt", "the goods\n")

	st, p := testProject(t, root)
	mgr = NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	meta := store.SessionMeta{ID: "s1", Cwd: worktree, Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle", ProjectID: p.ID}
	if err := st.CreateSession(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	return mgr, "s1", worktree, outside
}

func TestSessionFileTreeRespectsGitignore(t *testing.T) {
	mgr, id, _, _ := filesFixture(t)
	tree, err := mgr.SessionFileTree(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tracked.txt", "untracked.txt", "sub/deep.txt"} {
		if !slices.Contains(tree.Files, want) {
			t.Fatalf("%s missing from tree: %v", want, tree.Files)
		}
	}
	if slices.Contains(tree.Files, "ignored.log") {
		t.Fatalf("ignored file listed: %v", tree.Files)
	}

	// With the filter off, the ignored file is part of the answer.
	tree, err = mgr.SessionFileTree(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tree.Files, "ignored.log") {
		t.Fatalf("includeIgnored did not include the ignored file: %v", tree.Files)
	}
}

func TestSessionFileTreeOutsideGitStillLists(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "a\n")
	write(t, dir, "nested/b.txt", "b\n")

	st, err := store.Open(filepath.Join(t.TempDir(), "files.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	if err := st.CreateSession(context.Background(), store.SessionMeta{ID: "s2", Cwd: dir, Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}

	tree, err := mgr.SessionFileTree(context.Background(), "s2", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tree.Files, "a.txt") || !slices.Contains(tree.Files, "nested/b.txt") {
		t.Fatalf("plain directory not listed: %v", tree.Files)
	}
}

func TestSessionReadFileReadsInsideTheWorkspace(t *testing.T) {
	mgr, id, _, _ := filesFixture(t)
	file, err := mgr.SessionReadFile(context.Background(), id, "sub/deep.txt")
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "three\n" || file.Binary || file.Truncated {
		t.Fatalf("read mis-reported: %+v", file)
	}
}

func TestSessionReadFileRefusesEscapes(t *testing.T) {
	mgr, id, worktree, outside := filesFixture(t)
	ctx := context.Background()

	// A dot-dot path, however it is dressed.
	for _, p := range []string{"../secret.txt", "sub/../../secret.txt", "/etc/passwd"} {
		if _, err := mgr.SessionReadFile(ctx, id, p); err == nil {
			t.Fatalf("%q was allowed out of the workspace", p)
		}
	}

	// A symlink inside the workspace pointing out of it. String-prefix scoping
	// passes this; real resolution must not.
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(worktree, "innocent.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := mgr.SessionReadFile(ctx, id, "innocent.txt"); err == nil {
		t.Fatal("a symlink escape was followed")
	}
	// And a symlinked directory, which escapes on a parent rather than the leaf.
	if err := os.Symlink(outside, filepath.Join(worktree, "door")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := mgr.SessionReadFile(ctx, id, "door/secret.txt"); err == nil {
		t.Fatal("a symlinked directory escape was followed")
	}
}

func TestSessionReadFileAcceptsAbsolutePathsUnderTheRoot(t *testing.T) {
	mgr, id, worktree, _ := filesFixture(t)
	file, err := mgr.SessionReadFile(context.Background(), id, filepath.Join(worktree, "sub", "deep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "three\n" {
		t.Fatalf("absolute in-root read mis-reported: %+v", file)
	}
}

func TestSessionReadFileRefusesSpecialFiles(t *testing.T) {
	mgr, id, worktree, _ := filesFixture(t)
	if err := syscall.Mkfifo(filepath.Join(worktree, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	// A FIFO would block the open forever; it must be refused, not waited on.
	if _, err := mgr.SessionReadFile(context.Background(), id, "pipe"); err == nil {
		t.Fatal("a FIFO was opened")
	}
}

func TestSessionReadFileDetectsBinaryAndTruncates(t *testing.T) {
	mgr, id, worktree, _ := filesFixture(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(worktree, "blob.bin"), []byte{0x89, 'P', 'N', 'G', 0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := mgr.SessionReadFile(ctx, id, "blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !file.Binary || file.Content != "" {
		t.Fatalf("binary not detected: %+v", file)
	}

	write(t, worktree, "big.txt", strings.Repeat("x", maxFileReadBytes+10))
	file, err = mgr.SessionReadFile(ctx, id, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !file.Truncated || len(file.Content) != maxFileReadBytes {
		t.Fatalf("oversized read not truncated: truncated=%v len=%d", file.Truncated, len(file.Content))
	}
}
