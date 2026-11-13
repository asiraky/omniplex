package thread

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

// changesFixture is a thread working in a worktree cut from main, with one
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
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	meta := store.ThreadMeta{ID: "s1", Cwd: worktree, Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle", ProjectID: p.ID, Branch: branch, WorkspaceMode: "borrowed"}
	if err := st.CreateThread(context.Background(), meta); err != nil {
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

func TestThreadChangesAggregatesTheWholeWorktreeAgainstItsBase(t *testing.T) {
	mgr, id := changesFixture(t)
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffBranch, "")
	if err != nil {
		t.Fatal(err)
	}
	if changes.Warning != "" {
		t.Fatalf("unexpected warning: %s", changes.Warning)
	}
	if changes.BaseRef != "main" {
		t.Fatalf("base branch not found: %+v", changes)
	}

	// A committed edit counts as much as an uncommitted one: the thread made
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

func TestThreadChangesDefaultsToUncommittedWork(t *testing.T) {
	mgr, id := changesFixture(t)
	changes, err := mgr.ThreadChanges(context.Background(), id, "", "")
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
	meta, err := mgr.store.Thread(context.Background(), id)
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

	changes, err := mgr.ThreadChanges(context.Background(), id, DiffPullRequest, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path != "keep.txt" {
		t.Fatalf("PR files include dirty worktree changes: %+v", changes.Files)
	}
	if _, err := mgr.ThreadFileDiff(context.Background(), id, "keep.txt", DiffPullRequest, "", base, base); err == nil {
		t.Fatal("a client-supplied range must not replace the attached PR range")
	}
	if diff := threadFileDiff(t, mgr, id, "keep.txt", DiffPullRequest); !strings.Contains(diff.Patch, "+four") {
		t.Fatalf("PR patch missing committed change: %q", diff.Patch)
	}
}

func threadFileDiff(t *testing.T, mgr *Manager, id, path, mode string) FileDiff {
	t.Helper()
	changes, err := mgr.ThreadChanges(context.Background(), id, mode, "")
	if err != nil {
		t.Fatal(err)
	}
	diff, err := mgr.ThreadFileDiff(context.Background(), id, path, mode, "", changes.Base, changes.Head)
	if err != nil {
		t.Fatal(err)
	}
	return diff
}

func TestThreadFileDiffRendersAPatchForTrackedAndUntrackedFiles(t *testing.T) {
	mgr, id := changesFixture(t)
	tracked := threadFileDiff(t, mgr, id, "keep.txt", DiffBranch)
	if !strings.Contains(tracked.Patch, "+four") {
		t.Fatalf("tracked patch missing its edit: %q", tracked.Patch)
	}
	untracked := threadFileDiff(t, mgr, id, "untracked.txt", DiffBranch)
	if !strings.Contains(untracked.Patch, "+a") {
		t.Fatalf("untracked patch missing its content: %q", untracked.Patch)
	}
}

// The checkout is not a file server: only paths the change list reported can
// be read back through the diff command.
func TestThreadFileDiffRefusesPathsThatDidNotChange(t *testing.T) {
	mgr, id := changesFixture(t)
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffUncommitted, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ThreadFileDiff(context.Background(), id, "../../etc/passwd", DiffUncommitted, "", changes.Base, changes.Head); err == nil {
		t.Fatal("an unrelated path must be refused")
	}
	if _, err := mgr.ThreadFileDiff(context.Background(), id, "README", DiffUncommitted, "", changes.Base, changes.Head); err == nil {
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

func TestThreadChangesExplainsACheckoutThatIsNotARepository(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	if err := st.CreateThread(context.Background(), store.ThreadMeta{ID: "s2", Cwd: t.TempDir(), Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	changes, err := mgr.ThreadChanges(context.Background(), "s2", DiffUncommitted, "")
	if err != nil {
		t.Fatalf("a plain directory must not be an error: %v", err)
	}
	if changes.Warning == "" || len(changes.Files) != 0 {
		t.Fatalf("expected an explained empty list: %+v", changes)
	}
}

// turnFixture brackets one turn with real checkpoints on top of the changes
// fixture's dirty worktree, then keeps working after the turn ended, so the
// turn's range has something on either side of it to leave out.
func turnFixture(t *testing.T) (*Manager, string, string) {
	t.Helper()
	mgr, id := changesFixture(t)
	ctx := context.Background()
	meta, err := mgr.store.Thread(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	const turn = "0b6c1f9e-3d2a-4c55-9a8e-7f00c0ffee01"
	if _, err := captureCheckpoint(ctx, meta.Cwd, checkpointRef(id, turn, "base")); err != nil {
		t.Fatal(err)
	}
	write(t, meta.Cwd, "keep.txt", "one\ntwo\nthree\nfour\nfive\n")
	write(t, meta.Cwd, "turn-new.txt", "made\nduring\n")
	if _, err := captureCheckpoint(ctx, meta.Cwd, checkpointRef(id, turn, "end")); err != nil {
		t.Fatal(err)
	}
	write(t, meta.Cwd, "new.txt", "fresh\nlater\n")
	write(t, meta.Cwd, "after.txt", "left behind\n")
	return mgr, id, turn
}

func TestTurnChangesCoverOnlyWhatHappenedBetweenItsSnapshots(t *testing.T) {
	mgr, id, turn := turnFixture(t)
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffTurn, turn)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Warning != "" {
		t.Fatalf("unexpected warning: %s", changes.Warning)
	}
	if changes.TurnID != turn || changes.Mode != DiffTurn || changes.Base == "" || changes.Head == "" || changes.BaseRef != "" {
		t.Fatalf("turn range not recorded: %+v", changes)
	}
	// Work from before the turn, edits after it, and files left untracked
	// after it all belong to some other range.
	if len(changes.Files) != 2 {
		t.Fatalf("want exactly the turn's two files, got %+v", changes.Files)
	}
	if got := fileNamed(t, changes.Files, "keep.txt"); got.Status != "modified" || got.Additions != 1 {
		t.Fatalf("turn edit mis-measured: %+v", got)
	}
	// A file that was untracked when the turn ended is in the end snapshot, so
	// it is an ordinary addition there, not an untracked worktree file.
	if got := fileNamed(t, changes.Files, "turn-new.txt"); got.Status != "added" || got.Untracked || got.Additions != 2 {
		t.Fatalf("turn's new file mis-measured: %+v", got)
	}
	if changes.Additions != 3 || changes.Deletions != 0 {
		t.Fatalf("totals do not add up: %+v", changes)
	}
}

func TestTurnFileDiffReadsTheSnapshotRange(t *testing.T) {
	mgr, id, turn := turnFixture(t)
	ctx := context.Background()
	changes, err := mgr.ThreadChanges(ctx, id, DiffTurn, turn)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffTurn, turn, changes.Base, changes.Head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+five") || strings.Contains(diff.Patch, "+four") {
		t.Fatalf("patch is not the turn's own edit: %q", diff.Patch)
	}
	added, err := mgr.ThreadFileDiff(ctx, id, "turn-new.txt", DiffTurn, turn, changes.Base, changes.Head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(added.Patch, "+during") {
		t.Fatalf("new file patch missing its content: %q", added.Patch)
	}

	for _, path := range []string{"after.txt", "new.txt", "../../etc/passwd"} {
		if _, err := mgr.ThreadFileDiff(ctx, id, path, DiffTurn, turn, changes.Base, changes.Head); err == nil {
			t.Fatalf("%s is not part of the turn and must be refused", path)
		}
	}
	if _, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffTurn, turn, changes.Head, changes.Base); err == nil {
		t.Fatal("a range that is not the turn's must be refused")
	}

	// The list a card seeds carries no range yet; the snapshots supply it.
	seeded, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffTurn, turn, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Patch != diff.Patch {
		t.Fatalf("a seeded read must name the same range: %q", seeded.Patch)
	}
}

func TestTurnChangesExplainMissingSnapshots(t *testing.T) {
	mgr, id, turn := turnFixture(t)
	ctx := context.Background()
	meta, err := mgr.store.Thread(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// A turn that was cut short has a start but no end.
	if _, err := captureCheckpoint(ctx, meta.Cwd, checkpointRef(id, "half", "base")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, meta.Cwd, "update-ref", "-d", checkpointRef(id, turn, "base"))
	gitRun(t, meta.Cwd, "update-ref", "-d", checkpointRef(id, turn, "end"))

	for _, missing := range []string{turn, "half", "never-measured"} {
		changes, err := mgr.ThreadChanges(ctx, id, DiffTurn, missing)
		if err != nil {
			t.Fatalf("%s: missing snapshots must not be an error: %v", missing, err)
		}
		if changes.Warning == "" || len(changes.Files) != 0 || changes.Head != "" {
			t.Fatalf("%s: expected an explained empty list: %+v", missing, changes)
		}
		if _, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffTurn, missing, "", ""); err == nil {
			t.Fatalf("%s: a file diff with no turn range must be refused", missing)
		}
	}
}

func TestTurnChangesRefuseAnythingButATurnID(t *testing.T) {
	mgr, id, _ := turnFixture(t)
	for _, bad := range []string{"", "../base", "t1/base", "a:b", "HEAD~1", "a b", "x^{commit}"} {
		if _, err := mgr.ThreadChanges(context.Background(), id, DiffTurn, bad); err == nil {
			t.Fatalf("%q must be refused as a turn id", bad)
		}
		if _, err := mgr.ThreadFileDiff(context.Background(), id, "keep.txt", DiffTurn, bad, "", ""); err == nil {
			t.Fatalf("%q must be refused as a turn id for a file diff", bad)
		}
	}
}
