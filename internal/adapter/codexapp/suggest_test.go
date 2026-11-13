package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// suggestions returns the turn.suggested payloads among the emissions.
func suggestions(got []proto.Emission) []proto.TurnSuggestedPayload {
	var out []proto.TurnSuggestedPayload
	for _, e := range got {
		if e.Type == proto.TurnSuggested {
			out = append(out, e.Payload.(proto.TurnSuggestedPayload))
		}
	}
	return out
}

func sampleReply(text any) map[string]any {
	return map[string]any{
		"thread/sample": map[string]any{"responseId": "resp-1", "sourceTurnId": "codex-turn-1", "text": text},
	}
}

// A turn that ends normally is followed by one thread/sample for that turn,
// and its answer is suggested for our turn, not codex's. Nothing else asks:
// not with the setting off, and not after a stopped or failed turn, whose
// next step is the human's to decide.
func TestSuggestionFollowsOnlyANormalEnd(t *testing.T) {
	for _, tc := range []struct {
		name    string
		suggest bool
		turn    string
		want    bool
	}{
		{"normal end", true, `{"id":"codex-turn-1","status":"completed"}`, true},
		{"setting off", false, `{"id":"codex-turn-1","status":"completed"}`, false},
		{"interrupted", true, `{"id":"codex-turn-1","status":"interrupted"}`, false},
		{"failed", true, `{"id":"codex-turn-1","status":"failed","error":{"message":"boom"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := stopSession(t, sampleReply("  run the tests\n"))
			s.suggest = tc.suggest
			s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-1"

			notify(s, "turn/completed", `{"threadId":"thread-1","turn":`+tc.turn+`}`)

			if !tc.want {
				// The quota read goes out from the same place as the sample
				// would; once it has, give a stray sample a moment to land.
				waitFor(t, "the post-turn quota read", func() bool { return rec.count("account/rateLimits/read") > 0 })
				time.Sleep(50 * time.Millisecond)
				if n := rec.count("thread/sample"); n != 0 {
					t.Fatalf("thread/sample sent %d times, want none", n)
				}
				if got := suggestions(drain(s)); len(got) != 0 {
					t.Fatalf("suggested %v, want nothing", got)
				}
				return
			}

			var got []proto.TurnSuggestedPayload
			waitFor(t, "turn.suggested", func() bool {
				got = append(got, suggestions(drain(s))...)
				return len(got) > 0
			})
			if want := (proto.TurnSuggestedPayload{TurnID: "omniplex-turn", Text: "run the tests"}); got[0] != want {
				t.Fatalf("suggested %+v, want %+v", got[0], want)
			}
			var p struct {
				ThreadID       string `json:"threadId"`
				ExpectedTurnID string `json:"expectedTurnId"`
			}
			if err := json.Unmarshal(rec.sent("thread/sample"), &p); err != nil {
				t.Fatal(err)
			}
			if p.ThreadID != "thread-1" || p.ExpectedTurnID != "codex-turn-1" {
				t.Fatalf("thread/sample params = %+v", p)
			}
		})
	}
}

// A refused sample, or one that answers with no text, suggests nothing.
func TestSuggestionDropsFailuresAndEmptyText(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply map[string]any
	}{
		{"refused", map[string]any{"thread/sample": errors.New("sample source turn must be the latest finished turn")}},
		{"null text", sampleReply(nil)},
		{"blank text", sampleReply(" \n\t ")},
		{"declined", sampleReply("NONE")},
		{"declined, dressed up", sampleReply(" none.\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := stopSession(t, tc.reply)
			s.lastTurn = "omniplex-turn"
			s.suggestNext("omniplex-turn", "codex-turn-1")
			if rec.count("thread/sample") != 1 {
				t.Fatal("no thread/sample sent")
			}
			if got := suggestions(drain(s)); len(got) != 0 {
				t.Fatalf("suggested %v, want nothing", got)
			}
		})
	}
}

// A suggestion that comes back after the human has started the next turn is
// for a turn that is no longer the latest, and is dropped.
func TestSuggestionYieldsToTheNextTurn(t *testing.T) {
	var s *session
	s, _ = stopSession(t, map[string]any{
		"thread/sample": func(json.RawMessage) (any, error) {
			s.mu.Lock()
			s.turnID = "next-turn"
			s.mu.Unlock()
			return map[string]any{"responseId": "r", "sourceTurnId": "codex-turn-1", "text": "continue"}, nil
		},
	})
	s.lastTurn = "omniplex-turn"
	s.suggestNext("omniplex-turn", "codex-turn-1")
	if got := suggestions(drain(s)); len(got) != 0 {
		t.Fatalf("suggested %v after the next turn started, want nothing", got)
	}
}

// Closing the session abandons a sample still waiting on codex rather than
// leaving it for the full timeout.
func TestSuggestionStopsWithTheSession(t *testing.T) {
	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })
	s, _ := stopSession(t, map[string]any{
		"thread/sample": func(json.RawMessage) (any, error) {
			<-hung
			return nil, nil
		},
	})
	s.lastTurn = "omniplex-turn"
	returned := make(chan struct{})
	go func() {
		s.suggestNext("omniplex-turn", "codex-turn-1")
		close(returned)
	}()
	close(s.done)
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("suggestNext still waiting after the session closed")
	}
}

// The session opts into the experimental API, which thread/sample needs, only
// when it will suggest: otherwise it stays on the stable one.
func TestExperimentalAPIOnlyWhenSuggesting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	for _, suggest := range []bool{true, false} {
		dir := t.TempDir()
		s, err := New(fakeAppServer(t, dir)).CreateSession(context.Background(), &elicitHost{},
			adapter.CreateOptions{Cwd: dir, SuggestPrompts: suggest})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		_ = s.Close()
		raw, err := os.ReadFile(filepath.Join(dir, "lines"))
		if err != nil {
			t.Fatal(err)
		}
		sent := false
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var m struct {
				Method string `json:"method"`
				Params struct {
					Capabilities struct {
						ExperimentalAPI bool `json:"experimentalApi"`
					} `json:"capabilities"`
				} `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("line %q: %v", line, err)
			}
			if m.Method == "initialize" {
				sent = true
				if m.Params.Capabilities.ExperimentalAPI != suggest {
					t.Fatalf("suggest=%v: initialize sent experimentalApi=%v", suggest, m.Params.Capabilities.ExperimentalAPI)
				}
			}
		}
		if !sent {
			t.Fatal("no initialize sent")
		}
	}
}
