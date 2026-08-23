package claudecode

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// lastTurnFinished drains the event channel and returns the newest
// turn.finished payload.
func lastTurnFinished(t *testing.T, s *session) proto.TurnFinishedPayload {
	t.Helper()
	var last proto.TurnFinishedPayload
	found := false
	for {
		select {
		case e := <-s.events:
			if f, ok := e.Payload.(proto.TurnFinishedPayload); ok {
				last, found = f, true
			}
		default:
			if !found {
				t.Fatal("no turn.finished emitted")
			}
			return last
		}
	}
}

// The bug this guards against: a turn that failed used to close with an empty
// error, so the transcript could say only "this turn ended with an error" —
// and the reader, with nothing else to go on, reached for the continuation
// prompt and its story about a restart. The harness knew perfectly well that
// the login had expired; the message just never left the adapter.
func TestFailedResultCarriesTheHarnessMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  map[string]any
		want string
	}{
		{
			name: "result text",
			msg:  map[string]any{"is_error": true, "result": "Invalid API key · Please run /login"},
			want: "Invalid API key · Please run /login",
		},
		{
			name: "structured errors",
			msg: map[string]any{
				"is_error": true, "subtype": "error_during_execution",
				"errors": []string{"OAuth token expired", "run /login"},
			},
			want: "OAuth token expired; run /login",
		},
		{
			name: "terminal reason",
			msg:  map[string]any{"is_error": true, "subtype": "error_during_execution", "terminal_reason": "api_error"},
			want: "the harness stopped: api error",
		},
		{
			name: "subtype only",
			msg:  map[string]any{"is_error": true, "subtype": "error_max_turns"},
			want: "the harness reported error max turns",
		},
		{
			name: "nothing at all",
			msg:  map[string]any{"is_error": true},
			want: "the harness reported an error without saying what it was",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSession()
			s.turnID = "turn-1"
			tc.msg["type"] = "result"
			s.handleSDKMessage(rawSDK(t, tc.msg))

			got := lastTurnFinished(t, s)
			if got.StopReason != proto.StopError {
				t.Fatalf("stop reason = %q; want %q", got.StopReason, proto.StopError)
			}
			if got.Error != tc.want {
				t.Fatalf("error = %q; want %q", got.Error, tc.want)
			}
		})
	}
}

// A turn that ended cleanly carries no error, whatever else the result says.
func TestSuccessfulResultCarriesNoError(t *testing.T) {
	s := newTestSession()
	s.turnID = "turn-1"
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": "all done",
	}))
	if got := lastTurnFinished(t, s); got.Error != "" {
		t.Fatalf("error = %q; want empty on a clean turn", got.Error)
	}
}
