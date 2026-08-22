package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func fileByPath(t *testing.T, files []ChangedFile, path string) ChangedFile {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no %q in %v", path, files)
	return ChangedFile{}
}

// A turn's diff has to cover everything the turn did, whether or not the
// harness ever staged or committed it.
func TestDiffCheckpointsCoversEveryKindOfChange(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)
	write(t, worktree, "keep.txt", "one\ntwo\n")
	write(t, worktree, "gone.txt", "bye\n")
	write(t, worktree, "old-name.txt", "same\n")
	gitRun(t, worktree, "add", ".")
	gitRun(t, worktree, "commit", "-m", "before the turn")

	before, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "base"))
	if err != nil {
		t.Fatal(err)
	}

	// What a turn might do: edit without staging, create a file git has never
	// seen, delete, rename, and commit something.
	write(t, worktree, "keep.txt", "one\ntwo\nthree\n")
	write(t, worktree, "brand-new.txt", "a\nb\n")
	gitRun(t, worktree, "rm", "-q", "gone.txt")
	gitRun(t, worktree, "mv", "old-name.txt", "new-name.txt")
	write(t, worktree, "committed.txt", "c\n")
	gitRun(t, worktree, "add", "committed.txt")
	gitRun(t, worktree, "commit", "-m", "during the turn")

	after, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "end"))
	if err != nil {
		t.Fatal(err)
	}

	changes, err := diffCheckpoints(ctx, worktree, before, after)
	if err != nil {
		t.Fatal(err)
	}

	if got := fileByPath(t, changes.Files, "keep.txt"); got.Status != "modified" || got.Additions != 1 {
		t.Errorf("unstaged edit: got %+v, want modified +1", got)
	}
	if got := fileByPath(t, changes.Files, "brand-new.txt"); got.Status != "added" || got.Additions != 2 {
		t.Errorf("untracked file: got %+v, want added +2", got)
	}
	if got := fileByPath(t, changes.Files, "gone.txt"); got.Status != "deleted" {
		t.Errorf("delete: got %+v, want deleted", got)
	}
	if got := fileByPath(t, changes.Files, "new-name.txt"); got.Status != "renamed" || got.OldPath != "old-name.txt" {
		t.Errorf("rename: got %+v, want renamed from old-name.txt", got)
	}
	if got := fileByPath(t, changes.Files, "committed.txt"); got.Status != "added" {
		t.Errorf("committed file: got %+v, want added", got)
	}
	if changes.Additions == 0 || changes.Deletions == 0 {
		t.Errorf("totals should count both sides: %+v", changes)
	}
}

// A snapshot is the server's bookkeeping. It must leave no trace in anything
// the person looks at: not their index, not their branch, not their status.
func TestCaptureCheckpointLeavesTheWorkingStateAlone(t *testing.T) {
	ctx := context.Background()
	_, worktree, branch := gitRepo(t)
	write(t, worktree, "staged.txt", "s\n")
	write(t, worktree, "unstaged.txt", "u\n")
	gitRun(t, worktree, "add", "staged.txt")

	statusBefore := gitOut(t, worktree, "status", "--porcelain")
	headBefore := gitOut(t, worktree, "rev-parse", "HEAD")

	if _, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "base")); err != nil {
		t.Fatal(err)
	}

	if got := gitOut(t, worktree, "status", "--porcelain"); got != statusBefore {
		t.Errorf("status changed:\nbefore %q\nafter  %q", statusBefore, got)
	}
	if got := gitOut(t, worktree, "rev-parse", "HEAD"); got != headBefore {
		t.Error("HEAD moved")
	}
	if got := gitOut(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Errorf("branch is %q, want %q", got, branch)
	}
	// The snapshot must not show up as a branch or in the log.
	if got := gitOut(t, worktree, "branch", "--list"); strings.Contains(got, "checkpoint") {
		t.Errorf("checkpoint is visible as a branch: %q", got)
	}
}

