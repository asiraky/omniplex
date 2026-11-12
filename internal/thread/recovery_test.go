package thread

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// ---- helpers ----

// scriptedAdapter is a fakeAdapter whose sessions consult refuse before each
// prompt and record every prompt they were asked to send, refused or not. It
// lets a test make a resumed harness refuse from its very first prompt, which
// the manager sends from a goroutine before the test could reach the session.
type scriptedAdapter struct {
	*fakeAdapter
	mu     sync.Mutex
	refuse func(in adapter.PromptInput) error
	calls  []adapter.PromptInput
}

func (s *scriptedAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	sess, err := s.fakeAdapter.CreateSession(ctx, host, o)
	if err != nil {
		return nil, err
	}
	ft, ok := sess.(*fakeThread)
	if !ok {
		return sess, nil
	}
	return &scriptedThread{fakeThread: ft, owner: s}, nil
}

func (s *scriptedAdapter) script(refuse func(in adapter.PromptInput) error) {
	s.mu.Lock()
	s.refuse = refuse
	s.calls = nil
	s.mu.Unlock()
}

// callsWhere counts the prompts seen since the last script that match keep.
func (s *scriptedAdapter) callsWhere(keep func(in adapter.PromptInput) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, in := range s.calls {
		if keep(in) {
			n++
		}
	}
	return n
}

type scriptedThread struct {
	*fakeThread
	owner *scriptedAdapter
}

func (t *scriptedThread) Prompt(ctx context.Context, in adapter.PromptInput) error {
	t.owner.mu.Lock()
	refuse := t.owner.refuse
	t.owner.calls = append(t.owner.calls, in)
	t.owner.mu.Unlock()
	if refuse != nil {
		if err := refuse(in); err != nil {
			return err
		}
	}
	return t.fakeThread.Prompt(ctx, in)
}

func isRestartContinuation(in adapter.PromptInput) bool {
	return strings.HasPrefix(in.Text, restartPrompt)
}

func openRecoveryStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// startRecoveryThread creates a thread on a fresh manager. The caller shuts
// the manager down: that is the restart under test.
func startRecoveryThread(t *testing.T, st *store.Store, ad adapter.Adapter) (*Manager, *Actor) {
	t.Helper()
	mgr := NewManager(st, func(string, ...any) {}, ad)
	a, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return a.Head() >= 1 })
	return mgr, a
}

func rowPhase(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	meta, err := st.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return meta.Phase
}

func waitRowPhase(t *testing.T, st *store.Store, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rowPhase(t, st, id) == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("row phase = %q; want %q", rowPhase(t, st, id), want)
}

func recvPrompt(t *testing.T, ch <-chan adapter.PromptInput) adapter.PromptInput {
	t.Helper()
	select {
	case in := <-ch:
		return in
	case <-time.After(5 * time.Second):
		t.Fatal("no prompt reached the harness within 5s")
	}
	return adapter.PromptInput{}
}

func noPrompt(t *testing.T, ch <-chan adapter.PromptInput, wait time.Duration) {
	t.Helper()
	select {
	case in := <-ch:
		t.Fatalf("unexpected prompt reached the harness: %q", in.Text)
	case <-time.After(wait):
	}
}

func startJob(fa *fakeAdapter, id, kind, name string) {
	fa.thread().emit(proto.Emit(proto.JobStarted, proto.JobPayload{
		JobID: id, Kind: kind, Name: name, Status: proto.JobRunning,
	}))
}

func waitJobs(t *testing.T, a *Actor, n int) {
	t.Helper()
	waitFor(t, func() bool {
		s, err := a.State(context.Background())
		return err == nil && len(s.Jobs) == n
	})
}

// deadLines is the "- ..." list a dead-work note carries, in order.
func deadLines(note string) []string {
	var out []string
	for _, line := range strings.Split(note, "\n") {
		if strings.HasPrefix(line, "- ") {
			out = append(out, line)
		}
	}
	return out
}

func lastTurnOf(s *projection.State) projection.Turn {
	return s.Turns[len(s.Turns)-1]
}

// ---- end to end: background work across a restart ----

// The issue's "done when": an idle thread waiting on a subagent is woken by
// the next start and told the subagent died, without anyone attaching.
func TestRestartWakesIdleThreadWaitingOnASubagent(t *testing.T) {
	st := openRecoveryStore(t)
	fa := &fakeAdapter{}
	mgr, actor := startRecoveryThread(t, st, fa)
	ctx := context.Background()

	if _, err := actor.Prompt(ctx, "explore the repo", nil); err != nil {
		t.Fatal(err)
	}
	first := recvPrompt(t, fa.thread().prompts)
	startJob(fa, "j1", proto.JobAgent, "map the auth flow")
	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool {
		s, err := actor.State(ctx)
		return err == nil && s.Phase == "idle" && s.LiveJobs().Agents == 1
	})
	id := actor.ID
	mgr.Shutdown()

	if got := rowPhase(t, st, id); got != "background" {
		t.Fatalf("row phase after shutdown with a live subagent = %q; want background", got)
	}

	mgr2 := NewManager(st, func(string, ...any) {}, fa)
	defer mgr2.Shutdown()
	mgr2.recoverAll(ctx)

	in := recvPrompt(t, fa.thread().prompts)
	if !strings.Contains(in.Text, "- agent: map the auth flow") {
		t.Fatalf("wake-up prompt = %q; want it to name the dead subagent", in.Text)
	}
	// The turn was not cut, so the agent is not told its turn was.
	if strings.Contains(in.Text, restartPrompt) {
		t.Fatalf("wake-up prompt for an idle thread claims a cut turn: %q", in.Text)
	}

	resumed, ok := mgr2.Peek(id)
	if !ok {
		t.Fatal("thread was not brought back")
	}
	waitFor(t, func() bool {
		s, err := resumed.State(ctx)
		return err == nil && len(s.Turns) == 2
	})
	s, _ := resumed.State(ctx)
	woke := s.Turns[1]
	if woke.ID != in.TurnID {
		t.Fatalf("harness was prompted for turn %q; log started %q", in.TurnID, woke.ID)
	}
	if woke.Recovery == nil || woke.Recovery.Cause != proto.RecoveryRestart || woke.Recovery.Attempt != 1 {
		t.Fatalf("wake-up turn recovery = %+v; want restart attempt 1", woke.Recovery)
	}
	if woke.Recovery.ResumeOf != s.Turns[0].ID {
		t.Fatalf("wake-up resumes %q; want %q", woke.Recovery.ResumeOf, s.Turns[0].ID)
	}
	// The first turn ended cleanly before the restart; waking the thread
	// must not rewrite it as interrupted.
	if s.Turns[0].StopReason != proto.StopEndTurn {
		t.Fatalf("finished turn rewritten: %+v", s.Turns[0])
	}
	if s.Title != "explore the repo" {
		t.Fatalf("title = %q; want the human prompt", s.Title)
	}
	waitFor(t, func() bool {
		s, err := resumed.State(ctx)
		return err == nil && !s.LiveJobs().Any()
	})
}

