package codexapp

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// TestSteeredMessageIsInjected: codex reads a steered prompt into the running
// turn as a userMessage item carrying the clientUserMessageId it was sent
// with — the queue id. That is prompt.injected. The turn's own prompt, which
// has no client id, is already in the log.
func TestSteeredMessageIsInjected(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn"

	s.handleNotification("item/completed", json.RawMessage(
		`{"threadId":"thread-1","turnId":"codex-turn","item":{"type":"userMessage","id":"u1","clientId":null,"content":[{"type":"text","text":"go"}]}}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("own prompt emitted %+v", got)
	}

	s.handleNotification("item/completed", json.RawMessage(
		`{"threadId":"thread-1","turnId":"codex-turn","item":{"type":"userMessage","id":"u2","clientId":"q1","content":[{"type":"text","text":"also"}]}}`))
	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.PromptInjected {
		t.Fatalf("events = %+v, want one prompt.injected", got)
	}
	if p := got[0].Payload.(proto.PromptInjectedPayload); p.QueueID != "q1" || p.TurnID != "omniplex-turn" {
		t.Fatalf("payload = %+v", p)
	}
}
