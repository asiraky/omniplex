package claudecode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// permissionResult is the whole answer handleRequest gives the SDK, including
// the part that makes "always" mean always.
type permissionResult struct {
	Behavior           string           `json:"behavior"`
	UpdatedPermissions []map[string]any `json:"updatedPermissions"`
}

func decodeFull(t *testing.T, v any) permissionResult {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got permissionResult
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func permissionParams(t *testing.T, tool, suggestions string) json.RawMessage {
	t.Helper()
	frame := map[string]any{"toolName": tool, "input": map[string]any{"command": "ls"}}
	if suggestions != "" {
		frame["suggestions"] = json.RawMessage(suggestions)
	}
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The reported bug: answering "Always allow" asked again on the very next call,
// because the SDK's suggestions — the rules that make it stop asking — were
// dropped. This covers the three answers and both shapes of always.
func TestAlwaysAllowIsRecorded(t *testing.T) {
	ctx := context.Background()
	const suggestions = `[
		{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"ls:*"}],"behavior":"allow","destination":"session"},
		{"type":"setMode","mode":"acceptEdits","destination":"userSettings"}
	]`

	t.Run("always with suggestions hands them back, rewritten", func(t *testing.T) {
		asked := 0
		s := &session{host: &fakeHost{permission: func(adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			asked++
			return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowAlways}, nil
		}}, alwaysAllow: map[string]bool{}, events: make(chan proto.Emission, 8)}

		res, err := s.handleRequest(ctx, "permission", permissionParams(t, "Bash", suggestions))
		if err != nil {
			t.Fatal(err)
		}
		got := decodeFull(t, res)
		if got.Behavior != "allow" || len(got.UpdatedPermissions) != 2 {
			t.Fatalf("result = %+v", got)
		}
		// A rule goes to the workspace's settings so the next session in this
		// worktree does not ask again; a mode change stays in the session,
		// because writing one to disk reconfigures the project behind the
		// user's back.
		if got.UpdatedPermissions[0]["destination"] != "localSettings" {
			t.Fatalf("rule destination = %v", got.UpdatedPermissions[0]["destination"])
		}
		if got.UpdatedPermissions[1]["destination"] != "session" {
			t.Fatalf("setMode destination = %v", got.UpdatedPermissions[1]["destination"])
		}
		if asked != 1 {
			t.Fatalf("asked %d times", asked)
		}
	})

	// The real SDK answers "always allow this edit" with a single setMode
	// suggestion: acceptEdits. Applying it without saying so would leave the
	// mode chip reading Manual while every edit sails through.
	t.Run("a mode the suggestion switches to is announced", func(t *testing.T) {
		s := &session{host: &fakeHost{permission: func(adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowAlways}, nil
		}}, alwaysAllow: map[string]bool{}, events: make(chan proto.Emission, 8), mode: "default"}

		if _, err := s.handleRequest(ctx, "permission", permissionParams(t, "Write",
			`[{"type":"setMode","mode":"acceptEdits","destination":"session"}]`)); err != nil {
			t.Fatal(err)
		}
		if s.mode != "acceptEdits" {
			t.Fatalf("session mode = %q, want acceptEdits", s.mode)
		}
		select {
		case e := <-s.events:
			if e.Type != proto.SessionConfigChanged {
				t.Fatalf("emitted %q", e.Type)
			}
			payload, ok := e.Payload.(proto.SessionConfigChangedPayload)
			if !ok {
				t.Fatalf("payload = %T", e.Payload)
			}
			if payload.Mode != "acceptEdits" {
				t.Fatalf("payload mode = %q", payload.Mode)
			}
		default:
			t.Fatal("the mode change was applied silently")
		}
	})

	t.Run("always without suggestions is remembered here", func(t *testing.T) {
		asked := 0
		s := &session{host: &fakeHost{permission: func(adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			asked++
			return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowAlways}, nil
		}}, alwaysAllow: map[string]bool{}}

		for range 2 {
			res, err := s.handleRequest(ctx, "permission", permissionParams(t, "Grep", ""))
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeFull(t, res); got.Behavior != "allow" || len(got.UpdatedPermissions) != 0 {
				t.Fatalf("result = %+v", got)
			}
		}
		if asked != 1 {
			t.Fatalf("the human was asked %d times; always must only ask once", asked)
		}
	})

	t.Run("once records nothing and asks again", func(t *testing.T) {
		asked := 0
		s := &session{host: &fakeHost{permission: func(adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			asked++
			return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowOnce}, nil
		}}, alwaysAllow: map[string]bool{}}

		for range 2 {
			res, err := s.handleRequest(ctx, "permission", permissionParams(t, "Bash", suggestions))
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeFull(t, res); got.Behavior != "allow" || len(got.UpdatedPermissions) != 0 {
				t.Fatalf("allow_once must not record a rule: %+v", got)
			}
		}
		if asked != 2 {
			t.Fatalf("asked %d times, want 2", asked)
		}
	})

	t.Run("deny stays a deny", func(t *testing.T) {
		s := &session{host: &fakeHost{permission: func(adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
			return adapter.PermissionOutcome{Outcome: proto.OutcomeRejectOnce}, nil
		}}, alwaysAllow: map[string]bool{}}
		res, err := s.handleRequest(ctx, "permission", permissionParams(t, "Bash", suggestions))
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeFull(t, res); got.Behavior != "deny" {
			t.Fatalf("result = %+v", got)
		}
	})
}
