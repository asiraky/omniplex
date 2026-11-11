package thread

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// hitLimit runs a turn, optionally queues a prompt behind it, and has the
// harness stop it at a usage limit that resets at resetAt.
func hitLimit(t *testing.T, a *Actor, th *fakeThread, resetAt int64, queue string) string {
	t.Helper()
	ctx := context.Background()
	res, err := a.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-th.prompts
	if queue != "" {
		if q, err := a.Prompt(ctx, queue, nil); err != nil || !q.Queued() {
			t.Fatalf("queue %q = %+v, %v", queue, q, err)
		}
	}
	th.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: res.TurnID, StopReason: proto.StopError, Error: "usage limit reached",
		Failure: proto.FailureLimit, ResetAt: resetAt,
	}))
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
	return res.TurnID
}

func limitSchedules(t *testing.T, a *Actor) []proto.ScheduledPrompt {
	t.Helper()
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.ScheduledPrompt
	for _, p := range s.Scheduled {
		if p.Kind == proto.ScheduleLimit {
			out = append(out, p)
		}
	}
	return out
}

func nothingSent(t *testing.T, th *fakeThread, why string) {
	t.Helper()
	select {
	case got := <-th.prompts:
		t.Fatalf("%s: harness got %q", why, got.Text)
	default:
	}
}

func nextPrompt(t *testing.T, th *fakeThread) string {
	t.Helper()
	select {
	case got := <-th.prompts:
		return got.Text
	case <-time.After(time.Second):
		t.Fatal("no prompt reached the harness")
		return ""
	}
}

// A weekly limit resets days out; the continuation is armed for a minute
// after it all the same, in the due index the scheduler reads.
func TestLimitArmsContinuationAfterReset(t *testing.T) {
	a, fa, st := newTestActor(t)
	ctx := context.Background()
	reset := time.Now().Add(72 * time.Hour).UnixMilli()
	turn := hitLimit(t, a, fa.thread(), reset, "")

	got := scheduled(t, a, limitScheduleID(turn))
	state, _ := a.State(ctx)
	if got.Kind != proto.ScheduleLimit || got.Status != "pending" || got.DueAt != reset+limitDelay || got.Model != state.Model || got.Mode != state.Mode {
		t.Fatalf("continuation = %+v", got)
	}
	if ids, err := st.DueScheduleThreads(ctx, got.DueAt-1); err != nil || len(ids) != 0 {
		t.Fatalf("due before its time: %v %v", ids, err)
	}
	if ids, err := st.DueScheduleThreads(ctx, got.DueAt); err != nil || len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("not in the due index: %v %v", ids, err)
	}
}

func TestLimitWithoutAUsableResetArmsNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure string
		reset   func() int64
	}{
		{"unknown reset", proto.FailureLimit, func() int64 { return 0 }},
		{"reset before the finish", proto.FailureLimit, func() int64 { return time.Now().Add(-time.Second).UnixMilli() }},
		{"not a limit", proto.FailureAuth, func() int64 { return time.Now().Add(time.Hour).UnixMilli() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, fa, _ := newTestActor(t)
			ctx := context.Background()
			res, err := a.Prompt(ctx, "work", nil)
			if err != nil {
				t.Fatal(err)
			}
			<-fa.thread().prompts
			fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: res.TurnID, StopReason: proto.StopError, Failure: tc.failure, ResetAt: tc.reset(),
			}))
			waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
			if got := limitSchedules(t, a); len(got) != 0 {
				t.Fatalf("armed %+v", got)
			}
		})
	}
}

// The queue waits out the limit; the continuation goes past it when due, and
// the queue drains once the continuation's turn ends.
func TestLimitHoldsTheQueueUntilTheContinuationRuns(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	th := fa.thread()
	turn := hitLimit(t, a, th, time.Now().Add(time.Hour).UnixMilli(), "after")
	nothingSent(t, th, "queue dispatched into the limit")
	if s, _ := a.State(ctx); len(s.Queued) != 1 {
		t.Fatalf("queue = %+v, want it held, not dropped", s.Queued)
	}

	p := scheduled(t, a, limitScheduleID(turn))
	if err := a.scheduleTick(ctx, p.DueAt-1); err != nil {
		t.Fatal(err)
	}
	nothingSent(t, th, "continuation sent early")
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	var cont string
	select {
	case got := <-th.prompts:
		if got.Text != limitContinuePrompt {
			t.Fatalf("first prompt after the reset = %q, want the continuation", got.Text)
		}
		cont = got.TurnID
	case <-time.After(time.Second):
		t.Fatal("continuation never sent")
	}
	if got := scheduled(t, a, p.ID).Status; got != "sent" {
		t.Fatalf("continuation status = %s", got)
	}
	th.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: cont, StopReason: proto.StopEndTurn}))
	if got := nextPrompt(t, th); got != "after" {
		t.Fatalf("after the continuation = %q, want the queued prompt", got)
	}
}

