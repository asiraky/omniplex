// Copies of the projection headed for a client. The actor's own state holds
// everything; what goes over the wire is the tail a presenter shows, with
// what it left out counted so the client can ask for it.
package projection

import "github.com/asiraky/omniplex/internal/proto"

const (
	// WireFinishedJobs is how many finished jobs a wire copy keeps beyond the
	// running ones and those the windowed items spawned.
	WireFinishedJobs = 30
	// WireChildTail is how many of a still-running parent's child items a
	// wire copy keeps. A settled parent's children are left out entirely.
	WireChildTail = 20
)

// WireCopy returns a copy of s for a client: Items windowed to maxTop
// top-level items (see windowStart), child items trimmed (see WireChildTail),
// Jobs windowed, and tool Input capped. Only what is kept is copied.
// TODO(slice A): implement.
func (s *State) WireCopy(maxTop int) *State { panic("projection.WireCopy: not implemented") }

// WirePage returns the page of at most maxTop top-level items ending before
// index `before`, child-trimmed and Input-capped like WireCopy, the index the
// page starts at, and the jobs the page's items spawned (with ancestors).
// TODO(slice A): implement.
func (s *State) WirePage(before, maxTop int) ([]Item, int, []Job) {
	panic("projection.WirePage: not implemented")
}

// WireChildren returns every child item of parentID, Input-capped, in
// timeline order.
// TODO(slice A): implement.
func (s *State) WireChildren(parentID string) []Item {
	panic("projection.WireChildren: not implemented")
}

// WireJobs returns a copy of every job.
// TODO(slice A): implement.
func (s *State) WireJobs() []Job { panic("projection.WireJobs: not implemented") }

// WireEvent returns ev as a client should see it: a tool_call.started or
// tool_call.updated has its Content and RawInput capped the way the
// projection caps them, with Truncated set when anything was cut. Every other
// event, and a tool event with nothing to cut, comes back unchanged.
// TODO(slice A): implement.
func WireEvent(ev proto.Event) proto.Event { panic("projection.WireEvent: not implemented") }
