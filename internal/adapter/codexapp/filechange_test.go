package codexapp

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// logHost records every line the adapter logs.
type logHost struct {
	elicitHost
	mu    sync.Mutex
	lines []string
}

func (h *logHost) Logf(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, fmt.Sprintf(format, args...))
}

// TestFileChangeFromRealPayload decodes an item/completed codex 0.162 sent for
// one patch that edits, adds, deletes and renames. kind is an object there;
// decoding it as a string dropped every Codex edit from the timeline.
func TestFileChangeFromRealPayload(t *testing.T) {
	completed, err := os.ReadFile("testdata/filechange-completed.json")
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := json.Unmarshal(completed, &frame); err != nil {
		t.Fatal(err)
	}
	// item/started carries the same item, still in progress.
	var started map[string]any
	_ = json.Unmarshal(completed, &started)
	started["item"].(map[string]any)["status"] = "inProgress"
	startedJSON, _ := json.Marshal(started)

	s := subagentSession(t)
	s.threadID = frame.ThreadID
	s.turnID, s.serverTurnID = "omniplex-turn", frame.TurnID
	s.handleNotification("item/started", startedJSON)
	s.handleNotification("item/completed", completed)

	got := drain(s)
	if len(got) != 2 || got[0].Type != proto.ToolCallStarted || got[1].Type != proto.ToolCallUpdated {
		t.Fatalf("emissions = %+v, want a tool call started then updated", got)
	}
	wantTitle := "Edit /repo/a.txt, /repo/c.txt, /repo/gone.txt, /repo/old.txt → /repo/new.txt"
	start := got[0].Payload.(proto.ToolCallStartedPayload)
	if start.Kind != proto.KindEdit || start.Title != wantTitle || start.TurnID != "omniplex-turn" {
		t.Fatalf("started = %+v", start)
	}
	update := got[1].Payload.(proto.ToolCallUpdatedPayload)
	if update.Status != proto.StatusCompleted || update.Title != wantTitle {
		t.Fatalf("updated = %+v", update)
	}
	want := []proto.ToolContent{
		{Type: "diff", Path: "/repo/a.txt", Text: "@@ -1,2 +1,2 @@\n alpha\n-beta\n+gamma\n"},
		{Type: "diff", Path: "/repo/c.txt", Text: "hello\n"},
		{Type: "diff", Path: "/repo/gone.txt", Text: "doomed\n"},
		{Type: "diff", Path: "/repo/new.txt", Text: "moved from /repo/old.txt\n"},
	}
	if !slices.Equal(update.Content, want) {
		t.Fatalf("content = %+v\nwant %+v", update.Content, want)
	}
}

func TestFileChangeKindFollowsTheOperation(t *testing.T) {
	change := func(path, typ, movePath, diff string) codexChange {
		var c codexChange
		c.Path, c.Kind.Type, c.Diff = path, typ, diff
		if movePath != "" {
			c.Kind.MovePath = &movePath
		}
		return c
	}
	cases := []struct {
		name      string
		changes   []codexChange
		kind      string
		title     string
		firstText string
	}{
		{"rename", []codexChange{change("a.go", "update", "b.go", "\n\nMoved to: b.go")},
			proto.KindMove, "Move a.go → b.go", "moved from a.go\n"},
		{"rename with an edit keeps the diff", []codexChange{change("a.go", "update", "b.go", "@@ -1 +1 @@\n-x\n+y\n\n\nMoved to: b.go")},
			proto.KindMove, "Move a.go → b.go", "moved from a.go\n@@ -1 +1 @@\n-x\n+y\n"},
		{"delete", []codexChange{change("a.go", "delete", "", "x\n"), change("b.go", "delete", "", "y\n")},
			proto.KindDelete, "Delete a.go, b.go", "x\n"},
		{"add", []codexChange{change("a.go", "add", "", "x\n")},
			proto.KindEdit, "Add a.go", "x\n"},
		{"null move_path is an edit", []codexChange{change("a.go", "update", "", "@@\n")},
			proto.KindEdit, "Edit a.go", "@@\n"},
		{"mixed is an edit", []codexChange{change("a.go", "delete", "", "x\n"), change("b.go", "add", "", "y\n")},
			proto.KindEdit, "Edit a.go, b.go", "x\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, title, content := fileChangeCall(c.changes)
			if kind != c.kind || title != c.title || len(content) != len(c.changes) || content[0].Text != c.firstText {
				t.Fatalf("got kind=%q title=%q content=%+v", kind, title, content)
			}
		})
	}
}

// TestUndecodableItemIsLoggedOnce: a notification that fails to decode is
// dropped, but said so — once per method and error, since a drifted codex
// repeats the same shape on every frame.
func TestUndecodableItemIsLoggedOnce(t *testing.T) {
	host := &logHost{}
	s := subagentSession(t)
	s.host = host
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn"
	bad := json.RawMessage(`{"threadId":"thread-1","turnId":"codex-turn","item":{"type":"fileChange","id":"f1","changes":"oops"}}`)
	s.handleNotification("item/completed", bad)
	s.handleNotification("item/completed", bad)
	s.handleNotification("item/started", bad)
	s.handleNotification("turn/plan/updated", json.RawMessage(`{"threadId":"thread-1","plan":"oops"}`))

	if got := drain(s); len(got) != 0 {
		t.Fatalf("undecodable items emitted %+v", got)
	}
	if len(host.lines) != 3 {
		t.Fatalf("logged %d lines, want one per method: %q", len(host.lines), host.lines)
	}
}