// Sending by hand lifts the hold, and the continuation is called off: it
// would land in a conversation that has moved on.
func TestManualSendReleasesTheHoldAndCancelsTheContinuation(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	th := fa.thread()
	turn := hitLimit(t, a, th, time.Now().Add(time.Hour).UnixMilli(), "after")
	res, err := a.Prompt(ctx, "by hand", nil)
	if err != nil || res.Queued() {
		t.Fatalf("manual send = %+v, %v; want it started", res, err)
	}
	if got := nextPrompt(t, th); got != "by hand" {
		t.Fatalf("got %q", got)
	}
	p := scheduled(t, a, limitScheduleID(turn))
	if p.Status != "cancelled" {
		t.Fatalf("continuation = %s, want cancelled", p.Status)
	}
	th.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn}))
	if got := nextPrompt(t, th); got != "after" {
		t.Fatalf("got %q, want the queue to drain", got)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	nothingSent(t, th, "cancelled continuation sent")
}

func TestStopCancelsTheContinuation(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	turn := hitLimit(t, a, fa.thread(), time.Now().Add(time.Hour).UnixMilli(), "")
	if err := a.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	p := scheduled(t, a, limitScheduleID(turn))
	if p.Status != "cancelled" {
		t.Fatalf("continuation = %s, want cancelled", p.Status)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	nothingSent(t, fa.thread(), "stopped continuation sent")
}

// The reset belongs to the account the thread hit it on.
func TestAccountSwitchCancelsTheContinuation(t *testing.T) {
	mgr, fa, _ := switchTestManager(t)
	ctx := context.Background()
	a, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	turn := hitLimit(t, a, fa.thread(), time.Now().Add(time.Hour).UnixMilli(), "")
	if err := mgr.SwitchAccount(ctx, a.ID, "fake-work"); err != nil {
		t.Fatal(err)
	}
	if got := scheduled(t, a, limitScheduleID(turn)).Status; got != "cancelled" {
		t.Fatalf("continuation = %s, want cancelled", got)
	}
}

// The continuation lives in the log and the due index, so a host that was
// down when it fell due still sends it — up to a day late — ahead of the
// held queue.
func TestLimitContinuationSurvivesRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		late   int64
		status string
	}{{"two hours late", 2 * scheduleGrace, "sent"}, {"over a day late", limitGrace + 1, "missed"}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "limit-restart.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			first := &fakeAdapter{}
			m := NewManager(st, t.Logf, first)
			a, err := m.Create(ctx, "fake", "", t.TempDir(), "", "")
			if err != nil {
				t.Fatal(err)
			}
			turn := hitLimit(t, a, first.thread(), time.Now().Add(time.Hour).UnixMilli(), "after")
			due := scheduled(t, a, limitScheduleID(turn)).DueAt
			id := a.ID
			m.Shutdown()

			fa := &fakeAdapter{}
			m = NewManager(st, t.Logf, fa)
			defer m.Shutdown()
			if ids, err := st.DueScheduleThreads(ctx, due+tc.late); err != nil || len(ids) != 1 {
				t.Fatalf("lost continuation %v %v", ids, err)
			}
			a, err = m.View(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.scheduleTick(ctx, due+tc.late); err != nil {
				t.Fatal(err)
			}
			if got := scheduled(t, a, limitScheduleID(turn)).Status; got != tc.status {
				t.Fatalf("got %s want %s", got, tc.status)
			}
			if tc.status == "missed" {
				if fa.thread() != nil {
					t.Fatal("missed continuation started the harness")
				}
				return
			}
			if got := nextPrompt(t, fa.thread()); got != limitContinuePrompt {
				t.Fatalf("first prompt after restart = %q, want the continuation", got)
			}
		})
	}
}

// harnessEmit hands the actor an event and waits for it to land in the log,
// so a test can check that it changed nothing.
func harnessEmit(t *testing.T, a *Actor, th *fakeThread, em proto.Emission) {
	t.Helper()
	ctx := context.Background()
	before, err := a.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	th.emit(em)
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.Seq > before.Seq })
}

func backFill(turn string, resetAt int64) proto.Emission {
	return proto.Emit(proto.TurnLimitReset, proto.TurnLimitResetPayload{TurnID: turn, ResetAt: resetAt})
}

