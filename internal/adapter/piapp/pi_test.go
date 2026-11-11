package piapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// fakeHost records log lines and answers elicitations with a canned accept.
type fakeHost struct{}

func (fakeHost) RequestPermission(ctx context.Context, req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	return adapter.PermissionOutcome{Outcome: proto.OutcomeAllowOnce}, nil
}
func (fakeHost) Elicit(ctx context.Context, req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	return adapter.ElicitationResult{Action: "accept", Value: json.RawMessage(`{"value":"Yes"}`)}, nil
}
func (fakeHost) Logf(format string, args ...any) {}

// fakePi writes a shell script that speaks just enough of pi's RPC protocol
// for one test: it records argv and every stdin line under dir, answers
// get_state, and on prompt/abort replays the events files the test staged. An
// ack_gate file holds the prompt's response until ack_open appears, standing
// in for pi's preflight.
func fakePi(t *testing.T, dir string, promptResponse string) string {
	t.Helper()
	if promptResponse == "" {
		promptResponse = `{\"type\":\"response\",\"id\":\"$id\",\"command\":\"prompt\",\"success\":true}`
	}
	script := fmt.Sprintf(`#!/bin/sh
DIR=%q
printf '%%s\n' "$@" > "$DIR/args"
printf '%%s' "$PI_TEST_MARKER" > "$DIR/env"
while IFS= read -r line; do
  printf '%%s\n' "$line" >> "$DIR/lines"
  id=$(printf '%%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"get_state"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"harness-123\",\"thinkingLevel\":\"medium\",\"model\":{\"provider\":\"anthropic\",\"id\":\"claude-x\",\"contextWindow\":200000}}}"
      ;;
    *'"type":"prompt"'*)
      if [ -f "$DIR/ack_gate" ]; then
        # Pi's preflight: the ack waits, but the loop keeps reading stdin.
        ( while [ ! -f "$DIR/ack_open" ]; do sleep 0.02; done
          printf '%%s\n' "%s"
          [ -f "$DIR/events" ] && cat "$DIR/events" ) &
      else
        printf '%%s\n' "%s"
        [ -f "$DIR/events" ] && cat "$DIR/events"
      fi
      ;;
    *'"type":"abort"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"abort\",\"success\":true}"
      [ -f "$DIR/abort_events" ] && cat "$DIR/abort_events"
      ;;
    *'"type":"get_commands"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[{\"name\":\"skill:merge\",\"description\":\"Land a PR\",\"source\":\"skill\",\"sourceInfo\":{\"path\":\"/home/me/.agents/skills/merge/SKILL.md\",\"scope\":\"user\",\"origin\":\"top-level\"}},{\"name\":\"llama\",\"description\":\"Manage models\",\"source\":\"extension\",\"sourceInfo\":{\"path\":\"<inline>\",\"scope\":\"temporary\",\"origin\":\"top-level\"}}]}}"
      ;;
    *'"type":"set_model"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"set_model\",\"success\":true,\"data\":{\"provider\":\"openai\",\"id\":\"gpt-x\",\"contextWindow\":400000}}"
      ;;
    *'"type":"set_thinking_level"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"set_thinking_level\",\"success\":true}"
      ;;
  esac
done
`, dir, promptResponse, promptResponse)
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// drain collects emissions until pred is satisfied or the deadline passes.
func drain(t *testing.T, s adapter.Session, pred func([]proto.Emission) bool) []proto.Emission {
	t.Helper()
	var got []proto.Emission
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				return got
			}
			got = append(got, e)
			if pred(got) {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for events; got %+v", got)
		}
	}
}

