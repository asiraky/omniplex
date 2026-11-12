// Windowing the timeline for the wire. A long thread's items dwarf the rest
// of the state put together, and a presenter opening it only needs the tail on
// screen — the rest can arrive as the reader scrolls up. The window is a
// presentation concern only: Seq still means "everything up to here", the
// event log and attach/replay are untouched, and the actor's own state is
// never windowed. Only copies headed for a client are (see WireCopy).
package projection

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
	if start == 0 || start >= len(items) {
		return start
	}
	// A compaction notice has no turn and can land mid-turn, so the walk hops
	// over turnless runs: if what lies beyond one is the same turn, the notice
	// and everything back to the turn's start come along. If it is a different
	// turn — the notice sat between turns — the walk stops and leaves it out.
	t := items[start].TurnID
	for start > 0 && t != "" {
		j := start - 1
		for j >= 0 && items[j].TurnID == "" {
			j--
		}
		if j < 0 || items[j].TurnID != t {
			break
		}
		start = j
	}
	return start
}
