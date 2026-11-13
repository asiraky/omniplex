package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// runTurn sends prompt, has the harness answer it and finish the turn with
// ref as its anchor, and waits for the thread to be idle again.
func runTurn(t *testing.T, actor *Actor, fa *fakeAdapter, prompt, ref string) string {
	t.Helper()
	ctx := context.Background()
	res, err := actor.Prompt(ctx, prompt, nil)
	if err != nil || res.TurnID == "" {
		t.Fatalf("prompt %q = %+v, %v; want a turn", prompt, res, err)
	}
	<-fa.thread().prompts
	fa.thread().emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: res.TurnID, Role: "agent", Kind: "text", BlockID: "answer-" + res.TurnID, Delta: "answer to " + prompt,
	}))
	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn, HarnessRef: ref}))
	waitFor(t, func() bool {
		s, err := actor.State(ctx)
		return err == nil && s.Phase == "idle" && len(s.Turns) > 0 && s.Turns[len(s.Turns)-1].ID == res.TurnID && s.Turns[len(s.Turns)-1].Done
	})
	return res.TurnID
}

// rewoundEvents is every thread.rewound in the thread's log.
func rewoundEvents(t *testing.T, st *store.Store, threadID string) []proto.ThreadRewoundPayload {
	t.Helper()
	evs, err := st.ReadEvents(context.Background(), threadID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.ThreadRewoundPayload
	for _, ev := range evs {
		if ev.Type != proto.ThreadRewound {
			continue
		}
		var p proto.ThreadRewoundPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func turnIDs(s *projection.State) []string {
	var ids []string
	for _, t := range s.Turns {
		ids = append(ids, t.ID)
	}
	return ids
}

// A rewind asks the harness to forget from the chosen turn on, naming that
// turn's anchor and the one before it, and then cuts the transcript at the
// turn's opening prompt. Rewinding to the first turn has nothing before it.
func TestRewindCutsTheHarnessThenTheTranscript(t *testing.T) {
	actor, fa, st := newTestActorWith(t, &fakeAdapter{rewind: true})
	ctx := context.Background()
	t1 := runTurn(t, actor, fa, "one", "r1")
	t2 := runTurn(t, actor, fa, "two", "r2")
	runTurn(t, actor, fa, "three", "r3")

	before, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := before.RewindAt(t2)
	if at <= 0 {
		t.Fatalf("turn two's prompt is at %d", at)
	}

	if err := actor.Rewind(ctx, t2, false); err != nil {
		t.Fatal(err)
	}
	if got := fa.thread().rewindTargets(); len(got) != 1 || got[0] != (adapter.RewindTarget{Turn: "r2", Previous: "r1"}) {
		t.Fatalf("harness asked for %+v, want r2 after r1", got)
	}
	after, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ids := turnIDs(after); len(ids) != 1 || ids[0] != t1 {
		t.Fatalf("turns after the rewind = %v, want only %s", ids, t1)
	}
	if len(after.Items) != at {
		t.Fatalf("%d items left, want the %d before turn two", len(after.Items), at)
	}
	if got := rewoundEvents(t, st, actor.ID); len(got) != 1 || got[0] != (proto.ThreadRewoundPayload{TurnID: t2, At: at}) {
		t.Fatalf("logged %+v, want turn two cut at %d without files", got, at)
	}

	if err := actor.Rewind(ctx, t1, false); err != nil {
		t.Fatal(err)
	}
	if got := fa.thread().rewindTargets(); len(got) != 2 || got[1] != (adapter.RewindTarget{Turn: "r1"}) {
		t.Fatalf("harness asked for %+v, want r1 with nothing before it", got)
	}
	if s, _ := actor.State(ctx); len(s.Turns) != 0 || len(s.Items) != 0 {
		t.Fatalf("after rewinding to the first turn: %d turns, %d items", len(s.Turns), len(s.Items))
	}
}

// Everything in flight belongs to the conversation being cut, and a turn
// without an anchor gives the harness nothing to cut to. Each refusal leaves
// the harness unasked and the log untouched.
func TestRewindRefusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		fa    *fakeAdapter
		setup func(t *testing.T, a *Actor, fa *fakeAdapter) string
		want  string
	}{{
		name: "a harness that cannot rewind",
		fa:   &fakeAdapter{},
		setup: func(t *testing.T, a *Actor, fa *fakeAdapter) string {
			return runTurn(t, a, fa, "one", "r1")
		},
		want: "cannot rewind",
	}, {
		name: "a running turn",
		fa:   &fakeAdapter{rewind: true},
		setup: func(t *testing.T, a *Actor, fa *fakeAdapter) string {
			t1 := runTurn(t, a, fa, "one", "r1")
			if _, err := a.Prompt(ctx, "still going", nil); err != nil {
				t.Fatal(err)
			}
			<-fa.thread().prompts
			return t1
		},
		want: "wait for the turn",
	}, {
		name: "a running background job",
		fa:   &fakeAdapter{rewind: true},
		setup: func(t *testing.T, a *Actor, fa *fakeAdapter) string {
			t1 := runTurn(t, a, fa, "one", "r1")
			fa.thread().emit(proto.Emit(proto.JobStarted, proto.JobPayload{JobID: "sh", Kind: proto.JobShell, Status: proto.JobRunning}))
			waitFor(t, func() bool { s, _ := a.State(ctx); return len(s.Jobs) == 1 })
			return t1
		},
		want: "background work",
	}, {
		name: "a turn with no anchor",
		fa:   &fakeAdapter{rewind: true},
		setup: func(t *testing.T, a *Actor, fa *fakeAdapter) string {
			return runTurn(t, a, fa, "one", "")
		},
		want: "predates rewind support",
	}, {
		name: "a turn after one with no anchor",
		fa:   &fakeAdapter{rewind: true},
		setup: func(t *testing.T, a *Actor, fa *fakeAdapter) string {
			runTurn(t, a, fa, "one", "")
			return runTurn(t, a, fa, "two", "r2")
		},
		want: "the turn before this one",
	}, {
		name: "a turn the thread does not have",
		fa:   &fakeAdapter{rewind: true},
		setup: func(t *testing.T, a *Actor, fa *fakeAdapter) string {
			runTurn(t, a, fa, "one", "r1")
			return "nope"
		},
		want: "no turn",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actor, fa, st := newTestActorWith(t, tc.fa)
			turn := tc.setup(t, actor, fa)
			head := actor.Head()
			err := actor.Rewind(ctx, turn, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("rewind = %v, want an error saying %q", err, tc.want)
			}
			if got := fa.thread().rewindTargets(); len(got) != 0 {
				t.Fatalf("the harness was asked to rewind: %+v", got)
			}
			if actor.Head() != head || len(rewoundEvents(t, st, actor.ID)) != 0 {
				t.Fatal("a refused rewind wrote to the log")
			}
		})
	}
}

