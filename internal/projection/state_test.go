package projection

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func event(t *testing.T, seq int64, typ string, payload any) proto.Event {
	t.Helper()
	blob, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return proto.Event{Seq: seq, Type: typ, Payload: blob}
}

// A harness-initiated turn has no prompt: it must open the turn and flip the
// phase, but not fabricate an empty user message in the timeline.
func TestHarnessInitiatedTurnHasNoPromptItem(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}))

	if s.Phase != "turn" {
		t.Fatalf("phase = %q, want turn", s.Phase)
	}
	if len(s.Turns) != 1 || s.Turns[0].ID != "t1" {
		t.Fatalf("turns = %+v, want one turn t1", s.Turns)
	}
	if len(s.Items) != 0 {
		t.Fatalf("items = %+v, want none: an unprompted turn has no user message", s.Items)
	}

	s.Apply(event(t, 2, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	if s.Phase != "idle" || !s.Turns[0].Done {
		t.Fatalf("after finish: phase=%q done=%v, want idle/true", s.Phase, s.Turns[0].Done)
	}
}

// A turn carries when it started and finished, from the events' own clock:
// the UI labels a folded turn "Worked for 34s" from these. Mirrored by
// web/src/apply.test.ts.
func TestTurnRecordsItsTimestamps(t *testing.T) {
	s := New("s1")
	started := event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"})
	started.Timestamp = 1000
	s.Apply(started)
	finished := event(t, 2, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn})
	finished.Timestamp = 35000
	s.Apply(finished)

	if s.Turns[0].StartedAt != 1000 || s.Turns[0].FinishedAt != 35000 {
		t.Fatalf("turn timestamps = %d/%d, want 1000/35000", s.Turns[0].StartedAt, s.Turns[0].FinishedAt)
	}
}

// A prompted turn still records the prompt as a timeline item.
func TestPromptedTurnKeepsItsPromptItem(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "do the thing"}))

	if len(s.Items) != 1 || s.Items[0].Text != "do the thing" {
		t.Fatalf("items = %+v, want the prompt item", s.Items)
	}
}

// Streaming while idle is evidence a turn is running that the log did not
// announce. The projection trusts the activity over the phase, so a lifecycle
// desync cannot freeze attached UIs. web/src/apply.ts mirrors this.
func TestStreamingWhileIdleImpliesTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: "", Role: "agent", Kind: "text", BlockID: "b1", Delta: "The web",
	}))
	if s.Phase != "turn" {
		t.Fatalf("phase after message.chunk while idle = %q, want turn", s.Phase)
	}

	s2 := New("s2")
	s2.Apply(event(t, 1, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		ToolCallID: "c1", Kind: proto.KindExecute, Title: "ls", Status: proto.StatusPending,
	}))
	if s2.Phase != "turn" {
		t.Fatalf("phase after tool_call.started while idle = %q, want turn", s2.Phase)
	}
}

// A finish for a turn that is not the open one — a stale close from an
// adapter, a duplicate — must not take the session idle while different work
// is running, and must not paint the running turn's tools as failed. Mirrored
// by web/src/apply.test.ts.
func TestStaleTurnFinishedDoesNotGoIdle(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindExecute, Title: "ls", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 3, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t-stale", StopReason: proto.StopError}))

	if s.Phase != "turn" {
		t.Fatalf("phase after stale finish = %q, want turn", s.Phase)
	}
	if s.Items[1].Status != proto.StatusInProgress {
		t.Fatalf("running tool status = %q, want in_progress untouched", s.Items[1].Status)
	}

	// The real finish still closes the turn.
	s.Apply(event(t, 4, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	if s.Phase != "idle" || !s.Turns[0].Done {
		t.Fatalf("after real finish: phase=%q done=%v, want idle/true", s.Phase, s.Turns[0].Done)
	}
	if s.Items[1].Status != proto.StatusFailed {
		t.Fatalf("tool status after its turn finished = %q, want failed", s.Items[1].Status)
	}
}

// A tool going active while idle is a running turn; a straggling completion of
// a background tool after the turn ended is not, and must not reopen
// "working" with nothing left to close it. Mirrored by web/src/apply.test.ts.
func TestToolUpdateWhileIdle(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: "c1", Status: proto.StatusCompleted,
	}))
	if s.Phase != "idle" {
		t.Fatalf("phase after straggling completion = %q, want idle", s.Phase)
	}

	s.Apply(event(t, 2, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: "c2", Status: proto.StatusInProgress,
	}))
	if s.Phase != "turn" {
		t.Fatalf("phase after tool going active = %q, want turn", s.Phase)
	}
}

