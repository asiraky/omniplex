package codexapp

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// TestPlanUpdatedMapsStepsOntoTheTurn: codex's update_plan list becomes one
// plan.updated on the omniplex turn, with codex's statuses mapped onto the
// canonical ones and the explanation carried through.
func TestPlanUpdatedMapsStepsOntoTheTurn(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn"
	s.handleNotification("turn/plan/updated", json.RawMessage(`{
		"threadId":"thread-1","turnId":"codex-turn",
		"explanation":"Found the bug earlier than expected",
		"plan":[
			{"step":"Read the adapter","status":"completed"},
			{"step":"Fix the decoder","status":"inProgress"},
			{"step":"Write tests","status":"pending"},
			{"step":"Ship it","status":"blocked"}
		]}`))

	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.PlanUpdated {
		t.Fatalf("emitted %+v, want one plan.updated", got)
	}
	want := proto.PlanUpdatedPayload{
		TurnID:      "omniplex-turn",
		Explanation: "Found the bug earlier than expected",
		Steps: []proto.PlanStep{
			{Text: "Read the adapter", Status: proto.PlanCompleted},
			{Text: "Fix the decoder", Status: proto.PlanRunning},
			{Text: "Write tests", Status: proto.PlanPending},
			{Text: "Ship it", Status: proto.PlanPending},
		},
	}
	if p := got[0].Payload.(proto.PlanUpdatedPayload); !reflect.DeepEqual(p, want) {
		t.Fatalf("payload = %+v\nwant      %+v", p, want)
	}
}

// TestPlanWhileIdleOpensATurn: a plan arriving with no turn open is work
// codex started on its own, so it opens a turn and the plan lands on it.
func TestPlanWhileIdleOpensATurn(t *testing.T) {
	s := subagentSession(t)
	s.handleNotification("turn/plan/updated", json.RawMessage(
		`{"threadId":"thread-1","turnId":"codex-turn","explanation":null,"plan":[{"step":"Look around","status":"pending"}]}`))

	got := drain(s)
	if len(got) != 2 || got[0].Type != proto.TurnStarted || got[1].Type != proto.PlanUpdated {
		t.Fatalf("emitted %+v, want turn.started then plan.updated", got)
	}
	started := got[0].Payload.(proto.TurnStartedPayload)
	plan := got[1].Payload.(proto.PlanUpdatedPayload)
	if plan.TurnID != started.TurnID || plan.TurnID == "codex-turn" {
		t.Fatalf("plan turn = %q, opened turn = %q", plan.TurnID, started.TurnID)
	}
	if plan.Explanation != "" {
		t.Fatalf("null explanation became %q", plan.Explanation)
	}
	if s.serverTurnID != "codex-turn" {
		t.Fatalf("serverTurnID = %q, want codex-turn so Cancel can interrupt", s.serverTurnID)
	}
}

// TestPlanWithoutStepsEmitsNothing: an empty list carries nothing to show,
// and must not open a turn while idle either.
func TestPlanWithoutStepsEmitsNothing(t *testing.T) {
	s := subagentSession(t)
	s.handleNotification("turn/plan/updated", json.RawMessage(
		`{"threadId":"thread-1","turnId":"codex-turn","explanation":null,"plan":[]}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("empty plan emitted %+v", got)
	}
}

// TestPlanDeltaIsIgnored: item/plan/delta streams a plan-mode item's text,
// not a step list. It must not become a plan.updated.
func TestPlanDeltaIsIgnored(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "omniplex-turn"
	s.handleNotification("item/plan/delta", json.RawMessage(
		`{"threadId":"thread-1","turnId":"codex-turn","itemId":"p1","delta":"1. Read the code"}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("plan delta emitted %+v", got)
	}
}
