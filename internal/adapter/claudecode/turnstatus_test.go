package claudecode

import (
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// turnStatuses drains the event channel and returns every turn.status payload.
func turnStatuses(t *testing.T, s *session) []proto.TurnStatusPayload {
	t.Helper()
	var out []proto.TurnStatusPayload
	for {
		select {
		case e := <-s.events:
			if e.Type != proto.TurnStatus {
				continue
			}
			p, ok := e.Payload.(proto.TurnStatusPayload)
			if !ok {
				t.Fatalf("turn.status payload is %T", e.Payload)
			}
			out = append(out, p)
		default:
			return out
		}
	}
}

// apiRetry is an SDKAPIRetryMessage as the CLI sends it; status is nil for a
// connection error with no HTTP response.
func apiRetry(attempt, max int, delayMs int64, status any, class string) map[string]any {
	return map[string]any{
		"type":           "system",
		"subtype":        "api_retry",
		"attempt":        attempt,
		"max_retries":    max,
		"retry_delay_ms": delayMs,
		"error_status":   status,
		"error":          class,
		"uuid":           "6f1c2a9e-3b7d-4e1a-9c55-0d2b8e4f7a10",
		"session_id":     "conv",
	}
}

// statusMsg is an SDKStatusMessage; status nil is the SDK's null.
func statusMsg(status any) map[string]any {
	return map[string]any{
		"type":           "system",
		"subtype":        "status",
		"status":         status,
		"permissionMode": "default",
		"uuid":           "a3d4e5f6-0718-4293-8a1b-2c3d4e5f6071",
		"session_id":     "conv",
	}
}

func TestAPIRetryBecomesRetryingStatus(t *testing.T) {
	cases := []struct {
		name    string
		msg     map[string]any
		attempt int
		max     int
		delay   int64
		reason  string
	}{
		{"overloaded", apiRetry(1, 10, 2000, 529, "overloaded"), 1, 10, 2000, "overloaded"},
		{"rate limit", apiRetry(3, 10, 8000, 429, "rate_limit"), 3, 10, 8000, "rate limit"},
		{"server error", apiRetry(2, 10, 4000, 500, "server_error"), 2, 10, 4000, "server error"},
		{"unknown class with status", apiRetry(1, 10, 1000, 529, "unknown"), 1, 10, 1000, "HTTP 529"},
		{"connection error", apiRetry(2, 10, 1500, nil, "unknown"), 2, 10, 1500, "connection error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestSession()
			before := time.Now().UnixMilli()
			s.handleSDKMessage(rawSDK(t, c.msg))
			after := time.Now().UnixMilli()

			got := turnStatuses(t, s)
			if len(got) != 1 {
				t.Fatalf("got %d turn.status, want 1: %+v", len(got), got)
			}
			p := got[0]
			if p.State != proto.TurnRetrying || p.Attempt != c.attempt || p.Max != c.max || p.Reason != c.reason {
				t.Fatalf("payload = %+v, want retrying %d/%d %q", p, c.attempt, c.max, c.reason)
			}
			if p.RetryAt < before+c.delay || p.RetryAt > after+c.delay {
				t.Fatalf("RetryAt = %d, want within [%d, %d]", p.RetryAt, before+c.delay, after+c.delay)
			}
		})
	}
}

func TestStatusCompactingSetsAndNullClears(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(rawSDK(t, statusMsg("compacting")))
	s.handleSDKMessage(rawSDK(t, statusMsg(nil)))

	got := turnStatuses(t, s)
	want := []proto.TurnStatusPayload{{State: proto.TurnCompacting}, {}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
}

// "requesting" goes out before the retried request too, so it must leave the
// retry standing: the line stays until the model answers.
func TestRequestingLeavesARetryStanding(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(rawSDK(t, apiRetry(1, 10, 2000, 529, "overloaded")))
	s.handleSDKMessage(rawSDK(t, statusMsg("requesting")))

	got := turnStatuses(t, s)
	if len(got) != 1 || got[0].State != proto.TurnRetrying {
		t.Fatalf("statuses = %+v, want only the retry", got)
	}
}

// A discarded steer's turn is kept out of the log; its retries are not the
// status of any turn the user can see.
func TestSuppressedTurnEmitsNoStatus(t *testing.T) {
	s := newTestSession()
	s.suppress = true
	s.handleSDKMessage(rawSDK(t, apiRetry(1, 10, 2000, 529, "overloaded")))
	s.handleSDKMessage(rawSDK(t, statusMsg("compacting")))
	if got := turnStatuses(t, s); len(got) != 0 {
		t.Fatalf("suppressed turn emitted %+v", got)
	}
}
