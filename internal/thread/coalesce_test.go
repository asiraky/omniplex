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

func chunk(block, delta string) proto.Emission {
	return proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: "t1", Role: "agent", Kind: "text", BlockID: block, Delta: delta,
	})
}

func toolUpdate(p proto.ToolCallUpdatedPayload) proto.Emission {
	return proto.Emit(proto.ToolCallUpdated, p)
}

func textContent(s string) proto.ToolContent { return proto.ToolContent{Type: "text", Text: s} }

func TestCoalescerMergesChunksForOneBlock(t *testing.T) {
	var c coalescer
	for _, d := range []string{"Hel", "lo, ", "world"} {
		if !c.add(chunk("b1", d), allKnown) {
			t.Fatalf("chunk %q refused", d)
		}
	}
	out := c.take()
	if len(out) != 1 {
		t.Fatalf("pending = %d entries, want 1: %+v", len(out), out)
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
		if !c.add(toolUpdate(u), allKnown) {
			t.Fatalf("update %+v refused", u)
		}
	}
	out := c.take()
	if len(out) != 1 {
		t.Fatalf("pending = %d entries, want 1: %+v", len(out), out)
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

func TestCoalescerKeepsToolStatusChangesApart(t *testing.T) {
	var c coalescer
	c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusInProgress, Content: []proto.ToolContent{textContent("a")}}), allKnown)
	c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusCompleted, Content: []proto.ToolContent{textContent("b")}}), allKnown)
	out := c.take()
	if len(out) != 2 {
		t.Fatalf("pending = %d entries, want 2: %+v", len(out), out)
	}
	first := out[0].Payload.(proto.ToolCallUpdatedPayload)
	second := out[1].Payload.(proto.ToolCallUpdatedPayload)
	if first.Status != proto.StatusInProgress || second.Status != proto.StatusCompleted {
		t.Fatalf("statuses = %q, %q; want in_progress then completed", first.Status, second.Status)
	}
}

func TestCoalescerRefusesDeltaForUnknownItem(t *testing.T) {
	var c coalescer
	known := func(id string) bool { return id == "known" }
	if c.add(chunk("new", "x"), known) {
		t.Fatal("chunk for an item the projection lacks was held")
	}
	if c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "new", Status: proto.StatusInProgress}), known) {
		t.Fatal("tool update for an item the projection lacks was held")
	}
	if out := c.take(); len(out) != 0 {
		t.Fatalf("pending after refusals = %+v", out)
	}
}

func TestCoalescerRefusesNonDeltaTypes(t *testing.T) {
	var c coalescer
	for _, em := range []proto.Emission{
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "c1", Title: "ls", Status: proto.StatusInProgress}),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}),
		// A delta payload under some other type is not a delta.
		proto.Emit(proto.ToolCallStarted, proto.MessageChunkPayload{BlockID: "b1", Delta: "x"}),
		proto.Emit(proto.MessageChunk, proto.ToolCallUpdatedPayload{ToolCallID: "c1"}),
	} {
		if c.add(em, allKnown) {
			t.Fatalf("%s with %T was held", em.Type, em.Payload)
		}
	}
	if out := c.take(); len(out) != 0 {
		t.Fatalf("pending after refusals = %+v", out)
	}
}

func TestCoalescerInterleavedItemsKeepFirstAppearanceOrder(t *testing.T) {
	var c coalescer
	c.add(chunk("A", "a1"), allKnown)
	c.add(chunk("B", "b1"), allKnown)
	c.add(chunk("A", "a2"), allKnown)
	out := c.take()
	if len(out) != 2 {
		t.Fatalf("pending = %d entries, want 2: %+v", len(out), out)
	}
	a := out[0].Payload.(proto.MessageChunkPayload)
	b := out[1].Payload.(proto.MessageChunkPayload)
	if a.BlockID != "A" || a.Delta != "a1a2" || b.BlockID != "B" || b.Delta != "b1" {
		t.Fatalf("pending = %+v, %+v", a, b)
	}
}

