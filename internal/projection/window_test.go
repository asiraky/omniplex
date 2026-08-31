package projection

import "testing"

func item(id, turnID, parentID string) Item {
	return Item{ID: id, Kind: ItemMessage, TurnID: turnID, ParentID: parentID}
}

func TestWindowKeepsShortTimelinesWhole(t *testing.T) {
	s := New("s")
	s.Items = []Item{item("a", "t1", ""), item("b", "t1", "")}
	s.Window(10)
	if s.ItemsBefore != 0 || len(s.Items) != 2 {
		t.Fatalf("short timeline was trimmed: before=%d len=%d", s.ItemsBefore, len(s.Items))
	}
}

func TestWindowTrimsToTailAtTurnBoundary(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""), item("b", "t1", ""),
		item("c", "t2", ""), item("d", "t2", ""),
		item("e", "t3", ""),
	}
	// Two top-level items reach back into t2; the boundary walk then pulls in
	// all of t2 rather than splitting it.
	s.Window(2)
	if s.ItemsBefore != 2 {
		t.Fatalf("ItemsBefore = %d, want 2", s.ItemsBefore)
	}
	if len(s.Items) != 3 || s.Items[0].ID != "c" {
		t.Fatalf("window = %v", ids(s.Items))
	}
}

func TestWindowDoesNotCountChildren(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		item("a", "t1", ""),
		item("b", "t2", ""),
		item("b1", "t2", "b"), item("b2", "t2", "b"),
		item("c", "t2", ""),
	}
	s.Window(2)
	// b and c are the two top-level items; b's children ride along.
	if s.ItemsBefore != 1 || s.Items[0].ID != "b" || len(s.Items) != 4 {
		t.Fatalf("before=%d window=%v", s.ItemsBefore, ids(s.Items))
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
	s.Window(2)
	// c and d are the two top-level items; the boundary walk hops the notice
	// and pulls the whole of t2 in rather than splitting it there.
	if s.ItemsBefore != 1 || s.Items[0].ID != "b" {
		t.Fatalf("before=%d window=%v", s.ItemsBefore, ids(s.Items))
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
	s.Window(2)
	if s.ItemsBefore != 2 || s.Items[0].ID != "b" {
		t.Fatalf("before=%d window=%v", s.ItemsBefore, ids(s.Items))
	}
}

func TestWindowBeforePages(t *testing.T) {
	items := []Item{
		item("a", "t1", ""), item("b", "t1", ""),
		item("c", "t2", ""),
		item("d", "t3", ""),
	}
	page, start := WindowBefore(items, 3, 1)
	if start != 2 || len(page) != 1 || page[0].ID != "c" {
		t.Fatalf("page=%v start=%d", ids(page), start)
	}
	page, start = WindowBefore(items, 2, 10)
	if start != 0 || len(page) != 2 || page[0].ID != "a" {
		t.Fatalf("page=%v start=%d", ids(page), start)
	}
	// Out-of-range cursors clamp instead of panicking.
	if page, start = WindowBefore(items, 99, 1); start != 3 || len(page) != 1 {
		t.Fatalf("clamped page=%v start=%d", ids(page), start)
	}
	if page, start = WindowBefore(items, 0, 1); start != 0 || len(page) != 0 {
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
