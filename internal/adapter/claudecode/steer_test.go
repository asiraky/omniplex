package claudecode

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func drainEvents(s *session) []proto.Emission {
	var out []proto.Emission
	for {
		select {
		case e := <-s.events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func replay(t *testing.T, uuid, text string) map[string]any {
	t.Helper()
	return map[string]any{
		"type":     "user",
		"isReplay": true,
		"uuid":     uuid,
		"message":  map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": text}}},
	}
}

// TestSteerReadMidTurnIsInjected: the CLI echoes a steered message, with the
// queue id stamped on it, when it folds it into the running turn. That echo
// is prompt.injected for the turn that was open.
func TestSteerReadMidTurnIsInjected(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	s.steers["q1"] = false

	s.handleSDKMessage(rawSDK(t, replay(t, "q1", "also this")))
	got := drainEvents(s)
	if len(got) != 1 || got[0].Type != proto.PromptInjected {
		t.Fatalf("events = %+v, want one prompt.injected", got)
	}
	p := got[0].Payload.(proto.PromptInjectedPayload)
	if p.QueueID != "q1" || p.TurnID != "turn-1" {
		t.Fatalf("payload = %+v", p)
	}
	if _, pending := s.steers["q1"]; pending {
		t.Fatal("steer still pending after being read")
	}
}

// TestSteerReadIdleStartsATurn: a steer the CLI reads once the turn had ended
// starts a turn of the CLI's own. That is a turn.started naming the queue
// entry, and the session is busy again.
func TestSteerReadIdleStartsATurn(t *testing.T) {
	s := newTestSession()
	s.steers["q1"] = false

	s.handleSDKMessage(rawSDK(t, replay(t, "q1", "later")))
	got := drainEvents(s)
	if len(got) != 1 || got[0].Type != proto.TurnStarted {
		t.Fatalf("events = %+v, want one turn.started", got)
	}
	p := got[0].Payload.(proto.TurnStartedPayload)
	if p.QueueID != "q1" || p.Prompt != "later" || p.TurnID == "" || s.turnID != p.TurnID {
		t.Fatalf("payload = %+v, session turn = %q", p, s.turnID)
	}
}

// TestOwnPromptEchoIsNotAnEvent: with replay on, the CLI echoes the turn's
// own prompt too. It carries the turn id, not a queue id, and says nothing
// new.
func TestOwnPromptEchoIsNotAnEvent(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	s.handleSDKMessage(rawSDK(t, replay(t, "turn-1", "the prompt")))
	if got := drainEvents(s); len(got) != 0 {
		t.Fatalf("own prompt echo emitted %+v", got)
	}
}

// TestDiscardedSteerIsSuppressed: a steer Cancel discarded is still run by
// the CLI as a fresh turn. Its echo opens nothing, its output is dropped, and
// its result lifts the suppression without finishing a turn.
func TestDiscardedSteerIsSuppressed(t *testing.T) {
	s := newTestSession()
	s.steers["q1"] = true // as Cancel leaves it
	s.suppress = true     // as handleReplay would set it (the interrupt RPC needs a bridge)

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":  "stream_event",
		"event": map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text"}},
	}))
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true}))
	for _, e := range drainEvents(s) {
		if e.Type == proto.TurnStarted || e.Type == proto.TurnFinished || e.Type == proto.MessageChunk {
			t.Fatalf("suppressed turn leaked %s", e.Type)
		}
	}
	if s.suppress || s.turnID != "" {
		t.Fatalf("suppress=%v turnID=%q after the result", s.suppress, s.turnID)
	}
}
