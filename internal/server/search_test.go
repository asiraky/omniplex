package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// seedConversation logs three prompted turns, each answered. Timeline:
// 0 prompt:t1, 1 a1, 2 prompt:t2, 3 a2, 4 prompt:t3, 5 a3.
func seedConversation(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := st.CreateThread(ctx, store.ThreadMeta{
		ID: "t1", Cwd: t.TempDir(), Harness: "claudecode", CreatedAt: now, UpdatedAt: now, Phase: "idle",
	}); err != nil {
		t.Fatal(err)
	}
	turns := []struct{ id, prompt, answer string }{
		{"t1", "where is the Needle?", "not in the haystack"},
		{"t2", "look again", "found a needle in main.go"},
		{"t3", "thanks", "you're welcome"},
	}
	for _, turn := range turns {
		for _, em := range []proto.Emission{
			proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: turn.id, Prompt: turn.prompt}),
			proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: turn.id, Role: "agent", Kind: "text", BlockID: "a" + turn.id[1:], Delta: turn.answer}),
			proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turn.id}),
		} {
			if _, err := st.Append(ctx, "t1", em); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSearchRouteFindsMatchesAcrossTheThread(t *testing.T) {
	handler, _, st := testServerWithStore(t)
	seedConversation(t, st)
	ts := httptest.NewServer(handler)
	defer ts.Close()
	base := ts.URL + "/api/threads/t1"

	var res struct {
		Matches []projection.Match `json:"matches"`
		Total   int                `json:"total"`
	}
	if code := getJSON(t, base+"/search?q=NEEDLE", &res); code != http.StatusOK {
		t.Fatalf("search: status %d", code)
	}
	if res.Total != 2 || len(res.Matches) != 2 || res.Matches[0].Index != 0 || res.Matches[1].Index != 3 {
		t.Fatalf("search = %+v, want the t1 prompt and the t2 answer", res)
	}

	res.Matches = nil
	if code := getJSON(t, base+"/search?q="+url.QueryEscape("zzz"), &res); code != http.StatusOK || res.Matches == nil || res.Total != 0 {
		t.Fatalf("no match: status %d %#v, want an empty list", code, res)
	}

	for _, q := range []string{"", "n", "é"} {
		if code := getJSON(t, base+"/search?q="+url.QueryEscape(q), nil); code != http.StatusBadRequest {
			t.Errorf("q=%q returned %d, want 400", q, code)
		}
	}
	// Two runes is enough even when they are four bytes.
	if code := getJSON(t, base+"/search?q="+url.QueryEscape("éé"), nil); code != http.StatusOK {
		t.Errorf("q=éé returned %d, want 200", code)
	}
	if code := getJSON(t, ts.URL+"/api/threads/ghost/search?q=needle", nil); code != http.StatusNotFound {
		t.Errorf("ghost search returned %d, want 404", code)
	}
}

// from= pages from the start of the match's turn down to the cursor in one
// response, whatever the page size.
func TestItemsFromJumpsToAMatchsTurn(t *testing.T) {
	handler, _, st := testServerWithStore(t)
	seedConversation(t, st)
	ts := httptest.NewServer(handler)
	defer ts.Close()
	base := ts.URL + "/api/threads/t1"

	var page struct {
		Items       []projection.Item `json:"items"`
		ItemsBefore *int              `json:"itemsBefore"`
		Jobs        []projection.Job  `json:"jobs"`
	}
	// The client holds t3 (from 4); the match is t2's answer at 3.
	if code := getJSON(t, base+"/items?before=4&from=3", &page); code != http.StatusOK {
		t.Fatalf("items: status %d", code)
	}
	var got []string
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	if page.ItemsBefore == nil || *page.ItemsBefore != 2 || len(got) != 2 || got[0] != "prompt:t2" || page.Jobs == nil {
		t.Fatalf("from page = %v before %v, want t2 from index 2", got, page.ItemsBefore)
	}

	// count is ignored when from is given.
	if code := getJSON(t, base+"/items?before=6&from=1&count=all", &page); code != http.StatusOK || *page.ItemsBefore != 0 || len(page.Items) != 6 {
		t.Fatalf("from=1 count=all: before %v items %d", page.ItemsBefore, len(page.Items))
	}

	for _, q := range []string{"from=-1", "from=x", "from=1.5"} {
		if code := getJSON(t, base+"/items?before=4&"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", q, code)
		}
	}
	if code := getJSON(t, ts.URL+"/api/threads/ghost/items?before=4&from=1", nil); code != http.StatusNotFound {
		t.Errorf("ghost from= returned %d, want 404", code)
	}
}
