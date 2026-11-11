package projection

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func job(id, status string) Job { return Job{ID: id, Kind: proto.JobAgent, Status: status} }

func jobIDs(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

// bigText is a string well past the cap.
var bigText = strings.Repeat("h", 6000) + strings.Repeat("m", 10000) + strings.Repeat("t", 6000)

// A wire copy keeps every live job and the last few finished, plus an older
// finished job a kept item spawned and every ancestor of a kept job. The rest
// are counted, and what is kept stays in roster order.
func TestWireCopyWindowsJobs(t *testing.T) {
	s := New("s")
	s.Items = []Item{tool("spawn", "t1", proto.StatusCompleted)}

	spawned := job("old-spawned", proto.JobCompleted)
	spawned.ToolCallID = "spawn"
	parent := job("old-parent", proto.JobCompleted)
	parent.ParentJobID = "old-grand"
	live := job("live", proto.JobRunning)
	live.ParentJobID = "old-parent"
	s.Jobs = []Job{
		spawned,
		job("old-dropped", proto.JobFailed),
		job("old-grand", proto.JobCompleted),
		parent,
		live,
		job("old-dropped-2", proto.JobCompleted),
	}
	want := []string{"old-spawned", "old-grand", "old-parent", "live"}
	for i := range WireFinishedJobs {
		id := "f" + strconv.Itoa(i)
		s.Jobs = append(s.Jobs, job(id, proto.JobCompleted))
		want = append(want, id)
		// A live job among the recent finished ones takes none of their
		// places.
		if i == WireFinishedJobs/2 {
			s.Jobs = append(s.Jobs, job("live-2", proto.JobRunning))
			want = append(want, "live-2")
		}
	}
	s.reindex()

	w := s.WireCopy(10)
	if got := jobIDs(w.Jobs); !reflect.DeepEqual(got, want) {
		t.Fatalf("jobs = %v\nwant %v", got, want)
	}
	if w.JobsBefore != len(s.Jobs)-len(want) {
		t.Fatalf("JobsBefore = %d, want %d", w.JobsBefore, len(s.Jobs)-len(want))
	}
}

// A settled parent's children are left out and counted; a running parent
// keeps only the tail of its children, and counts the rest.
func TestWireCopyTrimsChildren(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		tool("settled", "t1", proto.StatusCompleted),
		item("s0", "t1", "settled"), item("s1", "t1", "settled"),
		tool("live", "t2", proto.StatusInProgress),
	}
	extra := 3
	var tail []string
	for i := range WireChildTail + extra {
		id := "l" + strconv.Itoa(i)
		s.Items = append(s.Items, item(id, "t2", "live"))
		if i >= extra {
			tail = append(tail, id)
		}
	}
	s.reindex()

	w := s.WireCopy(0)
	want := append([]string{"settled", "live"}, tail...)
	if got := ids(w.Items); !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %v\nwant %v", got, want)
	}
	if w.ItemsBefore != 0 {
		t.Fatalf("ItemsBefore = %d: dropped children are not a window", w.ItemsBefore)
	}
	if w.Items[0].ChildrenBefore != 2 || w.Items[1].ChildrenBefore != extra {
		t.Fatalf("ChildrenBefore settled=%d live=%d, want 2/%d", w.Items[0].ChildrenBefore, w.Items[1].ChildrenBefore, extra)
	}
	for _, it := range s.Items {
		if it.ChildrenBefore != 0 {
			t.Fatal("the live state was counted on")
		}
	}
}

// A child in the window whose running parent fell outside it still rides
// along: the parent is looked up across the whole timeline.
func TestWireCopyKeepsChildOfRunningParentOutsideWindow(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		tool("p", "t1", proto.StatusInProgress),
		item("c0", "t1", "p"),
		item("a", "t2", ""), item("b", "t2", ""),
		item("c1", "t2", "p"),
		item("c", "t2", ""),
	}
	s.reindex()
	w := s.WireCopy(2)
	if got := ids(w.Items); !reflect.DeepEqual(got, []string{"a", "b", "c1", "c"}) {
		t.Fatalf("window = %v", got)
	}
}

// Past a running parent's tail, a child still running rides along — left
// out, it would miss the update that settles it — and so does the parent's
// newest message, which may still be streaming. Older settled children do
// not.
func TestWireCopyKeepsLiveChildrenBeyondTail(t *testing.T) {
	s := New("s")
	busy := tool("busy", "t1", proto.StatusInProgress)
	busy.ParentID = "p"
	done := tool("done", "t1", proto.StatusCompleted)
	done.ParentID = "p"
	s.Items = []Item{
		tool("p", "t1", proto.StatusInProgress),
		item("old-msg", "t1", "p"), busy, item("new-msg", "t1", "p"), done,
	}
	for i := range WireChildTail {
		c := tool("l"+strconv.Itoa(i), "t1", proto.StatusCompleted)
		c.ParentID = "p"
		s.Items = append(s.Items, c)
	}
	s.reindex()
	w := s.WireCopy(0)
	if got := ids(w.Items[:3]); !reflect.DeepEqual(got, []string{"p", "busy", "new-msg"}) || len(w.Items) != WireChildTail+3 {
		t.Fatalf("items = %v", ids(w.Items))
	}
	if w.Items[0].ChildrenBefore != 2 {
		t.Fatalf("ChildrenBefore = %d, want 2", w.Items[0].ChildrenBefore)
	}
}

