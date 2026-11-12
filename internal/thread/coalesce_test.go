package thread

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
)

func allKnown(string) bool { return true }

func chunkOf(turn, kind, block, delta string) proto.Emission {
	return proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turn, Role: "agent", Kind: kind, BlockID: block, Delta: delta,
	})
}

func chunk(block, delta string) proto.Emission   { return chunkOf("t1", "text", block, delta) }
func thought(block, delta string) proto.Emission { return chunkOf("t1", "thought", block, delta) }

func toolUpdate(p proto.ToolCallUpdatedPayload) proto.Emission {
	return proto.Emit(proto.ToolCallUpdated, p)
}

func textContent(s string) proto.ToolContent { return proto.ToolContent{Type: "text", Text: s} }

// drive feeds emitted through c the way the actor does, calling write for
// each event the log would get, in order: what add flushes, then em unless it
// is held, and whatever is still held at the end.
func drive(c *coalescer, emitted []proto.Emission, known func(string) bool, write func(proto.Emission)) {
	for _, em := range emitted {
		flushed, held := c.add(em, known)
		for _, f := range flushed {
			write(f)
		}
		if !held {
			write(em)
		}
	}
	for _, f := range c.take() {
		write(f)
	}
}

// run is one stretch of the log: consecutive deltas for the same item
// collapse into one, with their text and their output each joined.
type run struct{ typ, item, text, output string }

// runsOf collapses consecutive deltas for the same item, so a log can be
// compared with what was emitted however the windows happened to fall.
func runsOf(ems []proto.Emission) []run {
	var out []run
	for _, em := range ems {
		r := run{typ: em.Type}
		switch p := em.Payload.(type) {
		case proto.MessageChunkPayload:
			r.item, r.text = p.BlockID, p.Delta
		case proto.ToolCallUpdatedPayload:
			r.item = p.ToolCallID
			for _, c := range p.Content {
				r.text += c.Text
			}
			r.output = p.OutputDelta
		case proto.ToolCallStartedPayload:
			r.item = p.ToolCallID
		}
		delta := em.Type == proto.MessageChunk || em.Type == proto.ToolCallUpdated
		if n := len(out); delta && n > 0 && out[n-1].typ == r.typ && out[n-1].item == r.item {
			out[n-1].text += r.text
			out[n-1].output += r.output
			continue
		}
		out = append(out, r)
	}
	return out
}

// decodeEvent turns a stored event back into the emission it was written
// from, for the payload types these tests read.
func decodeEvent(t *testing.T, ev proto.Event) proto.Emission {
	t.Helper()
	var p any
	switch ev.Type {
	case proto.MessageChunk:
		var c proto.MessageChunkPayload
		if err := json.Unmarshal(ev.Payload, &c); err != nil {
			t.Fatal(err)
		}
		p = c
	case proto.ToolCallUpdated:
		var u proto.ToolCallUpdatedPayload
		if err := json.Unmarshal(ev.Payload, &u); err != nil {
			t.Fatal(err)
		}
		p = u
	case proto.ToolCallStarted:
		var s proto.ToolCallStartedPayload
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			t.Fatal(err)
		}
		p = s
	}
	return proto.Emit(ev.Type, p)
}

func decodeEvents(t *testing.T, evs []proto.Event) []proto.Emission {
	t.Helper()
	out := make([]proto.Emission, len(evs))
	for i, ev := range evs {
		out[i] = decodeEvent(t, ev)
	}
	return out
}

// chunkDeltas returns the deltas the log holds for block, in log order.
func chunkDeltas(t *testing.T, evs []proto.Event, block string) []string {
	t.Helper()
	var out []string
	for _, em := range decodeEvents(t, evs) {
		if p, ok := em.Payload.(proto.MessageChunkPayload); ok && p.BlockID == block {
			out = append(out, p.Delta)
		}
	}
	return out
}

