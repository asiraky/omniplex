package thread

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// withStopGrace shortens how long Stop waits before killing the harness.
// Call it before the actor is made, so the actor is gone before it is reset.
func withStopGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := stopGrace
	stopGrace = d
	t.Cleanup(func() { stopGrace = old })
}

// gate returns a channel that holds a fake's call until the test ends.
func gate(t *testing.T) chan struct{} {
	g := make(chan struct{})
	t.Cleanup(func() { close(g) })
	return g
}

// quick bounds a call that must not wait on the harness.
func quick(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

// A harness that ignores Stop, never answering the interrupt and never ending
// the turn, is killed once the grace runs out. The turn ends as cancelled,
// whatever the dead process was waiting on is released, and the old process's
// last words change nothing.
func TestStopKillsAHarnessThatIgnoresIt(t *testing.T) {
	withStopGrace(t, 50*time.Millisecond)
	actor, fa, st := newTestActorWith(t, &fakeAdapter{cancelGate: gate(t)})
	ctx := context.Background()

	first, err := actor.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	<-old.prompts
	if _, err := actor.Prompt(ctx, "later", nil); err != nil {
		t.Fatal(err)
	}
	old.emit(proto.Emit(proto.JobStarted, proto.JobPayload{JobID: "j1", Kind: proto.JobShell, Status: proto.JobRunning}))
	asked := make(chan adapter.PermissionOutcome, 1)
	go func() {
		out, _ := old.host.RequestPermission(context.Background(), adapter.PermissionRequest{TurnID: first.TurnID, Title: "rm -rf"})
		asked <- out
	}()
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.Pending) == 1 && len(state.Jobs) == 1
	})

	start := time.Now()
	if err := actor.Cancel(quick(t)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return old.isClosed() })
	if time.Since(start) < stopGrace {
		t.Fatalf("killed after %s, before the %s grace", time.Since(start), stopGrace)
	}
	if out := <-asked; out.Outcome != proto.OutcomeCancelled {
		t.Fatalf("permission the dead process waited on = %+v, want cancelled", out)
	}

	// The process's last event and its exit are still on their way when it
	// is killed. Both are about a process this actor no longer runs.
	sess := adapter.Session(old)
	actor.inbox <- command{kind: cmdHarnessEvent, from: sess, emission: ptr(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: first.TurnID, StopReason: proto.StopError, Error: "harness exited",
	}))}
	actor.inbox <- command{kind: cmdHarnessExit, from: sess}

	state, err := actor.State(quick(t))
	if err != nil {
		t.Fatalf("the actor did not survive the old process's exit: %v", err)
	}
	turn := state.Turns[len(state.Turns)-1]
	if turn.ID != first.TurnID || !turn.Done || turn.StopReason != proto.StopCancelled || turn.Error != "" {
		t.Fatalf("stopped turn = %+v, want cancelled", turn)
	}
	if state.Phase != "idle" || len(state.Queued) != 0 || len(state.Pending) != 0 {
		t.Fatalf("state after kill: phase=%s queued=%+v pending=%+v", state.Phase, state.Queued, state.Pending)
	}
	if state.Jobs[0].Status != proto.JobInterrupted {
		t.Fatalf("job of the killed process = %+v, want interrupted", state.Jobs[0])
	}
	meta, err := st.Thread(ctx, actor.ID)
	if err != nil || meta.Phase != "idle" {
		t.Fatalf("stored phase = %q, %v; want idle", meta.Phase, err)
	}
	select {
	case in := <-old.prompts:
		t.Fatalf("the queue behind a stopped turn ran: %+v", in)
	default:
	}
}

func ptr[T any](v T) *T { return &v }

// Asking twice is the human saying the first Stop did not work: the harness
// is killed then and there, not when the grace runs out.
func TestSecondStopKillsAtOnce(t *testing.T) {
	withStopGrace(t, time.Hour)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{cancelGate: gate(t)})
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "work", nil)
	<-fa.thread().prompts
	if err := actor.Cancel(quick(t)); err != nil {
		t.Fatal(err)
	}
	state, _ := actor.State(ctx)
	if state.Phase != "turn" || fa.thread().isClosed() {
		t.Fatalf("one Stop ended the turn without the harness: phase=%s", state.Phase)
	}
	if err := actor.Cancel(quick(t)); err != nil {
		t.Fatal(err)
	}
	state, _ = actor.State(ctx)
	if turn := state.Turns[0]; turn.ID != first.TurnID || !turn.Done || turn.StopReason != proto.StopCancelled || state.Phase != "idle" {
		t.Fatalf("after a second Stop: phase=%s turn=%+v", state.Phase, turn)
	}
	waitFor(t, func() bool { return fa.thread().isClosed() })
}

