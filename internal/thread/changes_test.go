package thread

import (
	"context"
	"fmt"
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
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffBranch})
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
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{})
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

	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffPullRequest})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path != "keep.txt" {
		t.Fatalf("PR files include dirty worktree changes: %+v", changes.Files)
	}
	if _, err := mgr.ThreadFileDiff(context.Background(), id, "keep.txt", DiffRequest{Mode: DiffPullRequest, Base: base, Head: base}, PatchOptions{}); err == nil {
		t.Fatal("a client-supplied range must not replace the attached PR range")
	}
	if diff := threadFileDiff(t, mgr, id, "keep.txt", DiffPullRequest); !strings.Contains(diff.Patch, "+four") {
		t.Fatalf("PR patch missing committed change: %q", diff.Patch)
	}
}

func threadFileDiff(t *testing.T, mgr *Manager, id, path, mode string) FileDiff {
	t.Helper()
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := mgr.ThreadFileDiff(context.Background(), id, path, DiffRequest{Mode: mode, Base: changes.Base, Head: changes.Head}, PatchOptions{})
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
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffUncommitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ThreadFileDiff(context.Background(), id, "../../etc/passwd", DiffRequest{Mode: DiffUncommitted, Base: changes.Base, Head: changes.Head}, PatchOptions{}); err == nil {
		t.Fatal("an unrelated path must be refused")
	}
	if _, err := mgr.ThreadFileDiff(context.Background(), id, "README", DiffRequest{Mode: DiffUncommitted, Base: changes.Base, Head: changes.Head}, PatchOptions{}); err == nil {
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
	changes, err := mgr.ThreadChanges(context.Background(), "s2", DiffRequest{Mode: DiffUncommitted})
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
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffTurn, TurnID: turn})
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
	changes, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffTurn, TurnID: turn})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffRequest{Mode: DiffTurn, TurnID: turn, Base: changes.Base, Head: changes.Head}, PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+five") || strings.Contains(diff.Patch, "+four") {
		t.Fatalf("patch is not the turn's own edit: %q", diff.Patch)
	}
	added, err := mgr.ThreadFileDiff(ctx, id, "turn-new.txt", DiffRequest{Mode: DiffTurn, TurnID: turn, Base: changes.Base, Head: changes.Head}, PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(added.Patch, "+during") {
		t.Fatalf("new file patch missing its content: %q", added.Patch)
	}

	for _, path := range []string{"after.txt", "new.txt", "../../etc/passwd"} {
		if _, err := mgr.ThreadFileDiff(ctx, id, path, DiffRequest{Mode: DiffTurn, TurnID: turn, Base: changes.Base, Head: changes.Head}, PatchOptions{}); err == nil {
			t.Fatalf("%s is not part of the turn and must be refused", path)
		}
	}
	if _, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffRequest{Mode: DiffTurn, TurnID: turn, Base: changes.Head, Head: changes.Base}, PatchOptions{}); err == nil {
		t.Fatal("a range that is not the turn's must be refused")
	}

	// The list a card seeds carries no range yet; the snapshots supply it.
	seeded, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffRequest{Mode: DiffTurn, TurnID: turn}, PatchOptions{})
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
		changes, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffTurn, TurnID: missing})
		if err != nil {
			t.Fatalf("%s: missing snapshots must not be an error: %v", missing, err)
		}
		if changes.Warning == "" || len(changes.Files) != 0 || changes.Head != "" {
			t.Fatalf("%s: expected an explained empty list: %+v", missing, changes)
		}
		if _, err := mgr.ThreadFileDiff(ctx, id, "keep.txt", DiffRequest{Mode: DiffTurn, TurnID: missing}, PatchOptions{}); err == nil {
			t.Fatalf("%s: a file diff with no turn range must be refused", missing)
		}
	}
}

func TestTurnChangesRefuseAnythingButATurnID(t *testing.T) {
	mgr, id, _ := turnFixture(t)
	for _, bad := range []string{"", "../base", "t1/base", "a:b", "HEAD~1", "a b", "x^{commit}"} {
		if _, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffTurn, TurnID: bad}); err == nil {
			t.Fatalf("%q must be refused as a turn id", bad)
		}
		if _, err := mgr.ThreadFileDiff(context.Background(), id, "keep.txt", DiffRequest{Mode: DiffTurn, TurnID: bad}, PatchOptions{}); err == nil {
			t.Fatalf("%q must be refused as a turn id for a file diff", bad)
		}
	}
}

// longFileFixture adds a committed forty-line file to the changes fixture,
// then edits its first line by whitespace alone and its thirtieth for real,
// so the two hunks are far enough apart to be told apart.
func longFileFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	mgr, id := changesFixture(t)
	meta, err := mgr.store.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %02d", i+1)
	}
	write(t, meta.Cwd, "long.txt", strings.Join(rows, "\n")+"\n")
	gitRun(t, meta.Cwd, "add", "long.txt")
	gitRun(t, meta.Cwd, "commit", "-m", "long file")
	rows[0] = "row 01   "
	rows[29] = "row thirty"
	write(t, meta.Cwd, "long.txt", strings.Join(rows, "\n")+"\n")
	return mgr, id
}

func filePatch(t *testing.T, mgr *Manager, id, path string, opts PatchOptions) string {
	t.Helper()
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffUncommitted})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := mgr.ThreadFileDiff(context.Background(), id, path, DiffRequest{Mode: DiffUncommitted, Base: changes.Base, Head: changes.Head}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return diff.Patch
}

