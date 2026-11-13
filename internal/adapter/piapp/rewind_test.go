package piapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// settledRun is a prompt's run as pi streams it, ending at the turn boundary.
var settledRun = []string{
	`{"type":"agent_start"}`,
	`{"type":"message_start","message":{"role":"assistant"}}`,
	`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","usage":{}}}`,
	`{"type":"agent_settled"}`,
}

func stage(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func finishedOf(t *testing.T, got []proto.Emission) proto.TurnFinishedPayload {
	t.Helper()
	for _, e := range got {
		if p, ok := e.Payload.(proto.TurnFinishedPayload); ok {
			return p
		}
	}
	t.Fatalf("no turn.finished in %+v", got)
	return proto.TurnFinishedPayload{}
}

func setRefTimeout(s adapter.Session, d time.Duration) {
	ss := s.(*session)
	ss.mu.Lock()
	ss.refTimeout = d
	ss.mu.Unlock()
}

// sentLines is every command the fake pi read, decoded.
func sentLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "lines"))
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestTurnFinishedCarriesPromptEntry(t *testing.T) {
	dir := t.TempDir()
	stage(t, dir, map[string]string{
		"events":        strings.Join(settledRun, "\n") + "\n",
		"fork_messages": `[{"entryId":"e1","text":"hi"}]`,
		// The prompt adds its own entry, and a steer lands after it. An
		// earlier prompt with the same text is not this turn's.
		"fork_messages_next": `[{"entryId":"e1","text":"hi"},{"entryId":"e2","text":"hi"},{"entryId":"e3","text":"also this"}]`,
	})
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	fin := finishedOf(t, drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) }))
	if fin.TurnID != "t1" || fin.HarnessRef != "e2" {
		t.Fatalf("turn finished = %+v, want t1 anchored at e2", fin)
	}
}

func TestMatchPrompt(t *testing.T) {
	msgs := func(pairs ...string) []forkMessage {
		var out []forkMessage
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, forkMessage{EntryID: pairs[i], Text: pairs[i+1]})
		}
		return out
	}
	cases := []struct {
		name   string
		after  []forkMessage
		before map[string]bool
		text   string
		want   string
	}{
		{"exact among new", msgs("e1", "hi", "e2", "hi"), map[string]bool{"e1": true}, "hi", "e2"},
		{"expanded by pi", msgs("e1", "old", "e2", "<skill name=\"merge\">…</skill>\n\ngo", "e3", "steer"), map[string]bool{"e1": true}, "/skill:merge go", "e2"},
		{"nothing new", msgs("e1", "hi"), map[string]bool{"e1": true}, "hi", ""},
		{"no listing before, exact", msgs("e1", "hi", "e2", "other"), nil, "hi", "e1"},
		{"no listing before, last exact", msgs("e1", "hi", "e2", "hi"), nil, "hi", "e2"},
		{"no listing before, rewritten", msgs("e1", "<skill>"), nil, "/skill:x", ""},
	}
	for _, c := range cases {
		if got := matchPrompt(c.after, c.before, c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A lookup pi refuses or never answers still finishes the turn, unanchored.
func TestTurnFinishesWithoutRefWhenLookupFails(t *testing.T) {
	for _, file := range []string{"fork_messages_fail", "fork_messages_hang"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			stage(t, dir, map[string]string{
				"events": strings.Join(settledRun, "\n") + "\n",
				file:     "",
			})
			s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
			setRefTimeout(s, 200*time.Millisecond)
			if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			fin := finishedOf(t, drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) }))
			if fin.TurnID != "t1" || fin.StopReason != proto.StopEndTurn || fin.HarnessRef != "" {
				t.Fatalf("turn finished = %+v, want t1 ended with no anchor", fin)
			}
		})
	}
}

// Pi starting work of its own while the last turn's anchor is still being
// read must not open the new turn before the old one has finished.
func TestHeldFinishGoesOutBeforeNextTurnStarts(t *testing.T) {
	dir := t.TempDir()
	stage(t, dir, map[string]string{
		"events":             strings.Join(append(append([]string{}, settledRun...), `{"type":"agent_start"}`), "\n") + "\n",
		"fork_messages_hang": "",
	})
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	setRefTimeout(s, time.Minute)
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnStarted) })
	var order []string
	for _, e := range got {
		switch p := e.Payload.(type) {
		case proto.TurnFinishedPayload:
			order = append(order, "finished:"+p.TurnID)
		case proto.TurnStartedPayload:
			order = append(order, "started")
		}
	}
	if len(order) != 2 || order[0] != "finished:t1" || order[1] != "started" {
		t.Fatalf("order = %v, want t1 finished before the next turn started", order)
	}
}

func TestRewindForksAndReportsNewSession(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.ThreadConfigChanged) })

	if err := s.(adapter.Rewinder).Rewind(context.Background(), adapter.RewindTarget{Turn: "e2", Previous: "e1"}); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.ThreadConfigChanged) })
	cfg := got[len(got)-1].Payload.(proto.ThreadConfigChangedPayload)
	if cfg.HarnessSessionID != "harness-forked" {
		t.Fatalf("harness session id after rewind = %q, want harness-forked", cfg.HarnessSessionID)
	}

	var forks []map[string]any
	for _, l := range sentLines(t, dir) {
		if l["type"] == "fork" {
			forks = append(forks, l)
		}
	}
	if len(forks) != 1 || forks[0]["entryId"] != "e2" {
		t.Fatalf("fork commands = %v, want one at e2", forks)
	}
	// The forked runtime runs what the session ran: nothing to put back.
	for _, l := range sentLines(t, dir) {
		if l["type"] == "set_model" || l["type"] == "set_thinking_level" {
			t.Errorf("unexpected %v after a fork that kept the settings", l["type"])
		}
	}
}

func TestRewindRefused(t *testing.T) {
	dir := t.TempDir()
	stage(t, dir, map[string]string{"fork_refuse": ""})
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	err := s.(adapter.Rewinder).Rewind(context.Background(), adapter.RewindTarget{Turn: "e2"})
	if err == nil || !strings.Contains(err.Error(), "Invalid entry ID") {
		t.Fatalf("Rewind error = %v, want pi's refusal", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "session_id")); err == nil {
		t.Fatal("fake pi forked despite refusing")
	}
}

// A fork comes up on the CLI's model and the kept history's, not the one the
// session switched to since; Rewind switches back.
func TestRewindRestoresModelAndEffort(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	ctx := context.Background()
	if err := s.(adapter.ModelSwitcher).SetModel(ctx, "openai/gpt-x"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := s.(adapter.EffortSwitcher).SetEffort(ctx, "high"); err != nil {
		t.Fatalf("SetEffort: %v", err)
	}
	if err := s.(adapter.Rewinder).Rewind(ctx, adapter.RewindTarget{Turn: "e2"}); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	var afterFork []string
	forked := false
	for _, l := range sentLines(t, dir) {
		if l["type"] == "fork" {
			forked = true
			continue
		}
		if !forked {
			continue
		}
		switch l["type"] {
		case "set_model":
			afterFork = append(afterFork, "model:"+l["provider"].(string)+"/"+l["modelId"].(string))
		case "set_thinking_level":
			afterFork = append(afterFork, "effort:"+l["level"].(string))
		}
	}
	if len(afterFork) != 2 || afterFork[0] != "model:openai/gpt-x" || afterFork[1] != "effort:high" {
		t.Fatalf("after the fork pi was sent %v, want the model then the effort put back", afterFork)
	}
}