// The row is kept in step with live jobs as they come and go, so a kill -9
// (no shutdown hook) leaves the same evidence a clean shutdown does.
func TestRowPhaseTracksLiveBackgroundWork(t *testing.T) {
	st := openRecoveryStore(t)
	fa := &fakeAdapter{}
	mgr, actor := startRecoveryThread(t, st, fa)
	defer mgr.Shutdown()

	waitRowPhase(t, st, actor.ID, "idle")

	startJob(fa, "a1", proto.JobAgent, "survey")
	waitRowPhase(t, st, actor.ID, "background")

	// A second live job, then the first finishing, leaves work outstanding.
	startJob(fa, "m1", proto.JobMonitor, "watch CI")
	fa.thread().emit(proto.Emit(proto.JobFinished, proto.JobPayload{JobID: "a1", Status: proto.JobCompleted}))
	waitFor(t, func() bool {
		s, err := actor.State(context.Background())
		return err == nil && s.LiveJobs().Agents == 0 && s.LiveJobs().Monitors == 1
	})
	if got := rowPhase(t, st, actor.ID); got != "background" {
		t.Fatalf("row phase with a live monitor = %q; want background", got)
	}

	fa.thread().emit(proto.Emit(proto.JobFinished, proto.JobPayload{JobID: "m1", Status: proto.JobStopped}))
	waitRowPhase(t, st, actor.ID, "idle")

	// A shell is not work the agent is waiting on.
	startJob(fa, "s1", proto.JobShell, "npm run dev")
	waitJobs(t, actor, 3)
	if got := rowPhase(t, st, actor.ID); got != "idle" {
		t.Fatalf("row phase with only a shell live = %q; want idle", got)
	}

	// Mid-turn the row says turn whatever is in the background, and goes
	// back to background, not idle, when the turn ends with an agent live.
	if _, err := actor.Prompt(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, fa.thread().prompts)
	waitRowPhase(t, st, actor.ID, "turn")
	startJob(fa, "a2", proto.JobAgent, "inner")
	waitJobs(t, actor, 4)
	if got := rowPhase(t, st, actor.ID); got != "turn" {
		t.Fatalf("row phase mid-turn after a job started = %q; want turn", got)
	}
	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: in.TurnID, StopReason: proto.StopEndTurn}))
	waitRowPhase(t, st, actor.ID, "background")
}

// A dev server dying is worth knowing but not worth a turn: the restart wakes
// nothing, and the next human prompt carries the news, once.
func TestShellOnlyThreadHoldsTheNoteForTheNextHumanPrompt(t *testing.T) {
	st := openRecoveryStore(t)
	fa := &fakeAdapter{}
	mgr, actor := startRecoveryThread(t, st, fa)
	ctx := context.Background()

	startJob(fa, "s1", proto.JobShell, "npm run dev")
	waitJobs(t, actor, 1)
	id := actor.ID
	mgr.Shutdown()

	if got := rowPhase(t, st, id); got != "idle" {
		t.Fatalf("row phase after shutdown with only a shell = %q; want idle", got)
	}

	mgr2 := NewManager(st, func(string, ...any) {}, fa)
	defer mgr2.Shutdown()
	mgr2.recoverAll(ctx)
	if _, ok := mgr2.Peek(id); ok {
		t.Fatal("restart brought back a thread that had only a shell running")
	}

	a, err := mgr2.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	noPrompt(t, fa.thread().prompts, 100*time.Millisecond)
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && !s.LiveJobs().Any()
	})

	if _, err := a.Prompt(ctx, "fix the button", nil); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, fa.thread().prompts)
	label := strings.Index(in.Text, "- shell: npm run dev")
	if label < 0 {
		t.Fatalf("first human prompt after restart = %q; want the dead shell named", in.Text)
	}
	if !strings.HasSuffix(in.Text, "fix the button") || label > strings.Index(in.Text, "fix the button") {
		t.Fatalf("harness text = %q; want the note first and the human's text last", in.Text)
	}
	s, _ := a.State(ctx)
	turn := lastTurnOf(s)
	if turn.Prompt != "fix the button" || turn.Recovery != nil {
		t.Fatalf("turn = %+v; want the human's text alone and no recovery", turn)
	}
	if s.Title != "fix the button" {
		t.Fatalf("title = %q; want the human's text", s.Title)
	}

	// The note is delivered once.
	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: in.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && s.Phase == "idle"
	})
	if _, err := a.Prompt(ctx, "and the footer", nil); err != nil {
		t.Fatal(err)
	}
	if next := recvPrompt(t, fa.thread().prompts); next.Text != "and the footer" {
		t.Fatalf("second human prompt = %q; want it untouched", next.Text)
	}
}

// The note lives in the log, not in the process that read it: a second
// restart before the human's next prompt does not lose it.
func TestShellNoteSurvivesASecondRestart(t *testing.T) {
	st := openRecoveryStore(t)
	fa := &fakeAdapter{}
	mgr, actor := startRecoveryThread(t, st, fa)
	ctx := context.Background()
	startJob(fa, "s1", proto.JobShell, "npm run dev")
	waitJobs(t, actor, 1)
	id := actor.ID
	mgr.Shutdown()

	mgr2 := NewManager(st, func(string, ...any) {}, fa)
	a, err := mgr2.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && !s.LiveJobs().Any()
	})
	mgr2.Shutdown()

	mgr3 := NewManager(st, func(string, ...any) {}, fa)
	defer mgr3.Shutdown()
	a, err = mgr3.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Prompt(ctx, "fix the button", nil); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, fa.thread().prompts)
	if got := deadLines(in.Text); strings.Join(got, "|") != "- shell: npm run dev" || !strings.HasSuffix(in.Text, "\n\nfix the button") {
		t.Fatalf("first human prompt after two restarts = %q; want the dead shell named before it", in.Text)
	}
}

func TestRefusedHumanPromptLeavesTheNoteForTheNext(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	startJob(sa.fakeAdapter, "s1", proto.JobShell, "npm run dev")
	waitJobs(t, actor, 1)
	id := actor.ID
	mgr.Shutdown()

	refusals := 1
	sa.script(func(in adapter.PromptInput) error {
		if refusals > 0 {
			refusals--
			return errors.New("attachment unreadable")
		}
		return nil
	})
	mgr2 := NewManager(st, func(string, ...any) {}, sa)
	defer mgr2.Shutdown()
	a, err := mgr2.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Prompt(ctx, "first", nil); err == nil {
		t.Fatal("refused prompt reported success")
	}
	if _, err := a.Prompt(ctx, "second", nil); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, sa.thread().prompts)
	if got := deadLines(in.Text); strings.Join(got, "|") != "- shell: npm run dev" || !strings.HasSuffix(in.Text, "\n\nsecond") {
		t.Fatalf("prompt after a refused one = %q; want it to carry the note the refused one never delivered", in.Text)
	}
}