// A killed harness's exit is dropped as stale, so the statuses its
// extensions were keeping go with the kill rather than outliving it.
func TestKilledHarnessTakesItsStatusesWithIt(t *testing.T) {
	withStopGrace(t, time.Hour)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{cancelGate: gate(t)})
	ctx := context.Background()

	if _, err := actor.Prompt(ctx, "work", nil); err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	<-old.prompts
	old.emit(proto.Emit(proto.ExtensionStatus, proto.ExtensionStatusPayload{Key: "watch", Text: "watching"}))
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.ExtensionStatuses) == 1
	})

	for range 2 {
		if err := actor.Cancel(quick(t)); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return old.isClosed() })
	actor.inbox <- command{kind: cmdHarnessExit, from: adapter.Session(old)}

	state, err := actor.State(quick(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ExtensionStatuses) != 0 {
		t.Fatalf("statuses after the kill = %+v, want none", state.ExtensionStatuses)
	}
}

// A harness that ends the turn after Stop — before it has even answered the
// interrupt — is left alone, and the queue goes with the turn.
func TestStoppedTurnThatEndsInTimeIsNotKilled(t *testing.T) {
	withStopGrace(t, 50*time.Millisecond)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{cancelGate: gate(t)})
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "work", nil)
	sess := fa.thread()
	<-sess.prompts
	if _, err := actor.Prompt(ctx, "later", nil); err != nil {
		t.Fatal(err)
	}
	if err := actor.Cancel(quick(t)); err != nil {
		t.Fatal(err)
	}
	// Cancel has not answered, and the harness is still heard.
	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopCancelled}))
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return state.Phase == "idle"
	})
	if state, _ := actor.State(ctx); len(state.Queued) != 0 {
		t.Fatalf("queue survived the stopped turn: %+v", state.Queued)
	}

	// Well past the grace, and past the interrupt's own deadline.
	time.Sleep(4 * stopGrace)
	if sess.isClosed() {
		t.Fatal("a harness that stopped in time was killed")
	}
	select {
	case in := <-sess.prompts:
		t.Fatalf("the queue behind a stopped turn ran: %+v", in)
	default:
	}
	next, err := actor.Prompt(ctx, "again", nil)
	if err != nil || next.Queued() {
		t.Fatalf("prompt after the stop = %+v, %v; want a turn on the same harness", next, err)
	}
	if in := <-sess.prompts; in.Text != "again" {
		t.Fatalf("prompt after the stop = %+v", in)
	}
}

// A control call the harness is slow to answer holds up only whoever made it.
// While SetMode waits, the thread is still read, the harness is still heard,
// and a permission it asks for can be asked and answered.
func TestActorServesOtherCommandsWhileAControlCallIsPending(t *testing.T) {
	release, asked := make(chan struct{}), make(chan string, 1)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{modeGate: release, modeAsked: asked})
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "work", nil)
	sess := fa.thread()
	<-sess.prompts

	switched := make(chan error, 1)
	go func() { switched <- actor.SetMode(ctx, "acceptEdits") }()
	<-asked

	if _, err := actor.State(quick(t)); err != nil {
		t.Fatalf("state while SetMode is pending: %v", err)
	}
	sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: first.TurnID, Role: "agent", Kind: "text", BlockID: "b1", Delta: "still here"}))
	answered := make(chan adapter.PermissionOutcome, 1)
	go func() {
		out, _ := sess.host.RequestPermission(context.Background(), adapter.PermissionRequest{TurnID: first.TurnID, Title: "ls"})
		answered <- out
	}()
	var requestID string
	waitFor(t, func() bool {
		state, err := actor.State(quick(t))
		if err != nil || len(state.Pending) == 0 {
			return false
		}
		requestID = state.Pending[0].RequestID
		for _, it := range state.Items {
			if it.Text == "still here" {
				return true
			}
		}
		return false
	})
	if err := actor.ResolvePermission(quick(t), requestID, adapter.PermissionOutcome{Outcome: proto.OutcomeAllowOnce}); err != nil {
		t.Fatal(err)
	}
	if out := <-answered; out.Outcome != proto.OutcomeAllowOnce {
		t.Fatalf("permission answered while SetMode was pending = %+v", out)
	}
	select {
	case err := <-switched:
		t.Fatalf("SetMode returned before the harness answered: %v", err)
	default:
	}

	close(release)
	if err := <-switched; err != nil {
		t.Fatal(err)
	}
	if state, _ := actor.State(ctx); state.Mode != "acceptEdits" {
		t.Fatalf("mode after the harness answered = %q", state.Mode)
	}
}