func TestCoalescerUnmergeableDeltaStartsNewEntryAndLaterOnesMergeIntoIt(t *testing.T) {
	var c coalescer
	thought := func(d string) proto.Emission {
		return proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
			TurnID: "t1", Role: "agent", Kind: "thought", BlockID: "b1", Delta: d,
		})
	}
	otherTurn := func(d string) proto.Emission {
		return proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
			TurnID: "t2", Role: "agent", Kind: "text", BlockID: "b1", Delta: d,
		})
	}
	c.add(chunk("b1", "text1 "), allKnown)
	c.add(thought("think1 "), allKnown) // different kind: new entry
	c.add(chunk("b1", "text2 "), allKnown)
	// text2 could merge into the first entry by its fields, but that would
	// move it ahead of the thought. It must start a third entry.
	c.add(chunk("b1", "text3 "), allKnown) // merges into the third
	c.add(otherTurn("t2a "), allKnown)     // different turn: fourth entry
	c.add(otherTurn("t2b"), allKnown)      // merges into the fourth

	out := c.take()
	var got []string
	for _, em := range out {
		p := em.Payload.(proto.MessageChunkPayload)
		got = append(got, p.TurnID+"/"+p.Kind+":"+p.Delta)
	}
	want := []string{"t1/text:text1 ", "t1/thought:think1 ", "t1/text:text2 text3 ", "t2/text:t2a t2b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pending = %q\nwant      %q", got, want)
	}
}

func TestCoalescerUnmergeableToolUpdateMergesOnlyIntoLatest(t *testing.T) {
	var c coalescer
	c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusInProgress, Content: []proto.ToolContent{textContent("a")}}), allKnown)
	c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusCompleted, Content: []proto.ToolContent{textContent("b")}}), allKnown)
	// Status-less: merges into the completed entry, never the in_progress one.
	c.add(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("c")}}), allKnown)
	out := c.take()
	if len(out) != 2 {
		t.Fatalf("pending = %d entries, want 2: %+v", len(out), out)
	}
	first := out[0].Payload.(proto.ToolCallUpdatedPayload)
	second := out[1].Payload.(proto.ToolCallUpdatedPayload)
	if !reflect.DeepEqual(first.Content, []proto.ToolContent{textContent("a")}) {
		t.Fatalf("first entry content = %+v", first.Content)
	}
	if second.Status != proto.StatusCompleted || !reflect.DeepEqual(second.Content, []proto.ToolContent{textContent("b"), textContent("c")}) {
		t.Fatalf("second entry = %+v", second)
	}
}

func TestCoalescerTakeEmptiesWindow(t *testing.T) {
	var c coalescer
	c.add(chunk("b1", "x"), allKnown)
	if out := c.take(); len(out) != 1 {
		t.Fatalf("first take = %+v", out)
	}
	if out := c.take(); len(out) != 0 {
		t.Fatalf("second take = %+v, want empty", out)
	}
	// A fresh window does not merge into what was taken.
	c.add(chunk("b1", "y"), allKnown)
	out := c.take()
	if len(out) != 1 || out[0].Payload.(proto.MessageChunkPayload).Delta != "y" {
		t.Fatalf("take after refill = %+v", out)
	}
}

// foldSeq applies emissions to a fresh projection as the log would hold them.
type foldSeq struct {
	state *projection.State
	seq   int64
}

func newFold() *foldSeq { return &foldSeq{state: projection.New("th")} }

func (f *foldSeq) apply(t *testing.T, em proto.Emission) {
	t.Helper()
	raw, err := json.Marshal(em.Payload)
	if err != nil {
		t.Fatal(err)
	}
	f.seq++
	f.state.Apply(proto.Event{ThreadID: "th", Seq: f.seq, Timestamp: 1000, Type: em.Type, Payload: raw})
}

