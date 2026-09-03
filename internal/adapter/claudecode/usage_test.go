package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// rawSDK turns a value into the map[string]json.RawMessage shape the SDK-message
// handlers consume.
func rawSDK(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func newTestSession() *session {
	return &session{
		events:  make(chan proto.Emission, 256),
		done:    make(chan struct{}),
		streams: map[string]*stream{},
		steers:  map[string]bool{},
		model:   "claude-sonnet-test",
	}
}

// lastUsage drains the event channel and returns the most recent usage payload.
func lastUsage(t *testing.T, s *session) proto.UsageUpdatedPayload {
	t.Helper()
	var last proto.UsageUpdatedPayload
	found := false
	for {
		select {
		case e := <-s.events:
			if u, ok := e.Payload.(proto.UsageUpdatedPayload); ok {
				last, found = u, true
			}
		default:
			if !found {
				t.Fatal("no usage.updated emitted")
			}
			return last
		}
	}
}

// The bug this guards against: the result message's usage is a per-turn total,
// summed across every request in the turn. A turn with N tool calls makes N+1
// requests, each of which already carries the whole conversation, so summing
// them is roughly O(N²) in context size and reports a wildly inflated "used".
// The fix reports the final request's prompt size instead, which is the actual
// context in use. This synthetic turn with several tool calls asserts the
// reported occupancy tracks the final prompt, not the sum.
func TestResultOccupancyIsFinalPromptNotPerTurnSum(t *testing.T) {
	s := newTestSession()

	const rounds = 6
	// Each round's request carries a growing prompt; cache_read dominates, as it
	// does in a real conversation where nearly the whole context is re-read on
	// every tool round-trip.
	var perTurnInput, perTurnCacheRead, perTurnOutput int64
	var finalPrompt int64
	for k := 0; k <= rounds; k++ {
		input := int64(1000)
		cacheRead := int64(24000 + k*500)
		output := int64(400)
		perTurnInput += input
		perTurnCacheRead += cacheRead
		perTurnOutput += output
		finalPrompt = input + cacheRead // the prompt of the last request

		s.handleSDKMessage(rawSDK(t, map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []any{map[string]any{"type": "text"}},
				"usage": map[string]any{
					"input_tokens":                input,
					"cache_read_input_tokens":     cacheRead,
					"cache_creation_input_tokens": 0,
					"output_tokens":               output,
				},
			},
		}))
	}

	// The result carries the per-turn SUM — the number the old code used.
	perTurnSum := perTurnInput + perTurnCacheRead + perTurnOutput
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":           "result",
		"stop_reason":    "end_turn",
		"total_cost_usd": 0.1,
		"usage": map[string]any{
			"input_tokens":            perTurnInput,
			"cache_read_input_tokens": perTurnCacheRead,
			"output_tokens":           perTurnOutput,
		},
	}))

	u := lastUsage(t, s)
	if u.ContextUsed != finalPrompt {
		t.Fatalf("occupancy = %d, want the final prompt size %d", u.ContextUsed, finalPrompt)
	}
	if u.ContextUsed >= perTurnSum/2 {
		t.Fatalf("occupancy %d is close to the per-turn sum %d — the O(N²) bug is back", u.ContextUsed, perTurnSum)
	}
	// A short conversation must report a small fraction of the window.
	if u.ContextPct >= 20 {
		t.Fatalf("occupancy %.1f%% on a short turn is implausibly high", u.ContextPct)
	}
}