// A setting the old process took just before Stop killed it never reached
// the process that replaces it, so it is refused rather than recorded.
func TestSettingTakenByAKilledHarnessIsNotRecorded(t *testing.T) {
	withStopGrace(t, time.Hour)
	release, asked := make(chan struct{}), make(chan string, 1)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{cancelGate: gate(t), modeGate: release, modeAsked: asked})
	ctx := context.Background()

	if _, err := actor.Prompt(ctx, "work", nil); err != nil {
		t.Fatal(err)
	}
	<-fa.thread().prompts
	switched := make(chan error, 1)
	go func() { switched <- actor.SetMode(ctx, "acceptEdits") }()
	<-asked
	_ = actor.Cancel(quick(t))
	_ = actor.Cancel(quick(t))

	close(release)
	if err := <-switched; !errors.Is(err, errHarnessRestarted) {
		t.Fatalf("SetMode on a killed harness err = %v, want errHarnessRestarted", err)
	}
	if state, _ := actor.State(ctx); state.Mode == "acceptEdits" {
		t.Fatal("a setting the new process never got was recorded")
	}
}

// After Stop kills the harness, the next prompt through the manager starts a
// new process that resumes the same conversation.
func TestKilledHarnessResumesOnTheNextPrompt(t *testing.T) {
	withStopGrace(t, 50*time.Millisecond)
	st, err := store.Open(filepath.Join(t.TempDir(), "killed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	fa := &fakeAdapter{cancelGate: gate(t)}
	mgr := newManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()
	actor, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	old.emit(proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{HarnessSessionID: "conv-1"}))
	if _, err := actor.Prompt(ctx, "work", nil); err != nil {
		t.Fatal(err)
	}
	<-old.prompts
	if err := actor.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return old.isClosed() })
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return state.Phase == "idle"
	})

	// The actor itself has no process to send a prompt to; the manager is
	// what starts one.
	if _, err := actor.Prompt(ctx, "direct", nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("prompt to an actor with no process err = %v, want ErrNotReady", err)
	}
	again, err := mgr.Get(ctx, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again != actor {
		t.Fatal("the manager replaced an actor that was still alive")
	}
	fresh := fa.thread()
	if fresh == old {
		t.Fatal("no new harness was started")
	}
	fa.mu.Lock()
	o := fa.opts[len(fa.opts)-1]
	fa.mu.Unlock()
	if !o.Resume || o.HarnessSessionID != "conv-1" {
		t.Fatalf("new harness started with %+v, want a resume of conv-1", o)
	}
	res, err := again.Prompt(ctx, "carry on", nil)
	if err != nil || res.Queued() {
		t.Fatalf("prompt after resume = %+v, %v", res, err)
	}
	if in := <-fresh.prompts; in.Text != "carry on" {
		t.Fatalf("resumed harness got %+v", in)
	}
}

// While a prompt is on its way to the harness mid-turn it is the harness's:
// it cannot be taken back, and the queue does not start it even if the turn
// ends. Once the harness takes it, it shows as sent.
func TestSteerInFlightIsHeldUntilTheHarnessAnswers(t *testing.T) {
	release := make(chan struct{})
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{steer: true, steerGate: release})
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "work", nil)
	sess := fa.thread()
	<-sess.prompts
	steered := make(chan PromptResult, 1)
	go func() {
		res, _ := actor.Prompt(ctx, "also", nil)
		steered <- res
	}()
	var queueID string
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		if len(state.Queued) != 1 {
			return false
		}
		queueID = state.Queued[0].QueueID
		return true
	})
	if state, _ := actor.State(ctx); state.Queued[0].Sent {
		t.Fatal("shown as sent before the harness took it")
	}
	if err := actor.DequeuePrompt(ctx, queueID); !errors.Is(err, ErrAlreadySent) {
		t.Fatalf("dequeue while the hand-over is in flight err = %v, want ErrAlreadySent", err)
	}
	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return state.Phase == "idle"
	})

	close(release)
	if res := <-steered; res.QueueID != queueID {
		t.Fatalf("steered prompt result = %+v, want queue id %s", res, queueID)
	}
	if in := <-sess.prompts; in.QueueID != queueID || in.TurnID != "" {
		t.Fatalf("harness got %+v, want the steer", in)
	}
	state, _ := actor.State(ctx)
	if len(state.Queued) != 1 || !state.Queued[0].Sent {
		t.Fatalf("queued after the harness took it = %+v, want sent", state.Queued)
	}
	select {
	case in := <-sess.prompts:
		t.Fatalf("a prompt the harness holds was started as well: %+v", in)
	default:
	}
}

