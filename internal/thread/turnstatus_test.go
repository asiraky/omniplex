package thread

import (
	"context"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// settleStream emits an event that is logged but does not touch the turn status,
// and waits for it to land. The harness stream is ordered, so everything
// emitted before it has been handled too.
func settleStream(t *testing.T, actor *Actor, fa *fakeAdapter) {
	t.Helper()
	head := actor.Head()
	fa.thread().emit(proto.Emit(proto.UsageUpdated, proto.UsageUpdatedPayload{Input: head + 1}))
	waitFor(t, func() bool { return actor.Head() > head })
}

func expectSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no status change signalled")
	}
}

func expectNoSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("status change signalled when nothing changed")
	default:
	}
}

func startFakeTurn(t *testing.T, actor *Actor, fa *fakeAdapter) string {
	t.Helper()
	if _, err := actor.Prompt(context.Background(), "go", nil); err != nil {
		t.Fatal(err)
	}
	return (<-fa.thread().prompts).TurnID
}

func TestTurnStatusIsHeldAndPushedButNeverLogged(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()
	sub := actor.Subscribe()
	defer actor.Unsubscribe(sub)

	turnID := startFakeTurn(t, actor, fa)
	settleStream(t, actor, fa)
	head := actor.Head()

	retrying := proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 2, Max: 10, Reason: "overloaded", RetryAt: 1731300000000}
	fa.thread().emit(proto.Emit(proto.TurnStatus, retrying))
	expectSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got == nil || *got != retrying {
		t.Fatalf("TurnStatus = %+v, want %+v", got, retrying)
	}

	// The same status again is not news.
	fa.thread().emit(proto.Emit(proto.TurnStatus, retrying))
	settleStream(t, actor, fa)
	expectNoSignal(t, sub.StatusChanged)

	// Nothing took a seq: the status never reached the log or the projection.
	if got := actor.Head(); got != head+1 {
		t.Fatalf("head = %d, want %d (only the settle event)", got, head+1)
	}
	evs, err := st.ReadEvents(ctx, actor.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type == proto.TurnStatus {
			t.Fatalf("turn.status was logged at seq %d", ev.Seq)
		}
	}

	// The harness speaking again clears it, with nobody having said so.
	fa.thread().emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turnID, Role: "agent", Kind: "text", BlockID: "b1", Delta: "back",
	}))
	expectSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got != nil {
		t.Fatalf("TurnStatus after a message chunk = %+v, want nil", got)
	}

	// An explicit clear with nothing held changes nothing.
	fa.thread().emit(proto.Emit(proto.TurnStatus, proto.TurnStatusPayload{}))
	settleStream(t, actor, fa)
	expectNoSignal(t, sub.StatusChanged)
}

func TestTurnStatusClearsWhenTheTurnFinishes(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sub := actor.Subscribe()
	defer actor.Unsubscribe(sub)

	turnID := startFakeTurn(t, actor, fa)
	fa.thread().emit(proto.Emit(proto.TurnStatus, proto.TurnStatusPayload{State: proto.TurnCompacting}))
	expectSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got == nil || got.State != proto.TurnCompacting {
		t.Fatalf("TurnStatus = %+v, want compacting", got)
	}

	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turnID, StopReason: proto.StopEndTurn}))
	expectSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got != nil {
		t.Fatalf("TurnStatus after turn.finished = %+v, want nil", got)
	}
}

func TestTurnStatusWhileIdleIsDropped(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sub := actor.Subscribe()
	defer actor.Unsubscribe(sub)

	fa.thread().emit(proto.Emit(proto.TurnStatus, proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 1}))
	settleStream(t, actor, fa)
	expectNoSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got != nil {
		t.Fatalf("TurnStatus while idle = %+v, want nil", got)
	}
}

func TestSubagentOutputLeavesTheTurnStatus(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sub := actor.Subscribe()
	defer actor.Unsubscribe(sub)

	turnID := startFakeTurn(t, actor, fa)
	retrying := proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 1, Max: 10}
	fa.thread().emit(proto.Emit(proto.TurnStatus, retrying))
	expectSignal(t, sub.StatusChanged)

	// A subagent the turn spawned keeps working while the top level retries.
	fa.thread().emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turnID, Role: "agent", Kind: "text", BlockID: "sub-b1", Delta: "digging", ParentToolCallID: "task-1",
	}))
	fa.thread().emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: turnID, ToolCallID: "sub-read", Kind: proto.KindRead, Title: "Read", Status: proto.StatusInProgress, ParentToolCallID: "task-1",
	}))
	settleStream(t, actor, fa)
	expectNoSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got == nil || *got != retrying {
		t.Fatalf("TurnStatus after subagent output = %+v, want %+v", got, retrying)
	}
}

func TestStaleTurnFinishedLeavesTheTurnStatus(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sub := actor.Subscribe()
	defer actor.Unsubscribe(sub)

	turnID := startFakeTurn(t, actor, fa)
	retrying := proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 3, Max: 10}
	fa.thread().emit(proto.Emit(proto.TurnStatus, retrying))
	expectSignal(t, sub.StatusChanged)

	// A late close for a turn the actor has already moved past.
	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "old-turn", StopReason: proto.StopEndTurn}))
	settleStream(t, actor, fa)
	expectNoSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got == nil || *got != retrying {
		t.Fatalf("TurnStatus after a stale turn.finished = %+v, want %+v", got, retrying)
	}

	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turnID, StopReason: proto.StopEndTurn}))
	expectSignal(t, sub.StatusChanged)
	if got := actor.TurnStatus(); got != nil {
		t.Fatalf("TurnStatus after the turn finished = %+v, want nil", got)
	}
}