// Attention is the derived whose-turn-is-it signal. A pending permission or
// question outranks the running turn: the agent is blocked on a human, so it
// is the user's turn, categorised.
func TestAttention(t *testing.T) {
	s := New("s1")
	if got := s.Attention(); got != AttentionNeedsPrompt {
		t.Fatalf("idle attention = %q, want needs_prompt", got)
	}

	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	if got := s.Attention(); got != AttentionWorking {
		t.Fatalf("mid-turn attention = %q, want working", got)
	}

	s.Apply(event(t, 2, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "r1", TurnID: "t1"}))
	if got := s.Attention(); got != AttentionNeedsPermission {
		t.Fatalf("pending-permission attention = %q, want needs_permission", got)
	}

	s.Apply(event(t, 3, proto.PermissionResolved, proto.PermissionResolvedPayload{RequestID: "r1", Outcome: proto.OutcomeAllowOnce}))
	if got := s.Attention(); got != AttentionWorking {
		t.Fatalf("attention after resolve = %q, want working", got)
	}

	s.Apply(event(t, 4, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "e1", Prompt: "which?"}))
	if got := s.Attention(); got != AttentionNeedsAnswer {
		t.Fatalf("pending-question attention = %q, want needs_answer", got)
	}
	s.Apply(event(t, 5, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "e1", Action: "accept"}))

	s.Apply(event(t, 6, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	if got := s.Attention(); got != AttentionNeedsPrompt {
		t.Fatalf("post-turn attention = %q, want needs_prompt", got)
	}

	s.Apply(event(t, 7, proto.SessionClosed, proto.SessionClosedPayload{Reason: "done"}))
	if got := s.Attention(); got != AttentionClosed {
		t.Fatalf("closed attention = %q, want closed", got)
	}
}

// The defence must not resurrect a closed session.
func TestStreamingDoesNotReopenClosedSession(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.SessionClosed, proto.SessionClosedPayload{Reason: "closed"}))
	s.Apply(event(t, 2, proto.MessageChunk, proto.MessageChunkPayload{
		Role: "agent", Kind: "text", BlockID: "b1", Delta: "late",
	}))
	if s.Phase != "closed" {
		t.Fatalf("phase = %q, want closed", s.Phase)
	}
}

// Effort is the one config field whose empty value is a choice rather than an
// absence: clearing it hands the level back to the harness. A payload that
// says so must be able to say so, or the composer keeps showing — and a
// restart keeps resuming — a level the session no longer runs at. Mirrored by
// web/src/apply.test.ts.
func TestClearingEffortSticks(t *testing.T) {
	s := New("s1")
	high, cleared := "high", ""

	s.Apply(event(t, 1, proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Effort: &high}))
	if s.Effort != "high" {
		t.Fatalf("effort = %q, want high", s.Effort)
	}

	s.Apply(event(t, 2, proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Effort: &cleared}))
	if s.Effort != "" {
		t.Fatalf("effort = %q after clearing, want empty", s.Effort)
	}

	// An event about something else still leaves effort alone.
	s.Apply(event(t, 3, proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Effort: &high}))
	s.Apply(event(t, 4, proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Model: "sonnet"}))
	if s.Effort != "high" {
		t.Fatalf("effort = %q after an unrelated change, want high", s.Effort)
	}
}

func TestWindowBoundsTurnsAndTheirItems(t *testing.T) {
	s := New("s1")
	for i, id := range []string{"t1", "t2", "t3", "t4"} {
		s.Turns = append(s.Turns, Turn{ID: id})
		s.Items = append(s.Items, Item{ID: "i" + id, TurnID: id}, Item{ID: "child" + id, TurnID: id, ParentID: "agent"})
		s.Seq = int64(i + 1)
	}
	s.Items = append(s.Items, Item{ID: "notice"})

	newest := s.Window(2, "", 0, "")
	if got := []string{newest.Turns[0].ID, newest.Turns[1].ID}; got[0] != "t3" || got[1] != "t4" {
		t.Fatalf("newest turns = %v, want [t3 t4]", got)
	}
	if newest.History == nil || !newest.History.HasMore || newest.History.BeforeTurn != "t3" {
		t.Fatalf("history = %+v, want more before t3", newest.History)
	}
	if len(newest.Items) != 5 { // two items per kept turn plus the notice
		t.Fatalf("newest items = %d, want 5", len(newest.Items))
	}

	older := s.Window(2, newest.History.BeforeTurn, 0, "")
	if got := []string{older.Turns[0].ID, older.Turns[1].ID}; got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("older turns = %v, want [t1 t2]", got)
	}
	if older.History == nil || older.History.HasMore {
		t.Fatalf("older history = %+v, want exhausted", older.History)
	}
	if len(s.Turns) != 4 || len(s.Items) != 9 {
		t.Fatal("window mutated canonical state")
	}
}

func TestWindowPagesItemsInsideOneLargeTurn(t *testing.T) {
	s := New("s1")
	s.Turns = []Turn{{ID: "t1"}}
	for _, id := range []string{"i1", "i2", "i3", "i4", "i5"} {
		s.Items = append(s.Items, Item{ID: id, TurnID: "t1"})
	}

	newest := s.Window(10, "", 2, "")
	if len(newest.Items) != 2 || newest.Items[0].ID != "i4" || newest.History.BeforeItem != "i4" {
		t.Fatalf("newest item page = %+v history=%+v", newest.Items, newest.History)
	}
	older := s.Window(10, "", 2, newest.History.BeforeItem)
	if len(older.Items) != 2 || older.Items[0].ID != "i2" || older.History.BeforeItem != "i2" {
		t.Fatalf("older item page = %+v history=%+v", older.Items, older.History)
	}
	oldest := s.Window(10, "", 2, older.History.BeforeItem)
	if len(oldest.Items) != 1 || oldest.Items[0].ID != "i1" || oldest.History.HasMore {
		t.Fatalf("oldest item page = %+v history=%+v", oldest.Items, oldest.History)
	}
}
