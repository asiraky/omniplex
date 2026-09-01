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
// get_state, and on prompt/abort replays the events files the test staged.
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
      printf '%%s\n' "%s"
      [ -f "$DIR/events" ] && cat "$DIR/events"
      ;;
    *'"type":"abort"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"abort\",\"success\":true}"
      [ -f "$DIR/abort_events" ] && cat "$DIR/abort_events"
      ;;
    *'"type":"set_model"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"set_model\",\"success\":true,\"data\":{\"provider\":\"openai\",\"id\":\"gpt-x\",\"contextWindow\":400000}}"
      ;;
    *'"type":"set_thinking_level"'*)
      printf '%%s\n' "{\"type\":\"response\",\"id\":\"$id\",\"command\":\"set_thinking_level\",\"success\":true}"
      ;;
  esac
done
`, dir, promptResponse)
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
	a := New(fakePi(t, dir, ""))
	s, err := a.CreateSession(context.Background(), fakeHost{}, o)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateSessionHandshake(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{
		SessionID: "sess-1",
		Model:     "anthropic/claude-x",
		Effort:    "high",
		Env:       map[string]string{"PI_TEST_MARKER": "overlay-applied"},
	})

	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.SessionConfigChanged) })
	cfg := got[len(got)-1].Payload.(proto.SessionConfigChangedPayload)
	if cfg.HarnessSessionID != "harness-123" {
		t.Fatalf("harness session id = %q, want harness-123", cfg.HarnessSessionID)
	}

	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--mode\nrpc", "--session-id\nsess-1", "--model\nanthropic/claude-x", "--thinking\nhigh"} {
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
		SessionID:        "sess-1",
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

	s := startSession(t, dir, adapter.CreateOptions{SessionID: "sess-1"})
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

	s := startSession(t, dir, adapter.CreateOptions{SessionID: "sess-1"})
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
	s := startSession(t, dir, adapter.CreateOptions{SessionID: "sess-1"})
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
	s, err := a.CreateSession(context.Background(), fakeHost{}, adapter.CreateOptions{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer s.Close()

	err = s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"})
	if err == nil {
		t.Fatal("expected the prompt to be refused")
	}
	if adapter.FailureOf(err) != proto.FailureAuth {
		t.Fatalf("failure kind = %q, want %q (err: %v)", adapter.FailureOf(err), proto.FailureAuth, err)
	}
	// The refusal must clear the turn so the next prompt is not rejected by
	// the collision guard.
	var fe *adapter.FailureError
	if !errors.As(err, &fe) {
		t.Fatalf("expected FailureError, got %T", err)
	}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("session left busy after a refused prompt: %v", err)
	}
}

func TestSetModelAndEffort(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{SessionID: "sess-1"})

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
		wantReason   string
		wantFailure  string
	}{
		{"stop", "", proto.StopEndTurn, ""},
		{"toolUse", "", proto.StopEndTurn, ""},
		{"aborted", "", proto.StopCancelled, ""},
		{"length", "", proto.StopMaxTokens, ""},
		{"error", "boom", proto.StopError, ""},
		{"error", `No API key found for "anthropic"`, proto.StopError, proto.FailureAuth},
		{"error", "Provider is not configured: openrouter", proto.StopError, proto.FailureAuth},
	}
	for _, c := range cases {
		s := &session{events: make(chan proto.Emission, 8), done: make(chan struct{})}
		s.turnID, s.lastStop, s.lastErr = "t1", c.stop, c.errMsg
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
	if got.Facts["piVer"] != "0.84.4" {
		t.Errorf("piVer = %q", got.Facts["piVer"])
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