// A continuation still waiting on its retry when the server stops again is
// owed by the next start, which plans it again from the log.
func TestContinuationOwedAcrossASecondRestart(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	if _, err := actor.Prompt(ctx, "explore", nil); err != nil {
		t.Fatal(err)
	}
	first := recvPrompt(t, sa.thread().prompts)
	startJob(sa.fakeAdapter, "j1", proto.JobAgent, "map the auth flow")
	sa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool {
		s, err := actor.State(ctx)
		return err == nil && s.Phase == "idle" && s.LiveJobs().Agents == 1
	})
	id := actor.ID
	mgr.Shutdown()

	isWake := func(in adapter.PromptInput) bool { return strings.Contains(in.Text, deadWorkIntro) }
	sa.script(func(in adapter.PromptInput) error {
		if isWake(in) {
			return errors.New("harness hiccup")
		}
		return nil
	})
	mgr2 := NewManager(st, func(string, ...any) {}, sa)
	mgr2.recoveryRetries = []time.Duration{time.Hour}
	mgr2.recoverAll(ctx)
	waitFor(t, func() bool { return sa.callsWhere(isWake) == 1 })
	refused, ok := mgr2.Peek(id)
	if !ok {
		t.Fatal("thread was not brought back")
	}
	waitFor(t, func() bool {
		s, err := refused.State(ctx)
		return err == nil && lastTurnOf(s).Recovery != nil && lastTurnOf(s).Done
	})
	// Held through the refused turn's own idle, so a stop now finds it again.
	waitRowPhase(t, st, id, "background")
	mgr2.Shutdown()
	if got := rowPhase(t, st, id); got != "background" {
		t.Fatalf("row phase after stopping with the continuation owed = %q; want background", got)
	}

	sa.script(nil)
	mgr3 := NewManager(st, func(string, ...any) {}, sa)
	defer mgr3.Shutdown()
	mgr3.recoverAll(ctx)
	in := recvPrompt(t, sa.thread().prompts)
	if got := deadLines(in.Text); strings.Join(got, "|") != "- agent: map the auth flow" {
		t.Fatalf("continuation after the second restart = %q; want it to name the dead subagent", in.Text)
	}
	a, ok := mgr3.Peek(id)
	if !ok {
		t.Fatal("thread was not brought back")
	}
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && lastTurnOf(s).ID == in.TurnID
	})
	s, _ := a.State(ctx)
	if rec := lastTurnOf(s).Recovery; rec == nil || rec.Attempt != 2 {
		t.Fatalf("continuation recovery = %+v; want attempt 2, counting the refused one", rec)
	}
}

// A human who picks the thread up before the continuation starts supersedes
// it: their prompt carries the dead-work news, and the continuation is never
// sent.
func TestHumanPromptSupersedesPendingContinuation(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()

	startJob(sa.fakeAdapter, "a1", proto.JobAgent, "audit deps")
	waitJobs(t, actor, 1)
	id := actor.ID
	mgr.Shutdown()

	// The first continuation is refused for a passing reason, and the retry
	// is far enough off that the human gets in first.
	sa.script(func(in adapter.PromptInput) error {
		// The human's prompt reaches the harness with the note in front.
		if strings.HasSuffix(in.Text, "ship it") {
			return nil
		}
		return errors.New("harness hiccup")
	})
	m := NewManager(st, func(string, ...any) {}, sa)
	m.recoveryRetries = []time.Duration{time.Hour}
	defer m.Shutdown()
	a, err := m.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && len(s.Turns) == 1 && s.Turns[0].Done
	})
	if n := sa.callsWhere(func(adapter.PromptInput) bool { return true }); n != 1 {
		t.Fatalf("continuation tries before the human = %d; want 1", n)
	}

	if _, err := a.Prompt(ctx, "ship it", nil); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, sa.thread().prompts)
	if !strings.Contains(in.Text, "- agent: audit deps") || !strings.HasSuffix(in.Text, "ship it") {
		t.Fatalf("human prompt = %q; want the continuation's note in front of the human's text", in.Text)
	}
	s, _ := a.State(ctx)
	if turn := lastTurnOf(s); turn.Prompt != "ship it" || turn.Recovery != nil {
		t.Fatalf("human turn = %+v; want the human's text and no recovery", turn)
	}

	// Nothing is left to continue.
	if err := a.Recover(ctx); err != nil {
		t.Fatalf("recover after a human superseded it: %v", err)
	}
	noPrompt(t, sa.thread().prompts, 50*time.Millisecond)
	if n := sa.callsWhere(func(adapter.PromptInput) bool { return true }); n != 2 {
		t.Fatalf("harness prompts = %d; want the refused continuation and the human's", n)
	}
}

// Pressing continue is a human picking the thread up too.
func TestHumanContinueSupersedesPendingContinuation(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	startJob(sa.fakeAdapter, "a1", proto.JobAgent, "audit deps")
	waitJobs(t, actor, 1)
	id := actor.ID
	mgr.Shutdown()

	sa.script(func(in adapter.PromptInput) error {
		if strings.HasSuffix(in.Text, continuePrompt) {
			return nil
		}
		return errors.New("harness hiccup")
	})
	m := NewManager(st, func(string, ...any) {}, sa)
	m.recoveryRetries = []time.Duration{time.Hour}
	defer m.Shutdown()
	a, err := m.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && len(s.Turns) == 1 && s.Turns[0].Done
	})

	if _, err := a.Continue(ctx); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, sa.thread().prompts)
	if got := deadLines(in.Text); strings.Join(got, "|") != "- agent: audit deps" || !strings.HasSuffix(in.Text, "\n\n"+continuePrompt) {
		t.Fatalf("continue sent as %q; want the note in front of the continue prompt", in.Text)
	}
	if err := a.Recover(ctx); err != nil {
		t.Fatalf("recover after a human continued: %v", err)
	}
	noPrompt(t, sa.thread().prompts, 50*time.Millisecond)
}

// ---- retries ----

// interruptWithQueuedPrompt leaves a thread cut mid-turn with "after" queued
// behind it, and returns its id.
func interruptWithQueuedPrompt(t *testing.T, st *store.Store, sa *scriptedAdapter) string {
	t.Helper()
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	if _, err := actor.Prompt(ctx, "start", nil); err != nil {
		t.Fatal(err)
	}
	recvPrompt(t, sa.thread().prompts)
	if res, err := actor.Prompt(ctx, "after", nil); err != nil || !res.Queued() {
		t.Fatalf("queued prompt = %+v, %v", res, err)
	}
	id := actor.ID
	mgr.Shutdown()
	return id
}

