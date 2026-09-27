package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// changesFixture is a session working in a worktree cut from main, with one
// committed edit, one uncommitted edit, one delete, one rename and one
// untracked file — every status the file list has to name.
func changesFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root, worktree, branch := gitRepo(t)
	write(t, root, "keep.txt", "one\ntwo\nthree\n")
	write(t, root, "gone.txt", "bye\n")
	write(t, root, "old-name.txt", "same\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "base")
	gitRun(t, worktree, "merge", "main")

	write(t, worktree, "keep.txt", "one\ntwo\nthree\nfour\n")
	gitRun(t, worktree, "add", "keep.txt")
	gitRun(t, worktree, "commit", "-m", "committed edit")
	write(t, worktree, "new.txt", "fresh\n")
	gitRun(t, worktree, "add", "new.txt")
	gitRun(t, worktree, "rm", "-q", "gone.txt")
	gitRun(t, worktree, "mv", "old-name.txt", "new-name.txt")
	write(t, worktree, "untracked.txt", "a\nb\n")

	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	meta := store.SessionMeta{ID: "s1", Cwd: worktree, Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle", ProjectID: p.ID, Branch: branch, WorkspaceMode: "borrowed"}
	if err := st.CreateSession(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	return mgr, "s1"
}

func fileNamed(t *testing.T, files []ChangedFile, path string) ChangedFile {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("%s missing from %+v", path, files)
	return ChangedFile{}
}

func TestSessionChangesAggregatesTheWholeWorktreeAgainstItsBase(t *testing.T) {
	mgr, id := changesFixture(t)
	changes, err := mgr.SessionChanges(context.Background(), id, DiffBranch)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Warning != "" {
		t.Fatalf("unexpected warning: %s", changes.Warning)
	}
	if changes.BaseRef != "main" {
		t.Fatalf("base branch not found: %+v", changes)
	}

	// A committed edit counts as much as an uncommitted one: the session made
	// both, and a reviewer wants to see them together.
	if got := fileNamed(t, changes.Files, "keep.txt"); got.Status != "modified" || got.Additions != 1 || got.Deletions != 0 {
		t.Fatalf("committed edit mis-measured: %+v", got)
	}
	if got := fileNamed(t, changes.Files, "gone.txt"); got.Status != "deleted" {
		t.Fatalf("delete mis-measured: %+v", got)
	}
	if got := fileNamed(t, changes.Files, "new-name.txt"); got.Status != "renamed" || got.OldPath != "old-name.txt" {
		t.Fatalf("rename mis-measured: %+v", got)
	}
	if got := fileNamed(t, changes.Files, "new.txt"); got.Status != "added" || got.Additions != 1 {
		t.Fatalf("added file mis-measured: %+v", got)
	}
	// A file the agent wrote but never staged is still a change it made.
	if got := fileNamed(t, changes.Files, "untracked.txt"); !got.Untracked || got.Additions != 2 {
		t.Fatalf("untracked file mis-measured: %+v", got)
	}
	if changes.Additions < 4 {
		t.Fatalf("totals do not add up: %+v", changes)
	}
}

func TestSessionChangesDefaultsToUncommittedWork(t *testing.T) {
	mgr, id := changesFixture(t)
	changes, err := mgr.SessionChanges(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range changes.Files {
		if file.Path == "keep.txt" {
			t.Fatalf("committed branch edit appeared in uncommitted changes: %+v", file)
		}
	}
	fileNamed(t, changes.Files, "new.txt")
	fileNamed(t, changes.Files, "untracked.txt")
}

func TestPullRequestChangesExcludeDirtyWorktree(t *testing.T) {
	mgr, id := changesFixture(t)
	meta, err := mgr.store.Session(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	baseRaw, err := runGit(context.Background(), meta.Cwd, "merge-base", "main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	headRaw, err := runGit(context.Background(), meta.Cwd, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base, head := strings.TrimSpace(string(baseRaw)), strings.TrimSpace(string(headRaw))
	fakeGh(t, prJSON(`{"number":7,"state":"OPEN","baseRefName":"main","baseRefOid":"`+base+`","headRefOid":"`+head+`"}`), "", 0)

	changes, err := mgr.SessionChanges(context.Background(), id, DiffPullRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path != "keep.txt" {
		t.Fatalf("PR files include dirty worktree changes: %+v", changes.Files)
	}
	if _, err := mgr.SessionFileDiff(context.Background(), id, "keep.txt", DiffPullRequest, base, base); err == nil {
		t.Fatal("a client-supplied range must not replace the attached PR range")
	}
	if diff := sessionFileDiff(t, mgr, id, "keep.txt", DiffPullRequest); !strings.Contains(diff.Patch, "+four") {
		t.Fatalf("PR patch missing committed change: %q", diff.Patch)
	}
}

func sessionFileDiff(t *testing.T, mgr *Manager, id, path, mode string) FileDiff {
	t.Helper()
	changes, err := mgr.SessionChanges(context.Background(), id, mode)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := mgr.SessionFileDiff(context.Background(), id, path, mode, changes.Base, changes.Head)
	if err != nil {
		t.Fatal(err)
	}
	return diff
}

func TestSessionFileDiffRendersAPatchForTrackedAndUntrackedFiles(t *testing.T) {
	mgr, id := changesFixture(t)
	tracked := sessionFileDiff(t, mgr, id, "keep.txt", DiffBranch)
	if !strings.Contains(tracked.Patch, "+four") {
		t.Fatalf("tracked patch missing its edit: %q", tracked.Patch)
	}
	untracked := sessionFileDiff(t, mgr, id, "untracked.txt", DiffBranch)
	if !strings.Contains(untracked.Patch, "+a") {
		t.Fatalf("untracked patch missing its content: %q", untracked.Patch)
	}
}

// The checkout is not a file server: only paths the change list reported can
// be read back through the diff command.
func TestSessionFileDiffRefusesPathsThatDidNotChange(t *testing.T) {
	mgr, id := changesFixture(t)
	changes, err := mgr.SessionChanges(context.Background(), id, DiffUncommitted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SessionFileDiff(context.Background(), id, "../../etc/passwd", DiffUncommitted, changes.Base, changes.Head); err == nil {
		t.Fatal("an unrelated path must be refused")
	}
	if _, err := mgr.SessionFileDiff(context.Background(), id, "README", DiffUncommitted, changes.Base, changes.Head); err == nil {
		t.Fatal("an unchanged path must be refused")
	}
}

// A file git has never seen is counted here rather than by a git process per
// file, so the counting has to be right about the awkward cases itself.
func TestNewFileLineCounting(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, body string
		lines      int
		binary     bool
	}{
		{"empty.txt", "", 0, false},
		{"trailing.txt", "a\nb\n", 2, false},
		{"no-trailing-newline.txt", "a\nb", 2, false},
		{"binary.bin", "MZ\x00\x01rest", 0, true},
	}
	for _, c := range cases {
		write(t, dir, c.name, c.body)
		lines, binary := newFileLines(filepath.Join(dir, c.name))
		if lines != c.lines || binary != c.binary {
			t.Fatalf("%s: got %d lines binary=%v, want %d/%v", c.name, lines, binary, c.lines, c.binary)
		}
	}
	if lines, _ := newFileLines(filepath.Join(dir, "does-not-exist")); lines != 0 {
		t.Fatal("a file that vanished mid-read must not be an error")
	}
}

func TestSessionChangesExplainsACheckoutThatIsNotARepository(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	if err := st.CreateSession(context.Background(), store.SessionMeta{ID: "s2", Cwd: t.TempDir(), Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	changes, err := mgr.SessionChanges(context.Background(), "s2", DiffUncommitted)
	if err != nil {
		t.Fatalf("a plain directory must not be an error: %v", err)
	}
	if changes.Warning == "" || len(changes.Files) != 0 {
		t.Fatalf("expected an explained empty list: %+v", changes)
	}
}
