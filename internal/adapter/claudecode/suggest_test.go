package claudecode

import (
	"context"
	"io"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

func suggestion(text string) map[string]any {
	return map[string]any{
		"type":       "prompt_suggestion",
		"suggestion": text,
		"uuid":       "0b6c1d2e-3f40-4a51-8b62-7c83d94ea5f6",
		"session_id": "conv",
	}
}

func suggestions(s *session) []proto.TurnSuggestedPayload {
	var out []proto.TurnSuggestedPayload
	for _, e := range drainEvents(s) {
		if e.Type == proto.TurnSuggested {
			out = append(out, e.Payload.(proto.TurnSuggestedPayload))
		}
	}
	return out
}

// endTurn runs a turn to a result with the given SDK fields.
func endTurn(t *testing.T, s *session, turn string, result map[string]any) {
	t.Helper()
	s.turnID = turn
	msg := map[string]any{"type": "result", "subtype": "success"}
	for k, v := range result {
		msg[k] = v
	}
	s.handleSDKMessage(rawSDK(t, msg))
}

// TestSuggestionFollowsTheTurnThatEnded: the suggestion arrives after the
// result has cleared the turn, and still names that turn.
func TestSuggestionFollowsTheTurnThatEnded(t *testing.T) {
	s := newTestSession()
	endTurn(t, s, "turn-1", nil)
	s.handleSDKMessage(rawSDK(t, suggestion("  run the\n tests ")))

	got := suggestions(s)
	if len(got) != 1 || got[0].TurnID != "turn-1" || got[0].Text != "run the tests" {
		t.Fatalf("suggestions = %+v, want one for turn-1", got)
	}

	// At most one per turn: a second is not attached to it again.
	s.handleSDKMessage(rawSDK(t, suggestion("again")))
	if got := suggestions(s); len(got) != 0 {
		t.Fatalf("second suggestion emitted %+v", got)
	}
}

func TestEmptySuggestionEmitsNothing(t *testing.T) {
	s := newTestSession()
	endTurn(t, s, "turn-1", nil)
	s.handleSDKMessage(rawSDK(t, suggestion(" \n ")))
	if got := suggestions(s); len(got) != 0 {
		t.Fatalf("empty suggestion emitted %+v", got)
	}
}

// TestSuggestionDroppedOnceTheNextTurnStarted: by the time a slow suggestion
// lands, the human may have sent the next prompt. It would belong to a turn
// that is no longer the latest, and must not be put on the new one either.
func TestSuggestionDroppedOnceTheNextTurnStarted(t *testing.T) {
	cases := map[string]func(s *session){
		"prompted": func(s *session) {
			pr, pw := io.Pipe()
			t.Cleanup(func() { _ = pw.Close() })
			s.conn = jsonrpc.NewConn(pr, io.Discard, nil, nil, nil)
			if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "turn-2", Text: "next"}); err != nil {
				t.Fatal(err)
			}
		},
		// The harness resuming on its own opens a turn through ensureTurn.
		"harness resumed": func(s *session) { s.ensureTurn() },
		// A steer read once the turn had ended starts the CLI's own turn.
		"steer read": func(s *session) {
			s.steers["q1"] = false
			s.handleSDKMessage(rawSDK(t, replay(t, "q1", "later")))
		},
	}
	for name, start := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestSession()
			endTurn(t, s, "turn-1", nil)
			start(s)
			s.handleSDKMessage(rawSDK(t, suggestion("run the tests")))
			if got := suggestions(s); len(got) != 0 {
				t.Fatalf("stale suggestion emitted %+v", got)
			}
			// Nor does it wait for that turn to end and attach to it.
			s.turnID = ""
			s.handleSDKMessage(rawSDK(t, suggestion("run the tests")))
			if got := suggestions(s); len(got) != 0 {
				t.Fatalf("stale suggestion emitted after the next turn %+v", got)
			}
		})
	}
}

// TestNoSuggestionAfterAnAbnormalEnd: only a turn that ended normally is
// offered a next prompt.
func TestNoSuggestionAfterAnAbnormalEnd(t *testing.T) {
	cases := map[string]map[string]any{
		"error":      {"subtype": "error_during_execution", "is_error": true},
		"max tokens": {"stop_reason": "max_tokens"},
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestSession()
			endTurn(t, s, "turn-0", nil) // a normal end before it must not leak through
			drainEvents(s)
			endTurn(t, s, "turn-1", result)
			s.handleSDKMessage(rawSDK(t, suggestion("try again")))
			if got := suggestions(s); len(got) != 0 {
				t.Fatalf("suggestion after %s emitted %+v", name, got)
			}
		})
	}
}

// TestNoSuggestionAfterASuppressedTurn: the turn the CLI runs from a discarded
// steer is invisible, and so is its suggestion; it is not the previous turn's.
func TestNoSuggestionAfterASuppressedTurn(t *testing.T) {
	s := newTestSession()
	endTurn(t, s, "turn-1", nil)
	s.suppress = true
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "subtype": "success"}))
	s.handleSDKMessage(rawSDK(t, suggestion("keep going")))
	if got := suggestions(s); len(got) != 0 {
		t.Fatalf("suppressed turn's suggestion emitted %+v", got)
	}
}

func TestPromptSuggestionsReachTheBridgeOnlyWhenAsked(t *testing.T) {
	for _, on := range []bool{false, true} {
		cfg, _ := startStandIn(t, adapter.CreateOptions{SuggestPrompts: on})
		if cfg.PromptSuggestions != on {
			t.Fatalf("SuggestPrompts=%v: promptSuggestions = %v", on, cfg.PromptSuggestions)
		}
	}
}