// A hand-over the harness refuses after the turn has already ended leaves an
// ordinary queued prompt, which starts at once since nothing is running.
func TestSteerRefusedAfterTheTurnEndedStartsTheQueue(t *testing.T) {
	release := make(chan struct{})
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{steer: true, steerGate: release, steerRefuse: errors.New("not mid-turn")})
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "work", nil)
	sess := fa.thread()
	<-sess.prompts
	steered := make(chan PromptResult, 1)
	go func() {
		res, _ := actor.Prompt(ctx, "also", nil)
		steered <- res
	}()
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.Queued) == 1
	})
	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return state.Phase == "idle"
	})
	select {
	case in := <-sess.prompts:
		t.Fatalf("started while the hand-over was in flight: %+v", in)
	default:
	}

	close(release)
	<-steered
	in := <-sess.prompts
	if in.Text != "also" || in.TurnID == "" {
		t.Fatalf("prompt after a refused hand-over = %+v, want its own turn", in)
	}
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.Queued) == 0 && len(state.Turns) == 2 && state.Turns[1].Prompt == "also"
	})
}

// Two settings asked one after the other reach the harness in that order, and
// are recorded in that order, however long the harness takes to answer the
// first. Otherwise the second's answer could come back first, and the log
// would end on the setting the harness no longer has.
func TestSettingsReachTheHarnessAndTheLogInTheOrderAsked(t *testing.T) {
	held, asked := make(chan struct{}), make(chan string, 2)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{modeHeld: map[string]<-chan struct{}{"plan": held}, modeAsked: asked})
	ctx := context.Background()

	first := command{kind: cmdSetMode, mode: "plan", reply: make(chan cmdResult, 1)}
	second := command{kind: cmdSetMode, mode: "acceptEdits", reply: make(chan cmdResult, 1)}
	actor.inbox <- first
	actor.inbox <- second
	if mode := <-asked; mode != "plan" {
		t.Fatalf("first mode the harness was asked for = %q, want plan", mode)
	}
	// The harness has taken plan and not yet said so.
	select {
	case mode := <-asked:
		t.Fatalf("the harness was asked for %q before it answered plan", mode)
	case <-time.After(50 * time.Millisecond):
	}

	close(held)
	for _, c := range []command{first, second} {
		if r := <-c.reply; r.err != nil {
			t.Fatalf("SetMode %s: %v", c.mode, r.err)
		}
	}
	state, _ := actor.State(ctx)
	if state.Mode != "acceptEdits" || fa.thread().currentMode() != "acceptEdits" {
		t.Fatalf("mode recorded %q, harness has %q; want acceptEdits for both", state.Mode, fa.thread().currentMode())
	}
}

// A steer still on its way to the harness when Stop is pressed reaches it
// before the interrupt does, so the interrupt discards it with the rest. The
// other way round, the harness would take it after the interrupt and start a
// turn for a prompt the user stopped.
func TestSteerInFlightReachesTheHarnessBeforeStop(t *testing.T) {
	withStopGrace(t, time.Hour)
	release := make(chan struct{})
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{steer: true, steerGate: release})
	ctx := context.Background()

	if _, err := actor.Prompt(ctx, "work", nil); err != nil {
		t.Fatal(err)
	}
	sess := fa.thread()
	<-sess.prompts
	go func() { _, _ = actor.Prompt(ctx, "also", nil) }()
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.Queued) == 1
	})
	if err := actor.Cancel(quick(t)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if calls := sess.taken(); len(calls) != 0 {
		t.Fatalf("the harness took %v while the steer was still on its way", calls)
	}

	close(release)
	waitFor(t, func() bool { return len(sess.taken()) == 2 })
	if calls := sess.taken(); calls[0] != "steer" || calls[1] != "cancel" {
		t.Fatalf("the harness took %v, want the steer before the interrupt", calls)
	}
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.Queued) == 0
	})
}

