package codexapp

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// TestRetryingErrorBecomesTurnStatus maps codex's ErrorNotification, as the
// app-server sends it, onto the retrying status.
func TestRetryingErrorBecomesTurnStatus(t *testing.T) {
	cases := []struct {
		name   string
		params string
		want   proto.TurnStatusPayload
	}{
		{
			name: "no code, cause in the message's brackets",
			params: `{"error":{"message":"Reconnecting... 4/5 (stream disconnected before completion: idle timeout waiting for SSE)",
				"codexErrorInfo":null,"additionalDetails":null,"misalignment":null},"willRetry":true,"threadId":"thread-1","turnId":"codex-turn-42"}`,
			want: proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 4, Max: 5, Reason: "stream disconnected before completion"},
		},
		{
			name: "stream disconnected, counter in message",
			params: `{"error":{"message":"Reconnecting... 2/5 (stream disconnected before completion: Transport error: error decoding response body)",
				"codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":null}},"additionalDetails":null,"misalignment":null},
				"willRetry":true,"threadId":"thread-1","turnId":"codex-turn-7"}`,
			want: proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 2, Max: 5, Reason: "response stream disconnected"},
		},
		{
			name: "server overloaded, string code",
			params: `{"error":{"message":"Reconnecting... 1/5","codexErrorInfo":"serverOverloaded",
				"additionalDetails":"unexpected status 529 Overloaded","misalignment":null},
				"willRetry":true,"threadId":"thread-1","turnId":"codex-turn-7"}`,
			want: proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 1, Max: 5, Reason: "server overloaded"},
		},
		{
			name: "no useful code falls back to the details",
			params: `{"error":{"message":"Reconnecting... 3/5","codexErrorInfo":"other",
				"additionalDetails":"stream disconnected before completion: Incomplete response returned","misalignment":null},
				"willRetry":true,"threadId":"thread-1","turnId":"codex-turn-7"}`,
			want: proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 3, Max: 5, Reason: "stream disconnected before completion"},
		},
		{
			name: "no counter, no code, no details",
			params: `{"error":{"message":"retrying after auth recovery","codexErrorInfo":null,"additionalDetails":null,"misalignment":null},
				"willRetry":true,"threadId":"thread-1","turnId":"codex-turn-7"}`,
			want: proto.TurnStatusPayload{State: proto.TurnRetrying, Reason: "retrying after auth recovery"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := subagentSession(t)
			s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-7"
			s.handleNotification("error", json.RawMessage(tc.params))

			got := drain(s)
			if len(got) != 1 || got[0].Type != proto.TurnStatus {
				t.Fatalf("emissions = %+v, want one turn.status", got)
			}
			if p := got[0].Payload.(proto.TurnStatusPayload); p != tc.want {
				t.Fatalf("status = %+v, want %+v", p, tc.want)
			}
		})
	}
}

// TestErrorWithoutStatus: an error codex will not retry ends the turn through
// turn/completed, and a subagent's retry is not this thread's.
func TestErrorWithoutStatus(t *testing.T) {
	cases := []struct {
		name   string
		params string
	}{
		{
			name: "will not retry",
			params: `{"error":{"message":"exceeded retry limit, last status: 429 Too Many Requests",
				"codexErrorInfo":{"responseTooManyFailedAttempts":{"httpStatusCode":429}},"additionalDetails":null,"misalignment":null},
				"willRetry":false,"threadId":"thread-1","turnId":"codex-turn-7"}`,
		},
		{
			name: "foreign thread",
			params: `{"error":{"message":"Reconnecting... 2/5","codexErrorInfo":"serverOverloaded","additionalDetails":null,"misalignment":null},
				"willRetry":true,"threadId":"subagent-9","turnId":"subagent-turn"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := subagentSession(t)
			s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-7"
			s.handleNotification("error", json.RawMessage(tc.params))
			if got := drain(s); len(got) != 0 {
				t.Fatalf("emissions = %+v, want none", got)
			}
		})
	}
}

// TestCompactionItemIsAStatusUntilItLands: a contextCompaction item starting
// shows compacting; completing emits the boundary, which clears it.
func TestCompactionItemIsAStatusUntilItLands(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-7"
	item := `"threadId":"thread-1","turnId":"codex-turn-7","item":{"type":"contextCompaction","id":"compact-1"}`

	s.handleNotification("item/started", json.RawMessage(`{`+item+`}`))
	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.TurnStatus ||
		got[0].Payload.(proto.TurnStatusPayload) != (proto.TurnStatusPayload{State: proto.TurnCompacting}) {
		t.Fatalf("started emissions = %+v, want one compacting status", got)
	}

	s.handleNotification("item/completed", json.RawMessage(`{`+item+`}`))
	got = drain(s)
	if len(got) != 1 || got[0].Type != proto.ContextCompacted {
		t.Fatalf("completed emissions = %+v, want one context.compacted", got)
	}
}

// TestDuplicateCompactionStillClears: when thread/compacted has already sent
// the boundary, the item completing sends no second one, so it must clear the
// status itself.
func TestDuplicateCompactionStillClears(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-7"
	item := `"threadId":"thread-1","turnId":"codex-turn-7","item":{"type":"contextCompaction","id":"compact-1"}`

	s.handleNotification("item/started", json.RawMessage(`{`+item+`}`))
	s.handleNotification("thread/compacted", json.RawMessage(`{"threadId":"thread-1","turnId":"codex-turn-7"}`))
	drain(s)
	s.handleNotification("item/completed", json.RawMessage(`{`+item+`}`))

	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.TurnStatus ||
		got[0].Payload.(proto.TurnStatusPayload) != (proto.TurnStatusPayload{}) {
		t.Fatalf("emissions = %+v, want one clearing status", got)
	}
}

// TestSubagentCompactionSetsNoStatus: a subagent compacting its own context
// says nothing about this thread's.
func TestSubagentCompactionSetsNoStatus(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-7"
	s.handleNotification("item/started", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","item":{"type":"contextCompaction","id":"compact-9"}}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("emissions = %+v, want none", got)
	}
}