func TestFileDiffIgnoringWhitespaceDropsAWhitespaceOnlyHunk(t *testing.T) {
	mgr, id := longFileFixture(t)
	if plain := filePatch(t, mgr, id, "long.txt", PatchOptions{}); !strings.Contains(plain, "+row 01   ") {
		t.Fatalf("the whitespace edit should show by default: %q", plain)
	}
	quiet := filePatch(t, mgr, id, "long.txt", PatchOptions{IgnoreWhitespace: true})
	if strings.Contains(quiet, "row 01") || !strings.Contains(quiet, "+row thirty") {
		t.Fatalf("only the real edit should remain: %q", quiet)
	}
	if untracked := filePatch(t, mgr, id, "untracked.txt", PatchOptions{IgnoreWhitespace: true, FullContext: true}); !strings.Contains(untracked, "+a") {
		t.Fatalf("options must not break an untracked file's patch: %q", untracked)
	}
}

func TestFileDiffWithFullContextShowsFarAwayLines(t *testing.T) {
	mgr, id := longFileFixture(t)
	const far = "\n row 15\n"
	if plain := filePatch(t, mgr, id, "long.txt", PatchOptions{}); strings.Contains(plain, far) {
		t.Fatalf("default context should not reach row 15: %q", plain)
	}
	if full := filePatch(t, mgr, id, "long.txt", PatchOptions{FullContext: true}); !strings.Contains(full, far) || !strings.Contains(full, "\n row 40\n") {
		t.Fatalf("full context should show the whole file: %q", full)
	}
}

func TestFileDiffWithFullContextKeepsTheChangesOfAFileTooBigToShowWhole(t *testing.T) {
	mgr, id := changesFixture(t)
	meta, err := mgr.store.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// Well past the patch cap in unchanged lines, with the one edit at the end.
	rows := make([]string, 30000)
	for i := range rows {
		rows[i] = fmt.Sprintf("unchanged row %05d", i)
	}
	write(t, meta.Cwd, "big.txt", strings.Join(rows, "\n")+"\n")
	gitRun(t, meta.Cwd, "add", "big.txt")
	gitRun(t, meta.Cwd, "commit", "-m", "big file")
	rows[len(rows)-1] = "the edit"
	write(t, meta.Cwd, "big.txt", strings.Join(rows, "\n")+"\n")

	full := filePatch(t, mgr, id, "big.txt", PatchOptions{FullContext: true})
	if !strings.Contains(full, "+the edit") || strings.Contains(full, "unchanged row 00000") {
		t.Fatalf("expected the edit with the usual context, got %d bytes", len(full))
	}
}

// findFixture adds a staged file and an untracked one whose text has regex
// metacharacters in it, on top of the changes fixture.
func findFixture(t *testing.T) (*Manager, string, ThreadChanges) {
	t.Helper()
	mgr, id := changesFixture(t)
	meta, err := mgr.store.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	write(t, meta.Cwd, "staged.txt", "Hello [World] 3x4 Needle\n")
	gitRun(t, meta.Cwd, "add", "staged.txt")
	write(t, meta.Cwd, "loose.txt", "a needle (in) a haystack\n")
	write(t, meta.Cwd, "loose.bin", "needle\x00")
	changes, err := mgr.ThreadChanges(context.Background(), id, DiffRequest{Mode: DiffBranch})
	if err != nil {
		t.Fatal(err)
	}
	return mgr, id, changes
}

func diffFind(t *testing.T, mgr *Manager, id string, changes ThreadChanges, query string) []string {
	t.Helper()
	paths, err := mgr.ThreadDiffFind(context.Background(), id, query, DiffRequest{Mode: DiffBranch, Base: changes.Base, Head: changes.Head})
	if err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	return paths
}

func TestDiffFindMatchesChangedLinesLiterallyAndIgnoringCase(t *testing.T) {
	mgr, id, changes := findFixture(t)
	cases := []struct {
		query string
		want  []string
	}{
		// Tracked and untracked hits, in the list's order, binary skipped.
		{"NEEDLE", []string{"staged.txt", "loose.txt"}},
		{"FOUR", []string{"keep.txt"}},
		// "one" is in keep.txt, but only as unchanged context.
		{"one", []string{}},
		{"[world]", []string{"staged.txt"}},
		{"(in)", []string{"loose.txt"}},
		// As a pattern this would match "3x4"; as text it does not.
		{"3.4", []string{}},
		{"  bye  ", []string{"gone.txt"}},
	}
	for _, c := range cases {
		got := diffFind(t, mgr, id, changes, c.query)
		if strings.Join(got, ",") != strings.Join(c.want, ",") || got == nil {
			t.Fatalf("%q: got %v, want %v", c.query, got, c.want)
		}
	}
}

func TestDiffFindRefusesBadQueriesAndStaleLists(t *testing.T) {
	mgr, id, changes := findFixture(t)
	ctx := context.Background()
	req := DiffRequest{Mode: DiffBranch, Base: changes.Base, Head: changes.Head}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 201), "a\nb", "a\x00b"} {
		if _, err := mgr.ThreadDiffFind(ctx, id, bad, req); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if _, err := mgr.ThreadDiffFind(ctx, id, strings.Repeat("é", 200), req); err != nil {
		t.Fatalf("the limit is in characters, not bytes: %v", err)
	}
	stale := DiffRequest{Mode: DiffBranch, Base: changes.Head, Head: changes.Head}
	if _, err := mgr.ThreadDiffFind(ctx, id, "needle", stale); err == nil {
		t.Fatal("a search against a list that has moved must be refused")
	}
}
