// Copies of the projection headed for a client. The actor's own state holds
// everything; what goes over the wire is the tail a presenter shows, with
// what it left out counted so the client can ask for it.
package projection

import (
	"encoding/json"

	"github.com/asiraky/omniplex/internal/proto"
)

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
// Jobs windowed, and tool Input capped. Only what is kept is copied. maxTop
// <= 0 keeps every top-level item; the other trims still apply.
//
// The jobs kept are the live ones, the last WireFinishedJobs finished, those
// a kept item spawned — a spawn card in the transcript finds its job by tool
// call id — and every ancestor of a kept job, so the roster can nest it.
func (s *State) WireCopy(maxTop int) *State {
	out := s.cloneRest()
	start := 0
	if maxTop > 0 {
		start = windowStart(s.Items, len(s.Items), maxTop)
	}
	out.ItemsBefore = start
	out.Items = s.wireItems(start, len(s.Items))

	keep := s.jobsSpawnedBy(out.Items)
	finished := WireFinishedJobs
	for i := len(s.Jobs) - 1; i >= 0; i-- {
		if !proto.JobDone(s.Jobs[i].Status) {
			keep[i] = true
		} else if finished > 0 {
			keep[i] = true
			finished--
		}
	}
	out.Jobs = s.keptJobs(keep)
	out.JobsBefore = len(s.Jobs) - len(out.Jobs)
	return out
}

// WirePage returns the page of at most maxTop top-level items ending before
// index `before`, child-trimmed and Input-capped like WireCopy, the index the
// page starts at, and the jobs the page's items spawned (with ancestors).
// maxTop <= 0 pages in everything before `before`.
func (s *State) WirePage(before, maxTop int) ([]Item, int, []Job) {
	before = min(max(before, 0), len(s.Items))
	start := 0
	if maxTop > 0 {
		start = windowStart(s.Items, before, maxTop)
	}
	page := s.wireItems(start, before)
	return page, start, s.keptJobs(s.jobsSpawnedBy(page))
}

// WireChildren returns every child item of parentID, Input-capped, in
// timeline order.
func (s *State) WireChildren(parentID string) []Item {
	out := []Item{}
	if parentID == "" {
		return out
	}
	for _, it := range s.Items {
		if it.ParentID == parentID {
			out = append(out, wireItem(it))
		}
	}
	return out
}

// WireJobs returns a copy of every job.
func (s *State) WireJobs() []Job { return append([]Job{}, s.Jobs...) }

// WireEvent returns ev as a client should see it: a tool_call.started or
// tool_call.updated has its Content and RawInput capped the way the
// projection caps them, with Truncated set when anything was cut. Every other
// event, and a tool event with nothing to cut, comes back unchanged.
func WireEvent(ev proto.Event) proto.Event {
	var payload any
	switch ev.Type {
	case proto.ToolCallStarted:
		var p proto.ToolCallStartedPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return ev
		}
		var cut bool
		if p.RawInput, cut = capJSONStrings(p.RawInput); !cut {
			return ev
		}
		p.Truncated = true
		payload = p
	case proto.ToolCallUpdated:
		var p proto.ToolCallUpdatedPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return ev
		}
		var inCut bool
		p.RawInput, inCut = capJSONStrings(p.RawInput)
		// The same rule the projection folds with, applied to this one
		// update as if it were the item's first.
		var it Item
		appendToolContent(&it, p.Content)
		if !inCut && !it.Truncated {
			return ev
		}
		p.Content = it.Content
		p.Truncated = true
		payload = p
	default:
		return ev
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ev
	}
	ev.Payload = b
	return ev
}

// wireItems copies s.Items[lo:hi] for a client, Input-capped. A child item
// rides along only while its parent is still running, and then only if it is
// among the parent's last WireChildTail children; a settled parent's
// narration lives in the roster's drill-down, fetched on demand. Each parent
// in the copy counts the children it went without, across the whole timeline.
func (s *State) wireItems(lo, hi int) []Item {
	// Walking back from the end ranks each child among its parent's: the
	// first WireChildTail met are the tail. The walk ends with every
	// parent's total.
	children := map[string]int{}
	tail := make([]bool, hi-lo)
	for i := len(s.Items) - 1; i >= 0; i-- {
		p := s.Items[i].ParentID
		if p == "" {
			continue
		}
		if i >= lo && i < hi {
			tail[i-lo] = children[p] < WireChildTail
		}
		children[p]++
	}

	running := map[string]bool{}
	carried := map[string]int{}
	out := make([]Item, 0, hi-lo)
	for i := lo; i < hi; i++ {
		it := s.Items[i]
		if p := it.ParentID; p != "" {
			live, ok := running[p]
			if !ok {
				parent, found := s.ItemByID(p)
				live = found && (parent.Status == proto.StatusInProgress || parent.Status == proto.StatusPending)
				running[p] = live
			}
			if !live || !tail[i-lo] {
				continue
			}
			carried[p]++
		}
		out = append(out, wireItem(it))
	}
	for i := range out {
		if n := children[out[i].ID]; n > 0 {
			out[i].ChildrenBefore = n - carried[out[i].ID]
		}
	}
	return out
}

// wireItem deep-copies it with its Input capped. A capped Input is fresh
// bytes already, so only an uncut one is copied.
func wireItem(it Item) Item {
	in, cut := capJSONStrings(it.Input)
	if cut {
		it.Input = nil
	}
	cp := copyItem(it)
	if cut {
		cp.Input = in
		cp.Truncated = true
	}
	return cp
}

// jobsSpawnedBy marks, by position in s.Jobs, the jobs one of items spawned.
func (s *State) jobsSpawnedBy(items []Item) []bool {
	keep := make([]bool, len(s.Jobs))
	if len(items) == 0 {
		return keep
	}
	ids := make(map[string]struct{}, len(items))
	for _, it := range items {
		ids[it.ID] = struct{}{}
	}
	for i, j := range s.Jobs {
		if _, ok := ids[j.ToolCallID]; ok && j.ToolCallID != "" {
			keep[i] = true
		}
	}
	return keep
}

// keptJobs copies the jobs keep marks, adding every ancestor of each so a
// nested job never arrives without the jobs above it. Order is s.Jobs's.
func (s *State) keptJobs(keep []bool) []Job {
	if s.jobIndex == nil {
		s.reindexJobs()
	}
	// walked marks a job whose ancestors are all kept already, so each chain
	// is climbed once and a cycle cannot loop.
	walked := make([]bool, len(s.Jobs))
	for i := range s.Jobs {
		if !keep[i] {
			continue
		}
		for j := i; !walked[j]; {
			walked[j], keep[j] = true, true
			parent := s.Jobs[j].ParentJobID
			p, ok := s.jobIndex[parent]
			if parent == "" || !ok {
				break
			}
			j = p
		}
	}
	out := []Job{}
	for i, k := range keep {
		if k {
			out = append(out, s.Jobs[i])
		}
	}
	return out
}
