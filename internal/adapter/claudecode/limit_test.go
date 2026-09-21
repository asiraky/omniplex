package claudecode

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func finished(t *testing.T, s *session) proto.TurnFinishedPayload {
	t.Helper()
	for {
		select {
		case em := <-s.events:
			if p, ok := em.Payload.(proto.TurnFinishedPayload); ok {
				return p
			}
		default:
			t.Fatal("no turn.finished")
		}
	}
}

func limitHit(t *testing.T, s *session, parent string) {
	t.Helper()
	msg := map[string]any{
		"type":  "assistant",
		"error": "rate_limit",
		"message": map[string]any{
			"id": "m1", "model": "<synthetic>", "role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": "You've hit your session limit · resets 2:10pm"}},
		},
	}
	if parent != "" {
		msg["parent_tool_use_id"] = parent
	}
	s.handleSDKMessage(rawSDK(t, msg))
}

// A plan usage limit arrives as a synthetic assistant message flagged
// rate_limit; the result that follows does not say it was a limit, and may
// not even be flagged an error. The turn must still end as a limit failure —
// that classification is what offers the switch to another account.
func TestUsageLimitEndsTurnAsLimitFailure(t *testing.T) {
	for _, isError := range []bool{true, false} {
		s := newTestSession()
		s.turnID = "turn-1"
		limitHit(t, s, "")
		s.handleSDKMessage(rawSDK(t, map[string]any{
			"type": "result", "is_error": isError, "result": "You've hit your session limit · resets 2:10pm",
		}))
		p := finished(t, s)
		if p.Failure != proto.FailureLimit || p.StopReason != proto.StopError {
			t.Errorf("is_error=%v: failure=%q stop=%q, want a limit error", isError, p.Failure, p.StopReason)
		}
		if p.Error == "" {
			t.Errorf("is_error=%v: the failure does not say when the limit resets", isError)
		}

		// The flag belongs to one turn: the next one, run after the reset,
		// ends normally.
		s.turnID = "turn-2"
		s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "stop_reason": "end_turn"}))
		if p := finished(t, s); p.Failure != "" || p.StopReason == proto.StopError {
			t.Errorf("a later turn inherited the limit: %+v", p)
		}
	}
}

// A subagent refused for a limit is the subagent's failure; the main thread
// reports its own outcome.
func TestSubagentLimitDoesNotFailTheTurn(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	limitHit(t, s, "toolu_1")
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "stop_reason": "end_turn"}))
	if p := finished(t, s); p.Failure == proto.FailureLimit {
		t.Error("a subagent's limit failed the main turn")
	}
}