func rebuild(threadID string, evs []proto.Event) *projection.State {
	s := projection.New(threadID)
	for _, ev := range evs {
		s.Apply(ev)
	}
	return s
}

// projectionWriter applies emissions to a fresh projection with the seqs the
// log would give them.
type projectionWriter struct {
	state *projection.State
	seq   int64
}

func newProjectionWriter() *projectionWriter {
	return &projectionWriter{state: projection.New("th")}
}

func (w *projectionWriter) write(t *testing.T, em proto.Emission) {
	t.Helper()
	raw, err := json.Marshal(em.Payload)
	if err != nil {
		t.Fatal(err)
	}
	w.seq++
	w.state.Apply(proto.Event{ThreadID: "th", Seq: w.seq, Timestamp: 1000, Type: em.Type, Payload: raw})
}

func TestCoalescerMergesChunksForOneBlock(t *testing.T) {
	var c coalescer
	for _, d := range []string{"Hel", "lo, ", "world"} {
		flushed, held := c.add(chunk("b1", d), allKnown)
		if !held || len(flushed) != 0 {
			t.Fatalf("chunk %q: flushed %+v, held %v; want merged and held", d, flushed, held)
		}
	}
	out := c.take()
	if len(out) != 1 {
		t.Fatalf("held = %d entries, want 1: %+v", len(out), out)
	}
	p := out[0].Payload.(proto.MessageChunkPayload)
	if out[0].Type != proto.MessageChunk || p.BlockID != "b1" || p.Delta != "Hello, world" {
		t.Fatalf("merged = %s %+v", out[0].Type, p)
	}
}

func TestCoalescerMergesToolUpdatesForOneCall(t *testing.T) {
	var c coalescer
	updates := []proto.ToolCallUpdatedPayload{
		{ToolCallID: "c1", Status: proto.StatusInProgress, Title: "first", RawInput: json.RawMessage(`{"a":1}`), Content: []proto.ToolContent{textContent("one")}},
		{ToolCallID: "c1", Content: []proto.ToolContent{textContent("two")}},
		{ToolCallID: "c1", Title: "second", ParentToolCallID: "parent", Content: []proto.ToolContent{textContent("three")}},
		{ToolCallID: "c1", Status: proto.StatusInProgress, RawInput: json.RawMessage(`{"a":2}`)},
	}
	for _, u := range updates {
		if flushed, held := c.add(toolUpdate(u), allKnown); !held || len(flushed) != 0 {
			t.Fatalf("update %+v: flushed %+v, held %v; want merged and held", u, flushed, held)
		}
	}
	out := c.take()
	if len(out) != 1 {
		t.Fatalf("held = %d entries, want 1: %+v", len(out), out)
	}
	got := out[0].Payload.(proto.ToolCallUpdatedPayload)
	want := proto.ToolCallUpdatedPayload{
		ToolCallID:       "c1",
		Status:           proto.StatusInProgress,
		Title:            "second",
		RawInput:         json.RawMessage(`{"a":2}`),
		ParentToolCallID: "parent",
		Content:          []proto.ToolContent{textContent("one"), textContent("two"), textContent("three")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %+v\nwant     %+v", got, want)
	}
	// Merging must not alias the first update's content slice.
	if len(updates[0].Content) != 1 {
		t.Fatalf("first update's content was mutated: %+v", updates[0].Content)
	}
}

func TestCoalescerJoinsOutputDeltas(t *testing.T) {
	var c coalescer
	for _, u := range []proto.ToolCallUpdatedPayload{
		{ToolCallID: "c1", OutputDelta: "compiling "},
		{ToolCallID: "c1", OutputDelta: "a\n"},
		{ToolCallID: "c1", Status: proto.StatusCompleted, OutputDelta: "ok\n", Content: []proto.ToolContent{textContent("compiling a\nok\n")}},
	} {
		if flushed, held := c.add(toolUpdate(u), allKnown); !held || len(flushed) != 0 {
			t.Fatalf("update %+v: flushed %+v, held %v; want merged and held", u, flushed, held)
		}
	}
	out := c.take()
	if len(out) != 1 {
		t.Fatalf("held = %+v", out)
	}
	got := out[0].Payload.(proto.ToolCallUpdatedPayload)
	if got.Status != proto.StatusCompleted || got.OutputDelta != "compiling a\nok\n" || len(got.Content) != 1 {
		t.Fatalf("merged = %+v", got)
	}
}

func TestCoalescerToolStatusChangeFlushesThePendingUpdate(t *testing.T) {
	var c coalescer
	c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusInProgress, Content: []proto.ToolContent{textContent("a")}}), allKnown)

	flushed, held := c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusCompleted, Content: []proto.ToolContent{textContent("b")}}), allKnown)
	if !held || len(flushed) != 1 {
		t.Fatalf("status change: flushed %+v, held %v; want the in_progress update flushed and completed held", flushed, held)
	}
	if p := flushed[0].Payload.(proto.ToolCallUpdatedPayload); p.Status != proto.StatusInProgress || !reflect.DeepEqual(p.Content, []proto.ToolContent{textContent("a")}) {
		t.Fatalf("flushed = %+v", p)
	}

	// Status-less: merges into the completed update now held.
	if flushed, held := c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("c")}}), allKnown); !held || len(flushed) != 0 {
		t.Fatalf("status-less update: flushed %+v, held %v; want merged", flushed, held)
	}
	out := c.take()
	if len(out) != 1 {
		t.Fatalf("held = %+v", out)
	}
	if p := out[0].Payload.(proto.ToolCallUpdatedPayload); p.Status != proto.StatusCompleted || !reflect.DeepEqual(p.Content, []proto.ToolContent{textContent("b"), textContent("c")}) {
		t.Fatalf("held = %+v", p)
	}
}