// A finished job goes over the wire without its activity; a running one keeps
// it. The live state keeps both.
func TestWireJobsDropFinishedActivity(t *testing.T) {
	s := New("s")
	live, done := job("live", proto.JobRunning), job("done", proto.JobCompleted)
	live.Activity, done.Activity = "Read", "Edit"
	s.Jobs = []Job{live, done}
	s.reindex()
	for name, got := range map[string][]Job{"WireCopy": s.WireCopy(0).Jobs, "WireJobs": s.WireJobs()} {
		if len(got) != 2 || got[0].Activity != "Read" || got[1].Activity != "" {
			t.Fatalf("%s: jobs = %+v", name, got)
		}
	}
	if s.Jobs[1].Activity != "Edit" {
		t.Fatal("the live state lost its activity")
	}
}

// Nothing in a wire copy shares memory with the live state.
func TestWireCopyDoesNotAliasTheLiveState(t *testing.T) {
	s := New("s")
	s.Items = []Item{{
		ID: "c1", Kind: ItemTool, Status: proto.StatusCompleted,
		Input:   json.RawMessage(`{"command":"ls"}`),
		Content: []proto.ToolContent{{Type: "text", Text: "out"}},
		Card:    json.RawMessage(`{"k":1}`),
	}}
	s.Jobs = []Job{job("j1", proto.JobRunning)}
	before, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	s.reindex()

	w := s.WireCopy(10)
	w.Items[0].Content[0].Text = "changed"
	w.Items[0].Input[2] = 'X'
	w.Items[0].Card[2] = 'X'
	w.Items[0].Title = "changed"
	w.Jobs[0].Status = proto.JobCompleted
	w.Items = append(w.Items[:0], item("other", "", ""))

	after, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("mutating the copy changed the source:\n%s\n%s", before, after)
	}
	if _, ok := w.ItemByID("other"); !ok {
		t.Fatal("the copy's index was carried over from the source")
	}
}

// Tool input on the wire has its long strings capped and the item marked.
// Input with nothing to cut goes out as it was, and a pending permission's
// input is never capped: the user has to see what they approve.
func TestWireCopyCapsInput(t *testing.T) {
	big, err := json.Marshal(map[string]any{"file_path": "a.go", "content": bigText})
	if err != nil {
		t.Fatal(err)
	}
	small := json.RawMessage(`{"command":"ls","n":12345678901234567890}`)
	s := New("s")
	s.Items = []Item{
		{ID: "w", Kind: ItemTool, Status: proto.StatusCompleted, Input: big},
		{ID: "r", Kind: ItemTool, Status: proto.StatusCompleted, Input: small},
	}
	s.Pending = []PendingPermission{{RequestID: "q", ToolCallID: "w", Input: big}}
	s.reindex()

	w := s.WireCopy(0)
	var in struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(w.Items[0].Input, &in); err != nil {
		t.Fatal(err)
	}
	if !w.Items[0].Truncated || in.FilePath != "a.go" {
		t.Fatalf("truncated=%v file_path=%q", w.Items[0].Truncated, in.FilePath)
	}
	if head, n, tail := splitPreview(t, in.Content); len(head)+n+len(tail) != len(bigText) {
		t.Fatal("input string is not the original's head and tail")
	}
	if w.Items[1].Truncated || !bytes.Equal(w.Items[1].Input, small) {
		t.Fatalf("small input changed: truncated=%v %s", w.Items[1].Truncated, w.Items[1].Input)
	}
	if !bytes.Equal(w.Pending[0].Input, big) {
		t.Fatal("pending permission input was capped")
	}
	if s.Items[0].Truncated || !bytes.Equal(s.Items[0].Input, big) {
		t.Fatal("capping the copy touched the source")
	}
}

// A page above the window is trimmed like the window, counts children past
// its end, and brings the jobs its items spawned with their ancestors only.
func TestWirePageTrimsAndBringsItsJobs(t *testing.T) {
	s := New("s")
	s.Items = []Item{
		tool("p", "t1", proto.StatusInProgress),
		item("c0", "t1", "p"), item("c1", "t1", "p"),
		tool("q", "t2", proto.StatusCompleted),
		item("c2", "t2", "p"),
	}
	inPage := job("in-page", proto.JobRunning)
	inPage.ToolCallID = "p"
	inPage.ParentJobID = "root"
	outside := job("outside", proto.JobCompleted)
	outside.ToolCallID = "q"
	s.Jobs = []Job{job("root", proto.JobCompleted), outside, job("unrelated", proto.JobRunning), inPage}
	s.reindex()

	page, start, jobs := s.WirePage(3, 1)
	if start != 0 || !reflect.DeepEqual(ids(page), []string{"p", "c0", "c1"}) {
		t.Fatalf("page=%v start=%d", ids(page), start)
	}
	if page[0].ChildrenBefore != 1 {
		t.Fatalf("ChildrenBefore = %d, want 1 (c2, past the page)", page[0].ChildrenBefore)
	}
	if got := jobIDs(jobs); !reflect.DeepEqual(got, []string{"root", "in-page"}) {
		t.Fatalf("jobs = %v", got)
	}
}

