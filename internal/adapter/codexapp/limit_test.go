package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

func TestUsageLimitClassification(t *testing.T) {
	cases := []struct {
		name string
		info string
		want bool
	}{
		{"usage limit code", `"usageLimitExceeded"`, true},
		{"rate limit code", `"rateLimitExceeded"`, true},
		{"retries exhausted on 429", `{"responseTooManyFailedAttempts":{"httpStatusCode":429}}`, true},
		{"retries exhausted on 500", `{"responseTooManyFailedAttempts":{"httpStatusCode":500}}`, false},
		{"retries exhausted, status unknown", `{"responseTooManyFailedAttempts":{"httpStatusCode":null}}`, false},
		{"429 that was not retry exhaustion", `{"httpConnectionFailed":{"httpStatusCode":429}}`, false},
		{"another code", `"contextWindowExceeded"`, false},
		{"null", `null`, false},
		{"absent", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := codexTurnError{Message: "boom", CodexErrorInfo: json.RawMessage(c.info)}
			if got := e.usageLimit(); got != c.want {
				t.Fatalf("usageLimit(%s) = %v, want %v", c.info, got, c.want)
			}
		})
	}
}

func TestLimitResetAt(t *testing.T) {
	pct := func(v float64) *float64 { return &v }
	at := func(v int64) *int64 { return &v }
	cases := []struct {
		name string
		snap *codexLimitSnapshot
		want int64
	}{
		{"nothing known", nil, 0},
		{"the latest exhausted window", &codexLimitSnapshot{
			Primary:   &codexLimitWindow{UsedPercent: pct(100), ResetsAt: at(1000)},
			Secondary: &codexLimitWindow{UsedPercent: pct(100), ResetsAt: at(5000)},
		}, 5_000_000},
		{"a window with room left does not hold the turn", &codexLimitSnapshot{
			Primary:   &codexLimitWindow{UsedPercent: pct(100), ResetsAt: at(1000)},
			Secondary: &codexLimitWindow{UsedPercent: pct(60), ResetsAt: at(5000)},
		}, 1_000_000},
		{"an exhausted window with no reset", &codexLimitSnapshot{
			Primary:   &codexLimitWindow{UsedPercent: pct(100), ResetsAt: at(1000)},
			Secondary: &codexLimitWindow{UsedPercent: pct(100)},
		}, 0},
		{"nothing exhausted", &codexLimitSnapshot{
			Primary: &codexLimitWindow{UsedPercent: pct(99), ResetsAt: at(1000)},
		}, 0},
		{"usage unknown", &codexLimitSnapshot{
			Primary: &codexLimitWindow{ResetsAt: at(1000)},
		}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.snap.resetAt(); got != c.want {
				t.Fatalf("resetAt = %d, want %d", got, c.want)
			}
		})
	}
}

// limitedSession is a session with a running turn and a live connection,
// so the post-turn quota read has somewhere to go.
func limitedSession(t *testing.T) *session {
	t.Helper()
	conn, _ := pairedConn(t, nil)
	s := subagentSession(t)
	s.conn = conn
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-1"
	return s
}

func finished(t *testing.T, got []proto.Emission) proto.TurnFinishedPayload {
	t.Helper()
	for _, e := range got {
		if e.Type == proto.TurnFinished {
			return e.Payload.(proto.TurnFinishedPayload)
		}
	}
	t.Fatalf("no turn.finished in %+v", got)
	return proto.TurnFinishedPayload{}
}

// TestLimitFailureCarriesTheMainAllowancesReset: the reset comes from the
// merged snapshot of the main allowance, never a model-specific limit's.
func TestLimitFailureCarriesTheMainAllowancesReset(t *testing.T) {
	s := limitedSession(t)
	s.handleNotification("account/rateLimits/updated", json.RawMessage(
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1789000000},"secondary":{"usedPercent":40,"resetsAt":1789500000}}}`))
	s.handleNotification("account/rateLimits/updated", json.RawMessage(
		`{"rateLimits":{"limitId":"codex_bengalfox","primary":{"usedPercent":100,"resetsAt":1789900000}}}`))
	// A sparse update naming only the secondary keeps the primary it omits.
	s.handleNotification("account/rateLimits/updated", json.RawMessage(
		`{"rateLimits":{"limitId":"codex","secondary":{"usedPercent":45,"resetsAt":1789500000}}}`))
	s.handleNotification("turn/completed", json.RawMessage(
		`{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"failed","error":{"message":"usage limit","codexErrorInfo":"usageLimitExceeded"}}}`))

	fin := finished(t, drain(s))
	if fin.StopReason != proto.StopError || fin.Failure != proto.FailureLimit || fin.Error != "usage limit" {
		t.Fatalf("turn.finished = %+v, want a limit failure", fin)
	}
	if fin.ResetAt != 1789000000*1000 {
		t.Fatalf("ResetAt = %d, want the main allowance's exhausted window", fin.ResetAt)
	}
}

func TestNonLimitFailureIsUnclassified(t *testing.T) {
	s := limitedSession(t)
	s.handleNotification("account/rateLimits/updated", json.RawMessage(
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1789000000}}}`))
	s.handleNotification("turn/completed", json.RawMessage(
		`{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"failed","error":{"message":"stream died","codexErrorInfo":{"responseTooManyFailedAttempts":{"httpStatusCode":502}}}}}`))

	fin := finished(t, drain(s))
	if fin.Failure != "" || fin.ResetAt != 0 || fin.Error != "stream died" {
		t.Fatalf("turn.finished = %+v, want an unclassified error", fin)
	}
}

