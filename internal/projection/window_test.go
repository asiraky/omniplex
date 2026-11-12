package projection

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func item(id, turnID, parentID string) Item {
	return Item{ID: id, Kind: ItemMessage, TurnID: turnID, ParentID: parentID}
}

// tool is a top-level tool item with a status, the shape a parent of child
// items has.
func tool(id, turnID, status string) Item {
	return Item{ID: id, Kind: ItemTool, TurnID: turnID, Status: status}
}

func TestWindowKeepsShortTimelinesWhole(t *testing.T) {
	s := New("s")
	s.Items = []Item{item("a", "t1", ""), item("b", "t1", "")}
	s.reindex()
	w := s.WireCopy(10)
	if w.ItemsBefore != 0 || len(w.Items) != 2 {
		t.Fatalf("short timeline was trimmed: before=%d len=%d", w.ItemsBefore, len(w.Items))
	}
}

func TestWindowTrimsToTailAtTurnBoundary(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""), item("b", "t1", ""),
		item("c", "t2", ""), item("d", "t2", ""),
		item("e", "t3", ""),
	}
	s.reindex()
	// Two top-level items reach back into t2; the boundary walk then pulls in
	// all of t2 rather than splitting it.
	w := s.WireCopy(2)
	if w.ItemsBefore != 2 {
		t.Fatalf("ItemsBefore = %d, want 2", w.ItemsBefore)
	}
	if len(w.Items) != 3 || w.Items[0].ID != "c" {
		t.Fatalf("window = %v", ids(w.Items))
	}
	if len(s.Items) != 5 {
		t.Fatalf("source was trimmed to %v", ids(s.Items))
	}
}

func TestWindowDoesNotCountChildren(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""),
		tool("b", "t2", proto.StatusInProgress),
		item("b1", "t2", "b"), item("b2", "t2", "b"),
		item("c", "t2", ""),
	}
	s.reindex()
	w := s.WireCopy(2)
	// b and c are the two top-level items; b is running, so its children
	// ride along.
	if w.ItemsBefore != 1 || w.Items[0].ID != "b" || len(w.Items) != 4 {
		t.Fatalf("before=%d window=%v", w.ItemsBefore, ids(w.Items))
	}
}

// maxTop <= 0 is no window at all.
func TestWindowOffKeepsEveryTopLevelItem(t *testing.T) {
	s := New("s")
	s.Items = []Item{item("a", "t1", ""), item("b", "t2", ""), item("c", "t3", "")}
	s.reindex()
	for _, n := range []int{0, -1} {
		if w := s.WireCopy(n); w.ItemsBefore != 0 || len(w.Items) != 3 {
			t.Fatalf("WireCopy(%d): before=%d window=%v", n, w.ItemsBefore, ids(w.Items))
		}
	}
}

func TestWindowHopsMidTurnNotices(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""),
		item("b", "t2", ""),
		item("compact", "", ""), // mid-turn compaction notice, no turn id
		item("c", "t2", ""),
		item("d", "t2", ""),
	}
	s.reindex()
	w := s.WireCopy(2)
	// c and d are the two top-level items; the boundary walk hops the notice
	// and pulls the whole of t2 in rather than splitting it there.
	if w.ItemsBefore != 1 || w.Items[0].ID != "b" {
		t.Fatalf("before=%d window=%v", w.ItemsBefore, ids(w.Items))
	}
}

func TestWindowLeavesBetweenTurnNoticesOut(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""),
		item("compact", "", ""), // between turns
		item("b", "t2", ""),
		item("c", "t2", ""),
	}
	s.reindex()
	w := s.WireCopy(2)
	if w.ItemsBefore != 2 || w.Items[0].ID != "b" {
		t.Fatalf("before=%d window=%v", w.ItemsBefore, ids(w.Items))
	}
}

func TestWirePagePages(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""), item("b", "t1", ""),
		item("c", "t2", ""),
		item("d", "t3", ""),
	}
	s.reindex()
	page, start, _ := s.WirePage(3, 1)
	if start != 2 || len(page) != 1 || page[0].ID != "c" {
		t.Fatalf("page=%v start=%d", ids(page), start)
	}
	page, start, _ = s.WirePage(2, 10)
	if start != 0 || len(page) != 2 || page[0].ID != "a" {
		t.Fatalf("page=%v start=%d", ids(page), start)
	}
	page, start, _ = s.WirePage(3, 0)
	if start != 0 || len(page) != 3 {
		t.Fatalf("unbounded page=%v start=%d", ids(page), start)
	}
	// Out-of-range cursors clamp instead of panicking.
	if page, start, _ = s.WirePage(99, 1); start != 3 || len(page) != 1 {
		t.Fatalf("clamped page=%v start=%d", ids(page), start)
	}
	if page, start, _ = s.WirePage(-5, 1); start != 0 || len(page) != 0 {
		t.Fatalf("empty page=%v start=%d", ids(page), start)
	}
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}