func TestCoalescerRefusesDeltaForUnknownItem(t *testing.T) {
	var c coalescer
	known := func(id string) bool { return id == "known" }
	if flushed, held := c.add(chunk("new", "x"), known); held || len(flushed) != 0 {
		t.Fatalf("chunk for unknown item on empty window: flushed %+v, held %v", flushed, held)
	}
	if flushed, held := c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "new", Status: proto.StatusInProgress}), known); held || len(flushed) != 0 {
		t.Fatalf("tool update for unknown item on empty window: flushed %+v, held %v", flushed, held)
	}

	// With something held, the unknown item's delta still is not held, and
	// what was held is flushed ahead of it.
	c.add(chunk("known", "k"), known)
	flushed, held := c.add(chunk("new", "x"), known)
	if held || len(flushed) != 1 || flushed[0].Payload.(proto.MessageChunkPayload).BlockID != "known" {
		t.Fatalf("unknown after held: flushed %+v, held %v", flushed, held)
	}
	if out := c.take(); len(out) != 0 {
		t.Fatalf("held after refusal = %+v", out)
	}
}

func TestCoalescerRefusesNonDeltaTypes(t *testing.T) {
	for _, em := range []proto.Emission{
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "c1", Title: "ls", Status: proto.StatusInProgress}),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}),
		// A delta payload under some other type is not a delta.
		proto.Emit(proto.ToolCallStarted, proto.MessageChunkPayload{TurnID: "t1", Role: "agent", Kind: "text", BlockID: "b1", Delta: "x"}),
		proto.Emit(proto.MessageChunk, proto.ToolCallUpdatedPayload{ToolCallID: "b1"}),
	} {
		var c coalescer
		if flushed, held := c.add(em, allKnown); held || len(flushed) != 0 {
			t.Fatalf("%s with %T on empty window: flushed %+v, held %v", em.Type, em.Payload, flushed, held)
		}
		// Behind a held delta for the same id, it ends the window.
		c.add(chunk("b1", "held"), allKnown)
		flushed, held := c.add(em, allKnown)
		if held || len(flushed) != 1 || flushed[0].Payload.(proto.MessageChunkPayload).Delta != "held" {
			t.Fatalf("%s with %T after held chunk: flushed %+v, held %v", em.Type, em.Payload, flushed, held)
		}
		if out := c.take(); len(out) != 0 {
			t.Fatalf("held after %s = %+v", em.Type, out)
		}
	}
}

