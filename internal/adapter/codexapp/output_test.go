package codexapp

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// The frames below are a codex app-server recording of one foreground command,
// verbatim but for the item/started and item/completed envelopes, which carry
// the fields handleItem reads.
const (
	recordedThread = "01a10004-0a27-7421-852d-0f7f99ace2d6"
	recordedTurn   = "01a10004-0b1b-78f0-a41c-b253bded078e"
	recordedItem   = "exec-1a2dcd5e-1591-48a8-bb8e-47cb40883d2c"
)

var recordedDeltas = []string{
	`{"threadId":"01a10004-0a27-7421-852d-0f7f99ace2d6","turnId":"01a10004-0b1b-78f0-a41c-b253bded078e","itemId":"exec-1a2dcd5e-1591-48a8-bb8e-47cb40883d2c","delta":"\u001b[32mstream line 2\u001b[0m\n"}`,
	`{"threadId":"01a10004-0a27-7421-852d-0f7f99ace2d6","turnId":"01a10004-0b1b-78f0-a41c-b253bded078e","itemId":"exec-1a2dcd5e-1591-48a8-bb8e-47cb40883d2c","delta":"\u001b[32mstream line 3\u001b[0m\n"}`,
	`{"threadId":"01a10004-0a27-7421-852d-0f7f99ace2d6","turnId":"01a10004-0b1b-78f0-a41c-b253bded078e","itemId":"exec-1a2dcd5e-1591-48a8-bb8e-47cb40883d2c","delta":"\u001b[32mstream line 4\u001b[0m\n"}`,
	`{"threadId":"01a10004-0a27-7421-852d-0f7f99ace2d6","turnId":"01a10004-0b1b-78f0-a41c-b253bded078e","itemId":"exec-1a2dcd5e-1591-48a8-bb8e-47cb40883d2c","delta":"progress 50%\rprogress 100%\n"}`,
}

const recordedAggregate = "\u001b[32mstream line 2\u001b[0m\n\u001b[32mstream line 3\u001b[0m\n\u001b[32mstream line 4\u001b[0m\nprogress 50%\rprogress 100%\n"

