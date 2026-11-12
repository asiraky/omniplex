package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func appendToolUpdate(t *testing.T, s *Store, threadID, callID string, content ...proto.ToolContent) {
	t.Helper()
	em := proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: callID, Status: "running", Content: content})
	if _, err := s.Append(context.Background(), threadID, em); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func text(s string) proto.ToolContent { return proto.ToolContent{Type: "text", Text: s} }

func TestToolBodyFoldsOneCallsUpdatesInOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateThread(t, s, "t1")
	mustCreateThread(t, s, "t2")

	// Interleaved with another call, another thread reusing the same call id,
	// and a start that carries no content, none of which add to the output.
	appendToolUpdate(t, s, "t1", "call-a", text("one"))
	appendToolUpdate(t, s, "t1", "call-b", text("other call"))
	appendToolUpdate(t, s, "t2", "call-a", text("other thread"))
	if _, err := s.Append(ctx, "t1", proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "call-a", Title: "started"})); err != nil {
		t.Fatal(err)
	}
	appendToolUpdate(t, s, "t1", "call-a") // a status-only update adds nothing
	appendToolUpdate(t, s, "t1", "call-a", text("two"), proto.ToolContent{Type: "diff", Path: "a.go", Old: "x", New: "y"})

	got, err := s.ToolBody(ctx, "t1", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []proto.ToolContent{text("one"), text("two"), {Type: "diff", Path: "a.go", Old: "x", New: "y"}}
	if !reflect.DeepEqual(got.Content, want) {
		t.Fatalf("content = %+v, want %+v", got.Content, want)
	}
	if got.Input != nil {
		t.Fatalf("input = %s, want none: no row carried any", got.Input)
	}

	if _, err := s.ToolBody(ctx, "t1", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown call: got %v, want ErrNotFound", err)
	}
	if _, err := s.ToolBody(ctx, "ghost", "call-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread: got %v, want ErrNotFound", err)
	}
}

// A call whose updates never carried content still exists: it answers with
// an empty list, not ErrNotFound.
func TestToolBodyOfACallWithNoOutputIsEmpty(t *testing.T) {
	s := openTestStore(t)
	mustCreateThread(t, s, "t1")
	appendToolUpdate(t, s, "t1", "call-a")

	got, err := s.ToolBody(context.Background(), "t1", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content == nil || len(got.Content) != 0 {
		t.Fatalf("content = %#v, want an empty list", got.Content)
	}
}

// The input is whatever the call last said it was, from its start or a later
// update, so the whole of one the wire capped can be fetched. A call that has
// only started is found, with its input and no output.
func TestToolBodyCarriesTheLastInput(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateThread(t, s, "t1")
	start := func(callID, input string) {
		t.Helper()
		em := proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: callID, RawInput: json.RawMessage(input)})
		if _, err := s.Append(ctx, "t1", em); err != nil {
			t.Fatal(err)
		}
	}
	update := func(callID, input string) {
		t.Helper()
		p := proto.ToolCallUpdatedPayload{ToolCallID: callID, Content: []proto.ToolContent{text("out")}}
		if input != "" {
			p.RawInput = json.RawMessage(input)
		}
		if _, err := s.Append(ctx, "t1", proto.Emit(proto.ToolCallUpdated, p)); err != nil {
			t.Fatal(err)
		}
	}

	start("call-a", `{"partial":true}`)
	update("call-a", `{"command":"ls -la"}`)
	update("call-a", "") // an update without input keeps the last one
	start("call-b", `{"path":"b.go"}`)

	got, err := s.ToolBody(ctx, "t1", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Input) != `{"command":"ls -la"}` {
		t.Errorf("input = %s, want the update's", got.Input)
	}
	if len(got.Content) != 2 {
		t.Errorf("content = %+v, want both updates'", got.Content)
	}

	got, err = s.ToolBody(ctx, "t1", "call-b")
	if err != nil {
		t.Fatalf("a call with only a start: %v", err)
	}
	if string(got.Input) != `{"path":"b.go"}` || len(got.Content) != 0 {
		t.Errorf("body = %+v, want the start's input and no output", got)
	}
}

// The lookup has to be an index seek: on a long thread a scan reads every
// event's payload, which is the cost this read exists to avoid.
func TestToolBodySeeksTheIndex(t *testing.T) {
	s := openTestStore(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+toolBodyQuery, "t1", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "USING INDEX events_tool_body") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("plan does not seek events_tool_body in seq order:\n%s", joined)
	}
}