// A thread whose row is not asking to be recovered — a continuation given up
// on, a row from before this phase existed — is never continued, so it must
// not keep a plan that holds its queue back.
func TestUnaskedPlanDoesNotHoldTheQueue(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	id := interruptWithQueuedPrompt(t, st, sa)
	if err := st.SetPhase(context.Background(), id, "idle"); err != nil {
		t.Fatal(err)
	}

	m := NewManager(st, func(string, ...any) {}, sa)
	defer m.Shutdown()
	if _, err := m.Get(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if in := recvPrompt(t, sa.thread().prompts); in.Text != "after" {
		t.Fatalf("first prompt after activation = %q; want the queued one", in.Text)
	}
	if n := sa.callsWhere(isRestartContinuation); n != 0 {
		t.Fatalf("continuation sent %d times for a row that did not ask for one", n)
	}
}

func TestTransientRefusalIsRetriedThenDroppedForTheQueue(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	id := interruptWithQueuedPrompt(t, st, sa)

	sa.script(func(in adapter.PromptInput) error {
		if isRestartContinuation(in) {
			return errors.New("harness hiccup")
		}
		return nil
	})
	m := NewManager(st, func(string, ...any) {}, sa)
	m.recoveryRetries = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	defer m.Shutdown()
	a, err := m.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	// Once the tries are spent the continuation is dropped, and the queue it
	// was holding back runs.
	if in := recvPrompt(t, sa.thread().prompts); in.Text != "after" {
		t.Fatalf("first prompt the harness accepted = %q; want the queued one", in.Text)
	}
	if n := sa.callsWhere(isRestartContinuation); n != 1+len(m.recoveryRetries) {
		t.Fatalf("continuation tries = %d; want one plus one per retry delay (%d)", n, 1+len(m.recoveryRetries))
	}
	noPrompt(t, sa.thread().prompts, 50*time.Millisecond)
	if n := sa.callsWhere(isRestartContinuation); n != 1+len(m.recoveryRetries) {
		t.Fatalf("continuation tried again after it was dropped: %d tries", n)
	}
	s, _ := a.State(context.Background())
	if len(s.Queued) != 0 {
		t.Fatalf("queue = %+v; want it drained", s.Queued)
	}
	if last := lastTurnOf(s); last.Prompt != "after" || last.Recovery != nil {
		t.Fatalf("running turn = %+v; want the queued human prompt", last)
	}
}

// A continuation the harness refuses at a usage limit is not retried, and
// the queue behind it is held like any other limit's: each queued prompt
// would only hit the limit again.
func TestLimitFailureIsNotRetried(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	id := interruptWithQueuedPrompt(t, st, sa)

	sa.script(func(in adapter.PromptInput) error {
		if isRestartContinuation(in) {
			return &adapter.FailureError{Kind: proto.FailureLimit, Err: errors.New("out of quota")}
		}
		return nil
	})
	m := NewManager(st, func(string, ...any) {}, sa)
	m.recoveryRetries = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	defer m.Shutdown()
	a, err := m.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return sa.callsWhere(isRestartContinuation) == 1 })
	noPrompt(t, sa.thread().prompts, 50*time.Millisecond)
	if n := sa.callsWhere(isRestartContinuation); n != 1 {
		t.Fatalf("continuation tries after a limit failure = %d; want 1", n)
	}
	s, _ := a.State(context.Background())
	if !s.QueueHeld() || len(s.Queued) != 1 || s.Queued[0].Prompt != "after" {
		t.Fatalf("held %v, queue %+v; want the queued prompt held behind the limit", s.QueueHeld(), s.Queued)
	}
}

// A continuation that runs into a usage limit arms the limit's own
// continuation, and that is what carries the dead-work note the refused one
// never delivered. The queued prompt waits behind it, without the note.
func TestLimitedContinuationHandsItsNoteToTheLimitContinuation(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	if _, err := actor.Prompt(ctx, "start", nil); err != nil {
		t.Fatal(err)
	}
	recvPrompt(t, sa.thread().prompts)
	startJob(sa.fakeAdapter, "j1", proto.JobShell, "dev server")
	waitJobs(t, actor, 1)
	if res, err := actor.Prompt(ctx, "after", nil); err != nil || !res.Queued() {
		t.Fatalf("queued prompt = %+v, %v", res, err)
	}
	id := actor.ID
	mgr.Shutdown()

	m := NewManager(st, func(string, ...any) {}, sa)
	defer m.Shutdown()
	a, err := m.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	cont := recvPrompt(t, sa.thread().prompts)
	if !isRestartContinuation(cont) {
		t.Fatalf("first prompt after the restart = %q; want the continuation", cont.Text)
	}
	reset := time.Now().Add(time.Hour).UnixMilli()
	sa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: cont.TurnID, StopReason: proto.StopError, Error: "usage limit reached",
		Failure: proto.FailureLimit, ResetAt: reset,
	}))
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
	noPrompt(t, sa.thread().prompts, 50*time.Millisecond)

	p := scheduled(t, a, limitScheduleID(cont.TurnID))
	if p.Status != "pending" || p.DueAt != reset+limitDelay {
		t.Fatalf("limit continuation = %+v; want it armed for the reset", p)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, sa.thread().prompts)
	if !strings.HasSuffix(in.Text, "\n\n"+limitContinuePrompt) || strings.Join(deadLines(in.Text), "|") != "- shell: dev server" {
		t.Fatalf("limit continuation sent as %q; want the dead-work note in front of it", in.Text)
	}
	sa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: in.TurnID, StopReason: proto.StopEndTurn}))
	if in := recvPrompt(t, sa.thread().prompts); in.Text != "after" {
		t.Fatalf("queued prompt sent as %q; want it as written", in.Text)
	}
}

// A continuation refused outright at a limit leaves its note held once, for
// the prompt that goes past the held queue: not once for the refusal and
// again for the dropped plan.
func TestLimitRefusedContinuationHoldsItsNoteOnce(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	if _, err := actor.Prompt(ctx, "start", nil); err != nil {
		t.Fatal(err)
	}
	recvPrompt(t, sa.thread().prompts)
	startJob(sa.fakeAdapter, "j1", proto.JobShell, "dev server")
	waitJobs(t, actor, 1)
	id := actor.ID
	mgr.Shutdown()

	sa.script(func(in adapter.PromptInput) error {
		if isRestartContinuation(in) {
			return &adapter.FailureError{Kind: proto.FailureLimit, Err: errors.New("out of quota")}
		}
		return nil
	})
	m := NewManager(st, func(string, ...any) {}, sa)
	defer m.Shutdown()
	a, err := m.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.QueueHeld() })
	if _, err := a.Prompt(ctx, "by hand", nil); err != nil {
		t.Fatal(err)
	}
	in := recvPrompt(t, sa.thread().prompts)
	if !strings.HasSuffix(in.Text, "\n\nby hand") || strings.Join(deadLines(in.Text), "|") != "- shell: dev server" {
		t.Fatalf("prompt sent by hand as %q; want the dead-work note once in front of it", in.Text)
	}
}

