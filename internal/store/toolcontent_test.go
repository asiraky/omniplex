package store

import (
	"context"
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

func TestToolContentFoldsOneCallsUpdatesInOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateThread(t, s, "t1")
	mustCreateThread(t, s, "t2")

	// Interleaved with another call, another thread reusing the same call id,
	// and an event of another type that names the call, none of which belong
	// in the answer.
	appendToolUpdate(t, s, "t1", "call-a", text("one"))
	appendToolUpdate(t, s, "t1", "call-b", text("other call"))
	appendToolUpdate(t, s, "t2", "call-a", text("other thread"))
	if _, err := s.Append(ctx, "t1", proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{ToolCallID: "call-a", Title: "started"})); err != nil {
		t.Fatal(err)
	}
	appendToolUpdate(t, s, "t1", "call-a") // a status-only update adds nothing
	appendToolUpdate(t, s, "t1", "call-a", text("two"), proto.ToolContent{Type: "diff", Path: "a.go", Old: "x", New: "y"})

	got, err := s.ToolContent(ctx, "t1", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []proto.ToolContent{text("one"), text("two"), {Type: "diff", Path: "a.go", Old: "x", New: "y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content = %+v, want %+v", got, want)
	}

	if _, err := s.ToolContent(ctx, "t1", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown call: got %v, want ErrNotFound", err)
	}
	if _, err := s.ToolContent(ctx, "ghost", "call-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown thread: got %v, want ErrNotFound", err)
	}
}

// A call whose updates never carried content still exists: it answers with
// an empty list, not ErrNotFound.
func TestToolContentOfACallWithNoOutputIsEmpty(t *testing.T) {
	s := openTestStore(t)
	mustCreateThread(t, s, "t1")
	appendToolUpdate(t, s, "t1", "call-a")

	got, err := s.ToolContent(context.Background(), "t1", "call-a")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("content = %#v, want an empty list", got)
	}
}

// The lookup has to be an index seek: on a long thread a scan reads every
// event's payload, which is the cost this read exists to avoid.
func TestToolContentSeeksTheIndex(t *testing.T) {
	s := openTestStore(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+toolContentQuery, "t1", "call-a")
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
	if !strings.Contains(joined, "USING INDEX events_tool_call") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("plan does not seek events_tool_call in seq order:\n%s", joined)
	}
}