func hasType(got []proto.Emission, typ string) bool {
	for _, e := range got {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func startSession(t *testing.T, dir string, o adapter.CreateOptions) adapter.Session {
	t.Helper()
	return startSessionWith(t, dir, fakeHost{}, o)
}

func startSessionWith(t *testing.T, dir string, host adapter.HostServices, o adapter.CreateOptions) adapter.Session {
	t.Helper()
	a := New(fakePi(t, dir, ""))
	s, err := a.CreateSession(context.Background(), host, o)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateSessionHandshake(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{
		ThreadID:  "sess-1",
		Model:     "anthropic/claude-x",
		Effort:    "high",
		Env:       map[string]string{"PI_TEST_MARKER": "overlay-applied"},
		SkillDirs: []string{"/home/p/.agents/skills", "/other/skills"},
		Plugins:   []string{"/data/plugin"},
	})

	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.ThreadConfigChanged) })
	cfg := got[len(got)-1].Payload.(proto.ThreadConfigChangedPayload)
	if cfg.HarnessSessionID != "harness-123" {
		t.Fatalf("harness session id = %q, want harness-123", cfg.HarnessSessionID)
	}

	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--mode\nrpc", "--session-id\nsess-1", "--model\nanthropic/claude-x", "--thinking\nhigh", "--skill\n/home/p/.agents/skills", "--skill\n/other/skills", "--skill\n/data/plugin/skills"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("argv missing %q; got:\n%s", want, args)
		}
	}
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if string(env) != "overlay-applied" {
		t.Errorf("env overlay was not applied to the pi process; got %q", env)
	}
}

func TestResumePrefersHarnessSessionID(t *testing.T) {
	dir := t.TempDir()
	startSession(t, dir, adapter.CreateOptions{
		ThreadID:         "sess-1",
		Resume:           true,
		HarnessSessionID: "harness-old",
	})
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--session-id\nharness-old") {
		t.Errorf("resume should pass the harness's own id; argv:\n%s", args)
	}
}

func TestRejectsUnknownMode(t *testing.T) {
	a := New(fakePi(t, t.TempDir(), ""))
	if _, err := a.CreateSession(context.Background(), fakeHost{}, adapter.CreateOptions{Mode: "plan"}); err == nil {
		t.Fatal("expected an unknown permission mode to be rejected")
	}
}