// A thread that is not a managed worktree, here not even a checkout, has its
// files restore refused before the harness is asked.
func TestRewindRefusesARestoreTheThreadIsNotAllowed(t *testing.T) {
	actor, fa, st := newTestActorWith(t, &fakeAdapter{rewind: true})
	t1 := runTurn(t, actor, fa, "one", "r1")
	if err := actor.Rewind(context.Background(), t1, true); err == nil {
		t.Fatal("a restore outside a managed worktree was accepted")
	}
	if len(fa.thread().rewindTargets()) != 0 || len(rewoundEvents(t, st, actor.ID)) != 0 {
		t.Fatal("the refused restore still rewound")
	}
}

// A harness that refuses leaves the conversation where it was: no event, no
// files touched, no safety snapshot either.
func TestRewindTheHarnessRefusedChangesNothing(t *testing.T) {
	rt := newRestoreThread(t, &fakeAdapter{rewind: true, rewindErr: errors.New("harness said no")})
	t1 := runTurn(t, rt.actor, rt.fa, "one", "r1")
	write(t, rt.dir, "made.txt", "by the turn\n")

	err := rt.actor.Rewind(context.Background(), t1, true)
	if err == nil || !strings.Contains(err.Error(), "harness said no") {
		t.Fatalf("rewind = %v, want the harness's refusal", err)
	}
	if s, _ := rt.actor.State(context.Background()); len(s.Turns) != 1 {
		t.Fatalf("turns = %v, want the turn kept", turnIDs(s))
	}
	if len(rewoundEvents(t, rt.st, rt.actor.ID)) != 0 {
		t.Fatal("a refused rewind was logged")
	}
	if _, err := os.Stat(filepath.Join(rt.dir, "made.txt")); err != nil {
		t.Fatalf("the turn's file went: %v", err)
	}
	if checkpointExists(context.Background(), rt.dir, checkpointRef(rt.actor.ID, t1, "pre-rewind")) {
		t.Fatal("a safety snapshot was taken for a restore that never happened")
	}
}

