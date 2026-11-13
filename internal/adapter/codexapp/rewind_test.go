package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

// A finished turn carries codex's id for it, which is what thread/revert
// takes: the one its completion names, else the one turn/start answered with.
func TestTurnFinishedCarriesCodexTurnID(t *testing.T) {
	for _, tc := range []struct {
		name, completion, want string
	}{
		{"completion names it", `{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"completed"}}`, "codex-turn-1"},
		{"completion names none", `{"threadId":"thread-1","turn":{"status":"completed"}}`, "codex-turn-1"},
		{"failed turn", `{"threadId":"thread-1","turn":{"id":"codex-turn-1","status":"failed","error":{"message":"boom"}}}`, "codex-turn-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := limitedSession(t)
			notify(s, "turn/completed", tc.completion)
			done := finishedTurns(drain(s))
			if len(done) != 1 || done[0].HarnessRef != tc.want {
				t.Fatalf("finished = %+v, want HarnessRef %q", done, tc.want)
			}
		})
	}
}

// A turn codex started on its own, whose id nothing named, has no ref.
func TestTurnFinishedWithoutAKnownCodexIDHasNoRef(t *testing.T) {
	s, _ := stopSession(t, nil)
	s.ensureTurn("")
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"status":"completed"}}`)
	done := finishedTurns(drain(s))
	if len(done) != 1 || done[0].HarnessRef != "" {
		t.Fatalf("finished = %+v, want no HarnessRef", done)
	}
}

// A review runs under a second turn id, but its ref is the one review/start
// answered with and its completion names.
func TestReviewFinishesWithTheAnsweredTurnID(t *testing.T) {
	s, _ := stopSession(t, reviewReply())
	if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "omniplex-turn", Action: "review"}); err != nil {
		t.Fatalf("review: %v", err)
	}
	notify(s, "turn/started", `{"threadId":"thread-1","turn":{"id":"running-turn"}}`)
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"review-turn","status":"completed"}}`)
	done := finishedTurns(drain(s))
	if len(done) != 1 || done[0].HarnessRef != "review-turn" {
		t.Fatalf("finished = %+v, want HarnessRef review-turn", done)
	}
}

// The turn.finished held back for a limit read carries the ref too.
func TestLimitFinishCarriesCodexTurnID(t *testing.T) {
	s, server, _ := wiredSession(t, map[string]any{
		"account/rateLimits/read": errors.New("not signed in"),
	})
	if err := server.Notify("turn/completed", json.RawMessage(limitCompletion)); err != nil {
		t.Fatal(err)
	}
	if fin := nextFinished(t, s); fin.HarnessRef != "codex-turn-1" {
		t.Fatalf("turn.finished = %+v, want HarnessRef codex-turn-1", fin)
	}
}

func TestRewindRevertsBeforeTheTurn(t *testing.T) {
	s, rec := stopSession(t, nil)
	if err := s.Rewind(context.Background(), adapter.RewindTarget{Turn: "codex-turn-2"}); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	raw := rec.sent("thread/revert")
	if raw == nil {
		t.Fatal("no thread/revert sent")
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["threadId"] != "thread-1" || got["beforeTurnId"] != "codex-turn-2" {
		t.Fatalf("thread/revert params = %s, want threadId thread-1 and beforeTurnId codex-turn-2", raw)
	}
}

func TestRewindSurfacesARefusal(t *testing.T) {
	s, _ := stopSession(t, map[string]any{
		"thread/revert": errors.New("no rollout found for thread id thread-1"),
	})
	err := s.Rewind(context.Background(), adapter.RewindTarget{Turn: "codex-turn-2"})
	if err == nil || !strings.Contains(err.Error(), "no rollout found") {
		t.Fatalf("Rewind err = %v, want codex's refusal", err)
	}
}

// Without a turn to cut at, or with a turn running, nothing reaches codex.
func TestRewindRefusesWithoutSendingAnything(t *testing.T) {
	for _, tc := range []struct {
		name    string
		turn    string
		running bool
	}{
		{"no turn", "", false},
		{"turn running", "codex-turn-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := stopSession(t, nil)
			if tc.running {
				s.turnID = "omniplex-turn"
			}
			if err := s.Rewind(context.Background(), adapter.RewindTarget{Turn: tc.turn}); err == nil {
				t.Fatal("Rewind succeeded")
			}
			if n := rec.count("thread/revert"); n != 0 {
				t.Fatalf("thread/revert sent %d times, want none", n)
			}
		})
	}
}

// A rewind forgets the latest turn, so a reset owed to it is not back-filled.
func TestRewindForgetsTheUnknownReset(t *testing.T) {
	s := finishUnknown(t)
	if err := s.Rewind(context.Background(), adapter.RewindTarget{Turn: "codex-turn-1"}); err != nil {
		t.Fatalf("Rewind: %v", err)
	}
	s.handleNotification("account/rateLimits/updated", json.RawMessage(
		`{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1789000000}}}`))
	if got := limitResets(drain(s)); len(got) != 0 {
		t.Fatalf("back-filled a rewound turn: %+v", got)
	}
}