func TestContinuationAcceptedOnRetryRunsOnce(t *testing.T) {
	st := openRecoveryStore(t)
	sa := &scriptedAdapter{fakeAdapter: &fakeAdapter{}}
	mgr, actor := startRecoveryThread(t, st, sa)
	ctx := context.Background()
	if _, err := actor.Prompt(ctx, "start", nil); err != nil {
		t.Fatal(err)
	}
	recvPrompt(t, sa.thread().prompts)
	id := actor.ID
	mgr.Shutdown()

	var mu sync.Mutex
	refused := 0
	sa.script(func(in adapter.PromptInput) error {
		mu.Lock()
		defer mu.Unlock()
		if refused == 0 {
			refused++
			return errors.New("harness hiccup")
		}
		return nil
	})
	m := NewManager(st, func(string, ...any) {}, sa)
	m.recoveryRetries = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	defer m.Shutdown()
	a, err := m.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	in := recvPrompt(t, sa.thread().prompts)
	if !isRestartContinuation(in) {
		t.Fatalf("accepted prompt = %q; want the continuation", in.Text)
	}
	noPrompt(t, sa.thread().prompts, 50*time.Millisecond)
	if n := sa.callsWhere(isRestartContinuation); n != 2 {
		t.Fatalf("continuation tries = %d; want the refused one and the accepted one", n)
	}

	s, _ := a.State(ctx)
	var running []projection.Turn
	for _, turn := range s.Turns {
		if turn.Recovery != nil && !turn.Done {
			running = append(running, turn)
		}
	}
	if len(running) != 1 || running[0].ID != in.TurnID {
		t.Fatalf("running continuations = %+v; want exactly the accepted one", running)
	}
	if running[0].Recovery.ResumeOf != s.Turns[0].ID {
		t.Fatalf("retried continuation resumes %q; want the cut turn %q", running[0].Recovery.ResumeOf, s.Turns[0].ID)
	}
}

func TestRetryRecoverySchedule(t *testing.T) {
	newMgr := func(t *testing.T) *Manager {
		m := NewManager(openRecoveryStore(t), func(string, ...any) {})
		t.Cleanup(m.Shutdown)
		m.recoveryRetries = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
		return m
	}
	transient := errors.New("harness hiccup")

	t.Run("keeps trying a passing failure until the delays run out", func(t *testing.T) {
		m := newMgr(t)
		tries := 0
		err := m.retryRecovery("t", transient, func() error { tries++; return transient })
		if !errors.Is(err, transient) || tries != len(m.recoveryRetries) {
			t.Fatalf("err = %v after %d tries; want the last failure after %d", err, tries, len(m.recoveryRetries))
		}
	})
	t.Run("stops at the first success", func(t *testing.T) {
		m := newMgr(t)
		tries := 0
		err := m.retryRecovery("t", transient, func() error {
			tries++
			if tries == 2 {
				return nil
			}
			return transient
		})
		if err != nil || tries != 2 {
			t.Fatalf("err = %v after %d tries; want nil after 2", err, tries)
		}
	})
	t.Run("stops when a retry fails for good", func(t *testing.T) {
		m := newMgr(t)
		tries := 0
		err := m.retryRecovery("t", transient, func() error { tries++; return ErrClosed })
		if !errors.Is(err, ErrClosed) || tries != 1 {
			t.Fatalf("err = %v after %d tries; want ErrClosed after 1", err, tries)
		}
	})
	t.Run("does not try a failure that will not pass", func(t *testing.T) {
		m := newMgr(t)
		tries := 0
		auth := &adapter.FailureError{Kind: proto.FailureAuth, Err: errors.New("sign in")}
		err := m.retryRecovery("t", auth, func() error { tries++; return nil })
		if err != auth || tries != 0 {
			t.Fatalf("err = %v after %d tries; want the auth failure untried", err, tries)
		}
	})
	t.Run("gives up waiting when the server shuts down", func(t *testing.T) {
		m := newMgr(t)
		m.recoveryRetries = []time.Duration{time.Hour}
		m.recoverStop()
		tries := 0
		done := make(chan error, 1)
		go func() { done <- m.retryRecovery("t", transient, func() error { tries++; return nil }) }()
		select {
		case err := <-done:
			if !errors.Is(err, transient) || tries != 0 {
				t.Fatalf("err = %v after %d tries; want the first failure untried", err, tries)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("retry kept waiting after shutdown")
		}
	})
}

func TestRetryableRecovery(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"harness not up yet", ErrNotReady, true},
		{"wrapped not ready", fmt.Errorf("activate: %w", ErrNotReady), true},
		{"thread closed", ErrClosed, false},
		{"thread busy", ErrBusy, false},
		{"shutting down", context.Canceled, false},
		{"needs a login", &adapter.FailureError{Kind: proto.FailureAuth, Err: errors.New("x")}, false},
		{"out of quota", fmt.Errorf("prompt: %w", &adapter.FailureError{Kind: proto.FailureLimit, Err: errors.New("x")}), false},
		{"other classified failure", &adapter.FailureError{Kind: proto.FailureRestart, Err: errors.New("x")}, true},
		{"invalid params", fmt.Errorf("turn/start: %w", &jsonrpc.RPCError{Code: -32602, Message: "bad input"}), false},
		{"server error over rpc", &jsonrpc.RPCError{Code: -32000, Message: "connection closed"}, true},
		{"unclassified", errors.New("broken pipe"), true},
	}
	for _, c := range cases {
		if got := retryableRecovery(c.err); got != c.want {
			t.Errorf("%s: retryable = %v; want %v", c.name, got, c.want)
		}
	}
}

// ---- planRecovery ----

const planNow = int64(10_000_000_000)

func planState(turns []projection.Turn, jobs ...projection.Job) *projection.State {
	s := projection.New("t")
	s.Turns = append(s.Turns, turns...)
	s.Jobs = append(s.Jobs, jobs...)
	return s
}

func doneTurn(id string) projection.Turn {
	return projection.Turn{ID: id, Done: true, StopReason: proto.StopEndTurn, StartedAt: planNow - 3_600_000}
}

func cutTurn(id string) projection.Turn {
	return projection.Turn{ID: id, StartedAt: planNow - 3_600_000}
}