// TestErrorNotificationClassifiesTheTurn: codex reports the limit on the
// error notification and repeats only the bare message on the completion.
func TestErrorNotificationClassifiesTheTurn(t *testing.T) {
	s := limitedSession(t)
	s.handleNotification("account/rateLimits/updated", json.RawMessage(
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1789000000}}}`))
	s.handleNotification("error", json.RawMessage(
		`{"threadId":"thread-1","turnId":"codex-turn-1","willRetry":false,"error":{"message":"You've hit your usage limit.","additionalDetails":"Try again at 3:00 PM.","codexErrorInfo":"usageLimitExceeded"}}`))
	s.handleNotification("turn/completed", json.RawMessage(
		`{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"failed","error":{"message":"You've hit your usage limit."}}}`))

	fin := finished(t, drain(s))
	if fin.Failure != proto.FailureLimit || fin.ResetAt != 1789000000*1000 {
		t.Fatalf("turn.finished = %+v, want the notification's limit", fin)
	}
	if fin.Error != "Try again at 3:00 PM." {
		t.Fatalf("Error = %q, want the additional details", fin.Error)
	}
}

func TestErrorNotificationsThatAreNotTheTurnsFailure(t *testing.T) {
	limit := `"error":{"message":"limit","codexErrorInfo":"usageLimitExceeded"}`
	cases := []struct {
		name   string
		params string
	}{
		{"codex is retrying", `{"threadId":"thread-1","turnId":"codex-turn-1","willRetry":true,` + limit + `}`},
		{"a subagent's", `{"threadId":"subagent-9","turnId":"subagent-turn","willRetry":false,` + limit + `}`},
		{"an earlier turn's", `{"threadId":"thread-1","turnId":"codex-turn-0","willRetry":false,` + limit + `}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := limitedSession(t)
			s.handleNotification("error", json.RawMessage(c.params))
			s.handleNotification("turn/completed", json.RawMessage(
				`{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"failed","error":{"message":"something else"}}}`))
			fin := finished(t, drain(s))
			if fin.Failure != "" || fin.Error != "something else" {
				t.Fatalf("turn.finished = %+v, want the completion's own unclassified error", fin)
			}
		})
	}
}

// wiredSession is a session whose notifications arrive over its own
// connection's read loop, as they do from a real app-server: a handler that
// waited there on an RPC reply would deadlock.
func wiredSession(t *testing.T, reply map[string]any) (*session, *jsonrpc.Conn, *recorder) {
	t.Helper()
	rec := &recorder{counts: map[string]int{}}
	sr, cw := io.Pipe()
	cr, sw := io.Pipe()
	server := jsonrpc.NewConn(sr, sw, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		rec.mu.Lock()
		rec.counts[method]++
		rec.mu.Unlock()
		if r, ok := reply[method]; ok {
			if err, isErr := r.(error); isErr {
				return nil, err
			}
			return r, nil
		}
		return map[string]any{}, nil
	}, nil)
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-1"
	s.conn = jsonrpc.NewConn(cr, cw, nil, s.handleNotification)
	t.Cleanup(func() { _ = cw.Close(); _ = sw.Close() })
	return s, server, rec
}

func nextFinished(t *testing.T, s *session) proto.TurnFinishedPayload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-s.events:
			if e.Type == proto.TurnFinished {
				return e.Payload.(proto.TurnFinishedPayload)
			}
		case <-timeout:
			t.Fatal("no turn.finished")
		}
	}
}

const limitCompletion = `{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"failed","error":{"message":"usage limit","codexErrorInfo":"rateLimitExceeded"}}}`

// TestLimitWithNoSnapshotReadsTheAccount: a turn's first request can hit the
// limit before codex pushes any snapshot, so the reset is read from the
// account before the turn finishes.
func TestLimitWithNoSnapshotReadsTheAccount(t *testing.T) {
	s, server, rec := wiredSession(t, map[string]any{
		"account/rateLimits/read": json.RawMessage(`{
			"rateLimits": {"limitId": "codex", "primary": {"usedPercent": 100, "resetsAt": 1789000000}},
			"rateLimitsByLimitId": {
				"codex": {"limitId": "codex", "primary": {"usedPercent": 100, "resetsAt": 1789000000}, "secondary": {"usedPercent": 100, "resetsAt": 1789300000}},
				"codex_bengalfox": {"limitId": "codex_bengalfox", "primary": {"usedPercent": 100, "resetsAt": 1789900000}}
			}
		}`),
	})
	if err := server.Notify("turn/completed", json.RawMessage(limitCompletion)); err != nil {
		t.Fatal(err)
	}

	fin := nextFinished(t, s)
	if fin.TurnID != "omniplex-turn" || fin.Failure != proto.FailureLimit {
		t.Fatalf("turn.finished = %+v, want the limited turn", fin)
	}
	if fin.ResetAt != 1789300000*1000 {
		t.Fatalf("ResetAt = %d, want the read's latest exhausted main window", fin.ResetAt)
	}
	if n := rec.count("account/rateLimits/read"); n != 1 {
		t.Fatalf("account/rateLimits/read sent %d times, want 1", n)
	}
	if s.currentTurn() != "" {
		t.Fatalf("turn still open: %q", s.currentTurn())
	}
}

func TestLimitWithFailedReadStillFinishes(t *testing.T) {
	s, server, _ := wiredSession(t, map[string]any{
		"account/rateLimits/read": errors.New("not signed in"),
	})
	if err := server.Notify("turn/completed", json.RawMessage(limitCompletion)); err != nil {
		t.Fatal(err)
	}

	fin := nextFinished(t, s)
	if fin.Failure != proto.FailureLimit || fin.ResetAt != 0 {
		t.Fatalf("turn.finished = %+v, want a limit with the reset unknown", fin)
	}
}