func TestCoalescerMergeRefusesOtherItemsAndTypes(t *testing.T) {
	cases := []struct {
		name       string
		prev, next proto.Emission
	}{
		{"chunks for different blocks", chunk("A", "a"), chunk("B", "b")},
		{"updates for different calls", toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "A"}), toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "B"})},
		{"chunk then update with the same id", chunk("A", "a"), toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "A"})},
		{"update then chunk with the same id", toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "A"}), chunk("A", "a")},
		{"chunk then non-delta", chunk("A", "a"), proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "A"})},
		{"chunk payload under another type", chunk("A", "a"), proto.Emit(proto.ToolCallStarted, proto.MessageChunkPayload{TurnID: "t1", Role: "agent", Kind: "text", BlockID: "A"})},
		{"different kind", chunk("A", "a"), thought("A", "t")},
		{"different turn", chunk("A", "a"), chunkOf("t2", "text", "A", "b")},
		// Output with no status is judged by the call's own status, which
		// the merge would replace with the running one.
		{"status-less output then going active", toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "A", OutputDelta: "x"}), toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "A", Status: proto.StatusInProgress})},
	}
	for _, tc := range cases {
		if _, ok := merge(tc.prev, tc.next); ok {
			t.Errorf("%s: merged", tc.name)
		}
	}
}

func TestCoalescerInterleavedItemsAreNotMergedAcrossEachOther(t *testing.T) {
	var c coalescer
	var writes []proto.Emission
	drive(&c, []proto.Emission{chunk("A", "a1"), chunk("B", "b1"), chunk("A", "a2")}, allKnown, func(em proto.Emission) {
		writes = append(writes, em)
	})
	var got []string
	for _, em := range writes {
		p := em.Payload.(proto.MessageChunkPayload)
		got = append(got, p.BlockID+":"+p.Delta)
	}
	if want := []string{"A:a1", "B:b1", "A:a2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %q, want %q", got, want)
	}
}

func TestCoalescerNonMergeableDeltaFlushesThePendingOneFirst(t *testing.T) {
	var c coalescer
	var writes []proto.Emission
	drive(&c, []proto.Emission{
		chunk("b1", "text1 "),
		thought("b1", "think1 "),            // different kind: flushes text1
		chunk("b1", "text2 "),               // flushes the thought
		chunk("b1", "text3 "),               // merges with text2
		chunkOf("t2", "text", "b1", "t2a "), // different turn: flushes text2 text3
		chunkOf("t2", "text", "b1", "t2b"),  // merges
	}, allKnown, func(em proto.Emission) { writes = append(writes, em) })

	var got []string
	for _, em := range writes {
		p := em.Payload.(proto.MessageChunkPayload)
		got = append(got, p.TurnID+"/"+p.Kind+":"+p.Delta)
	}
	want := []string{"t1/text:text1 ", "t1/thought:think1 ", "t1/text:text2 text3 ", "t2/text:t2a t2b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %q\nwant     %q", got, want)
	}
}

func TestCoalescerTakeEmptiesWindow(t *testing.T) {
	var c coalescer
	if out := c.take(); len(out) != 0 {
		t.Fatalf("take on empty window = %+v", out)
	}
	c.add(chunk("b1", "x"), allKnown)
	if out := c.take(); len(out) != 1 {
		t.Fatalf("first take = %+v", out)
	}
	if out := c.take(); len(out) != 0 {
		t.Fatalf("second take = %+v, want empty", out)
	}
	// A fresh window does not merge into what was taken.
	if flushed, _ := c.add(chunk("b1", "y"), allKnown); len(flushed) != 0 {
		t.Fatalf("add after take flushed %+v", flushed)
	}
	out := c.take()
	if len(out) != 1 || out[0].Payload.(proto.MessageChunkPayload).Delta != "y" {
		t.Fatalf("take after refill = %+v", out)
	}
}

