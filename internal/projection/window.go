// Windowing the timeline for the wire. A long session's items dwarf the rest
// of the state put together, and a presenter opening it only needs the tail on
// screen — the rest can arrive as the reader scrolls up. The window is a
// presentation concern only: Seq still means "everything up to here", the
// event log and attach/replay are untouched, and the actor's own state is
// never windowed. Only clones headed for a client are.
package projection

// Window trims s.Items to a suffix holding at most maxTop top-level items,
// recording how many items were cut in ItemsBefore so the client can ask for
// the page above. Child items (ParentID set) ride along without counting: they
// narrate inside their parents, not in the transcript column.
//
// Must be called on a Clone, never on the actor's live state.
func (s *State) Window(maxTop int) {
	start := windowStart(s.Items, len(s.Items), maxTop)
	if start == 0 {
		return
	}
	s.ItemsBefore += start
	s.Items = s.Items[start:]
	s.itemIndex = nil
}

// WindowBefore returns the page of at most maxTop top-level items that ends
// just before index `before` in the full timeline, and the index the page
// starts at — the caller's next cursor. It reads; it never mutates.
func WindowBefore(items []Item, before, maxTop int) ([]Item, int) {
	if before > len(items) {
		before = len(items)
	}
	if before < 0 {
		before = 0
	}
	start := windowStart(items, before, maxTop)
	return items[start:before], start
}

// windowStart walks backwards from `end` until maxTop top-level items are
// included, then keeps walking to the start of the turn it landed in — a fold
// built from half a turn would claim a duration it did not see.
func windowStart(items []Item, end, maxTop int) int {
	if end > len(items) {
		end = len(items)
	}
	start := end
	top := 0
	for start > 0 {
		if items[start-1].ParentID == "" {
			if top == maxTop {
				break
			}
			top++
		}
		start--
	}
	for start > 0 && items[start].TurnID != "" && items[start-1].TurnID == items[start].TurnID {
		start--
	}
	return start
}
