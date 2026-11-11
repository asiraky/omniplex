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

func rateLimitEvent(t *testing.T, s *session, info map[string]any) {
	t.Helper()
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "rate_limit_event", "rate_limit_info": info}))
}

func limitResult(t *testing.T, s *session) proto.TurnFinishedPayload {
	t.Helper()
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "is_error": true, "result": "You've hit your limit"}))
	return finished(t, s)
}

// A turn refused by two windows can only continue once both reopen, so the
// reset is the later of the two.
func TestLimitResetIsLatestRejectedWindow(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "seven_day", "resetsAt": 1_800_000_000})
	rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "five_hour", "resetsAt": 1_700_000_000})
	limitHit(t, s, "")
	if p := limitResult(t, s); p.ResetAt != 1_800_000_000_000 {
		t.Errorf("resetAt = %d, want the later window's reset in ms", p.ResetAt)
	}
}

// The CLI throttles rate_limit_event, so the blocked response's own copy of
// the window may be the only one the turn sees.
func TestLimitResetFromBlockedResponse(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "assistant", "error": "rate_limit",
		"api_error_params": map[string]any{"rate_limit_info": map[string]any{
			"status": "rejected", "rateLimitType": "five_hour", "resetsAt": 1_700_000_000,
		}},
		"message": map[string]any{"id": "m1", "model": "<synthetic>", "role": "assistant", "content": []any{}},
	}))
	if p := limitResult(t, s); p.Failure != proto.FailureLimit || p.ResetAt != 1_700_000_000_000 {
		t.Errorf("got failure=%q resetAt=%d, want a limit resetting at the response's window", p.Failure, p.ResetAt)
	}
}

// One window with no known reset makes any reset time a guess that would
// continue into another refusal.
func TestLimitResetUnknownWhenAnyWindowIs(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "five_hour", "resetsAt": 1_700_000_000})
	rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "seven_day"})
	limitHit(t, s, "")
	if p := limitResult(t, s); p.Failure != proto.FailureLimit || p.ResetAt != 0 {
		t.Errorf("got failure=%q resetAt=%d, want a limit with unknown reset", p.Failure, p.ResetAt)
	}
}

// A window that reopens, or that overage carries past, no longer holds the
// turn back.
func TestAllowedWindowNoLongerSetsReset(t *testing.T) {
	for name, clear := range map[string]map[string]any{
		"allowed": {"status": "allowed", "rateLimitType": "seven_day"},
		"overage": {"status": "rejected", "rateLimitType": "seven_day", "resetsAt": 1_900_000_000, "overageStatus": "allowed"},
	} {
		s := newTestSession()
		s.turnID = "turn-1"
		rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "seven_day", "resetsAt": 1_800_000_000})
		rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "five_hour", "resetsAt": 1_700_000_000})
		rateLimitEvent(t, s, clear)
		limitHit(t, s, "")
		if p := limitResult(t, s); p.ResetAt != 1_700_000_000_000 {
			t.Errorf("%s: resetAt = %d, want the remaining window's reset", name, p.ResetAt)
		}
	}
}

// Windows recorded for one limit turn say nothing about the next; a limit
// with no window information has no reset rather than the last turn's.
func TestLimitResetDoesNotLeakIntoNextTurn(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	rateLimitEvent(t, s, map[string]any{"status": "rejected", "rateLimitType": "five_hour", "resetsAt": 1_700_000_000})
	limitHit(t, s, "")
	if p := limitResult(t, s); p.ResetAt == 0 {
		t.Fatal("first turn has no reset")
	}

	s.turnID = "turn-2"
	limitHit(t, s, "")
	if p := limitResult(t, s); p.Failure != proto.FailureLimit || p.ResetAt != 0 {
		t.Errorf("got failure=%q resetAt=%d, want a limit with no inherited reset", p.Failure, p.ResetAt)
	}
}

// A subagent refused for a limit doesn't fail the turn, so its window can't
// give the turn a reset either.
func TestSubagentLimitSetsNoReset(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "assistant", "error": "rate_limit", "parent_tool_use_id": "toolu_1",
		"api_error_params": map[string]any{"rate_limit_info": map[string]any{
			"status": "rejected", "rateLimitType": "five_hour", "resetsAt": 1_700_000_000,
		}},
		"message": map[string]any{"id": "m1", "model": "<synthetic>", "role": "assistant", "content": []any{}},
	}))
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "stop_reason": "end_turn"}))
	if p := finished(t, s); p.Failure != "" || p.ResetAt != 0 {
		t.Errorf("a subagent's limit reached the main turn: %+v", p)
	}
}