// recordedItemFrame builds the item/started or item/completed frame for the
// recorded command, on the given thread.
func recordedItemFrame(t *testing.T, thread string, completed bool) json.RawMessage {
	t.Helper()
	item := map[string]any{
		"type":             "commandExecution",
		"id":               recordedItem,
		"command":          `for i in 1 2 3 4; do printf '\033[32mstream line %s\033[0m\n' $i; sleep 1; done; printf 'progress 50%%\rprogress 100%%\n'`,
		"cwd":              "/tmp/project",
		"source":           "unifiedExecStartup",
		"status":           "inProgress",
		"aggregatedOutput": nil,
	}
	if completed {
		item["status"] = "completed"
		item["aggregatedOutput"] = recordedAggregate
		item["exitCode"] = 0
		item["durationMs"] = 3903
	}
	raw, err := json.Marshal(map[string]any{"threadId": thread, "turnId": recordedTurn, "item": item})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// onThread rewrites a recorded delta's threadId, so the same frames can stand
// in for a subagent's.
func onThread(t *testing.T, frame, thread string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(frame), &m); err != nil {
		t.Fatal(err)
	}
	m["threadId"] = thread
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// assertStreamedCommand checks the recorded command's emissions: started, one
// bare update per delta carrying exactly that text, then the completion with
// the aggregate as content and no delta.
func assertStreamedCommand(t *testing.T, got []proto.Emission, wantTurn string) {
	t.Helper()
	want := []string{
		"\u001b[32mstream line 2\u001b[0m\n",
		"\u001b[32mstream line 3\u001b[0m\n",
		"\u001b[32mstream line 4\u001b[0m\n",
		"progress 50%\rprogress 100%\n",
	}
	if len(got) != len(want)+2 {
		t.Fatalf("emitted %d events, want %d: %+v", len(got), len(want)+2, got)
	}

	if got[0].Type != proto.ToolCallStarted {
		t.Fatalf("first event = %s, want %s", got[0].Type, proto.ToolCallStarted)
	}
	started := got[0].Payload.(proto.ToolCallStartedPayload)
	if started.ToolCallID != recordedItem || started.Status != proto.StatusInProgress || started.TurnID != wantTurn {
		t.Fatalf("started = %+v, want %s in progress in turn %s", started, recordedItem, wantTurn)
	}

	for i, text := range want {
		e := got[i+1]
		if e.Type != proto.ToolCallUpdated {
			t.Fatalf("event %d = %s, want %s", i+1, e.Type, proto.ToolCallUpdated)
		}
		p := e.Payload.(proto.ToolCallUpdatedPayload)
		if p.ToolCallID != started.ToolCallID {
			t.Fatalf("delta %d on call %q, want the started call %q", i, p.ToolCallID, started.ToolCallID)
		}
		if p.OutputDelta != text {
			t.Fatalf("delta %d = %q, want %q", i, p.OutputDelta, text)
		}
		// A status would stop the coalescer merging these and could read an
		// idle session as working; content would be taken as the final output.
		if p.Status != "" || p.Content != nil || p.Title != "" || p.RawInput != nil {
			t.Fatalf("delta %d carries more than its text: %+v", i, p)
		}
	}

	last := got[len(got)-1]
	if last.Type != proto.ToolCallUpdated {
		t.Fatalf("last event = %s, want %s", last.Type, proto.ToolCallUpdated)
	}
	done := last.Payload.(proto.ToolCallUpdatedPayload)
	if done.ToolCallID != started.ToolCallID || done.Status != proto.StatusCompleted {
		t.Fatalf("completion = %+v, want %s completed", done, started.ToolCallID)
	}
	if done.OutputDelta != "" {
		t.Fatalf("completion carries a delta %q", done.OutputDelta)
	}
	if len(done.Content) != 1 || done.Content[0].Text != recordedAggregate {
		t.Fatalf("completion content = %+v, want the aggregated output", done.Content)
	}
}

func TestCommandOutputStreamsOntoItsToolCall(t *testing.T) {
	s := subagentSession(t)
	s.threadID = recordedThread
	s.turnID, s.serverTurnID = "omniplex-turn", recordedTurn

	s.handleNotification("item/started", recordedItemFrame(t, recordedThread, false))
	for _, d := range recordedDeltas {
		s.handleNotification("item/commandExecution/outputDelta", json.RawMessage(d))
	}
	s.handleNotification("item/completed", recordedItemFrame(t, recordedThread, true))

	assertStreamedCommand(t, drain(s), "omniplex-turn")
}

// A subagent's commands are rendered as tool calls under its spawn, so what
// they print streams onto those calls the same way, and keeps streaming after
// the turn that spawned it has ended.
func TestSubagentCommandOutputStreamsOntoItsToolCall(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-42"
	const sub = "subagent-9"
	spawn(s, "spawn-1", "look around", sub)
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-turn-42","status":"completed"}}`)
	drain(s)

	s.handleNotification("item/started", recordedItemFrame(t, sub, false))
	for _, d := range recordedDeltas {
		s.handleNotification("item/commandExecution/outputDelta", onThread(t, d, sub))
	}
	s.handleNotification("item/completed", recordedItemFrame(t, sub, true))

	var calls []proto.Emission
	for _, e := range drain(s) {
		if e.Type == proto.ToolCallStarted || e.Type == proto.ToolCallUpdated {
			calls = append(calls, e)
		}
	}
	assertStreamedCommand(t, calls, "omniplex-turn")
	if p := calls[0].Payload.(proto.ToolCallStartedPayload); p.ParentToolCallID != "spawn-1" {
		t.Fatalf("subagent's command parented on %q, want its spawn", p.ParentToolCallID)
	}
}

// A thread no spawn named is not a job; what it prints goes nowhere.
func TestUnknownThreadCommandOutputIsDropped(t *testing.T) {
	s := subagentSession(t)
	s.handleNotification("item/commandExecution/outputDelta", onThread(t, recordedDeltas[0], "subagent-9"))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("unknown thread's output emitted %+v", got)
	}
}

func TestEmptyCommandOutputDeltaEmitsNothing(t *testing.T) {
	s := subagentSession(t)
	s.threadID = recordedThread
	s.turnID = "omniplex-turn"
	s.handleNotification("item/commandExecution/outputDelta", json.RawMessage(
		`{"threadId":"`+recordedThread+`","turnId":"`+recordedTurn+`","itemId":"`+recordedItem+`","delta":""}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("empty delta emitted %+v", got)
	}
}

// command/exec/outputDelta belongs to a client-initiated command/exec process,
// not a thread item; there is no tool call for it to land on.
func TestExecRPCOutputEmitsNothing(t *testing.T) {
	s := subagentSession(t)
	s.threadID = recordedThread
	s.turnID = "omniplex-turn"
	s.handleNotification("command/exec/outputDelta", json.RawMessage(
		`{"processId":"proc-1","stream":"stdout","deltaBase64":"aGVsbG8K"}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("command/exec output emitted %+v", got)
	}
}