// TestCoalescedDeltasFoldLikeTheOriginals is the property the coalescer
// exists under: writing the merged emissions leaves the projection exactly
// where writing every original would have.
func TestCoalescedDeltasFoldLikeTheOriginals(t *testing.T) {
	thought := func(block, d string) proto.Emission {
		return proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: "t1", Role: "agent", Kind: "thought", BlockID: block, Delta: d})
	}
	seq := []proto.Emission{
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
		chunk("m2", "waiting"),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Status: proto.StatusCompleted, Content: []proto.ToolContent{{Type: "diff", Path: "x", Old: "1", New: "2"}}}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", ParentToolCallID: "task", Content: []proto.ToolContent{textContent("done")}}),
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c2", Kind: proto.KindExecute, Title: "cat", Status: proto.StatusInProgress}),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c2", Content: []proto.ToolContent{textContent("x")}}),
		chunk("m2", "!"),
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c2", Status: proto.StatusFailed}),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}),
		// A straggling update after the turn ended, then a fresh turn's chunk
		// into an old block.
		toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c2", Content: []proto.ToolContent{textContent("late")}}),
		proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: "t2", Role: "agent", Kind: "text", BlockID: "m2", Delta: "?"}),
	}

	plain := newFold()
	for _, em := range seq {
		plain.apply(t, em)
	}

	// The actor's path: hold what the coalescer takes, write the rest after
	// whatever is held.
	merged := newFold()
	var c coalescer
	known := func(id string) bool { _, ok := merged.state.ItemByID(id); return ok }
	written := 0
	flush := func() {
		for _, em := range c.take() {
			merged.apply(t, em)
			written++
		}
	}
	for _, em := range seq {
		if c.add(em, known) {
			continue
		}
		flush()
		merged.apply(t, em)
		written++
	}
	flush()

	if written >= len(seq) {
		t.Fatalf("nothing was merged: %d writes for %d events", written, len(seq))
	}
	a, _ := json.Marshal(plain.state.Items)
	b, _ := json.Marshal(merged.state.Items)
	if string(a) != string(b) {
		t.Fatalf("merged fold differs from the original:\noriginal: %s\nmerged:   %s", a, b)
	}
	if plain.state.Phase != merged.state.Phase {
		t.Fatalf("phase = %q merged, %q original", merged.state.Phase, plain.state.Phase)
	}
	// Spot-check the fold itself, so equal-but-wrong cannot pass.
	m1, _ := merged.state.ItemByID("m1")
	// A thought on the same block appends to its text; the fold keeps the latest kind.
	if m1.Text != "Let me hmmlook." || m1.ContentKind != "text" {
		t.Fatalf("m1 = %+v", m1)
	}
	c1, _ := merged.state.ItemByID("c1")
	if c1.Status != proto.StatusCompleted || c1.Title != "ls -la" || len(c1.Content) != 4 || c1.ParentID != "task" {
		t.Fatalf("c1 = %+v", c1)
	}
}