// TestCoalescedDeltasFoldLikeTheOriginals is the property the coalescer
// exists under: writing the merged emissions leaves the projection exactly
// where writing every original would have, and the log differs from the
// emitted sequence only by consecutive same-item deltas being joined.
func TestCoalescedDeltasFoldLikeTheOriginals(t *testing.T) {
	emitted := []proto.Emission{
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}),
		chunk("m1", "Let "),
		chunk("m1", "me "),
		thought("m1", "hmm"),
		chunk("m1", "look"),
		thought("th1", "plan"),
		thought("th1", "ning"),
		chunk("m1", "."),
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "ls", Status: "pending"}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusInProgress, RawInput: json.RawMessage(`{"cmd":"ls"}`)}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("a.go\n")}}),
		chunk("m2", "while "),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Title: "ls -la", Content: []proto.ToolContent{textContent("b.go\n")}}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("c.go\n")}}),
		chunk("m2", "waiting"),
		chunk("m2", "..."),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusCompleted, Content: []proto.ToolContent{{Type: "diff", Path: "x", Old: "1", New: "2"}}}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", ParentToolCallID: "task", Content: []proto.ToolContent{textContent("done")}}),
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c2", Kind: proto.KindExecute, Title: "cat", Status: proto.StatusInProgress}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c2", Content: []proto.ToolContent{textContent("x")}}),
		chunk("m2", "!"),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c2", Status: proto.StatusFailed}),
		// A command streaming output: going active then output, output then
		// more output, output then the finish with the result, and output
		// straggling in after the finish.
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c3", Kind: proto.KindExecute, Title: "make", Status: proto.StatusPending}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", Status: proto.StatusInProgress}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", OutputDelta: "cc a.c\n"}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", OutputDelta: "cc b"}),
		chunk("m2", " building"),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", OutputDelta: ".c\n"}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", Status: proto.StatusCompleted, Content: []proto.ToolContent{textContent("built\n")}}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", OutputDelta: "late\n"}),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}),
		// A straggling update after the turn ended, then a fresh turn's chunk
		// into an old block.
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c2", Content: []proto.ToolContent{textContent("late")}}),
		chunkOf("t2", "text", "m2", "?"),
		// Output straggling onto the finished c3 is dropped; the call is then
		// reopened, and only what it prints from there on is its tail.
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", OutputDelta: "later\n"}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", Status: proto.StatusInProgress}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c3", OutputDelta: "again\n"}),
	}

	original := newProjectionWriter()
	for _, em := range emitted {
		original.write(t, em)
	}

	coalesced := newProjectionWriter()
	var c coalescer
	var writes []proto.Emission
	known := func(id string) bool { _, ok := coalesced.state.ItemByID(id); return ok }
	drive(&c, emitted, known, func(em proto.Emission) {
		coalesced.write(t, em)
		writes = append(writes, em)
	})

	if len(writes) >= len(emitted) {
		t.Fatalf("nothing was merged: %d writes for %d events", len(writes), len(emitted))
	}
	if a, b := runsOf(emitted), runsOf(writes); !reflect.DeepEqual(a, b) {
		t.Fatalf("log order differs from emitted order:\nemitted: %+v\nwritten: %+v", a, b)
	}
	a, _ := json.Marshal(original.state.Items)
	b, _ := json.Marshal(coalesced.state.Items)
	if string(a) != string(b) {
		t.Fatalf("coalesced fold differs from the original:\noriginal:  %s\ncoalesced: %s", a, b)
	}
	if original.state.Phase != coalesced.state.Phase {
		t.Fatalf("phase = %q coalesced, %q original", coalesced.state.Phase, original.state.Phase)
	}
	// Spot-check the fold itself, so equal-but-wrong cannot pass. A thought
	// on the same block appends to its text; the fold keeps the latest kind.
	m1, _ := coalesced.state.ItemByID("m1")
	if m1.Text != "Let me hmmlook." || m1.ContentKind != "text" {
		t.Fatalf("m1 = %+v", m1)
	}
	c1, _ := coalesced.state.ItemByID("c1")
	if c1.Status != proto.StatusCompleted || c1.Title != "ls -la" || len(c1.Content) != 5 || c1.ParentID != "task" {
		t.Fatalf("c1 = %+v", c1)
	}
	c3, _ := coalesced.state.ItemByID("c3")
	if c3.Status != proto.StatusInProgress || c3.Tail != "again\n" || len(c3.Content) != 1 || c3.Content[0].Text != "built\n" {
		t.Fatalf("c3 = %+v", c3)
	}
}

