package codexapp

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

func TestCodexSkillOrigin(t *testing.T) {
	tests := []struct{ scope, path, want string }{
		{"user", "/home/me/.codex/skills/review/SKILL.md", "personal"},
		{"repo", "/work/repo/.agents/skills/review/SKILL.md", "repo"},
		{"system", "/opt/codex/skills/review/SKILL.md", "system"},
		{"user", "/home/me/.codex/plugins/acme/skills/review/SKILL.md", "plugin"},
		{"future", "/somewhere/review/SKILL.md", "other"},
	}
	for _, tt := range tests {
		if got := codexSkillOrigin(tt.scope, tt.path); got != tt.want {
			t.Errorf("origin(%q, %q) = %q, want %q", tt.scope, tt.path, got, tt.want)
		}
	}
}

func TestCodexBuiltinComposerItemsHaveRealHandlers(t *testing.T) {
	items := codexBuiltinComposerItems()
	want := map[string]struct{ behavior, action string }{
		"/status":  {adapter.ComposerClientAction, "status"},
		"/diff":    {adapter.ComposerClientAction, "diff"},
		"/compact": {adapter.ComposerAdapterAction, "compact"},
		"/review":  {adapter.ComposerAdapterAction, "review"},
	}
	if len(items) != len(want) {
		t.Fatalf("built-ins = %d, want %d: %+v", len(items), len(want), items)
	}
	for _, item := range items {
		entry, ok := want[item.InsertText]
		if !ok {
			t.Errorf("unexpected advertised command %q", item.InsertText)
			continue
		}
		if item.Behavior != entry.behavior || item.Action != entry.action {
			t.Errorf("%s = behavior %q action %q, want %q %q", item.InsertText, item.Behavior, item.Action, entry.behavior, entry.action)
		}
	}
}

func TestCodexComposerActionRequests(t *testing.T) {
	method, params, err := codexComposerActionRequest("thread-1", "compact", "")
	if err != nil || method != "thread/compact/start" || params["threadId"] != "thread-1" {
		t.Fatalf("compact request = %q %+v err=%v", method, params, err)
	}

	method, params, err = codexComposerActionRequest("thread-1", "review", "focus on races")
	if err != nil || method != "review/start" {
		t.Fatalf("review request = %q %+v err=%v", method, params, err)
	}
	target := params["target"].(map[string]any)
	if target["type"] != "custom" || target["instructions"] != "focus on races" {
		t.Errorf("review target = %+v", target)
	}

	_, _, err = codexComposerActionRequest("thread-1", "compact", "surprise")
	if err == nil {
		t.Fatal("/compact accepted arguments it cannot use")
	}
	_, _, err = codexComposerActionRequest("thread-1", "imaginary", "")
	if err == nil {
		t.Fatal("an unadvertised action reached app-server")
	}
}

func TestContextCompactionCarriesManualOrigin(t *testing.T) {
	s := &session{events: make(chan proto.Emission, 1), streamed: map[string]bool{}, manualCompact: true}
	params, _ := json.Marshal(map[string]any{
		"turnId": "codex-turn",
		"item":   map[string]any{"id": "compact-1", "type": "contextCompaction"},
	})
	s.handleItem(true, "", "", params)

	em := <-s.events
	if em.Type != proto.ContextCompacted {
		t.Fatalf("event = %s, want %s", em.Type, proto.ContextCompacted)
	}
	payload := em.Payload.(proto.ContextCompactedPayload)
	if payload.Trigger != "manual" || s.manualCompact {
		t.Errorf("payload = %+v, manual flag = %v", payload, s.manualCompact)
	}
	s.handleNotification("thread/compacted", json.RawMessage(`{"threadId":"thread-1","turnId":"codex-turn"}`))
	select {
	case duplicate := <-s.events:
		t.Fatalf("transitional duplicate was emitted: %+v", duplicate)
	default:
	}
}