// While the harness is rewinding, a prompt waits rather than starting a turn
// the rewind would cut, and what starts turns without waiting is turned away.
// Once the rewind lands, the waiting prompt runs after it.
func TestRewindHoldsNewTurnsUntilItLands(t *testing.T) {
	gate := make(chan struct{})
	actor, fa, st := newTestActorWith(t, &fakeAdapter{rewind: true, rewindGate: gate})
	ctx := context.Background()
	t1 := runTurn(t, actor, fa, "one", "r1")

	done := make(chan error, 1)
	go func() { done <- actor.Rewind(ctx, t1, false) }()
	waitFor(t, func() bool { return len(fa.thread().rewindTargets()) == 1 })

	res, err := actor.Prompt(ctx, "edited", nil)
	if err != nil || !res.Queued() {
		t.Fatalf("prompt during the rewind = %+v, %v; want it queued", res, err)
	}
	if _, err := actor.RunComposerAction(ctx, "review", "", "/review"); !errors.Is(err, errRewinding) {
		t.Fatalf("composer action during the rewind = %v", err)
	}
	if _, err := actor.Continue(ctx); !errors.Is(err, errRewinding) {
		t.Fatalf("continue during the rewind = %v", err)
	}
	if err := actor.Rewind(ctx, t1, false); err == nil {
		t.Fatal("a second rewind ran alongside the first")
	}

	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if in := nextInput(t, fa.thread()); in.Text != "edited" {
		t.Fatalf("after the rewind the harness got %+v", in)
	}
	waitFor(t, func() bool { s, _ := actor.State(ctx); return len(s.Turns) == 1 && s.Turns[0].Prompt == "edited" })

	evs, err := st.ReadEvents(ctx, actor.ID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	rewound, started := -1, -1
	for i, ev := range evs {
		switch {
		case ev.Type == proto.ThreadRewound:
			rewound = i
		case ev.Type == proto.TurnStarted && strings.Contains(string(ev.Payload), "edited"):
			started = i
		}
	}
	if rewound < 0 || started < rewound {
		t.Fatalf("thread.rewound at %d, the edited prompt's turn at %d; want the turn after", rewound, started)
	}
}

// restoreThread is a managed thread working in a real worktree, on a fake
// harness, so a rewind can restore files.
type restoreThread struct {
	actor *Actor
	fa    *fakeAdapter
	st    *store.Store
	dir   string
}

func newRestoreThread(t *testing.T, fa *fakeAdapter) restoreThread {
	t.Helper()
	ctx := context.Background()
	_, worktree, _ := gitRepo(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "rewind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	meta := store.ThreadMeta{ID: "rw-1", Cwd: worktree, Harness: "fake", Phase: "idle", WorkspaceMode: "managed"}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	actor, err := Start(ctx, st, fa, meta, "", "", nil, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { actor.Dispose("test over") })
	waitFor(t, func() bool { return actor.Head() >= 1 })
	return restoreThread{actor: actor, fa: fa, st: st, dir: worktree}
}

// The whole path: the harness rewinds, the files go back to how they were
// when the turn started, and the event says they did.
func TestRewindRestoresTheFiles(t *testing.T) {
	rt := newRestoreThread(t, &fakeAdapter{rewind: true})
	write(t, rt.dir, "README", "before the turn\n")
	t1 := runTurn(t, rt.actor, rt.fa, "one", "r1")
	// What turn one did, landing after its baseline: done by hand, since the
	// fake harness writes nothing.
	write(t, rt.dir, "README", "edited by the turn\n")
	write(t, rt.dir, "new.txt", "made by the turn\n")

	if err := rt.actor.Rewind(context.Background(), t1, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(rt.dir, "README")); string(got) != "before the turn\n" {
		t.Fatalf("README = %q", got)
	}
	if _, err := os.Stat(filepath.Join(rt.dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt is still there: %v", err)
	}
	if got := rewoundEvents(t, rt.st, rt.actor.ID); len(got) != 1 || !got[0].RestoredFiles || got[0].TurnID != t1 {
		t.Fatalf("logged %+v, want turn one rewound with its files", got)
	}
}

// A restore that fails once the harness has rewound cannot take the rewind
// back: the log follows the harness, says the files were not restored, and so
// does the error.
func TestRewindWhoseRestoreFailsIsStillRecorded(t *testing.T) {
	fa := &fakeAdapter{rewind: true}
	rt := newRestoreThread(t, fa)
	t1 := runTurn(t, rt.actor, fa, "one", "r1")
	// The snapshot vanishes while the harness rewinds, after the check that
	// it was there.
	base := checkpointRef(rt.actor.ID, t1, "base")
	fa.thread().onRewind = func() { gitRun(t, rt.dir, "update-ref", "-d", base) }
	write(t, rt.dir, "new.txt", "made by the turn\n")

	err := rt.actor.Rewind(context.Background(), t1, true)
	if err == nil || !strings.Contains(err.Error(), "rewound, but the files were not restored") {
		t.Fatalf("rewind = %v, want it to say the files were not restored", err)
	}
	if got := rewoundEvents(t, rt.st, rt.actor.ID); len(got) != 1 || got[0].RestoredFiles {
		t.Fatalf("logged %+v, want the rewind without its files", got)
	}
	if s, _ := rt.actor.State(context.Background()); len(s.Turns) != 0 {
		t.Fatalf("turns = %v, want none", turnIDs(s))
	}
	if _, err := os.Stat(filepath.Join(rt.dir, "new.txt")); err != nil {
		t.Fatalf("the files were touched: %v", err)
	}
}

// Restoring puts back exactly the files the base snapshot had — added ones
// deleted, edited ones reverted, deleted ones returned, untracked ones too —
// and leaves ignored files and the person's own index alone. The checkout as
// it was is kept under pre-rewind.
func TestRestoreCheckpointPutsBackExactlyTheBase(t *testing.T) {
	ctx := context.Background()
	_, wt, _ := gitRepo(t)
	write(t, wt, ".gitignore", "*.log\n")
	write(t, wt, "tracked.txt", "one\n")
	write(t, wt, "doomed.txt", "keep me\n")
	write(t, wt, "dir/file.sh", "#!/bin/sh\n")
	gitRun(t, wt, "add", ".")
	gitRun(t, wt, "commit", "-m", "base files")
	write(t, wt, "untracked.txt", "loose\n")
	write(t, wt, "staged.txt", "staged\n")
	gitRun(t, wt, "add", "staged.txt")
	write(t, wt, "build.log", "old log\n")

	if _, err := captureCheckpoint(ctx, wt, checkpointRef("th", "t1", "base")); err != nil {
		t.Fatal(err)
	}

	// The turn.
	write(t, wt, "tracked.txt", "two\n")
	write(t, wt, "untracked.txt", "tightened\n")
	if err := os.Remove(filepath.Join(wt, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(wt, "dir/file.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, wt, "added.txt", "new\n")
	write(t, wt, "deep/er/added.txt", "new and nested\n")
	write(t, wt, "build.log", "new log\n")

	indexPath := gitOut(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	cachedBefore := gitOut(t, wt, "diff", "--cached", "--name-status")

	if err := restoreCheckpoint(ctx, wt, "th", "t1"); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"tracked.txt": "one\n", "doomed.txt": "keep me\n", "untracked.txt": "loose\n",
		"staged.txt": "staged\n", "build.log": "new log\n",
	} {
		if got, err := os.ReadFile(filepath.Join(wt, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, gone := range []string{"added.txt", "deep"} {
		if _, err := os.Stat(filepath.Join(wt, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if info, err := os.Stat(filepath.Join(wt, "dir/file.sh")); err != nil || info.Mode().Perm()&0o111 != 0 {
		t.Errorf("dir/file.sh mode = %v, %v; want it back to not executable", info.Mode(), err)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Error("the person's index was rewritten")
	}
	if got := gitOut(t, wt, "diff", "--cached", "--name-status"); got != cachedBefore {
		t.Errorf("staged changes went from %q to %q", cachedBefore, got)
	}
	// The safety snapshot holds the checkout as it was before the restore.
	pre := checkpointRef("th", "t1", "pre-rewind")
	if got := gitOut(t, wt, "show", pre+":added.txt"); got != "new" {
		t.Errorf("pre-rewind added.txt = %q", got)
	}
	if got := gitOut(t, wt, "ls-tree", "--name-only", pre, "doomed.txt"); got != "" {
		t.Errorf("pre-rewind still has the file the turn deleted: %q", got)
	}
}

// A turn whose baseline was never taken has nothing to restore to, and that
// is said before anything is touched.
func TestRestoreCheckpointNeedsTheBase(t *testing.T) {
	ctx := context.Background()
	_, wt, _ := gitRepo(t)
	write(t, wt, "added.txt", "new\n")
	if err := restoreCheckpoint(ctx, wt, "th", "t1"); !errors.Is(err, errNoBaseCheckpoint) {
		t.Fatalf("restore = %v, want errNoBaseCheckpoint", err)
	}
	if checkpointExists(ctx, wt, checkpointRef("th", "t1", "pre-rewind")) {
		t.Fatal("a safety snapshot was taken for nothing")
	}
	if _, err := os.Stat(filepath.Join(wt, "added.txt")); err != nil {
		t.Fatal(err)
	}
}

// Files are restored only in a thread's own managed worktree, and only while
// no other open thread works in it, under it, or above it.
func TestRestoreBlockedByASharedCheckout(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "project", ".worktrees", "mine")
	if err := os.MkdirAll(filepath.Join(wt, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	self := store.ThreadMeta{ID: "me", Cwd: wt, WorkspaceMode: "managed", Phase: "idle"}
	cases := []struct {
		name    string
		self    store.ThreadMeta
		other   store.ThreadMeta
		blocked bool
	}{
		{"the same folder", self, store.ThreadMeta{ID: "o", Cwd: wt, Phase: "idle"}, true},
		{"a folder inside it", self, store.ThreadMeta{ID: "o", Cwd: filepath.Join(wt, "pkg"), Phase: "turn"}, true},
		{"a plain folder holding it", self, store.ThreadMeta{ID: "o", Cwd: base, Phase: "idle"}, true},
		{"the project checkout holding it", self, store.ThreadMeta{ID: "o", Cwd: filepath.Join(base, "project"), WorkspaceMode: "local", Phase: "idle"}, false},
		{"a closed thread", self, store.ThreadMeta{ID: "o", Cwd: wt, Phase: "closed"}, false},
		{"an unrelated folder", self, store.ThreadMeta{ID: "o", Cwd: filepath.Join(base, "project", ".worktrees", "minefield"), Phase: "idle"}, false},
		{"a managed thread still being made", self, store.ThreadMeta{ID: "o", Cwd: filepath.Join(base, "project"), WorkspaceMode: "managed", Phase: "provisioning"}, false},
		{"a thread with no folder", self, store.ThreadMeta{ID: "o", Phase: "idle"}, false},
		{"a thread outside a managed worktree", store.ThreadMeta{ID: "me", Cwd: wt, WorkspaceMode: "local", Phase: "idle"}, store.ThreadMeta{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			threads := []store.ThreadMeta{tc.self}
			if tc.other.ID != "" {
				threads = append(threads, tc.other)
			}
			// The project folder and the worktree are each a checkout of
			// their own; the folder above both is not one.
			checkoutOf := func(dir string) string {
				switch {
				case dir == wt || within(dir, wt):
					return wt
				case dir == filepath.Join(base, "project") || within(dir, filepath.Join(base, "project")):
					return filepath.Join(base, "project")
				}
				return ""
			}
			err := restoreBlocked(tc.self, wt, threads, checkoutOf)
			if (err != nil) != tc.blocked {
				t.Fatalf("restoreBlocked = %v, want blocked %v", err, tc.blocked)
			}
		})
	}
}

// The restore job waits its turn behind the snapshot of the turn that just
// ended, so that snapshot is of the turn's work, not of the restore.
func TestRestoreRunsAfterThePendingTurnSnapshot(t *testing.T) {
	ctx := context.Background()
	_, wt, _ := gitRepo(t)
	emitted := make(chan proto.Emission, 4)
	cp := newCheckpointer(wt, "th", func(em proto.Emission) { emitted <- em }, func(string, ...any) {})
	t.Cleanup(cp.stop)
	if !cp.baseline(ctx, "t1") {
		t.Fatal("no baseline")
	}
	write(t, wt, "made.txt", "by the turn\n")
	cp.turnEnded("t1")
	if err := cp.restore(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	select {
	case em := <-emitted:
		p := em.Payload.(proto.TurnDiffPayload)
		if len(p.Files) != 1 || p.Files[0].Path != "made.txt" {
			t.Fatalf("the turn's card = %+v, want made.txt", p.Files)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn's card never came")
	}
	if _, err := os.Stat(filepath.Join(wt, "made.txt")); !os.IsNotExist(err) {
		t.Fatalf("made.txt survived the restore: %v", err)
	}
}