// A repository with no commits still holds work, and a first turn in one must
// not fail just because there is no HEAD to read the index from.
func TestCaptureCheckpointOnRepoWithNoCommits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	write(t, dir, "first.txt", "hello\n")

	before, err := captureCheckpoint(ctx, dir, checkpointRef("s1", "t1", "base"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "second.txt", "world\n")
	after, err := captureCheckpoint(ctx, dir, checkpointRef("s1", "t1", "end"))
	if err != nil {
		t.Fatal(err)
	}

	changes, err := diffCheckpoints(ctx, dir, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path != "second.txt" {
		t.Fatalf("got %+v, want only second.txt", changes.Files)
	}
}

// Ignored files are not the turn's work, and a checkout carrying a node_modules
// would otherwise make every snapshot enormous.
func TestCaptureCheckpointSkipsIgnoredFiles(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)
	write(t, worktree, ".gitignore", "junk/\n")
	gitRun(t, worktree, "add", ".gitignore")
	gitRun(t, worktree, "commit", "-m", "ignore junk")

	before, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "base"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, worktree, "junk/big.bin", "noise\n")
	write(t, worktree, "real.txt", "work\n")
	after, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "end"))
	if err != nil {
		t.Fatal(err)
	}

	changes, err := diffCheckpoints(ctx, worktree, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Files) != 1 || changes.Files[0].Path != "real.txt" {
		t.Fatalf("got %+v, want only real.txt", changes.Files)
	}
}

// A turn that changed nothing gets no card, and the empty list has to be an
// empty list rather than a nil the wire renders as null.
func TestDiffCheckpointsWithNoChanges(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)
	before, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "base"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "end"))
	if err != nil {
		t.Fatal(err)
	}
	changes, err := diffCheckpoints(ctx, worktree, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Files == nil {
		t.Fatal("Files is nil; the wire needs an empty list")
	}
	if len(changes.Files) != 0 {
		t.Fatalf("got %+v, want nothing", changes.Files)
	}
}

func TestDropCheckpointsRemovesOnlyThisSession(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)
	for _, ref := range []string{
		checkpointRef("keep-me", "t1", "base"),
		checkpointRef("drop-me", "t1", "base"),
		checkpointRef("drop-me", "t2", "end"),
	} {
		if _, err := captureCheckpoint(ctx, worktree, ref); err != nil {
			t.Fatal(err)
		}
	}

	if err := dropCheckpoints(ctx, worktree, "drop-me"); err != nil {
		t.Fatal(err)
	}

	remaining := gitOut(t, worktree, "for-each-ref", "--format=%(refname)", checkpointRefPrefix+"/")
	if strings.Contains(remaining, "drop-me") {
		t.Errorf("drop-me survived: %q", remaining)
	}
	if !strings.Contains(remaining, "keep-me") {
		t.Errorf("keep-me was taken with it: %q", remaining)
	}
}

// The scratch index is a temp file, and a server that runs for weeks must not
// leave one behind per turn.
func TestCaptureCheckpointCleansUpItsScratchIndex(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)

	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "omniplex-checkpoint-index-*"))
	if _, err := captureCheckpoint(ctx, worktree, checkpointRef("s1", "t1", "base")); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "omniplex-checkpoint-index-*"))

	if len(after) > len(before) {
		t.Errorf("left %d scratch index files behind", len(after)-len(before))
	}
}

