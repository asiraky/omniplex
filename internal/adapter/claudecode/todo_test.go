package claudecode

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func drain(s *session) []proto.Emission {
	var out []proto.Emission
	for {
		select {
		case e := <-s.events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// todoWrite feeds one TodoWrite call through the SDK the way the CLI sends
// it: the stream's tool_use start, the full assistant message, then the
// tool result. withStream false is the CLI sending no stream events.
func todoWrite(t *testing.T, s *session, id, parent string, input any, withStream bool) {
	t.Helper()
	var p any
	if parent != "" {
		p = parent
	}
	if withStream {
		s.handleSDKMessage(rawSDK(t, map[string]any{
			"type": "stream_event", "parent_tool_use_id": p,
			"event": map[string]any{"type": "message_start", "message": map[string]any{"id": "msg-" + id}},
		}))
		s.handleSDKMessage(rawSDK(t, map[string]any{
			"type": "stream_event", "parent_tool_use_id": p,
			"event": map[string]any{
				"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "tool_use", "id": id, "name": "TodoWrite"},
			},
		}))
	}
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "assistant", "parent_tool_use_id": p,
		"message": map[string]any{
			"id": "msg-" + id,
			"content": []any{map[string]any{
				"type": "tool_use", "id": id, "name": "TodoWrite", "input": input,
			}},
		},
	}))
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "user", "parent_tool_use_id": p,
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": id,
			"content": "Todos have been modified successfully.",
		}}},
	}))
}

func todos(items ...[2]string) map[string]any {
	list := []any{}
	for _, it := range items {
		list = append(list, map[string]any{"content": it[0], "status": it[1], "activeForm": it[0] + "ing"})
	}
	return map[string]any{"todos": list}
}

func toolEvents(es []proto.Emission) []proto.Emission {
	var out []proto.Emission
	for _, e := range es {
		if e.Type == proto.ToolCallStarted || e.Type == proto.ToolCallUpdated {
			out = append(out, e)
		}
	}
	return out
}

func plans(es []proto.Emission) []proto.PlanUpdatedPayload {
	var out []proto.PlanUpdatedPayload
	for _, e := range es {
		if p, ok := e.Payload.(proto.PlanUpdatedPayload); ok && e.Type == proto.PlanUpdated {
			out = append(out, p)
		}
	}
	return out
}

func TestRootTodoWriteIsAPlanNotAToolCall(t *testing.T) {
	for _, withStream := range []bool{true, false} {
		s := newTestSession()
		s.turnID = "turn-1"
		todoWrite(t, s, "tu-1", "", todos(
			[2]string{"Read", "completed"},
			[2]string{"Edit", "in_progress"},
			[2]string{"Test", "pending"},
			[2]string{"Ship", "blocked"},
		), withStream)
		es := drain(s)

		if tc := toolEvents(es); len(tc) != 0 {
			t.Fatalf("stream=%v: tool events leaked: %+v", withStream, tc)
		}
		ps := plans(es)
		if len(ps) != 1 {
			t.Fatalf("stream=%v: want 1 plan.updated, got %d", withStream, len(ps))
		}
		p := ps[0]
		if p.TurnID != "turn-1" || p.ToolCallID != "tu-1" {
			t.Fatalf("stream=%v: wrong ids %+v", withStream, p)
		}
		want := []proto.PlanStep{
			{Text: "Read", Status: proto.PlanCompleted},
			{Text: "Edit", Status: proto.PlanRunning},
			{Text: "Test", Status: proto.PlanPending},
			{Text: "Ship", Status: proto.PlanPending},
		}
		if len(p.Steps) != len(want) {
			t.Fatalf("stream=%v: steps %+v", withStream, p.Steps)
		}
		for i := range want {
			if p.Steps[i] != want[i] {
				t.Fatalf("stream=%v: step %d = %+v, want %+v", withStream, i, p.Steps[i], want[i])
			}
		}
		if len(s.todos) != 0 {
			t.Fatalf("stream=%v: todo id kept after its result: %v", withStream, s.todos)
		}
	}
}

func TestSubagentTodoWriteStaysAToolCall(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	todoWrite(t, s, "tu-sub", "task-1", todos([2]string{"Look", "pending"}), true)
	es := drain(s)

	if ps := plans(es); len(ps) != 0 {
		t.Fatalf("subagent todo became a plan: %+v", ps)
	}
	tc := toolEvents(es)
	if len(tc) != 3 || tc[0].Type != proto.ToolCallStarted {
		t.Fatalf("want start + in-progress + result, got %+v", tc)
	}
	if st := tc[0].Payload.(proto.ToolCallStartedPayload); st.ToolCallID != "tu-sub" || st.ParentToolCallID != "task-1" {
		t.Fatalf("start %+v", st)
	}
	if done := tc[2].Payload.(proto.ToolCallUpdatedPayload); done.Status != proto.StatusCompleted {
		t.Fatalf("result %+v", done)
	}
}

func TestEachTodoWriteIsItsOwnPlan(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	todoWrite(t, s, "tu-1", "", todos([2]string{"A", "in_progress"}), true)
	todoWrite(t, s, "tu-2", "", todos([2]string{"A", "completed"}), true)
	ps := plans(drain(s))

	if len(ps) != 2 || ps[0].ToolCallID != "tu-1" || ps[1].ToolCallID != "tu-2" {
		t.Fatalf("want plans for tu-1 then tu-2, got %+v", ps)
	}
	if ps[1].Steps[0].Status != proto.PlanCompleted {
		t.Fatalf("second plan %+v", ps[1])
	}
}

// Input that is not a todo list must not vanish: the call shows as an
// ordinary tool, start included, even though the stream held its start back.
func TestUndecodableTodoWriteFallsBackToAToolCall(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	todoWrite(t, s, "tu-1", "", map[string]any{"items": "nope"}, true)
	es := drain(s)

	if ps := plans(es); len(ps) != 0 {
		t.Fatalf("plan from bad input: %+v", ps)
	}
	tc := toolEvents(es)
	if len(tc) != 3 || tc[0].Type != proto.ToolCallStarted {
		t.Fatalf("want start + in-progress + result, got %+v", tc)
	}
	if done := tc[2].Payload.(proto.ToolCallUpdatedPayload); done.ToolCallID != "tu-1" || done.Status != proto.StatusCompleted {
		t.Fatalf("result %+v", done)
	}
}
