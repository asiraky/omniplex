package thread

import (
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// deltaWindow is how long streamed deltas are held before they are written.
// A token-by-token reply is one event per token from the harness; held for
// this long it becomes roughly a dozen events a second, each one DB row and
// one WS frame, which is still smooth to watch.
const deltaWindow = 75 * time.Millisecond

// coalescer holds one streamed delta for the current window and merges each
// consecutive delta for the same item into it. It only ever merges what
// folds the same either way: message chunks concatenate, and a tool update's
// fields fold last-non-empty-wins with its content appended, which is exactly
// how the projection applies them. So the merged event leaves every
// projection where the separate ones would have.
//
// Anything that does not merge — a delta for another item, an event of any
// other type — ends the window, which is what keeps the log in exactly the
// order the harness emitted it.
type coalescer struct {
	pending *proto.Emission
}

// add offers em to the window. It returns what was held and must be written
// before em, and whether em itself is now held. known says whether the
// projection already has the item: an event that creates its item is never
// held, so whatever reads the projection to handle the next one — redaction
// looks up the call's title — finds it there.
func (c *coalescer) add(em proto.Emission, known func(id string) bool) (flushed []proto.Emission, held bool) {
	if c.pending != nil {
		if merged, ok := merge(*c.pending, em); ok {
			c.pending = &merged
			return nil, true
		}
	}
	flushed = c.take()
	if id, ok := deltaItem(em); ok && known(id) {
		c.pending = &em
		return flushed, true
	}
	return flushed, false
}

// take returns what is held, if anything, and empties the window.
func (c *coalescer) take() []proto.Emission {
	if c.pending == nil {
		return nil
	}
	out := []proto.Emission{*c.pending}
	c.pending = nil
	return out
}

// deltaItem names the item a streamed delta belongs to. Everything else —
// turn and lifecycle events, a tool starting, a question for a human — is not
// a delta and is written as it comes.
func deltaItem(em proto.Emission) (string, bool) {
	switch p := em.Payload.(type) {
	case proto.MessageChunkPayload:
		return p.BlockID, em.Type == proto.MessageChunk
	case proto.ToolCallUpdatedPayload:
		return p.ToolCallID, em.Type == proto.ToolCallUpdated
	}
	return "", false
}

// merge folds next into prev when both are deltas for the same item and the
// result applies the same as the two in order.
func merge(prev, next proto.Emission) (proto.Emission, bool) {
	prevID, ok := deltaItem(prev)
	if nextID, nextOK := deltaItem(next); !ok || !nextOK || prevID != nextID || prev.Type != next.Type {
		return prev, false
	}
	switch a := prev.Payload.(type) {
	case proto.MessageChunkPayload:
		b := next.Payload.(proto.MessageChunkPayload)
		if a.TurnID != b.TurnID || a.Role != b.Role || a.Kind != b.Kind || a.ParentToolCallID != b.ParentToolCallID {
			return prev, false
		}
		a.Delta += b.Delta
		return proto.Emit(prev.Type, a), true

	case proto.ToolCallUpdatedPayload:
		b := next.Payload.(proto.ToolCallUpdatedPayload)
		// A change of status is a step in the call's life, not more of the
		// same: going active can reopen an idle thread and finishing cannot,
		// so folding one into the other would change what the thread does.
		if a.Status != "" && b.Status != "" && a.Status != b.Status {
			return prev, false
		}
		if b.Status != "" {
			a.Status = b.Status
		}
		if b.Title != "" {
			a.Title = b.Title
		}
		if len(b.RawInput) > 0 {
			a.RawInput = b.RawInput
		}
		if b.ParentToolCallID != "" {
			a.ParentToolCallID = b.ParentToolCallID
		}
		if len(b.Content) > 0 {
			a.Content = append(append([]proto.ToolContent(nil), a.Content...), b.Content...)
		}
		return proto.Emit(prev.Type, a), true
	}
	return prev, false
}
