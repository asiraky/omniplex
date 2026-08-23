package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

// An account that needs to log in again is the single most common way a
// session refuses to start, and it is invisible from the outside: Claude Code
// exits at once, the SDK throws, and the bridge dies. Before this, the throw
// went to the server log and the turn was left for someone else to explain —
// which the rest of the system could only do as "the server restarted".
func TestLoginFailureIsNamed(t *testing.T) {
	for _, fatal := range []string{
		"Error: Invalid API key · Please run /login",
		"Error: Claude Code process exited with code 1: authentication_error",
		"Error: OAuth token has expired",
	} {
		msg, ok := loginRequired(fatal)
		if !ok {
			t.Fatalf("%q was not recognised as a login failure", fatal)
		}
		if !strings.Contains(msg, "sign in") {
			t.Fatalf("login message does not say what to do: %q", msg)
		}
	}
	if _, ok := loginRequired("Error: ENOENT: no such file or directory"); ok {
		t.Fatal("an unrelated failure was reported as a login failure")
	}
}

// newBridgeSession wires a session to a pipe standing in for the bridge's
// stdout, so a test can make the bridge say something and then die.
func newBridgeSession(t *testing.T) (*session, *io.PipeWriter) {
	t.Helper()
	pr, pw := io.Pipe()
	s := &session{
		host:    &fakeHost{},
		events:  make(chan proto.Emission, 16),
		done:    make(chan struct{}),
		streams: map[string]*stream{},
	}
	s.conn = jsonrpc.NewConn(pr, io.Discard, s.handleRequest, s.handleNotification)
	go s.watchExit()
	t.Cleanup(func() { _ = pw.Close() })
	return s, pw
}

// The turn that was running when the bridge died must say why it died. A
// generic "the process is gone" is what sent everyone downstream looking for a
// restart that never happened.
func TestDyingBridgeExplainsTheTurnItKilled(t *testing.T) {
	s, pw := newBridgeSession(t)
	s.turnID = "turn-1"

	if _, err := io.WriteString(pw, `{"jsonrpc":"2.0","method":"fatal","params":{"message":"Error: Invalid API key · Please run /login"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	// Give the read loop the frame before the stream ends; closing both at
	// once would race the notification against the exit.
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.fatal != ""
	})
	_ = pw.Close()

	select {
	case em := <-s.events:
		p, ok := em.Payload.(proto.TurnFinishedPayload)
		if !ok || em.Type != proto.TurnFinished {
			t.Fatalf("expected turn.finished, got %s", em.Type)
		}
		if p.StopReason != proto.StopError {
			t.Fatalf("stop reason = %q, want error", p.StopReason)
		}
		if !strings.Contains(p.Error, "sign in") {
			t.Fatalf("turn error does not name the login failure: %q", p.Error)
		}
		if p.Failure != proto.FailureAuth {
			t.Fatalf("failure kind = %q, want %q", p.Failure, proto.FailureAuth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the bridge died without finishing its turn")
	}
}

// A prompt sent to a bridge that is already dead used to open a turn that
// nothing would ever close: the write failed on a broken pipe, or worse
// succeeded into a pipe nobody reads. Refusing up front, with the reason,
// keeps the log honest.
func TestPromptRefusesADeadBridge(t *testing.T) {
	s, pw := newBridgeSession(t)
	if _, err := io.WriteString(pw, `{"jsonrpc":"2.0","method":"fatal","params":{"message":"Error: Please run /login"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.fatal != ""
	})
	_ = pw.Close()
	waitFor(t, func() bool {
		select {
		case <-s.conn.Done():
			return true
		default:
			return false
		}
	})

	err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "turn-2", Text: "hello"})
	if err == nil {
		t.Fatal("prompting a dead bridge succeeded")
	}
	if !strings.Contains(err.Error(), "sign in") {
		t.Fatalf("refusal does not name the login failure: %q", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// The bridge does not die when Claude is not logged in: the harness answers
// with an error result and stays up. That result carries the whole
// explanation, and dropping it left the turn saying only that something went
// wrong — which the UI could offer nothing for but "continue where it left
// off", a button whose prompt announces a server restart that never happened.
func TestErrorResultCarriesTheHarnessReason(t *testing.T) {
	s, _ := newBridgeSession(t)
	s.turnID = "turn-3"

	s.handleSDKMessage(rawMessage(t, map[string]any{
		"type":            "result",
		"subtype":         "success",
		"is_error":        true,
		"terminal_reason": "api_error",
		"result":          "Not logged in · Please run /login",
	}))

	for {
		select {
		case em := <-s.events:
			if em.Type != proto.TurnFinished {
				continue // usage lands first
			}
			p := em.Payload.(proto.TurnFinishedPayload)
			if p.StopReason != proto.StopError {
				t.Fatalf("stop reason = %q, want error", p.StopReason)
			}
			if !strings.Contains(p.Error, "sign in") {
				t.Fatalf("turn error does not name the login failure: %q", p.Error)
			}
			if p.Failure != proto.FailureAuth {
				t.Fatalf("failure kind = %q, want %q", p.Failure, proto.FailureAuth)
			}
			return
		case <-time.After(2 * time.Second):
			t.Fatal("the failed turn never finished")
		}
	}
}

// A failure the classifier does not recognise is still passed through in the
// harness's own words rather than swallowed.
func TestErrorResultWithoutAKnownCauseStillSaysSomething(t *testing.T) {
	if got, kind := resultFailure("", "api_error"); got != "the turn failed: api_error" || kind != "" {
		t.Fatalf("bare terminal reason = %q (%q)", got, kind)
	}
	if got, _ := resultFailure("Credit balance too low", ""); got != "Credit balance too low" {
		t.Fatalf("harness wording was not preserved: %q", got)
	}
	if got, _ := resultFailure("", ""); got == "" {
		t.Fatal("a failed turn was left with no explanation at all")
	}
}

func rawMessage(t *testing.T, m map[string]any) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out[k] = b
	}
	return out
}
