package claudecode

import (
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// The bug this file guards against: Claude Code rotates its conversation id in
// place when the user runs /clear — a new conversation, under a new id, in the
// same process. omniplex names the conversation at start and used to assume that
// name was forever, so after a /clear a server restart would resume the
// *original* id: the cleared conversation came back from the dead, and the
// live post-clear conversation (with everything the agent had asked since) was
// silently stranded. The fix has two halves — report rotations, and honour the
// reported id on resume — tested separately below.

// A rotation observed on any top-level SDK message must be re-emitted as
// session.config_changed, which is what the projection folds into the
// HarnessSessionID a later resume passes back.
func TestSessionIDRotationIsReported(t *testing.T) {
	s := newTestSession()
	s.harnessSessionID = "omniplex-chose-this"

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "rotated-by-clear",
	}))

	var got []string
	for {
		select {
		case e := <-s.events:
			if p, ok := e.Payload.(proto.ThreadConfigChangedPayload); ok && p.HarnessSessionID != "" {
				got = append(got, p.HarnessSessionID)
			}
			continue
		default:
		}
		break
	}
	if len(got) == 0 || got[0] != "rotated-by-clear" {
		t.Fatalf("rotation not reported: config_changed ids = %v", got)
	}
	if s.harnessSessionID != "rotated-by-clear" {
		t.Fatalf("session still believes it is %q", s.harnessSessionID)
	}
}

// The same id arriving again is not news: re-emitting it on every message
// would flood the log with a config_changed per SDK message.
func TestUnchangedSessionIDIsNotReemitted(t *testing.T) {
	s := newTestSession()
	s.harnessSessionID = "stable"

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":       "result",
		"session_id": "stable",
	}))

	for {
		select {
		case e := <-s.events:
			if p, ok := e.Payload.(proto.ThreadConfigChangedPayload); ok && p.HarnessSessionID != "" {
				t.Fatalf("unchanged id re-emitted: %q", p.HarnessSessionID)
			}
			continue
		default:
		}
		break
	}
}

// Subagent messages carry the id of the Task that spawned them, not the
// conversation's identity; they must never rename the session.
func TestSubagentMessagesDoNotRenameTheSession(t *testing.T) {
	s := newTestSession()
	s.harnessSessionID = "the-conversation"

	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type":               "assistant",
		"session_id":         "a-subagent",
		"parent_tool_use_id": "toolu_123",
		"message":            map[string]any{},
	}))

	if s.harnessSessionID != "the-conversation" {
		t.Fatalf("a subagent message renamed the session to %q", s.harnessSessionID)
	}
}

// Resume must continue the conversation the harness says this session is —
// the rotated id when one was reported — and only fall back to omniplex's own name
// when the harness never spoke (sessions from before rotations were tracked).
func TestConversationIDPrefersTheHarnessIDOnResume(t *testing.T) {
	cases := []struct {
		name string
		o    adapter.CreateOptions
		want string
	}{
		{"fresh session uses omniplex's name", adapter.CreateOptions{ThreadID: "omniplex-id"}, "omniplex-id"},
		{"resume without a rotation uses omniplex's name", adapter.CreateOptions{ThreadID: "omniplex-id", Resume: true}, "omniplex-id"},
		{"resume after a rotation uses the rotated id", adapter.CreateOptions{ThreadID: "omniplex-id", Resume: true, HarnessSessionID: "rotated"}, "rotated"},
		{"a rotation never renames a fresh session", adapter.CreateOptions{ThreadID: "omniplex-id", HarnessSessionID: "rotated"}, "omniplex-id"},
	}
	for _, tc := range cases {
		if got := conversationID(tc.o); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