func TestActorCoalescesManyDeltasIntoFewEvents(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()
	const n = 200
	var want strings.Builder
	for i := 0; i < n; i++ {
		d := fmt.Sprintf("w%d ", i)
		want.WriteString(d)
		fa.thread().emit(chunk("b1", d))
	}
	waitFor(t, func() bool {
		s, err := actor.State(ctx)
		if err != nil {
			return false
		}
		it, ok := s.ItemByID("b1")
		return ok && it.Text == want.String()
	})

	evs, err := st.ReadEvents(ctx, actor.ID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	deltas := chunkDeltas(t, evs, "b1")
	if len(deltas) >= n/4 {
		t.Fatalf("%d chunks stored for %d emitted; want them coalesced", len(deltas), n)
	}
	// The first creates the item and is written alone.
	if deltas[0] != "w0 " {
		t.Fatalf("first stored chunk = %q, want the creating chunk alone", deltas[0])
	}
	if got := strings.Join(deltas, ""); got != want.String() {
		t.Fatalf("stored text = %q", got)
	}
	if it, _ := rebuild(actor.ID, evs).ItemByID("b1"); it.Text != want.String() {
		t.Fatalf("rebuilt text = %q", it.Text)
	}
}

// A command printing far more than its tail shows is stored and sent only as
// much as the tail can use, and the running call shows the same lines.
func TestActorStoresOnlyTheOutputATailCanShow(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()
	sess := fa.thread()
	sess.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}))
	sess.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "make", Status: proto.StatusInProgress,
	}))
	var printed strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&printed, "compiling unit %d\n", i)
	}
	sess.emit(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", OutputDelta: printed.String()}))

	var evs []proto.Event
	waitFor(t, func() bool {
		evs, _ = st.ReadEvents(ctx, actor.ID, 0, 10000)
		it, ok := rebuild(actor.ID, evs).ItemByID("c1")
		return ok && it.Tail != ""
	})
	stored := 0
	for _, em := range decodeEvents(t, evs) {
		if p, ok := em.Payload.(proto.ToolCallUpdatedPayload); ok {
			stored += len(p.OutputDelta)
		}
	}
	if stored > 1000 {
		t.Fatalf("stored %d bytes of output for a 20-line tail", stored)
	}
	it, _ := rebuild(actor.ID, evs).ItemByID("c1")
	if !strings.HasPrefix(it.Tail, "compiling unit 4981\n") || !strings.HasSuffix(it.Tail, "compiling unit 5000\n") {
		t.Fatalf("tail = %q, want units 4981 to 5000", it.Tail)
	}
}