func TestPromptStreamsTurn(t *testing.T) {
	dir := t.TempDir()
	events := []string{
		`{"type":"agent_start"}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"hmm"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"Hello"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":" world"}}`,
		`{"type":"tool_execution_start","toolCallId":"call_1","toolName":"bash","args":{"command":"ls -la\n# extra"}}`,
		`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"bash","isError":false,"result":{"content":[{"type":"text","text":"total 48"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","usage":{"input":100,"output":20,"cacheRead":5,"cacheWrite":2,"totalTokens":127,"cost":{"total":0.04}}}}`,
		`{"type":"agent_settled"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })

	var chunks []proto.MessageChunkPayload
	var toolStart *proto.ToolCallStartedPayload
	var toolEnd *proto.ToolCallUpdatedPayload
	var usage *proto.UsageUpdatedPayload
	var finished *proto.TurnFinishedPayload
	for _, e := range got {
		switch p := e.Payload.(type) {
		case proto.MessageChunkPayload:
			chunks = append(chunks, p)
		case proto.ToolCallStartedPayload:
			toolStart = &p
		case proto.ToolCallUpdatedPayload:
			toolEnd = &p
		case proto.UsageUpdatedPayload:
			usage = &p
		case proto.TurnFinishedPayload:
			finished = &p
		}
	}

	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3 (%+v)", len(chunks), chunks)
	}
	if chunks[0].Kind != "thought" || chunks[0].Delta != "hmm" {
		t.Errorf("thinking chunk wrong: %+v", chunks[0])
	}
	if chunks[1].Kind != "text" || chunks[1].TurnID != "t1" || chunks[1].BlockID != chunks[2].BlockID {
		t.Errorf("text chunks wrong: %+v %+v", chunks[1], chunks[2])
	}
	if chunks[0].BlockID == chunks[1].BlockID {
		t.Errorf("thinking and text blocks share an id: %q", chunks[0].BlockID)
	}
	if toolStart == nil || toolStart.Kind != proto.KindExecute || toolStart.Title != "ls -la …" || toolStart.TurnID != "t1" {
		t.Errorf("tool start wrong: %+v", toolStart)
	}
	if toolEnd == nil || toolEnd.Status != proto.StatusCompleted || len(toolEnd.Content) != 1 || toolEnd.Content[0].Text != "total 48" {
		t.Errorf("tool end wrong: %+v", toolEnd)
	}
	if usage == nil || usage.Input != 100 || usage.Output != 20 || usage.Cost != 0.04 ||
		usage.ContextUsed != 127 || usage.ContextWindow != 200000 {
		t.Errorf("usage wrong: %+v", usage)
	}
	if finished == nil || finished.TurnID != "t1" || finished.StopReason != proto.StopEndTurn || finished.Failure != "" {
		t.Errorf("turn finished wrong: %+v", finished)
	}
}

// A resumed session is a fresh pi process whose message counter starts over,
// and pi can hand out the same synthetic tool call id ("toolcall:0") again.
// Neither may reuse an item id from an earlier turn, or the projection folds
// the new reply or tool output into that old item.
func TestRespawnedSessionDoesNotReuseItemIDs(t *testing.T) {
	events := []string{
		`{"type":"agent_start"}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"reply"}}`,
		`{"type":"tool_execution_start","toolCallId":"toolcall:0","toolName":"bash","args":{"command":"ls"}}`,
		`{"type":"tool_execution_end","toolCallId":"toolcall:0","toolName":"bash","isError":false,"result":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","usage":{}}}`,
		`{"type":"agent_settled"}`,
	}
	type ids struct{ block, toolStart, toolEnd string }
	runTurn := func(turnID string) ids {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "events"), []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1", Resume: true, HarnessSessionID: "harness-123"})
		if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: turnID, Text: "hi"}); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		var got ids
		for _, e := range drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) }) {
			switch p := e.Payload.(type) {
			case proto.MessageChunkPayload:
				got.block = p.BlockID
			case proto.ToolCallStartedPayload:
				got.toolStart = p.ToolCallID
			case proto.ToolCallUpdatedPayload:
				got.toolEnd = p.ToolCallID
			}
		}
		if got.block == "" || got.toolStart == "" {
			t.Fatalf("turn %s missing a chunk or tool call: %+v", turnID, got)
		}
		// Start and end must name the same item, or the result lands in a
		// separate, title-less row.
		if got.toolStart != got.toolEnd {
			t.Fatalf("tool start id %q and end id %q differ", got.toolStart, got.toolEnd)
		}
		return got
	}

	before, after := runTurn("t1"), runTurn("t2")
	if before.block == after.block {
		t.Errorf("respawned process reused block id %q from an earlier turn", before.block)
	}
	if before.toolStart == after.toolStart {
		t.Errorf("respawned process reused tool item id %q from an earlier turn", before.toolStart)
	}
}

