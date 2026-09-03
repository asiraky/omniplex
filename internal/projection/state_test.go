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
	return proto.Event{Seq: seq, Type: typ, Payload: blob, Timestamp: seq * 1000}
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
	if s.Items[1].Status != proto.StatusCancelled {
		t.Fatalf("tool status after its turn finished = %q, want cancelled", s.Items[1].Status)
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

// A tool standing for a live job outlives its turn: the turn's finish leaves
// it in flight, and the job's own finish settles it. Attention reports
// "background" for the gap. Mirrored by web/src/apply.test.ts.
func TestJobOutlivesTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindAgent, Title: "Task", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 3, proto.JobStarted, proto.JobPayload{JobID: "j1", ToolCallID: "c1", TaskType: "subagent", Name: "Explore"}))
	s.Apply(event(t, 4, proto.JobUpdated, proto.JobPayload{JobID: "j1", Usage: &proto.JobUsage{TotalTokens: 500}, Activity: "Read"}))
	s.Apply(event(t, 5, proto.JobStarted, proto.JobPayload{JobID: "j2", ParentJobID: "j1", TaskType: "local_bash"}))

	j := s.job("j1")
	if j == nil || j.Kind != proto.JobAgent || j.Name != "Explore" || j.Activity != "Read" || j.Usage.TotalTokens != 500 || j.TurnID != "t1" {
		t.Fatalf("job after merge = %+v", j)
	}
	if c := s.job("j2"); c.Kind != proto.JobShell || c.Depth != 1 {
		t.Fatalf("child job = %+v, want shell at depth 1", c)
	}

	s.Apply(event(t, 6, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	if s.Items[1].Status != proto.StatusInProgress {
		t.Fatalf("tool with live job after turn finish = %q, want in_progress", s.Items[1].Status)
	}
	if got := s.Attention(); got != AttentionBackground {
		t.Fatalf("attention with live jobs = %q, want background", got)
	}

	s.Apply(event(t, 7, proto.JobFinished, proto.JobPayload{JobID: "j2", Status: proto.JobStopped}))
	s.Apply(event(t, 8, proto.JobFinished, proto.JobPayload{JobID: "j1", Status: proto.JobStopped}))
	if s.Items[1].Status != proto.StatusCancelled {
		t.Fatalf("tool after job stopped = %q, want cancelled", s.Items[1].Status)
	}
	if s.job("j1").FinishedAt == 0 || s.LiveJobs().Any() || s.Attention() != AttentionNeedsPrompt {
		t.Fatalf("after finish: job=%+v attention=%q", s.job("j1"), s.Attention())
	}
}

// TestInjectedPromptJoinsTheTurn: a prompt the harness read into the running
// turn leaves the queue and becomes a user message in that turn, placed after
// the work already done, carrying the text and images the queue entry had.
func TestInjectedPromptJoinsTheTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c1", Title: "ls"}))
	s.Apply(event(t, 3, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "also this", Images: []proto.PromptImage{{ID: "img"}}, Sent: true}))
	if len(s.Queued) != 1 || !s.Queued[0].Sent {
		t.Fatalf("queued = %+v", s.Queued)
	}
	s.Apply(event(t, 4, proto.PromptInjected, proto.PromptInjectedPayload{QueueID: "q1", TurnID: "t1"}))
	if len(s.Queued) != 0 {
		t.Fatalf("queue after injection = %+v", s.Queued)
	}
	last := s.Items[len(s.Items)-1]
	if last.ID != "prompt:q1" || last.Role != "user" || last.Text != "also this" || last.TurnID != "t1" || len(last.Images) != 1 {
		t.Fatalf("injected item = %+v", last)
	}
	if s.Phase != "turn" || len(s.Turns) != 1 {
		t.Fatalf("phase=%s turns=%d", s.Phase, len(s.Turns))
	}
	// Injecting something not in the queue is not an event.
	before := len(s.Items)
	s.Apply(event(t, 5, proto.PromptInjected, proto.PromptInjectedPayload{QueueID: "nope", TurnID: "t1"}))
	if len(s.Items) != before {
		t.Fatalf("unknown injection added an item")
	}
}

// TestHeldPromptTurnFillsFromTheQueue: a turn the harness started from a
// prompt it was holding carries only the text; the queue entry supplies the
// images, and the title comes from the prompt as usual.
func TestHeldPromptTurnFillsFromTheQueue(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "next", Images: []proto.PromptImage{{ID: "img"}}, Sent: true}))
	s.Apply(event(t, 2, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t2", QueueID: "q1"}))
	if len(s.Queued) != 0 || len(s.Turns) != 1 {
		t.Fatalf("queued=%+v turns=%+v", s.Queued, s.Turns)
	}
	if s.Turns[0].Prompt != "next" || len(s.Turns[0].Images) != 1 || s.Title != "next" {
		t.Fatalf("turn = %+v title=%q", s.Turns[0], s.Title)
	}
	it := s.Items[len(s.Items)-1]
	if it.ID != "prompt:t2" || it.Text != "next" || len(it.Images) != 1 {
		t.Fatalf("prompt item = %+v", it)
	}
}