// chunkDeltas returns the deltas the log holds for block, in log order.
func chunkDeltas(t *testing.T, evs []proto.Event, block string) []string {
	t.Helper()
	var out []string
	for _, ev := range evs {
		if ev.Type != proto.MessageChunk {
			continue
		}
		var p proto.MessageChunkPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.BlockID == block {
			out = append(out, p.Delta)
		}
	}
	return out
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
	// The log alone rebuilds the same text.
	rebuilt := projection.New(actor.ID)
	for _, ev := range evs {
		rebuilt.Apply(ev)
	}
	if it, _ := rebuilt.ItemByID("b1"); it.Text != want.String() {
		t.Fatalf("rebuilt text = %q", it.Text)
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
	before := projection.New(actor.ID)
	for _, ev := range evs[:finished] {
		before.Apply(ev)
	}
	if it, _ := before.ItemByID("b1"); it.Text != want.String() {
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

	// Two blocks interleaved, then a tool starting, then more of both and the
	// tool's output, then the turn ending.
	sess.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}))
	sess.emit(chunk("A", "a1 "))
	sess.emit(chunk("B", "b1 "))
	sess.emit(chunk("A", "a2 "))
	sess.emit(chunk("B", "b2 "))
	sess.emit(chunk("A", "a3 "))
	sess.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "ls", Status: proto.StatusInProgress}))
	sess.emit(chunk("B", "b3 "))
	sess.emit(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("out1")}}))
	sess.emit(chunk("A", "a4"))
	sess.emit(toolUpdate(proto.ToolCallUpdatedPayload{ToolCallID: "c1", Content: []proto.ToolContent{textContent("out2")}}))
	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))

	var evs []proto.Event
	waitFor(t, func() bool {
		evs, _ = st.ReadEvents(ctx, actor.ID, 0, 10000)
		return len(evs) > 0 && evs[len(evs)-1].Type == proto.TurnFinished
	})

	// Label every event in the log by what it is and, for deltas, which
	// emitted deltas it carries.
	type entry struct {
		typ, item, text string
	}
	var log []entry
	for _, ev := range evs {
		switch ev.Type {
		case proto.MessageChunk:
			var p proto.MessageChunkPayload
			json.Unmarshal(ev.Payload, &p)
			log = append(log, entry{ev.Type, p.BlockID, p.Delta})
		case proto.ToolCallUpdated:
			var p proto.ToolCallUpdatedPayload
			json.Unmarshal(ev.Payload, &p)
			var s string
			for _, c := range p.Content {
				s += c.Text
			}
			log = append(log, entry{ev.Type, p.ToolCallID, s})
		case proto.ToolCallStarted, proto.TurnStarted, proto.TurnFinished:
			log = append(log, entry{ev.Type, "", ""})
		}
	}
	index := func(typ string) int {
		for i, e := range log {
			if e.typ == typ {
				return i
			}
		}
		t.Fatalf("no %s in log", typ)
		return -1
	}
	started, toolStart, finished := index(proto.TurnStarted), index(proto.ToolCallStarted), index(proto.TurnFinished)
	if !(started < toolStart && toolStart < finished && finished == len(log)-1) {
		t.Fatalf("lifecycle order wrong: %+v", log)
	}

	// Each item's deltas, in log order, are its emitted deltas in order; and
	// each emitted delta sits on the same side of tool_call.started as it was
	// emitted.
	textBefore := map[string]string{}
	textAfter := map[string]string{}
	firstSeen := map[string]int{}
	for i, e := range log {
		if e.item == "" {
			continue
		}
		if _, ok := firstSeen[e.item]; !ok {
			firstSeen[e.item] = i
		}
		if i < started {
			t.Fatalf("delta before turn.started: %+v", log)
		}
		if i < toolStart {
			textBefore[e.item] += e.text
		} else {
			textAfter[e.item] += e.text
		}
	}
	wantBefore := map[string]string{"A": "a1 a2 a3 ", "B": "b1 b2 "}
	wantAfter := map[string]string{"A": "a4", "B": "b3 ", "c1": "out1out2"}
	if !reflect.DeepEqual(textBefore, wantBefore) || !reflect.DeepEqual(textAfter, wantAfter) {
		t.Fatalf("before tool start = %q, after = %q\nlog: %+v", textBefore, textAfter, log)
	}
	if !(firstSeen["A"] < firstSeen["B"] && firstSeen["B"] < toolStart && toolStart < firstSeen["c1"]) {
		t.Fatalf("items first appear out of order: %v (tool start %d)\nlog: %+v", firstSeen, toolStart, log)
	}

	state, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range state.Items {
		ids = append(ids, it.ID)
	}
	if !reflect.DeepEqual(ids, []string{"A", "B", "c1"}) {
		t.Fatalf("item order = %v", ids)
	}
	if c1, _ := state.ItemByID("c1"); len(c1.Content) != 2 || c1.Content[0].Text != "out1" || c1.Content[1].Text != "out2" {
		t.Fatalf("c1 content = %+v", c1.Content)
	}
}