// interruptedJob is one a restart closed the given time before planNow.
func interruptedJob(id, kind, name string, ago time.Duration) projection.Job {
	return projection.Job{ID: id, Kind: kind, Name: name, Status: proto.JobInterrupted, FinishedAt: planNow - ago.Milliseconds()}
}

// restartClosedTurn is a cut turn as a resume leaves it.
func restartClosedTurn(id string) projection.Turn {
	return projection.Turn{ID: id, Done: true, StopReason: proto.StopError, Failure: proto.FailureRestart, StartedAt: planNow - 3_600_000}
}

// refusedContinuation is a continuation the harness would not start.
func refusedContinuation(id string) projection.Turn {
	return projection.Turn{
		ID: id, Done: true, StopReason: proto.StopError, StartedAt: planNow - 30_000,
		Recovery: &proto.TurnRecovery{Attempt: 1, Cause: proto.RecoveryRestart},
	}
}

// erroredContinuation is a continuation that ran, then ended in an error.
func erroredContinuation(id string) projection.Turn {
	t := refusedContinuation(id)
	t.StartedAt = planNow - 3_500_000
	return t
}

// refusedPrompt is a human prompt the harness would not take.
func refusedPrompt(id string) projection.Turn {
	return projection.Turn{ID: id, Done: true, StopReason: proto.StopError, StartedAt: planNow - 30_000}
}

// agentItem is something the agent did in a turn.
func agentItem(turnID string) projection.Item {
	return projection.Item{ID: "msg-" + turnID, Kind: projection.ItemMessage, Role: "agent", TurnID: turnID}
}

func toolItem(turnID string) projection.Item {
	return projection.Item{ID: "tool-" + turnID, Kind: projection.ItemTool, TurnID: turnID}
}

func withItems(s *projection.State, items ...projection.Item) *projection.State {
	s.Items = append(s.Items, items...)
	return s
}

func liveJob(id, kind, name string) projection.Job {
	return projection.Job{ID: id, Kind: kind, Name: name, Status: proto.JobRunning}
}

func TestPlanRecoveryDecidesWhetherToWake(t *testing.T) {
	cases := []struct {
		name     string
		state    *projection.State
		wantPlan bool
		wantHeld []string // labels the held note names; nil for no note
	}{
		{"fresh thread", planState(nil), false, nil},
		{"finished turn, nothing live", planState([]projection.Turn{doneTurn("t1")}), false, nil},
		{"finished jobs only", planState([]projection.Turn{doneTurn("t1")},
			projection.Job{ID: "a", Kind: proto.JobAgent, Name: "gone", Status: proto.JobCompleted}), false, nil},
		{"inert only", planState([]projection.Turn{doneTurn("t1")}, liveJob("p", proto.JobInert, "plan")), false, nil},
		{"shell only, idle", planState([]projection.Turn{doneTurn("t1")}, liveJob("s", proto.JobShell, "dev server")),
			false, []string{"- shell: dev server"}},
		{"cut turn", planState([]projection.Turn{cutTurn("t1")}), true, nil},
		{"agent, idle", planState([]projection.Turn{doneTurn("t1")}, liveJob("a", proto.JobAgent, "x")), true, nil},
		{"monitor, idle", planState([]projection.Turn{doneTurn("t1")}, liveJob("m", proto.JobMonitor, "x")), true, nil},
		{"agent, never prompted", planState(nil, liveJob("a", proto.JobAgent, "x")), true, nil},
		{"shell, cut turn", planState([]projection.Turn{cutTurn("t1")}, liveJob("s", proto.JobShell, "x")), true, nil},

		// A second restart, with the first one's closing already in the log.
		{"agent interrupted since the last turn", planState([]projection.Turn{doneTurn("t1")}, interruptedJob("a", proto.JobAgent, "x", time.Minute)), true, nil},
		{"shell interrupted since the last turn", planState([]projection.Turn{doneTurn("t1")}, interruptedJob("s", proto.JobShell, "dev server", time.Minute)),
			false, []string{"- shell: dev server"}},
		{"agent interrupted before the last turn", planState([]projection.Turn{doneTurn("t1")}, interruptedJob("a", proto.JobAgent, "x", 2*time.Hour)), false, nil},
		{"turn closed by a restart", planState([]projection.Turn{restartClosedTurn("t1")}), true, nil},
		{"turn closed by a restart, then its continuation refused", planState([]projection.Turn{restartClosedTurn("t1"), refusedContinuation("r")}), true, nil},
		{"agent interrupted, then its continuation refused", planState([]projection.Turn{doneTurn("t1"), refusedContinuation("r")},
			interruptedJob("a", proto.JobAgent, "x", time.Minute)), true, nil},
		{"turn that errored on its own", planState([]projection.Turn{{ID: "t1", Done: true, StopReason: proto.StopError, StartedAt: planNow - 60_000}}), false, nil},
		{"continuation that ran, then errored", withItems(planState([]projection.Turn{restartClosedTurn("t1"), erroredContinuation("r")},
			interruptedJob("a", proto.JobAgent, "x", time.Hour-time.Minute)), agentItem("r")), false, nil},
		{"shell interrupted, then a human prompt refused", planState([]projection.Turn{doneTurn("t1"), refusedPrompt("h")},
			interruptedJob("s", proto.JobShell, "dev server", time.Minute)), false, []string{"- shell: dev server"}},
		{"harness exited mid-turn, agent with it", withItems(planState([]projection.Turn{{ID: "t1", Done: true, StopReason: proto.StopError, StartedAt: planNow - 60_000}},
			interruptedJob("a", proto.JobAgent, "x", time.Second)), agentItem("t1")), true, nil},
	}
	for _, c := range cases {
		plan, held := planRecovery(c.state, planNow)
		if (plan != nil) != c.wantPlan {
			t.Errorf("%s: plan = %+v; want plan %v", c.name, plan, c.wantPlan)
			continue
		}
		if plan != nil && held != "" {
			t.Errorf("%s: both a plan and a held note (%q)", c.name, held)
		}
		if got := deadLines(held); strings.Join(got, "|") != strings.Join(c.wantHeld, "|") {
			t.Errorf("%s: held lines = %q; want %q", c.name, got, c.wantHeld)
		}
	}

	closed := planState([]projection.Turn{cutTurn("t1")}, liveJob("a", proto.JobAgent, "x"))
	closed.Closed = true
	if plan, held := planRecovery(closed, planNow); plan != nil || held != "" {
		t.Errorf("closed thread: plan %+v, held %q; want neither", plan, held)
	}
}