// A question the killed process asks on its way out, after Stop has already
// replaced it, is answered cancelled at once and leaves nothing waiting in
// the log: there is no process left for an answer to reach.
func TestQuestionFromAKilledHarnessIsCancelled(t *testing.T) {
	withStopGrace(t, time.Hour)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{cancelGate: gate(t)})
	ctx := context.Background()

	first, err := actor.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	<-old.prompts
	_ = actor.Cancel(quick(t))
	_ = actor.Cancel(quick(t))

	out, err := old.host.RequestPermission(quick(t), adapter.PermissionRequest{TurnID: first.TurnID, Title: "rm -rf"})
	if err != nil || out.Outcome != proto.OutcomeCancelled {
		t.Fatalf("permission from the killed process = %+v, %v; want cancelled", out, err)
	}
	res, err := old.host.Elicit(quick(t), adapter.ElicitationRequest{TurnID: first.TurnID, Prompt: "which?"})
	if err != nil || res.Action != "cancel" {
		t.Fatalf("elicitation from the killed process = %+v, %v; want cancel", res, err)
	}
	state, _ := actor.State(ctx)
	if len(state.Pending) != 0 || len(state.Elicitations) != 0 {
		t.Fatalf("left waiting: pending=%+v elicitations=%+v", state.Pending, state.Elicitations)
	}
}

// A prompt sent while a setting is still on its way to the harness waits for
// it, so a model or mode changed just before sending is the one the prompt
// runs under.
func TestPromptWaitsForASettingStillOnItsWay(t *testing.T) {
	held, asked := make(chan struct{}), make(chan string, 1)
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{modeHeld: map[string]<-chan struct{}{"plan": held}, modeAsked: asked})
	ctx := context.Background()

	set := command{kind: cmdSetMode, mode: "plan", reply: make(chan cmdResult, 1)}
	actor.inbox <- set
	<-asked
	res, err := actor.Prompt(ctx, "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Queued() {
		t.Fatalf("prompt = %+v, want it queued behind the setting", res)
	}
	select {
	case in := <-fa.thread().prompts:
		t.Fatalf("the harness got %+v before it answered the setting", in)
	case <-time.After(50 * time.Millisecond):
	}

	close(held)
	if r := <-set.reply; r.err != nil {
		t.Fatal(r.err)
	}
	in := <-fa.thread().prompts
	if in.Text != "go" || fa.thread().currentMode() != "plan" {
		t.Fatalf("prompt %+v ran under mode %q, want go under plan", in, fa.thread().currentMode())
	}
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return len(state.Queued) == 0 && len(state.Turns) == 1
	})
}

// Stop cancels what was waiting behind the turn, not what is sent after it: a
// prompt typed once Stop is pressed is held, not handed to the harness behind
// the interrupt, and starts its own turn once the stopped one has ended.
func TestPromptSentAfterStopRunsOnceTheTurnEnds(t *testing.T) {
	withStopGrace(t, time.Hour)
	release := make(chan struct{})
	actor, fa, _ := newTestActorWith(t, &fakeAdapter{steer: true, cancelGate: release})
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "work", nil)
	sess := fa.thread()
	<-sess.prompts
	if _, err := actor.Prompt(ctx, "before", nil); err != nil {
		t.Fatal(err)
	}
	<-sess.prompts
	if err := actor.Cancel(quick(t)); err != nil {
		t.Fatal(err)
	}
	after, err := actor.Prompt(quick(t), "after", nil)
	if err != nil || !after.Queued() {
		t.Fatalf("prompt after Stop = %+v, %v; want it queued", after, err)
	}

	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopCancelled}))
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return state.Phase == "idle" && len(state.Queued) == 1
	})
	if state, _ := actor.State(ctx); state.Queued[0].QueueID != after.QueueID {
		t.Fatalf("queue after the stopped turn = %+v, want only the prompt sent after Stop", state.Queued)
	}

	close(release)
	if in := <-sess.prompts; in.Text != "after" {
		t.Fatalf("next prompt the harness got = %+v", in)
	}
	if calls := sess.taken(); len(calls) != 2 || calls[0] != "steer" || calls[1] != "cancel" {
		t.Fatalf("the harness took %v, want the prompt after Stop not steered", calls)
	}
}