// When the harness reports its own occupancy, that is authoritative: used and
// window come from it, not from the fallback estimate or a hardcoded window.
func TestContextUsageIsAuthoritative(t *testing.T) {
	s := newTestSession()

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "context_usage",
		"usage": map[string]any{
			"model":                "claude-sonnet-test",
			"totalTokens":          42000,
			"maxTokens":            1000000,
			"rawMaxTokens":         200000,
			"percentage":           21,
			"isAutoCompactEnabled": true,
			"autoCompactThreshold": 184000,
			"categories": []any{
				map[string]any{"name": "System prompt", "tokens": 2000},
				map[string]any{"name": "Messages", "tokens": 40000},
				map[string]any{"name": "Free space", "tokens": 158000, "isDeferred": false},
			},
		},
	}))

	u := lastUsage(t, s)
	if u.ContextUsed != 42000 {
		t.Fatalf("used = %d, want 42000", u.ContextUsed)
	}
	if u.ContextWindow != 200000 {
		t.Fatalf("window = %d, want the 200000 compaction window", u.ContextWindow)
	}
	if u.ContextLimit != 1000000 {
		t.Fatalf("limit = %d, want the 1000000 model window", u.ContextLimit)
	}
	// The free-space row is not occupied context, so it must be dropped.
	if len(u.ContextCategories) != 2 {
		t.Fatalf("categories = %v, want the two used rows only", u.ContextCategories)
	}
	if !u.AutoCompact || u.AutoCompactThreshold != 184000 {
		t.Fatalf("auto-compaction = (%v, %d), want (true, 184000)", u.AutoCompact, u.AutoCompactThreshold)
	}
}

// The fallback must keep tracking later turns even after a context_usage
// report has arrived: if a subsequent report fails to arrive, the meter must
// reflect the newest turn rather than freeze on the last authoritative reading.
// It also reuses the window the harness last reported rather than the heuristic.
func TestFallbackRefreshesAfterContextUsage(t *testing.T) {
	s := newTestSession()

	// A first, authoritative report establishes the real window.
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "context_usage",
		"usage": map[string]any{
			"totalTokens":  50000,
			"rawMaxTokens": 160000,
			"maxTokens":    200000,
		},
	}))

	// A later turn whose context_usage never arrives — only an assistant
	// message (the new prompt size) and a result.
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{map[string]any{"type": "text"}},
			"usage": map[string]any{
				"input_tokens":            2000,
				"cache_read_input_tokens": 88000,
				"output_tokens":           500,
			},
		},
	}))
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":        "result",
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 2000, "cache_read_input_tokens": 88000},
	}))

	u := lastUsage(t, s)
	if u.ContextUsed != 90000 {
		t.Fatalf("used = %d, want the new prompt size 90000 (not frozen at 50000)", u.ContextUsed)
	}
	if u.ContextWindow != 160000 {
		t.Fatalf("window = %d, want the last reported window 160000, not the heuristic", u.ContextWindow)
	}
}

// The adapter must not clamp occupancy to 100%: an over-limit reading is a real
// signal, and clamping in the adapter is what hid the broken math. A test can
// only catch a >100% value if the adapter passes it through.
func TestOverLimitIsNotClamped(t *testing.T) {
	s := newTestSession()

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "context_usage",
		"usage": map[string]any{
			"totalTokens":  212000,
			"rawMaxTokens": 200000,
			"maxTokens":    200000,
		},
	}))

	u := lastUsage(t, s)
	if u.ContextPct <= 100 {
		t.Fatalf("pct = %.1f, want an unclamped value above 100", u.ContextPct)
	}
}

// A compaction boundary must surface as a canonical event carrying the token
// counts either side, so the transcript can show what happened.
func TestCompactBoundaryEmitsEvent(t *testing.T) {
	s := newTestSession()

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":    "system",
		"subtype": "compact_boundary",
		"compact_metadata": map[string]any{
			"trigger":     "auto",
			"pre_tokens":  180000,
			"post_tokens": 42000,
		},
	}))

	var got *proto.ContextCompactedPayload
	for {
		select {
		case e := <-s.events:
			if p, ok := e.Payload.(proto.ContextCompactedPayload); ok {
				got = &p
			}
			continue
		default:
		}
		break
	}
	if got == nil {
		t.Fatal("no context.compacted emitted")
	}
	if got.Trigger != "auto" || got.PreTokens != 180000 || got.PostTokens != 42000 {
		t.Fatalf("payload = %+v, want auto 180000→42000", *got)
	}
}