func TestPlanRecoveryPrompt(t *testing.T) {
	t.Run("cut turn with no dead work", func(t *testing.T) {
		plan, _ := planRecovery(planState([]projection.Turn{cutTurn("t1")}), planNow)
		if plan.prompt != restartPrompt || plan.note != "" {
			t.Fatalf("plan = %+v; want the restart prompt alone and no note", plan)
		}
	})
	t.Run("cut turn with dead work", func(t *testing.T) {
		plan, _ := planRecovery(planState([]projection.Turn{cutTurn("t1")}, liveJob("a", proto.JobAgent, "lint")), planNow)
		if !strings.HasPrefix(plan.note, "[omniplex] ") || plan.prompt != restartPrompt+"\n\n"+strings.TrimPrefix(plan.note, "[omniplex] ") {
			t.Fatalf("plan = %+v; want the restart prompt, a blank line, then the note without a second tag", plan)
		}
		if got := deadLines(plan.note); len(got) != 1 || got[0] != "- agent: lint" {
			t.Fatalf("note lines = %q", got)
		}
	})
	t.Run("idle with dead work", func(t *testing.T) {
		plan, _ := planRecovery(planState([]projection.Turn{doneTurn("t1")}, liveJob("m", proto.JobMonitor, "CI")), planNow)
		if plan.note == "" || plan.prompt != plan.note || !strings.HasPrefix(plan.prompt, "[omniplex] ") {
			t.Fatalf("plan = %+v; want the note alone, without the cut-turn prompt", plan)
		}
		if got := deadLines(plan.note); len(got) != 1 || got[0] != "- monitor: CI" {
			t.Fatalf("note lines = %q", got)
		}
	})
}

// owedNote reads what is still owed from the log, while the process runs:
// work interrupted since the agent last heard anything, and none still live.
func TestOwedNote(t *testing.T) {
	shell := interruptedJob("s1", proto.JobShell, "dev server", 30*time.Minute)
	limited := refusedContinuation("c1")
	limited.Failure = proto.FailureLimit
	acted := erroredContinuation("c1")
	acted.Failure = proto.FailureLimit
	early := interruptedJob("s1", proto.JobShell, "dev server", 59*time.Minute)
	cases := []struct {
		name  string
		state *projection.State
		want  string
	}{
		{"a refused continuation still owes it", planState([]projection.Turn{restartClosedTurn("t1"), limited}, shell), "- shell: dev server"},
		{"Claude's refusal is not the agent hearing it", withItems(planState([]projection.Turn{restartClosedTurn("t1"), limited}, shell), agentItem("c1")), "- shell: dev server"},
		{"one the agent acted in delivered it", withItems(planState([]projection.Turn{restartClosedTurn("t1"), acted}, early), toolItem("c1"), agentItem("c1")), ""},
		{"so did a reply before the refusal", withItems(planState([]projection.Turn{restartClosedTurn("t1"), acted}, early), agentItem("c1"), projection.Item{ID: "refusal", Kind: projection.ItemMessage, Role: "agent", TurnID: "c1"}), ""},
		{"live work is running, not dead", planState([]projection.Turn{doneTurn("t1"), refusedPrompt("t2")}, liveJob("s2", proto.JobShell, "watch")), ""},
	}
	for _, c := range cases {
		got := owedNote(c.state)
		if lines := strings.Join(deadLines(got), "|"); lines != c.want || (c.want != "" && !strings.HasPrefix(got, "[omniplex] ")) {
			t.Errorf("%s: owedNote = %q; want lines %q", c.name, got, c.want)
		}
	}
}

func TestDeadWorkNoteListsWhatTheAgentHasToRestart(t *testing.T) {
	note := func(jobs ...projection.Job) []string {
		plan, _ := planRecovery(planState([]projection.Turn{cutTurn("t1")}, jobs...), planNow)
		return deadLines(plan.note)
	}

	t.Run("in log order, with kind", func(t *testing.T) {
		got := note(liveJob("a", proto.JobAgent, "one"), liveJob("s", proto.JobShell, "two"), liveJob("m", proto.JobMonitor, "three"))
		want := []string{"- agent: one", "- shell: two", "- monitor: three"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("lines = %q; want %q", got, want)
		}
	})
	t.Run("leaves out finished and inert jobs", func(t *testing.T) {
		got := note(
			projection.Job{ID: "d", Kind: proto.JobAgent, Name: "done", Status: proto.JobCompleted},
			projection.Job{ID: "f", Kind: proto.JobAgent, Name: "failed", Status: proto.JobFailed},
			liveJob("p", proto.JobInert, "housekeeping"),
			liveJob("a", proto.JobAgent, "live"),
		)
		if len(got) != 1 || got[0] != "- agent: live" {
			t.Fatalf("lines = %q; want only the live agent", got)
		}
	})
	t.Run("reports a child under its live parent", func(t *testing.T) {
		child := liveJob("c", proto.JobShell, "child shell")
		child.ParentJobID = "p"
		grandchild := liveJob("g", proto.JobAgent, "grandchild")
		grandchild.ParentJobID = "c"
		got := note(liveJob("p", proto.JobAgent, "parent"), child, grandchild)
		if len(got) != 1 || got[0] != "- agent: parent" {
			t.Fatalf("lines = %q; want only the parent", got)
		}
	})
	t.Run("reports a shell under the subagent whose tool call started it", func(t *testing.T) {
		// Claude names no parent job for a subagent's shell; the tool call
		// that started it sits inside the subagent's own.
		agent := liveJob("a", proto.JobAgent, "subagent")
		agent.ToolCallID = "task-1"
		shell := liveJob("s", proto.JobShell, "watcher")
		shell.ToolCallID = "bash-1"
		s := planState([]projection.Turn{cutTurn("t1")}, agent, shell)
		s.Items = append(s.Items, projection.Item{ID: "bash-1", Kind: projection.ItemTool, ParentID: "task-1"})
		plan, _ := planRecovery(s, planNow)
		if got := deadLines(plan.note); len(got) != 1 || got[0] != "- agent: subagent" {
			t.Fatalf("lines = %q; want only the subagent", got)
		}

		// Once the subagent is done, the shell it left running is its own.
		s.Jobs[0].Status = proto.JobCompleted
		plan, _ = planRecovery(s, planNow)
		if got := deadLines(plan.note); len(got) != 1 || got[0] != "- shell: watcher" {
			t.Fatalf("lines = %q; want the shell once its subagent finished", got)
		}
	})
	t.Run("lists a child whose parent is no longer live", func(t *testing.T) {
		parent := projection.Job{ID: "p", Kind: proto.JobAgent, Name: "parent", Status: proto.JobCompleted}
		child := liveJob("c", proto.JobShell, "orphan")
		child.ParentJobID = "p"
		got := note(parent, child)
		if len(got) != 1 || got[0] != "- shell: orphan" {
			t.Fatalf("lines = %q; want the orphaned child", got)
		}
	})
	t.Run("lists a child under an inert parent", func(t *testing.T) {
		child := liveJob("c", proto.JobAgent, "under inert")
		child.ParentJobID = "p"
		got := note(liveJob("p", proto.JobInert, "plan"), child)
		if len(got) != 1 || got[0] != "- agent: under inert" {
			t.Fatalf("lines = %q; want the child, since inert work is not reported", got)
		}
	})
	t.Run("exactly the cap lists everything", func(t *testing.T) {
		var jobs []projection.Job
		for i := 0; i < maxDeadWork; i++ {
			jobs = append(jobs, liveJob(fmt.Sprint(i), proto.JobAgent, fmt.Sprintf("agent-%d", i)))
		}
		got := note(jobs...)
		if len(got) != maxDeadWork || strings.Contains(got[len(got)-1], "more") {
			t.Fatalf("lines = %q; want all %d and no overflow line", got, maxDeadWork)
		}
	})
	t.Run("past the cap counts the rest", func(t *testing.T) {
		var jobs []projection.Job
		for i := 0; i < maxDeadWork+3; i++ {
			jobs = append(jobs, liveJob(fmt.Sprint(i), proto.JobAgent, fmt.Sprintf("agent-%d", i)))
			// Children do not count towards the overflow.
			child := liveJob(fmt.Sprintf("c%d", i), proto.JobShell, "child")
			child.ParentJobID = fmt.Sprint(i)
			jobs = append(jobs, child)
		}
		got := note(jobs...)
		if len(got) != maxDeadWork+1 {
			t.Fatalf("lines = %q; want %d listed and one overflow line", got, maxDeadWork)
		}
		for i := 0; i < maxDeadWork; i++ {
			if want := fmt.Sprintf("- agent: agent-%d", i); got[i] != want {
				t.Fatalf("line %d = %q; want %q", i, got[i], want)
			}
		}
		var more int
		if _, err := fmt.Sscanf(got[maxDeadWork], "- and %d more", &more); err != nil || more != 3 {
			t.Fatalf("overflow line = %q; want it to count the 3 left out", got[maxDeadWork])
		}
	})
}