// Codex closes its turn and reads the reset before it reports the finish,
// and Stop in that gap cancels nothing: the turn is still running to the
// actor, and the harness has nothing left to interrupt. The finish that
// follows is still the turn the human stopped.
func TestStopBeforeALateLimitFinishArmsNothing(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	th := fa.thread()
	res, err := a.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-th.prompts
	if err := a.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	reset := time.Now().Add(time.Hour).UnixMilli()
	th.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: res.TurnID, StopReason: proto.StopError, Failure: proto.FailureLimit, ResetAt: reset,
	}))
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
	harnessEmit(t, a, th, backFill(res.TurnID, reset))
	if got := limitSchedules(t, a); len(got) != 0 {
		t.Fatalf("armed a continuation for a stopped turn: %+v", got)
	}
}

// A Codex turn can finish on a limit before the harness knows its reset;
// the back-fill arms the continuation once it does.
func TestBackFillArmsTheContinuation(t *testing.T) {
	a, fa, _ := newTestActor(t)
	th := fa.thread()
	turn := hitLimit(t, a, th, 0, "after")
	if got := limitSchedules(t, a); len(got) != 0 {
		t.Fatalf("armed with the reset unknown: %+v", got)
	}
	reset := time.Now().Add(time.Hour).UnixMilli()
	harnessEmit(t, a, th, backFill(turn, reset))
	if got := scheduled(t, a, limitScheduleID(turn)); got.Status != "pending" || got.DueAt != reset+limitDelay {
		t.Fatalf("continuation = %+v", got)
	}
}

func TestBackFillThatArmsNothing(t *testing.T) {
	hour := func() int64 { return time.Now().Add(time.Hour).UnixMilli() }
	for _, tc := range []struct {
		name string
		// setup leaves the thread where the back-fill lands, and names the
		// turn it is for and the continuations it should leave.
		setup func(t *testing.T, a *Actor, th *fakeThread) (turn string, want int)
		reset func() int64
	}{
		{"reset already past", func(t *testing.T, a *Actor, th *fakeThread) (string, int) {
			return hitLimit(t, a, th, 0, ""), 0
		}, func() int64 { return time.Now().Add(-time.Second).UnixMilli() }},
		{"the thread moved on", func(t *testing.T, a *Actor, th *fakeThread) (string, int) {
			turn := hitLimit(t, a, th, 0, "")
			ctx := context.Background()
			res, err := a.Prompt(ctx, "by hand", nil)
			if err != nil {
				t.Fatal(err)
			}
			<-th.prompts
			th.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn}))
			waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
			return turn, 0
		}, hour},
		{"not a limit", func(t *testing.T, a *Actor, th *fakeThread) (string, int) {
			ctx := context.Background()
			res, err := a.Prompt(ctx, "work", nil)
			if err != nil {
				t.Fatal(err)
			}
			<-th.prompts
			th.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: res.TurnID, StopReason: proto.StopError, Failure: proto.FailureAuth,
			}))
			waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
			return res.TurnID, 0
		}, hour},
		{"a continuation already armed", func(t *testing.T, a *Actor, th *fakeThread) (string, int) {
			return hitLimit(t, a, th, time.Now().Add(2*time.Hour).UnixMilli(), ""), 1
		}, hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, fa, _ := newTestActor(t)
			th := fa.thread()
			turn, want := tc.setup(t, a, th)
			before := limitSchedules(t, a)
			harnessEmit(t, a, th, backFill(turn, tc.reset()))
			got := limitSchedules(t, a)
			if len(got) != want || (want > 0 && (got[0].DueAt != before[0].DueAt || got[0].Revision != before[0].Revision)) {
				t.Fatalf("continuations = %+v, want %d unchanged", got, want)
			}
		})
	}
}

// A human's schedule waits behind a held queue while the continuation is
// armed: starting it would walk into the limit and call the continuation off.
func TestHumanScheduleWaitsWhileAContinuationIsArmed(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	th := fa.thread()
	turn := hitLimit(t, a, th, time.Now().Add(time.Hour).UnixMilli(), "after")
	p := scheduleInput("mine")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	noPrompt(t, th, "human schedule jumped the held queue")
	cont := scheduled(t, a, limitScheduleID(turn))
	if cont.Status != "pending" {
		t.Fatalf("continuation = %s, want it still armed", cont.Status)
	}
	if err := a.scheduleTick(ctx, cont.DueAt); err != nil {
		t.Fatal(err)
	}
	if got := nextPrompt(t, th); got != limitContinuePrompt {
		t.Fatalf("first prompt = %q, want the continuation", got)
	}
}

// With the reset unknown nothing is armed, and a human's schedule is the way
// round the limit: it goes past the held queue.
func TestHumanScheduleJumpsAHeldQueueWithNoContinuation(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	th := fa.thread()
	hitLimit(t, a, th, 0, "after")
	p := scheduleInput("mine")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	if got := nextPrompt(t, th); got != p.Prompt {
		t.Fatalf("first prompt = %q, want the human's schedule", got)
	}
}