// Output joined to the update that finishes its call is not stored: the
// finish drops the tail it would have fed, and the result carries it.
func TestActorDropsOutputJoinedToTheFinish(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()
	sess := fa.thread()
	sess.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}))
	sess.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "make", Status: proto.StatusInProgress,
	}))
	// As the coalescer joins a last delta to the finish that follows it.
	sess.emit(toolUpdate(proto.ToolCallUpdatedPayload{
		ToolCallID: "c1", Status: proto.StatusCompleted, OutputDelta: "compiling a\n",
		Content: []proto.ToolContent{textContent("compiling a\n")},
	}))

	var evs []proto.Event
	waitFor(t, func() bool {
		evs, _ = st.ReadEvents(ctx, actor.ID, 0, 100)
		it, ok := rebuild(actor.ID, evs).ItemByID("c1")
		return ok && it.Status == proto.StatusCompleted
	})
	for _, em := range decodeEvents(t, evs) {
		if p, ok := em.Payload.(proto.ToolCallUpdatedPayload); ok && p.OutputDelta != "" {
			t.Fatalf("stored output %q on %+v", p.OutputDelta, p)
		}
	}
}

func TestActorFlushesHeldDeltasBeforeOtherEvents(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()
	sess := fa.thread()

	sess.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}))
	var want strings.Builder
	for i := 0; i < 20; i++ {
		d := fmt.Sprintf("d%d ", i)
		want.WriteString(d)
		sess.emit(chunk("b1", d))
	}
	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))

	var evs []proto.Event
	finished := -1
	waitFor(t, func() bool {
		evs, _ = st.ReadEvents(ctx, actor.ID, 0, 10000)
		for i, ev := range evs {
			if ev.Type == proto.TurnFinished {
				finished = i
				return true
			}
		}
		return false
	})

	// Everything up to turn.finished already holds the whole reply.
	if it, _ := rebuild(actor.ID, evs[:finished]).ItemByID("b1"); it.Text != want.String() {
		t.Fatalf("text before turn.finished = %q, want %q", it.Text, want.String())
	}
	for _, ev := range evs[finished+1:] {
		if ev.Type == proto.MessageChunk {
			t.Fatalf("a chunk landed after turn.finished: %s", ev.Payload)
		}
	}
}

func TestActorDeltaOrderAcrossItemsIsPreserved(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()
	sess := fa.thread()

	// Runs of one item, items interleaved, and the case a reorder would
	// break: a delta for another item between two deltas for the same one.
	emitted := []proto.Emission{
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}),
		chunk("A", "a1 "),
		chunk("A", "a2 "),
		chunk("A", "a3 "),
		chunk("B", "b1 "),
		chunk("B", "b2 "),
		chunk("A", "a4 "),
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "ls", Status: proto.StatusInProgress}),
		chunk("A", "a5 "),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("out1")}}),
		chunk("A", "a6 "),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("out2")}}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("out3")}}),
		chunk("B", "b3"),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}),
	}
	for _, em := range emitted {
		sess.emit(em)
	}

	var evs []proto.Event
	waitFor(t, func() bool {
		evs, _ = st.ReadEvents(ctx, actor.ID, 0, 10000)
		return len(evs) > 0 && evs[len(evs)-1].Type == proto.TurnFinished
	})

	// The log is the emitted sequence with consecutive same-item deltas
	// joined, and nothing else changed. thread.created opens the log.
	logged := decodeEvents(t, evs)
	if len(logged) == 0 || logged[0].Type != proto.ThreadCreated {
		t.Fatalf("log does not open with thread.created: %+v", logged)
	}
	if want, got := runsOf(emitted), runsOf(logged[1:]); !reflect.DeepEqual(want, got) {
		t.Fatalf("log order differs from emitted order:\nemitted: %+v\nlogged:  %+v", want, got)
	}

	state, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := state.ItemByID("A"); a.Text != "a1 a2 a3 a4 a5 a6 " {
		t.Fatalf("A text = %q", a.Text)
	}
	if c1, _ := state.ItemByID("c1"); len(c1.Content) != 3 || c1.Content[0].Text != "out1" || c1.Content[2].Text != "out3" {
		t.Fatalf("c1 content = %+v", c1.Content)
	}
}
