package piapp

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// statusSession is a session mid-turn, as pi's retry and compaction events
// only ever matter inside one.
func statusSession() *session {
	return &session{
		host:    fakeHost{},
		events:  make(chan proto.Emission, 64),
		done:    make(chan struct{}),
		rpcDone: make(chan struct{}),
		pending: map[string]chan rpcResponse{},
		turnID:  "t1",
	}
}

func pending(s *session) []proto.Emission {
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

// feed hands pi's JSON lines to the translator the way readLoop does.
func feed(t *testing.T, s *session, lines ...string) []proto.Emission {
	t.Helper()
	for _, line := range lines {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			t.Fatalf("bad fixture %q: %v", line, err)
		}
		s.handleEvent(head.Type, json.RawMessage(line))
	}
	return pending(s)
}

func statuses(t *testing.T, es []proto.Emission) []proto.TurnStatusPayload {
	t.Helper()
	var out []proto.TurnStatusPayload
	for _, e := range es {
		if p, ok := e.Payload.(proto.TurnStatusPayload); ok {
			if e.Type != proto.TurnStatus {
				t.Fatalf("status payload under type %q", e.Type)
			}
			out = append(out, p)
		}
	}
	return out
}

func compactions(es []proto.Emission) []proto.ContextCompactedPayload {
	var out []proto.ContextCompactedPayload
	for _, e := range es {
		if p, ok := e.Payload.(proto.ContextCompactedPayload); ok {
			out = append(out, p)
		}
	}
	return out
}

// Fixtures copied from pi's docs/rpc.md.
const (
	compactionStart  = `{"type": "compaction_start", "reason": "threshold"}`
	compactionEndOK  = `{"type": "compaction_end", "reason": "threshold", "result": {"summary": "Summary of conversation...", "firstKeptEntryId": "abc123", "tokensBefore": 150000, "estimatedTokensAfter": 32000, "usage": {"input": 32000, "output": 1200, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 33200, "cost": {"input": 0.01, "output": 0.02, "cacheRead": 0, "cacheWrite": 0, "total": 0.03}}, "details": {}}, "aborted": false, "willRetry": false}`
	autoRetryStart   = `{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 3, "delayMs": 2000, "errorMessage": "529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}"}`
	autoRetryEndOK   = `{"type": "auto_retry_end", "success": true, "attempt": 2}`
	autoRetryEndFail = `{"type": "auto_retry_end", "success": false, "attempt": 3, "finalError": "529 overloaded_error: Overloaded"}`
)

func TestCompactionStatus(t *testing.T) {
	cleared := proto.TurnStatusPayload{}
	compacting := proto.TurnStatusPayload{State: proto.TurnCompacting}
	cases := []struct {
		name          string
		end           string
		wantCompacted bool
	}{
		{"success", compactionEndOK, true},
		{"aborted", `{"type":"compaction_end","reason":"manual","result":null,"aborted":true,"willRetry":false}`, false},
		{"failed", `{"type":"compaction_end","reason":"threshold","result":null,"aborted":false,"willRetry":false,"errorMessage":"quota exceeded"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := statusSession()
			es := feed(t, s, compactionStart, c.end)
			got := statuses(t, es)
			if want := []proto.TurnStatusPayload{compacting, cleared}; !reflect.DeepEqual(got, want) {
				t.Fatalf("statuses = %+v, want %+v", got, want)
			}
			comp := compactions(es)
			if !c.wantCompacted {
				if len(comp) != 0 {
					t.Fatalf("no boundary expected, got %+v", comp)
				}
				return
			}
			if len(comp) != 1 || comp[0].Trigger != "auto" || comp[0].PreTokens != 150000 || comp[0].PostTokens != 32000 {
				t.Fatalf("compaction = %+v", comp)
			}
		})
	}
}

func TestAutoRetryStatus(t *testing.T) {
	for _, end := range []string{autoRetryEndOK, autoRetryEndFail} {
		s := statusSession()
		before := time.Now()
		got := statuses(t, feed(t, s, autoRetryStart, end))
		after := time.Now()
		if len(got) != 2 {
			t.Fatalf("statuses = %+v, want retrying then clear", got)
		}
		r := got[0]
		if r.State != proto.TurnRetrying || r.Attempt != 1 || r.Max != 3 || r.Reason != "overloaded" {
			t.Fatalf("retrying = %+v", r)
		}
		lo, hi := before.Add(2*time.Second).UnixMilli(), after.Add(2*time.Second).UnixMilli()
		if r.RetryAt < lo || r.RetryAt > hi {
			t.Fatalf("RetryAt %d outside [%d, %d]", r.RetryAt, lo, hi)
		}
		if got[1] != (proto.TurnStatusPayload{}) {
			t.Fatalf("%s: want clear, got %+v", end, got[1])
		}
	}
}

func TestSummarizationRetryStatus(t *testing.T) {
	s := statusSession()
	got := statuses(t, feed(t, s,
		compactionStart,
		`{"type": "summarization_retry_scheduled", "attempt": 1, "maxAttempts": 3, "delayMs": 2000, "errorMessage": "terminated"}`,
		`{"type": "summarization_retry_attempt_start", "source": "compaction", "reason": "threshold"}`,
		`{"type": "summarization_retry_finished"}`,
		compactionEndOK,
	))
	states := make([]string, len(got))
	for i, p := range got {
		states[i] = p.State
	}
	want := []string{proto.TurnCompacting, proto.TurnRetrying, proto.TurnCompacting, ""}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %q, want %q", states, want)
	}
	if got[1].Attempt != 1 || got[1].Max != 3 || got[1].Reason != "connection lost" {
		t.Fatalf("retrying = %+v", got[1])
	}

	s = statusSession()
	got = statuses(t, feed(t, s, `{"type": "summarization_retry_attempt_start", "source": "branchSummary"}`))
	if len(got) != 1 || got[0] != (proto.TurnStatusPayload{}) {
		t.Fatalf("branch summary attempt should clear, got %+v", got)
	}
}

func TestRetryReason(t *testing.T) {
	long := "something unusual went wrong with the provider and it is explaining at length why"
	cases := []struct{ in, want string }{
		{`529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "overloaded"},
		{"529 overloaded_error: Overloaded", "overloaded"},
		{`429 {"error":{"message":"Rate limit reached for requests"}}`, "rate limit"},
		{"Too Many Requests", "rate limit"},
		{"Request timed out.", "timeout"},
		{"503 Service Unavailable", "server error"},
		{"502 Bad Gateway", "server error"},
		{"fetch failed", "connection lost"},
		{"terminated", "connection lost"},
		{"Unknown error", "Unknown error"},
		{"first line\nsecond line", "first line"},
		{long, "something unusual went wrong with the provider and it is ex…"},
		{"", ""},
	}
	for _, c := range cases {
		if got := retryReason(c.in); got != c.want {
			t.Errorf("retryReason(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