func TestCancelAbortsTurn(t *testing.T) {
	dir := t.TempDir()
	// The prompt starts work but never settles; abort answers with an aborted
	// message and the settle.
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte(`{"type":"agent_start"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	abortEvents := []string{
		`{"type":"message_end","message":{"role":"assistant","stopReason":"aborted","usage":{"input":1,"output":1,"cost":{"total":0}}}}`,
		`{"type":"agent_settled"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "abort_events"), []byte(strings.Join(abortEvents, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "go"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	final := got[len(got)-1].Payload.(proto.TurnFinishedPayload)
	if final.TurnID != "t1" || final.StopReason != proto.StopCancelled {
		t.Fatalf("cancelled turn wrong: %+v", final)
	}

	lines, _ := os.ReadFile(filepath.Join(dir, "lines"))
	if !strings.Contains(string(lines), `"type":"abort"`) {
		t.Errorf("abort command was never sent; pi saw:\n%s", lines)
	}
}

func TestCancelWhenIdleIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel while idle: %v", err)
	}
	lines, _ := os.ReadFile(filepath.Join(dir, "lines"))
	if strings.Contains(string(lines), `"type":"abort"`) {
		t.Error("an idle cancel must not send abort")
	}
}

func TestPromptAuthRefusal(t *testing.T) {
	dir := t.TempDir()
	refusal := `{\"type\":\"response\",\"id\":\"$id\",\"command\":\"prompt\",\"success\":false,\"error\":\"No API key found for anthropic. Run /login or set ANTHROPIC_API_KEY\"}`
	a := New(fakePi(t, dir, refusal))
	s, err := a.CreateSession(context.Background(), fakeHost{}, adapter.CreateOptions{ThreadID: "sess-1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer s.Close()

	// The refusal arrives after Prompt has returned, so it closes the turn
	// itself, carrying the failure kind so a login is offered.
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	final := got[len(got)-1].Payload.(proto.TurnFinishedPayload)
	if final.TurnID != "t1" || final.StopReason != proto.StopError || final.Failure != proto.FailureAuth {
		t.Fatalf("refused turn wrong: %+v", final)
	}
	if !strings.Contains(final.Error, "No API key found") {
		t.Errorf("refusal message lost: %q", final.Error)
	}
	// The refusal must clear the turn so the next prompt is not rejected by
	// the collision guard.
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t2", Text: "again"}); err != nil {
		t.Fatalf("session left busy after a refused prompt: %v", err)
	}
}

// Pi acks a prompt only after its preflight, which can run a whole compaction
// or wait on an extension's dialog. Prompt must return without that ack, and
// the session must still take a Stop while it is outstanding.
func TestPromptReturnsBeforeAck(t *testing.T) {
	dir := t.TempDir()
	events := []string{
		`{"type":"agent_start"}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"late"}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","usage":{}}}`,
		`{"type":"agent_settled"}`,
	}
	for name, body := range map[string]string{
		"events":   strings.Join(events, "\n") + "\n",
		"ack_gate": "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})

	returned := make(chan error, 1)
	go func() { returned <- s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Prompt: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt waited for pi's ack")
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel while the ack is outstanding: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "ack_open"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	var text string
	for _, e := range got {
		if p, ok := e.Payload.(proto.MessageChunkPayload); ok && p.TurnID == "t1" {
			text += p.Delta
		}
	}
	final := got[len(got)-1].Payload.(proto.TurnFinishedPayload)
	if text != "late" || final.TurnID != "t1" || final.StopReason != proto.StopEndTurn {
		t.Fatalf("turn after a delayed ack wrong: text=%q final=%+v", text, final)
	}
}

// A refusal only closes the turn it was sent for. One that lands after that
// turn settled, or after pi opened a turn of its own, must leave both alone.
func TestLatePromptRefusal(t *testing.T) {
	refusal := rpcResponse{Command: "prompt", Success: false, Error: "No API key found for anthropic"}
	cases := []struct {
		name, current string
		wantTurn      string
		wantFinished  bool
	}{
		{"still ours", "t1", "", true},
		{"already settled", "", "", false},
		{"relabelled", "harness-turn", "harness-turn", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &session{
				host:    fakeHost{},
				events:  make(chan proto.Emission, 8),
				done:    make(chan struct{}),
				rpcDone: make(chan struct{}),
				pending: map[string]chan rpcResponse{},
			}
			s.turnID = c.current
			ack := make(chan rpcResponse, 1)
			ack <- refusal
			released := false
			s.awaitPromptAck("t1", ack, func() { released = true })

			if !released {
				t.Error("pending entry not released")
			}
			if s.turnID != c.wantTurn {
				t.Errorf("turn id = %q, want %q", s.turnID, c.wantTurn)
			}
			select {
			case e := <-s.events:
				p := e.Payload.(proto.TurnFinishedPayload)
				if !c.wantFinished {
					t.Fatalf("late refusal emitted %+v", p)
				}
				if p.TurnID != "t1" || p.StopReason != proto.StopError || p.Failure != proto.FailureAuth {
					t.Errorf("refusal finish wrong: %+v", p)
				}
			default:
				if c.wantFinished {
					t.Fatal("refusal of the open turn emitted nothing")
				}
			}
		})
	}
}

func TestSetModelAndEffort(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})

	ms, ok := s.(adapter.ModelSwitcher)
	if !ok {
		t.Fatal("session must implement ModelSwitcher")
	}
	if err := ms.SetModel(context.Background(), "openai/gpt-x"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := ms.SetModel(context.Background(), "no-slash"); err == nil {
		t.Error("a model id without provider/ must be rejected")
	}

	es, ok := s.(adapter.EffortSwitcher)
	if !ok {
		t.Fatal("session must implement EffortSwitcher")
	}
	if err := es.SetEffort(context.Background(), "xhigh"); err != nil {
		t.Fatalf("SetEffort: %v", err)
	}

	lines, _ := os.ReadFile(filepath.Join(dir, "lines"))
	if !strings.Contains(string(lines), `"modelId":"gpt-x"`) || !strings.Contains(string(lines), `"provider":"openai"`) {
		t.Errorf("set_model split wrong; pi saw:\n%s", lines)
	}
	if !strings.Contains(string(lines), `"level":"xhigh"`) {
		t.Errorf("set_thinking_level not sent; pi saw:\n%s", lines)
	}
}

func TestHarnessInitiatedTurnAndCompaction(t *testing.T) {
	// Events arriving while idle (an overflow retry) must open their own turn,
	// and a compaction boundary must be recorded with its token counts.
	s := &session{
		host:    fakeHost{},
		events:  make(chan proto.Emission, 64),
		done:    make(chan struct{}),
		rpcDone: make(chan struct{}),
		pending: map[string]chan rpcResponse{},
	}
	s.handleEvent("agent_start", json.RawMessage(`{"type":"agent_start"}`))
	s.handleEvent("compaction_end", json.RawMessage(
		`{"type":"compaction_end","reason":"overflow","aborted":false,"result":{"tokensBefore":150000,"estimatedTokensAfter":32000}}`))
	s.handleEvent("compaction_end", json.RawMessage(
		`{"type":"compaction_end","reason":"manual","aborted":true,"result":null}`))
	s.handleEvent("agent_settled", nil)

	close(s.done)
	var started, finished, compacted int
	var compaction proto.ContextCompactedPayload
	for {
		select {
		case e := <-s.events:
			switch p := e.Payload.(type) {
			case proto.TurnStartedPayload:
				started++
			case proto.TurnFinishedPayload:
				finished++
			case proto.ContextCompactedPayload:
				compacted++
				compaction = p
			}
			continue
		default:
		}
		break
	}
	if started != 1 || finished != 1 {
		t.Fatalf("harness-initiated turn: started=%d finished=%d, want 1/1", started, finished)
	}
	if compacted != 1 || compaction.Trigger != "auto" || compaction.PreTokens != 150000 || compaction.PostTokens != 32000 {
		t.Fatalf("compaction wrong (count=%d): %+v", compacted, compaction)
	}
}

func TestFinishTurnStopMapping(t *testing.T) {
	cases := []struct {
		stop, errMsg string
		aborted      string // the turn Cancel aborted
		wantReason   string
		wantFailure  string
	}{
		{"stop", "", "", proto.StopEndTurn, ""},
		{"toolUse", "", "", proto.StopEndTurn, ""},
		{"aborted", "", "", proto.StopCancelled, ""},
		{"length", "", "", proto.StopMaxTokens, ""},
		{"error", "boom", "", proto.StopError, ""},
		{"error", `No API key found for "anthropic"`, "", proto.StopError, proto.FailureAuth},
		{"error", "Provider is not configured: openrouter", "", proto.StopError, proto.FailureAuth},
		// Stopped mid-tool, pi calls the run an error; the user stopped it.
		{"error", "This operation was aborted", "t1", proto.StopCancelled, ""},
		{"stop", "", "t1", proto.StopEndTurn, ""},
		{"error", "boom", "t0", proto.StopError, ""},
	}
	for _, c := range cases {
		s := &session{events: make(chan proto.Emission, 8), done: make(chan struct{})}
		s.turnID, s.lastStop, s.lastErr, s.aborted = "t1", c.stop, c.errMsg, c.aborted
		s.finishTurn()
		e := <-s.events
		p := e.Payload.(proto.TurnFinishedPayload)
		if p.StopReason != c.wantReason || p.Failure != c.wantFailure {
			t.Errorf("stop %q/%q: got %+v", c.stop, c.errMsg, p)
		}
	}
}

func TestToolKind(t *testing.T) {
	cases := map[string]string{
		"read": proto.KindRead, "edit": proto.KindEdit, "write": proto.KindEdit,
		"bash": proto.KindExecute, "grep": proto.KindSearch, "custom_ext": proto.KindOther,
	}
	for name, want := range cases {
		if got := toolKind(name); got != want {
			t.Errorf("toolKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestProbe(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 0.84.4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentHome := t.TempDir()
	authJSON := `{"anthropic":{"type":"oauth","refresh":"SECRET"},"openrouter":{"type":"api_key","key":"SECRET"}}`
	if err := os.WriteFile(filepath.Join(agentHome, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(bin)
	got := a.Probe(context.Background(), map[string]string{"PI_CODING_AGENT_DIR": agentHome})
	if !got.OK() {
		t.Fatalf("probe not ready: %+v", got)
	}
	if got.Facts[adapter.FactVersion] != "0.84.4" {
		t.Errorf("version = %q", got.Facts[adapter.FactVersion])
	}
	if got.Facts["auth"] != "anthropic (oauth), openrouter (api_key)" {
		t.Errorf("auth fact = %q", got.Facts["auth"])
	}
	if strings.Contains(fmt.Sprint(got.Facts), "SECRET") {
		t.Error("probe facts leaked credential material")
	}

	if avail := New(filepath.Join(dir, "missing")).Probe(context.Background(), nil); avail.OK() {
		t.Error("a missing binary must probe unavailable")
	}
}

// blockingHost holds every elicitation open until its ctx ends, as a human who
// never answers would, and reports how each one was released.
type blockingHost struct {
	fakeHost
	asked    chan struct{}
	released chan error
}

func newBlockingHost() *blockingHost {
	return &blockingHost{asked: make(chan struct{}, 4), released: make(chan error, 4)}
}

func (h *blockingHost) Elicit(ctx context.Context, req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	h.asked <- struct{}{}
	<-ctx.Done()
	h.released <- ctx.Err()
	return adapter.ElicitationResult{Action: "cancel"}, ctx.Err()
}

// startDialog opens a turn whose only event is the given extension dialog and
// waits until the host is holding it.
func startDialog(t *testing.T, dir string, h *blockingHost, request string) adapter.Session {
	t.Helper()
	events := `{"type":"agent_start"}` + "\n" + request + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startSessionWith(t, dir, h, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "go"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	select {
	case <-h.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the dialog never reached the host")
	}
	return s
}

func waitReleased(t *testing.T, h *blockingHost) error {
	t.Helper()
	select {
	case err := <-h.released:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the dialog was never released")
		return nil
	}
}

func TestDialogTimeoutWithdrawsElicitation(t *testing.T) {
	// Pi resolves a timed-out dialog to its default without a word, so the
	// card must expire on the same clock.
	dir := t.TempDir()
	h := newBlockingHost()
	startDialog(t, dir, h,
		`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"Proceed?","message":"","timeout":50}`)
	if err := waitReleased(t, h); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("released with %v, want deadline exceeded", err)
	}
}

func TestCancelWithdrawsOpenDialog(t *testing.T) {
	dir := t.TempDir()
	h := newBlockingHost()
	s := startDialog(t, dir, h,
		`{"type":"extension_ui_request","id":"d1","method":"input","title":"Name?"}`)
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := waitReleased(t, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("released with %v, want canceled", err)
	}

	// Pi must hear the dialog was cancelled, or the extension stays wedged.
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(dir, "lines"))
		for _, line := range strings.Split(string(raw), "\n") {
			var r struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				Cancelled bool   `json:"cancelled"`
			}
			if json.Unmarshal([]byte(line), &r) == nil && r.Type == "extension_ui_response" && r.ID == "d1" && r.Cancelled {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pi never got a cancelled response for d1; saw:\n%s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseWithdrawsOpenDialog(t *testing.T) {
	dir := t.TempDir()
	h := newBlockingHost()
	s := startDialog(t, dir, h,
		`{"type":"extension_ui_request","id":"d1","method":"select","title":"Pick","options":["a","b"]}`)
	_ = s.Close()
	if err := waitReleased(t, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("released with %v, want canceled", err)
	}
}

// A dialog pi raises as Stop is pressed lands after Cancel has swept, and
// the turn then settles. Its turn ending must withdraw it, or the card stays
// up with nothing left to take it down.
func TestTurnEndWithdrawsDialogRaisedDuringStop(t *testing.T) {
	dir := t.TempDir()
	h := newBlockingHost()
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte(`{"type":"agent_start"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	abort := `{"type":"extension_ui_request","id":"d1","method":"input","title":"Name?"}` + "\n" + `{"type":"agent_settled"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "abort_events"), []byte(abort), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startSessionWith(t, dir, h, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "go"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := waitReleased(t, h); !errors.Is(err, context.Canceled) {
		t.Fatalf("released with %v, want canceled", err)
	}
}

// pi's agent_end repeats every message of the run, screenshots and all, and
// can run past any line limit. Skipping it must leave the session reading, so
// the turn still settles.
func TestOversizedEventDoesNotEndSession(t *testing.T) {
	dir := t.TempDir()
	huge := `{"type":"agent_end","messages":"` + strings.Repeat("a", maxLine+1) + `"}`
	events := strings.Join([]string{
		`{"type":"agent_start"}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"done"}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","usage":{}}}`,
		huge,
		`{"type":"agent_settled"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}

	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1"})
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	final := got[len(got)-1].Payload.(proto.TurnFinishedPayload)
	if final.TurnID != "t1" || final.StopReason != proto.StopEndTurn || final.Error != "" {
		t.Fatalf("turn after an oversized event: %+v", final)
	}
}

// dyingPi is a pi that answers the handshake, then on prompt runs onPrompt
// and dies of a heap overflow.
func dyingPi(t *testing.T, onPrompt string) adapter.Session {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in
    *'"type":"get_state"'*)
      printf '%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"h\",\"model\":{\"provider\":\"anthropic\",\"id\":\"claude-x\",\"contextWindow\":200000}}}"
      ;;
    *'"type":"prompt"'*)
      ` + onPrompt + `
      echo "FATAL ERROR: Reached heap limit" >&2
      echo "    at Allocation failed - JavaScript heap out of memory" >&2
      exit 134
      ;;
  esac
done
`
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := New(bin).CreateSession(context.Background(), fakeHost{}, adapter.CreateOptions{ThreadID: "sess-1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

const heapDeath = "pi exited (exit status 134)\nFATAL ERROR: Reached heap limit\n    at Allocation failed - JavaScript heap out of memory"

// A pi that dies mid-turn must finish the turn with why: its exit status and
// what it said on stderr on the way out, indentation and all.
func TestHarnessDeathExplainsTurn(t *testing.T) {
	s := dyingPi(t, `printf '%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"prompt\",\"success\":true}"
      printf '%s\n' '{"type":"agent_start"}'`)

	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	final := got[len(got)-1].Payload.(proto.TurnFinishedPayload)
	if final.TurnID != "t1" || final.StopReason != proto.StopError || final.Error != heapDeath {
		t.Fatalf("turn after pi died: %+v\nwant error:\n%s", final, heapDeath)
	}
}

// A pi that dies before acknowledging the prompt leaves the ack unanswered;
// the turn it opened must still finish with why.
func TestHarnessDeathBeforePromptAckExplainsTurn(t *testing.T) {
	s := dyingPi(t, `:`)
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	final := got[len(got)-1].Payload.(proto.TurnFinishedPayload)
	if final.TurnID != "t1" || final.StopReason != proto.StopError || final.Error != heapDeath {
		t.Fatalf("turn after pi died: %+v\nwant error:\n%s", final, heapDeath)
	}
}
