package thread

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func chunkEvent(t *testing.T, seq int64, delta string) proto.Event {
	t.Helper()
	b, err := json.Marshal(proto.MessageChunkPayload{Role: "agent", Kind: "text", BlockID: "b1", Delta: delta})
	if err != nil {
		t.Fatal(err)
	}
	return proto.Event{Seq: seq, Type: proto.MessageChunk, Payload: b}
}

func toolOutputEvent(t *testing.T, seq int64, text string) proto.Event {
	t.Helper()
	b, err := json.Marshal(proto.ToolCallUpdatedPayload{
		ToolCallID: "call-a", Content: []proto.ToolContent{{Type: "text", Text: text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return proto.Event{Seq: seq, Type: proto.ToolCallUpdated, Payload: b}
}

func truncatedFlag(t *testing.T, ev proto.Event) bool {
	t.Helper()
	var p struct {
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Truncated
}

func TestReplayIsRefusedPastTheEventBudget(t *testing.T) {
	evs := make([]proto.Event, MaxReplayEvents+1)
	for i := range evs {
		evs[i] = chunkEvent(t, int64(i+1), "x")
	}
	if _, ok := wireReplay(evs[:MaxReplayEvents]); !ok {
		t.Fatal("a replay of exactly the event budget was refused")
	}
	if _, ok := wireReplay(evs); ok {
		t.Fatal("a replay past the event budget was accepted")
	}
}

// Message chunks are never capped, so a few big ones are a replay that is
// still too heavy once it is wire-shaped.
func TestReplayIsRefusedPastTheByteBudget(t *testing.T) {
	big := strings.Repeat("x", MaxReplayBytes/3)
	if _, ok := wireReplay([]proto.Event{chunkEvent(t, 1, big), chunkEvent(t, 2, big)}); !ok {
		t.Fatal("a replay under the byte budget was refused")
	}
	evs := []proto.Event{chunkEvent(t, 1, big), chunkEvent(t, 2, big), chunkEvent(t, 3, big), chunkEvent(t, 4, "x")}
	if _, ok := wireReplay(evs); ok {
		t.Fatal("a replay past the byte budget was accepted")
	}
}

// The budget counts each event as the frame encodes it, envelope and all, so
// payloads that just fit on their own do not.
func TestReplayBudgetCountsTheEnvelope(t *testing.T) {
	evs := make([]proto.Event, 4)
	delta := MaxReplayBytes/4 - len(chunkEvent(t, 1, "").Payload) - 8
	payloads := 0
	for i := range evs {
		evs[i] = chunkEvent(t, int64(i+1), strings.Repeat("x", delta))
		payloads += len(evs[i].Payload)
	}
	if payloads > MaxReplayBytes {
		t.Fatalf("payloads are %d bytes; the test needs them under budget", payloads)
	}
	if _, ok := wireReplay(evs); ok {
		t.Fatal("a replay over budget once encoded was accepted")
	}
}

// The budget is spent on what goes over the wire: tool output the log holds
// in full is capped first, so a catch-up whose raw payloads would blow the
// budget can still be a replay — of the capped events.
func TestReplayCarriesWireCopiesAndBudgetsThem(t *testing.T) {
	big := strings.Repeat("line of output\n", MaxReplayBytes/15/4+1) // a quarter of the budget each
	var evs []proto.Event
	raw := 0
	for i := range 8 {
		ev := toolOutputEvent(t, int64(i+1), big)
		raw += len(ev.Payload)
		evs = append(evs, ev)
	}
	evs = append(evs, chunkEvent(t, 9, "small"))
	if raw <= MaxReplayBytes {
		t.Fatalf("raw payloads are %d bytes; the test needs them over budget", raw)
	}

	wire, ok := wireReplay(evs)
	if !ok {
		t.Fatal("a replay that fits once capped was refused")
	}
	if len(wire) != len(evs) {
		t.Fatalf("replay carries %d events, want %d", len(wire), len(evs))
	}
	for i, ev := range wire[:8] {
		if ev.Seq != evs[i].Seq || !truncatedFlag(t, ev) || len(ev.Payload) >= len(evs[i].Payload) {
			t.Fatalf("event %d: seq %d truncated=%v bytes %d (raw %d), want the capped copy",
				i, ev.Seq, truncatedFlag(t, ev), len(ev.Payload), len(evs[i].Payload))
		}
	}
	if last := wire[8]; string(last.Payload) != string(evs[8].Payload) || truncatedFlag(t, last) {
		t.Fatalf("an event with nothing to cut changed: %s", last.Payload)
	}
}

func TestAttachChoosesReplayOrSnapshot(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sess := fa.thread()
	ctx := context.Background()

	head := actor.Head()
	sess.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: "call-a", Content: []proto.ToolContent{{Type: "text", Text: strings.Repeat("output line\n", 10000)}},
	}))
	waitFor(t, func() bool { return actor.Head() == head+1 })

	res, err := actor.Attach(ctx, head, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != AttachReplay || len(res.Events) != 1 || res.Seq != head+1 {
		t.Fatalf("cursor one behind: kind %s, %d events, seq %d; want a replay of 1 to %d", res.Kind, len(res.Events), res.Seq, head+1)
	}
	if !truncatedFlag(t, res.Events[0]) {
		t.Fatal("the replayed tool output was not capped")
	}

	res, err = actor.Attach(ctx, head+1, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != AttachReplay || len(res.Events) != 0 || res.Seq != head+1 {
		t.Fatalf("cursor at head: kind %s, %d events, seq %d; want an empty replay at %d", res.Kind, len(res.Events), res.Seq, head+1)
	}

	for name, cursor := range map[string]*int64{"no cursor": nil, "cursor ahead of head": ptr(head + 5)} {
		after, has := int64(0), cursor != nil
		if has {
			after = *cursor
		}
		res, err := actor.Attach(ctx, after, has, 10)
		if err != nil {
			t.Fatal(err)
		}
		if res.Kind != AttachSnapshot || res.Snapshot == nil || res.Seq != head+1 {
			t.Fatalf("%s: kind %s, seq %d; want a snapshot at %d", name, res.Kind, res.Seq, head+1)
		}
	}

	// Further behind than a replay may reach.
	for range MaxReplayEvents {
		sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{Role: "agent", Kind: "text", BlockID: "b1", Delta: "x"}))
	}
	waitFor(t, func() bool { return actor.Head() == head+1+MaxReplayEvents })
	res, err = actor.Attach(ctx, head, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != AttachSnapshot {
		t.Fatalf("cursor %d events behind: kind %s, want a snapshot", MaxReplayEvents+1, res.Kind)
	}
}
