package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// A built-in command the harness answers itself — /usage, /context, /cost —
// comes back as a synthetic assistant message with no stream events. Its text
// lives nowhere else, so dropping full assistant messages (as the streaming
// path is entitled to) made those commands run and show nothing.
func TestSyntheticAssistantTextIsShown(t *testing.T) {
	s, _ := newBridgeSession(t)
	s.turnID = "turn-1"

	var msg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(`{"type":"assistant","message":{"id":"m1","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"Current session: 3% used"}]}}`), &msg); err != nil {
		t.Fatal(err)
	}
	s.handleAssistant(msg)

	select {
	case em := <-s.events:
		p, ok := em.Payload.(proto.MessageChunkPayload)
		if !ok || em.Type != proto.MessageChunk {
			t.Fatalf("expected message.chunk, got %s", em.Type)
		}
		if p.Delta != "Current session: 3% used" || p.TurnID != "turn-1" || p.Kind != "text" {
			t.Fatalf("unexpected chunk %+v", p)
		}
	default:
		t.Fatal("synthetic text was dropped")
	}
}

// Text that did stream must not be shown twice when the full message follows.
func TestStreamedAssistantTextIsNotRepeated(t *testing.T) {
	s, _ := newBridgeSession(t)
	s.turnID = "turn-1"
	s.streams[""] = &stream{messageID: "m1", blocks: map[int]*block{}}

	var msg map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"already streamed"}]}}`), &msg)
	s.handleAssistant(msg)

	select {
	case em := <-s.events:
		t.Fatalf("streamed text was emitted again: %s", em.Type)
	default:
	}
}
