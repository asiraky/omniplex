package thread

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// An idle thread the loop is about to start a queued prompt on is working,
// and only then: startsNext asks what dispatchQueued asks.
func TestStartsNextOnlyWhenDispatchWould(t *testing.T) {
	ready := func(t *testing.T) (*Actor, *projectionWriter) {
		t.Helper()
		w := newProjectionWriter()
		w.write(t, proto.Emit(proto.WorkspaceRequested, proto.WorkspaceRequestedPayload{ProjectID: "p"}))
		w.write(t, proto.Emit(proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "go"}))
		w.write(t, proto.Emit(proto.WorkspaceReady, proto.WorkspaceReadyPayload{Cwd: "/w"}))
		return &Actor{state: w.state, sess: &fakeThread{}, sent: map[string]bool{}}, w
	}

	t.Run("workspace ready with a prompt queued", func(t *testing.T) {
		a, _ := ready(t)
		if !a.startsNext() {
			t.Fatal("the first queued prompt is about to start, but startsNext says no")
		}
	})
	t.Run("no harness to run it", func(t *testing.T) {
		a, _ := ready(t)
		a.sess = nil
		if a.startsNext() {
			t.Fatal("claims a start with no harness")
		}
	})
	t.Run("a stop still open", func(t *testing.T) {
		a, _ := ready(t)
		a.stopping = &stopRequest{}
		if a.startsNext() {
			t.Fatal("claims a start while a stop is open")
		}
	})
	t.Run("a control call in flight", func(t *testing.T) {
		a, _ := ready(t)
		a.controlPending = 1
		if a.startsNext() {
			t.Fatal("claims a start while a control call is pending")
		}
	})
	t.Run("the harness already holds it", func(t *testing.T) {
		a, _ := ready(t)
		a.sent["q1"] = true
		if a.startsNext() {
			t.Fatal("claims a start for a prompt the harness already has")
		}
	})
	t.Run("a held prompt heads the line", func(t *testing.T) {
		a, w := ready(t)
		w.write(t, proto.Emit(proto.PromptHeld, proto.PromptHeldPayload{QueueIDs: []string{"q1"}}))
		w.write(t, proto.Emit(proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q2", Prompt: "behind"}))
		if a.startsNext() {
			t.Fatal("looks past a held prompt the dispatcher stops at")
		}
	})
	t.Run("sent first past a held line", func(t *testing.T) {
		a, w := ready(t)
		w.write(t, proto.Emit(proto.PromptHeld, proto.PromptHeldPayload{QueueIDs: []string{"q1"}}))
		w.write(t, proto.Emit(proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q2", Prompt: "now"}))
		a.sendFirst = "q2"
		if !a.startsNext() {
			t.Fatal("a prompt sent past the held line is about to start, but startsNext says no")
		}
	})
	t.Run("the last turn failed", func(t *testing.T) {
		a, w := ready(t)
		w.write(t, proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "x"}))
		w.write(t, proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopError, Error: "boom"}))
		if a.startsNext() {
			t.Fatal("hides a failed turn behind the prompt queued after it")
		}
	})
	t.Run("a usage limit holds the queue", func(t *testing.T) {
		a, w := ready(t)
		w.write(t, proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "x"}))
		w.write(t, proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopError, Failure: proto.FailureLimit}))
		if a.startsNext() {
			t.Fatal("claims a start for a prompt a usage limit holds")
		}
	})
	t.Run("nothing queued", func(t *testing.T) {
		a, w := ready(t)
		w.write(t, proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go", QueueID: "q1"}))
		w.write(t, proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
		if a.startsNext() {
			t.Fatal("claims a start with the queue empty")
		}
	})
}