// The whole seam, end to end: a session in a real checkout, a turn that writes
// files, and a card on the turn that says what it changed.
func TestTurnDiffLandsOnTheTurnItMeasured(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "turndiff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	t.Cleanup(mgr.Shutdown)
	actor, err := mgr.Create(ctx, "fake", "", worktree, "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })

	turnID, err := actor.Prompt(ctx, "do some work", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts

	// What the harness does to the checkout, done directly: the point of
	// snapshots is that they see changes no tool call reported.
	write(t, worktree, "written.txt", "one\ntwo\n")
	write(t, worktree, "README", "replaced\n")

	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: turnID, StopReason: proto.StopEndTurn,
	}))

	var diff *proto.TurnDiffPayload
	waitFor(t, func() bool {
		state, err := actor.State(ctx)
		if err != nil {
			return false
		}
		for _, turn := range state.Turns {
			if turn.ID == turnID && turn.Diff != nil {
				diff = turn.Diff
				return true
			}
		}
		return false
	})

	if diff.Error != "" {
		t.Fatalf("measuring the turn failed: %s", diff.Error)
	}
	if len(diff.Files) != 2 {
		t.Fatalf("got %+v, want written.txt and README", diff.Files)
	}
	if got := fileByPath(t, diff.Files, "written.txt"); got.Status != "added" || got.Additions != 2 {
		t.Errorf("new file: got %+v, want added +2", got)
	}
	if got := fileByPath(t, diff.Files, "README"); got.Status != "modified" {
		t.Errorf("edited file: got %+v, want modified", got)
	}
	if diff.Additions != 3 || diff.Deletions != 1 {
		t.Errorf("totals = +%d/-%d, want +3/-1", diff.Additions, diff.Deletions)
	}

	// The card is rendered from the state a presenter is sent, so the diff has
	// to survive the wire, not just the projection.
	state, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"diff":{"turnId"`) {
		t.Errorf("no turn diff on the wire: %s", wire)
	}
	if !strings.Contains(string(wire), `"path":"written.txt"`) {
		t.Errorf("the file list did not survive the wire: %s", wire)
	}
}

// Each turn is measured against a baseline of its own, taken when it starts.
// Anything changed between turns — by a person, an editor, a build — belongs to
// nobody, and must not be billed to the turn that happens to come next.
func TestTurnDiffIgnoresChangesMadeBetweenTurns(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "between.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	t.Cleanup(mgr.Shutdown)
	actor, err := mgr.Create(ctx, "fake", "", worktree, "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })

	runTurn := func(prompt string, work func()) string {
		t.Helper()
		turnID, err := actor.Prompt(ctx, prompt, nil)
		if err != nil {
			t.Fatal(err)
		}
		<-fa.session().prompts
		work()
		fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turnID, StopReason: proto.StopEndTurn,
		}))
		waitFor(t, func() bool {
			state, _ := actor.State(ctx)
			for _, turn := range state.Turns {
				if turn.ID == turnID && turn.Done {
					return true
				}
			}
			return false
		})
		return turnID
	}

	first := runTurn("first", func() { write(t, worktree, "agent-one.txt", "a\n") })
	waitFor(t, func() bool { return turnDiffOf(t, actor, first) != nil })

	// A person edits the checkout while nothing is running.
	write(t, worktree, "typed-by-hand.txt", "mine\n")

	second := runTurn("second", func() { write(t, worktree, "agent-two.txt", "b\n") })
	waitFor(t, func() bool { return turnDiffOf(t, actor, second) != nil })

	diff := turnDiffOf(t, actor, second)
	for _, f := range diff.Files {
		if f.Path == "typed-by-hand.txt" {
			t.Errorf("the second turn was blamed for an edit made between turns: %+v", diff.Files)
		}
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "agent-two.txt" {
		t.Fatalf("got %+v, want only agent-two.txt", diff.Files)
	}
}

func turnDiffOf(t *testing.T, actor *Actor, turnID string) *proto.TurnDiffPayload {
	t.Helper()
	state, err := actor.State(context.Background())
	if err != nil {
		return nil
	}
	for _, turn := range state.Turns {
		if turn.ID == turnID {
			return turn.Diff
		}
	}
	return nil
}

// Snapshots are refs in the repository the worktree belongs to, so they outlive
// the worktree. Cleanup has to take them with it.
func TestPurgeCheckpointsClearsASessionsRefs(t *testing.T) {
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)
	if _, err := captureCheckpoint(ctx, worktree, checkpointRef("gone", "t1", "base")); err != nil {
		t.Fatal(err)
	}

	purgeCheckpoints(ctx, worktree, "gone", func(string, ...any) {})

	if got := gitOut(t, worktree, "for-each-ref", "--format=%(refname)", checkpointRefPrefix+"/"); got != "" {
		t.Errorf("refs survived the purge: %q", got)
	}
}

// A session that is not in a repository still runs; it just has no cards.
func TestNoCheckpointsOutsideARepository(t *testing.T) {
	ctx := context.Background()
	actor, fa, _ := newTestActor(t) // cwd is a bare temp dir
	if actor.checkpoints != nil {
		t.Fatal("checkpointing started outside a repository")
	}

	turnID, err := actor.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: turnID, StopReason: proto.StopEndTurn,
	}))

	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		for _, turn := range state.Turns {
			if turn.ID == turnID && turn.Done {
				return true
			}
		}
		return false
	})
	state, _ := actor.State(ctx)
	for _, turn := range state.Turns {
		if turn.Diff != nil {
			t.Fatalf("got a card without a repository: %+v", turn.Diff)
		}
	}
}
