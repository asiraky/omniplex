package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// seedTaskThread logs a thread whose one tool call spawned a child call and a
// job, then produced a long output. It returns the head.
func seedTaskThread(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := st.CreateThread(ctx, store.ThreadMeta{
		ID: "t1", Cwd: t.TempDir(), Harness: "claudecode", CreatedAt: now, UpdatedAt: now, Phase: "idle",
	}); err != nil {
		t.Fatal(err)
	}
	var head int64
	for _, em := range []proto.Emission{
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "task", Title: "Task", Status: "running"}),
		proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "inner", Title: "Read", Status: "running", ParentToolCallID: "task"}),
		proto.Emit(proto.JobStarted, proto.JobPayload{JobID: "j1", ToolCallID: "task", Kind: proto.JobAgent, Status: "running"}),
		proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: "task", Content: []proto.ToolContent{{Type: "text", Text: strings.Repeat("output line\n", 10000)}},
		}),
	} {
		ev, err := st.Append(ctx, "t1", em)
		if err != nil {
			t.Fatal(err)
		}
		head = ev.Seq
	}
	return head
}

// A reconnect within budget gets its catch-up as one replay frame of capped
// events, then synchronized — never a frame per event.
func TestAttachSendsOneReplayFrameThenSynchronized(t *testing.T) {
	handler, _, st := testServerWithStore(t)
	head := seedTaskThread(t, st)
	c := dialWS(t, handler)

	attach := func(after int64) []serverFrame {
		c.write(clientFrame{Type: "attach", ThreadID: "t1", AfterSeq: &after})
		var got []serverFrame
		for {
			f := c.read()
			if f.ThreadID != "t1" {
				continue
			}
			got = append(got, f)
			if f.Type == "synchronized" || f.Type == "error" {
				return got
			}
		}
	}

	frames := attach(0)
	if len(frames) != 2 || frames[0].Type != "replay" || frames[1].Type != "synchronized" {
		types := []string{}
		for _, f := range frames {
			types = append(types, f.Type)
		}
		t.Fatalf("frames %v, want [replay synchronized]", types)
	}
	replay := frames[0]
	if replay.Seq != head || frames[1].Seq != head || len(replay.Events) != int(head) {
		t.Fatalf("replay seq %d with %d events, synchronized seq %d; want %d of each", replay.Seq, len(replay.Events), frames[1].Seq, head)
	}
	var last struct {
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(replay.Events[head-1].Payload, &last); err != nil || !last.Truncated {
		t.Fatalf("the long output was not capped in the replay: %v", err)
	}

	// Already caught up: nothing to replay, so no replay frame at all.
	frames = attach(head)
	if len(frames) != 1 || frames[0].Type != "synchronized" || frames[0].Seq != head {
		t.Fatalf("caught-up attach sent %+v, want only synchronized at %d", frames, head)
	}
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

// What a wire copy trims, the client can fetch: a page with the jobs its
// items spawned, a parent's children, and every job.
func TestTrimmedPartsOfAThreadCanBeFetched(t *testing.T) {
	handler, _, st := testServerWithStore(t)
	seedTaskThread(t, st)
	ts := httptest.NewServer(handler) // loopback is trusted
	defer ts.Close()
	base := ts.URL + "/api/threads/t1"

	var page struct {
		Items       []projection.Item `json:"items"`
		ItemsBefore *int              `json:"itemsBefore"`
		Jobs        []projection.Job  `json:"jobs"`
	}
	if code := getJSON(t, base+"/items?before=2&count=all", &page); code != http.StatusOK {
		t.Fatalf("items: status %d", code)
	}
	if page.ItemsBefore == nil || *page.ItemsBefore != 0 || len(page.Items) == 0 || page.Items[0].ID != "task" {
		t.Fatalf("items page = %+v, want the whole timeline from the top", page)
	}
	if len(page.Jobs) != 1 || page.Jobs[0].ID != "j1" {
		t.Fatalf("items page jobs = %+v, want the one the task spawned", page.Jobs)
	}

	var children struct {
		Items []projection.Item `json:"items"`
	}
	if code := getJSON(t, base+"/items/task/children", &children); code != http.StatusOK {
		t.Fatalf("children: status %d", code)
	}
	if len(children.Items) != 1 || children.Items[0].ID != "inner" {
		t.Fatalf("children = %+v, want the inner call", children.Items)
	}
	if code := getJSON(t, base+"/items/inner/children", &children); code != http.StatusOK || children.Items == nil || len(children.Items) != 0 {
		t.Fatalf("children of a leaf: status %d, %#v; want an empty list", code, children.Items)
	}

	var jobs struct {
		Jobs []projection.Job `json:"jobs"`
	}
	if code := getJSON(t, base+"/jobs", &jobs); code != http.StatusOK {
		t.Fatalf("jobs: status %d", code)
	}
	if len(jobs.Jobs) != 1 || jobs.Jobs[0].ID != "j1" {
		t.Fatalf("jobs = %+v, want j1", jobs.Jobs)
	}

	for _, path := range []string{"/items?before=0", "/items/task/children", "/jobs"} {
		if code := getJSON(t, ts.URL+"/api/threads/ghost"+path, nil); code != http.StatusNotFound {
			t.Errorf("GET ghost%s returned %d, want 404", path, code)
		}
	}
}
