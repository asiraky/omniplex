package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

func TestToolContentServesTheWholeBodyFromTheLog(t *testing.T) {
	handler, guard, st := testServerWithStore(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	ctx := context.Background()
	now := time.Now().UnixMilli()
	if err := st.CreateThread(ctx, store.ThreadMeta{
		ID: "t1", Cwd: t.TempDir(), Harness: "claudecode", CreatedAt: now, UpdatedAt: now, Phase: "idle",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, "t1", proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
		ToolCallID: "call-a", RawInput: json.RawMessage(`{"command":"make test"}`),
	})); err != nil {
		t.Fatal(err)
	}
	for _, p := range []proto.ToolCallUpdatedPayload{
		{ToolCallID: "call-a", Content: []proto.ToolContent{{Type: "text", Text: "head"}}},
		{ToolCallID: "call-b", Content: []proto.ToolContent{{Type: "text", Text: "not this one"}}},
		{ToolCallID: "call-a", Status: "completed", Content: []proto.ToolContent{{Type: "text", Text: "tail"}}},
	} {
		if _, err := st.Append(ctx, "t1", proto.Emit(proto.ToolCallUpdated, p)); err != nil {
			t.Fatal(err)
		}
	}

	path := "/api/threads/t1/items/call-a/content"
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET %s from an unpaired device returned %d, want 401", path, res.StatusCode)
	}

	client := pairedClient(t, ts, guard)
	res, err = client.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	var body struct {
		Content []proto.ToolContent `json:"content"`
		Input   json.RawMessage     `json:"input"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := []proto.ToolContent{{Type: "text", Text: "head"}, {Type: "text", Text: "tail"}}
	if !reflect.DeepEqual(body.Content, want) {
		t.Fatalf("content = %+v, want %+v", body.Content, want)
	}
	if string(body.Input) != `{"command":"make test"}` {
		t.Fatalf("input = %s, want the call's", body.Input)
	}

	for _, missing := range []string{"/api/threads/t1/items/ghost/content", "/api/threads/ghost/items/call-a/content"} {
		res, err := client.Get(ts.URL + missing)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want 404", missing, res.StatusCode)
		}
	}
}