func TestJobLabel(t *testing.T) {
	cases := []struct {
		name string
		job  projection.Job
		want string
	}{
		{"name first", projection.Job{ID: "id", Name: "n", WorkflowName: "w", Role: "r", TaskType: "tt"}, "n"},
		{"then workflow", projection.Job{ID: "id", Name: "  ", WorkflowName: "w", Role: "r", TaskType: "tt"}, "w"},
		{"then role", projection.Job{ID: "id", WorkflowName: "\t", Role: "r", TaskType: "tt"}, "r"},
		{"then task type", projection.Job{ID: "id", TaskType: "tt"}, "tt"},
		{"then id", projection.Job{ID: "id"}, "id"},
		{"trimmed", projection.Job{ID: "id", Name: "  spaced  "}, "spaced"},
	}
	for _, c := range cases {
		if got := jobLabel(c.job); got != c.want {
			t.Errorf("%s: label = %q; want %q", c.name, got, c.want)
		}
	}

	// The cap is in runes, so a multi-byte label is not cut mid-character.
	atCap := strings.Repeat("é", maxDeadWorkLabel)
	if got := jobLabel(projection.Job{ID: "id", Name: atCap}); got != atCap {
		t.Errorf("label at the cap was changed: %d runes", len([]rune(got)))
	}
	long := strings.Repeat("é", maxDeadWorkLabel-1) + "xyz"
	got := []rune(jobLabel(projection.Job{ID: "id", Name: long}))
	if len(got) != maxDeadWorkLabel || got[len(got)-1] != '…' || string(got[:len(got)-1]) != string([]rune(long)[:maxDeadWorkLabel-1]) {
		t.Errorf("long label = %q (%d runes); want the first %d runes and an ellipsis", string(got), len(got), maxDeadWorkLabel-1)
	}
}

func TestPlanRecoveryAttempt(t *testing.T) {
	recoveryTurn := func(attempt int, startedAgo time.Duration, done bool) projection.Turn {
		return projection.Turn{
			ID: "r", Done: done, StartedAt: planNow - startedAgo.Milliseconds(),
			Recovery: &proto.TurnRecovery{ResumeOf: "t1", Attempt: attempt, Cause: proto.RecoveryRestart},
		}
	}
	cases := []struct {
		name  string
		state *projection.State
		want  int // 0: no plan
	}{
		{"human turn cut", planState([]projection.Turn{cutTurn("t1")}), 1},
		{"recovery cut within the window", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(1, time.Minute, false)}), 2},
		{"recovery cut just inside the window", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(2, recoveryWindow-time.Second, false)}), 3},
		{"recovery cut long ago", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(2, recoveryWindow+time.Second, false)}), 1},
		{"at the cap within the window", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(maxRecoveryAttempts, time.Minute, false)}), 0},
		{"at the cap long ago", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(maxRecoveryAttempts, time.Hour, false)}), 1},
		{"finished recovery turn with an agent live", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(1, time.Minute, true)},
			liveJob("a", proto.JobAgent, "x")), 2},
		{"finished recovery at the cap with an agent live", planState([]projection.Turn{doneTurn("t1"), recoveryTurn(maxRecoveryAttempts, time.Minute, true)},
			liveJob("a", proto.JobAgent, "x")), 0},
	}
	for _, c := range cases {
		plan, _ := planRecovery(c.state, planNow)
		switch {
		case c.want == 0 && plan != nil:
			t.Errorf("%s: plan = %+v; want none past the cap", c.name, plan.rec)
		case c.want == 0:
		case plan == nil:
			t.Errorf("%s: no plan; want attempt %d", c.name, c.want)
		case plan.rec.Attempt != c.want || plan.rec.Cause != proto.RecoveryRestart:
			t.Errorf("%s: recovery = %+v; want restart attempt %d", c.name, plan.rec, c.want)
		case plan.rec.ResumeOf != lastTurnOf(c.state).ID:
			t.Errorf("%s: resumes %q; want the last turn %q", c.name, plan.rec.ResumeOf, lastTurnOf(c.state).ID)
		}
	}

	if plan, _ := planRecovery(planState(nil, liveJob("a", proto.JobAgent, "x")), planNow); plan == nil || plan.rec.ResumeOf != "" || plan.rec.Attempt != 1 {
		t.Errorf("never-prompted thread plan = %+v; want attempt 1 resuming nothing", plan)
	}

	// Past the cap the thread is left for a human, who still hears what died.
	capped := planState([]projection.Turn{doneTurn("t1"), recoveryTurn(maxRecoveryAttempts, time.Minute, false)},
		liveJob("a", proto.JobAgent, "x"))
	if plan, held := planRecovery(capped, planNow); plan != nil || !strings.HasPrefix(held, "[omniplex] ") ||
		strings.Join(deadLines(held), "|") != "- agent: x" {
		t.Errorf("past the cap: plan %+v, held %q; want no plan and the note held", plan, held)
	}
}