func TestWireChildren(t *testing.T) {
	big, err := json.Marshal(map[string]string{"content": bigText})
	if err != nil {
		t.Fatal(err)
	}
	s := New("s")
	s.Items = []Item{
		tool("p", "t1", proto.StatusCompleted),
		item("c0", "t1", "p"),
		{ID: "c1", Kind: ItemTool, ParentID: "p", Input: big},
		item("x", "t1", "other"),
	}
	got := s.WireChildren("p")
	if !reflect.DeepEqual(ids(got), []string{"c0", "c1"}) {
		t.Fatalf("children = %v", ids(got))
	}
	if !got[1].Truncated || len(got[1].Input) >= len(big) {
		t.Fatal("child input not capped")
	}
	if none := s.WireChildren("nobody"); none == nil || len(none) != 0 {
		t.Fatalf("no children = %#v, want an empty slice", none)
	}
}

func TestWireJobsIsACopy(t *testing.T) {
	s := New("s")
	s.Jobs = []Job{job("j1", proto.JobRunning), job("j2", proto.JobCompleted)}
	got := s.WireJobs()
	got[0].Status = proto.JobCompleted
	if !reflect.DeepEqual(jobIDs(got), []string{"j1", "j2"}) || s.Jobs[0].Status != proto.JobRunning {
		t.Fatalf("jobs=%v source status=%s", jobIDs(got), s.Jobs[0].Status)
	}
}

// A tool update on the wire keeps the projection's head/tail and text budget,
// says it was cut, and leaves the log's bytes alone.
func TestWireEventCapsToolUpdate(t *testing.T) {
	content := make([]proto.ToolContent, maxToolContent+4)
	for i := range content {
		content[i] = proto.ToolContent{Type: "text", Text: strconv.Itoa(i) + bigText}
	}
	ev := toolUpdate(t, 1, "c1", content...)
	orig := append(json.RawMessage(nil), ev.Payload...)

	out := WireEvent(ev)
	if !bytes.Equal(ev.Payload, orig) {
		t.Fatal("WireEvent mutated the event's payload")
	}
	var p proto.ToolCallUpdatedPayload
	if err := json.Unmarshal(out.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if n := len(p.Content); !p.Truncated || n == 0 || n != keptUnder(len(p.Content[0].Text)) || p.ToolCallID != "c1" {
		t.Fatalf("truncated=%v entries=%d id=%q", p.Truncated, len(p.Content), p.ToolCallID)
	}
	for i, c := range p.Content {
		text := content[i].Text
		head, n, tail := splitPreview(t, c.Text)
		if head != text[:len(head)] || tail != text[len(text)-len(tail):] || len(head)+n+len(tail) != len(text) {
			t.Fatalf("entry %d is not the original's head and tail", i)
		}
	}
	if out.Seq != ev.Seq || out.Type != ev.Type {
		t.Fatal("envelope changed")
	}
}

func TestWireEventCapsToolStartInput(t *testing.T) {
	big, err := json.Marshal(map[string]string{"content": bigText})
	if err != nil {
		t.Fatal(err)
	}
	ev := event(t, 1, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		ToolCallID: "c1", Kind: "edit", Title: "Write", Status: proto.StatusPending, RawInput: big,
	})
	var p proto.ToolCallStartedPayload
	if err := json.Unmarshal(WireEvent(ev).Payload, &p); err != nil {
		t.Fatal(err)
	}
	if !p.Truncated || len(p.RawInput) >= len(big) || p.Title != "Write" {
		t.Fatalf("truncated=%v input %d bytes from %d", p.Truncated, len(p.RawInput), len(big))
	}
}

// Events with nothing to cut, and events that are not tool events, go out
// with the very bytes they came in with.
func TestWireEventPassesThroughWhatFits(t *testing.T) {
	for _, ev := range []proto.Event{
		toolUpdate(t, 1, "c1", proto.ToolContent{Type: "text", Text: "ok"}),
		event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "c1", RawInput: json.RawMessage(`{"a":1}`)}),
		event(t, 3, proto.MessageChunk, map[string]string{"text": bigText}),
	} {
		out := WireEvent(ev)
		if &out.Payload[0] != &ev.Payload[0] || len(out.Payload) != len(ev.Payload) {
			t.Fatalf("%s: payload was re-encoded", ev.Type)
		}
	}
}
