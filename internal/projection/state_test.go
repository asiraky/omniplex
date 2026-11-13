package projection

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

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
// adapter, a duplicate — must not take the thread idle while different work
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

	s.Apply(event(t, 7, proto.ThreadClosed, proto.ThreadClosedPayload{Reason: "done"}))
	if got := s.Attention(); got != AttentionClosed {
		t.Fatalf("closed attention = %q, want closed", got)
	}
}

// A failed turn goes idle like any other, so failure is its own signal: set
// by the latest turn ending in an error, cleared by the next turn starting.
func TestLastTurnFailed(t *testing.T) {
	s := New("s1")
	if s.LastTurnFailed() {
		t.Fatal("a thread with no turns reports a failed turn")
	}

	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	if s.LastTurnFailed() {
		t.Fatal("a turn that ended normally reports failed")
	}

	s.Apply(event(t, 3, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t2", Prompt: "again"}))
	s.Apply(event(t, 4, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t2", StopReason: proto.StopError, Error: "boom"}))
	if !s.LastTurnFailed() {
		t.Fatal("a turn that ended in an error does not report failed")
	}
	if got := s.Attention(); got != AttentionNeedsPrompt {
		t.Fatalf("failed-turn attention = %q, want needs_prompt", got)
	}

	s.Apply(event(t, 5, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t3", Prompt: "retry"}))
	if s.LastTurnFailed() {
		t.Fatal("a new turn after a failed one still reports failed")
	}
	s.Apply(event(t, 6, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t3", StopReason: proto.StopEndTurn}))
	if s.LastTurnFailed() {
		t.Fatal("a turn that succeeded after a failed one reports failed")
	}
}

// The defence must not resurrect a closed thread.
func TestStreamingDoesNotReopenClosedThread(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadClosed, proto.ThreadClosedPayload{Reason: "closed"}))
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
// restart keeps resuming — a level the thread no longer runs at. Mirrored by
// web/src/apply.test.ts.
func TestClearingEffortSticks(t *testing.T) {
	s := New("s1")
	high, cleared := "high", ""

	s.Apply(event(t, 1, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Effort: &high}))
	if s.Effort != "high" {
		t.Fatalf("effort = %q, want high", s.Effort)
	}

	s.Apply(event(t, 2, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Effort: &cleared}))
	if s.Effort != "" {
		t.Fatalf("effort = %q after clearing, want empty", s.Effort)
	}

	// An event about something else still leaves effort alone.
	s.Apply(event(t, 3, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Effort: &high}))
	s.Apply(event(t, 4, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: "sonnet"}))
	if s.Effort != "high" {
		t.Fatalf("effort = %q after an unrelated change, want high", s.Effort)
	}
}

// Turning Fast off is a change like any other, so it sticks; events about
// anything else, including the effort beside it, leave the tier alone.
func TestClearingServiceTierSticks(t *testing.T) {
	s := New("s1")
	fast, cleared, high := "priority", "", "high"

	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: "/w", Harness: "codex", ServiceTier: fast}))
	if s.ServiceTier != "priority" {
		t.Fatalf("service tier = %q after creation, want priority", s.ServiceTier)
	}

	s.Apply(event(t, 2, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{ServiceTier: &cleared}))
	if s.ServiceTier != "" {
		t.Fatalf("service tier = %q after clearing, want empty", s.ServiceTier)
	}

	s.Apply(event(t, 3, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{ServiceTier: &fast}))
	s.Apply(event(t, 4, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: "gpt-5.6-sol"}))
	s.Apply(event(t, 5, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Effort: &cleared}))
	s.Apply(event(t, 6, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{ReplaceSettings: true, Model: "gpt-5.5", Mode: "auto"}))
	s.Apply(event(t, 7, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Effort: &high}))
	if s.ServiceTier != "priority" {
		t.Fatalf("service tier = %q after unrelated changes, want priority", s.ServiceTier)
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

// A subagent keeps working after the turn that spawned it has ended. Its own
// output, parented to its job's call, is recorded against the job but does
// not reopen the parent's turn, and its running command survives the turn's
// finish while the parent's own stray tool is cancelled. Mirrored by
// web/src/apply.test.ts.
func TestSubagentWorkOutlivesParentTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindAgent, Title: "spawn", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 3, proto.JobStarted, proto.JobPayload{JobID: "j1", ToolCallID: "c1", TaskType: "subagent"}))
	s.Apply(event(t, 4, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "sub1", ParentToolCallID: "c1", Kind: proto.KindExecute, Title: "go test", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 5, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "own1", Kind: proto.KindExecute, Title: "ls", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 6, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))

	if it, _ := s.ItemByID("sub1"); it.Status != proto.StatusInProgress {
		t.Fatalf("subagent's tool after parent turn finished = %q, want in_progress", it.Status)
	}
	if it, _ := s.ItemByID("own1"); it.Status != proto.StatusCancelled {
		t.Fatalf("parent's own tool after its turn finished = %q, want cancelled", it.Status)
	}

	s.Apply(event(t, 7, proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: "t1", Role: "agent", Kind: "text", BlockID: "b1", Delta: "found it", ParentToolCallID: "c1",
	}))
	s.Apply(event(t, 8, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "sub2", ParentToolCallID: "c1", Kind: proto.KindRead, Title: "read", Status: proto.StatusPending,
	}))
	s.Apply(event(t, 9, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: "sub2", ParentToolCallID: "c1", Status: proto.StatusInProgress,
	}))
	if s.Phase != "idle" {
		t.Fatalf("phase after subagent output while idle = %q, want idle", s.Phase)
	}
	if it, ok := s.ItemByID("b1"); !ok || it.ParentID != "c1" || it.Text != "found it" {
		t.Fatalf("subagent message = %+v, want recorded under c1", it)
	}
	if it, ok := s.ItemByID("sub2"); !ok || it.ParentID != "c1" || it.Status != proto.StatusInProgress {
		t.Fatalf("subagent tool = %+v, want in_progress under c1", it)
	}
}

// A subagent's running command keeps streaming after its parent's turn ends,
// so the turn finishing leaves its tail; the job finishing settles the
// command and drops the tail with it. Mirrored by web/src/apply.test.ts.
func TestSubagentCommandTailOutlivesParentTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: proto.KindAgent, Title: "spawn", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 3, proto.JobStarted, proto.JobPayload{JobID: "j1", ToolCallID: "c1", TaskType: "subagent"}))
	s.Apply(event(t, 4, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "sub1", ParentToolCallID: "c1", Kind: proto.KindExecute, Title: "go test", Status: proto.StatusInProgress,
	}))
	s.Apply(event(t, 5, proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: "sub1", OutputDelta: "ok 1\n"}))
	s.Apply(event(t, 6, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))

	if it, _ := s.ItemByID("sub1"); it.Status != proto.StatusInProgress || it.Tail != "ok 1\n" {
		t.Fatalf("subagent's command after parent turn finished = %q tail %q, want in_progress with its tail", it.Status, it.Tail)
	}

	s.Apply(event(t, 7, proto.JobFinished, proto.JobPayload{JobID: "j1", ToolCallID: "c1", Status: proto.JobStopped}))
	if it, _ := s.ItemByID("sub1"); it.Status != proto.StatusCancelled || it.Tail != "" {
		t.Fatalf("subagent's command after its job finished = %q tail %q, want cancelled with no tail", it.Status, it.Tail)
	}
}

// A stopped subagent's command was spared by its parent's turn finishing, for
// the job's sake; the job finishing leaves nobody to settle it, so it does.
// Another job's work, and work already done, are untouched, and a completion
// the harness sends later still lands. Mirrored by web/src/apply.test.ts.
func TestFinishedJobCancelsItsUnfinishedWork(t *testing.T) {
	s := New("s1")
	var seq int64
	apply := func(typ string, payload any) {
		seq++
		s.Apply(event(t, seq, typ, payload))
	}
	apply(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"})
	for _, c := range []string{"c1", "c2"} {
		apply(proto.ToolCallStarted, proto.ToolCallStartedPayload{
			TurnID: "t1", ToolCallID: c, Kind: proto.KindAgent, Title: "spawn", Status: proto.StatusInProgress,
		})
	}
	apply(proto.JobStarted, proto.JobPayload{JobID: "j1", ToolCallID: "c1", TaskType: "subagent"})
	apply(proto.JobStarted, proto.JobPayload{JobID: "j2", ToolCallID: "c2", TaskType: "subagent"})
	for _, tool := range []struct{ id, parent, status string }{
		{"run1", "c1", proto.StatusInProgress},
		{"wait1", "c1", proto.StatusPending},
		{"done1", "c1", proto.StatusInProgress},
		{"other", "c2", proto.StatusInProgress},
		{"spawn3", "c1", proto.StatusInProgress},
	} {
		apply(proto.ToolCallStarted, proto.ToolCallStartedPayload{
			TurnID: "t1", ToolCallID: tool.id, ParentToolCallID: tool.parent, Kind: proto.KindExecute, Title: tool.id, Status: tool.status,
		})
	}
	// j1's child spawned a grandchild that outlives it.
	apply(proto.JobStarted, proto.JobPayload{JobID: "j3", ToolCallID: "spawn3", ParentJobID: "j1", TaskType: "subagent"})
	apply(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: "done1", Status: proto.StatusCompleted})
	apply(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn})
	apply(proto.JobFinished, proto.JobPayload{JobID: "j1", Status: proto.JobStopped})

	for id, want := range map[string]string{
		"c1": proto.StatusCancelled, "run1": proto.StatusCancelled, "wait1": proto.StatusCancelled,
		"done1": proto.StatusCompleted, "other": proto.StatusInProgress, "spawn3": proto.StatusInProgress,
	} {
		if it, _ := s.ItemByID(id); it.Status != want {
			t.Fatalf("%s after its job stopped = %q, want %q", id, it.Status, want)
		}
	}

	apply(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: "run1", ParentToolCallID: "c1", Status: proto.StatusCompleted})
	if it, _ := s.ItemByID("run1"); it.Status != proto.StatusCompleted {
		t.Fatalf("late completion = %q, want completed", it.Status)
	}
}

// A finished job reported running again was reopened: its finish time clears
// so its elapsed clock runs, and the next finish stamps it afresh. Mirrored by
// web/src/apply.test.ts.
func TestReopenedJobClearsFinishedAt(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.JobStarted, proto.JobPayload{JobID: "j1", TaskType: "subagent"}))
	s.Apply(event(t, 2, proto.JobFinished, proto.JobPayload{JobID: "j1", Status: proto.JobCompleted}))
	if got := s.job("j1").FinishedAt; got != 2000 {
		t.Fatalf("finishedAt after finish = %d, want 2000", got)
	}

	s.Apply(event(t, 3, proto.JobUpdated, proto.JobPayload{JobID: "j1", Status: proto.JobRunning}))
	if j := s.job("j1"); j.FinishedAt != 0 || j.Status != proto.JobRunning {
		t.Fatalf("job after reopen = %+v, want running with no finish", j)
	}

	// An update that sets no status leaves a finished job finished.
	s.Apply(event(t, 4, proto.JobFinished, proto.JobPayload{JobID: "j1", Status: proto.JobCompleted}))
	s.Apply(event(t, 5, proto.JobUpdated, proto.JobPayload{JobID: "j1", Activity: "done"}))
	if j := s.job("j1"); j.FinishedAt != 4000 || j.Status != proto.JobCompleted {
		t.Fatalf("job after second finish = %+v, want completed at 4000", j)
	}
}

// A live shell alone is not work anyone waits on — a dev server started from
// the thread runs for as long as it likes — so once the turn is over the thread
// is the user's again. A subagent or a monitor still holds it in background.
func TestBackgroundAttentionIgnoresShells(t *testing.T) {
	for _, tc := range []struct {
		taskType string
		want     string
	}{
		{"local_bash", AttentionNeedsPrompt},
		{"subagent", AttentionBackground},
		{"monitor", AttentionBackground},
	} {
		t.Run(tc.taskType, func(t *testing.T) {
			s := New("s1")
			s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
			s.Apply(event(t, 2, proto.JobStarted, proto.JobPayload{JobID: "j1", TaskType: tc.taskType}))
			s.Apply(event(t, 3, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
			if got := s.Attention(); got != tc.want {
				t.Fatalf("attention with a live %s job = %q, want %q", tc.taskType, got, tc.want)
			}
		})
	}
}

// TestInjectedPromptJoinsTheTurn: a prompt the harness read into the running
// turn leaves the queue and becomes a user message in that turn, placed after
// the work already done, carrying the text and images the queue entry had.
func TestInjectedPromptJoinsTheTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c1", Title: "ls"}))
	s.Apply(event(t, 3, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "also this", Images: []proto.PromptImage{{ID: "img"}}}))
	s.Apply(event(t, 4, proto.PromptSent, proto.PromptSentPayload{QueueID: "q1"}))
	if len(s.Queued) != 1 || !s.Queued[0].Sent {
		t.Fatalf("queued = %+v", s.Queued)
	}
	s.Apply(event(t, 5, proto.PromptInjected, proto.PromptInjectedPayload{QueueID: "q1", TurnID: "t1"}))
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
	s.Apply(event(t, 6, proto.PromptInjected, proto.PromptInjectedPayload{QueueID: "nope", TurnID: "t1"}))
	if len(s.Items) != before {
		t.Fatalf("unknown injection added an item")
	}
}

// TestSentMarksOnlyTheQueuedPrompt: prompt.sent marks the one entry it names.
// The harness can read a prompt before its acknowledgement is recorded, so a
// prompt.sent for an entry that already left the queue changes nothing.
func TestSentMarksOnlyTheQueuedPrompt(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "one"}))
	s.Apply(event(t, 3, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q2", Prompt: "two"}))
	if s.Queued[0].Sent || s.Queued[1].Sent {
		t.Fatalf("queued before any hand-over = %+v", s.Queued)
	}
	s.Apply(event(t, 4, proto.PromptSent, proto.PromptSentPayload{QueueID: "q2"}))
	if s.Queued[0].Sent || !s.Queued[1].Sent {
		t.Fatalf("queued after q2 was sent = %+v", s.Queued)
	}
	s.Apply(event(t, 5, proto.PromptInjected, proto.PromptInjectedPayload{QueueID: "q1", TurnID: "t1"}))
	s.Apply(event(t, 6, proto.PromptSent, proto.PromptSentPayload{QueueID: "q1"}))
	if len(s.Queued) != 1 || s.Queued[0].QueueID != "q2" {
		t.Fatalf("a late prompt.sent brought an entry back: %+v", s.Queued)
	}
}

// TestHeldPromptTurnFillsFromTheQueue: a turn the harness started from a
// prompt it was holding carries only the text; the queue entry supplies the
// images, and the title comes from the prompt as usual.
func TestHeldPromptTurnFillsFromTheQueue(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "next", Images: []proto.PromptImage{{ID: "img"}}}))
	s.Apply(event(t, 2, proto.PromptSent, proto.PromptSentPayload{QueueID: "q1"}))
	s.Apply(event(t, 3, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t2", QueueID: "q1"}))
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

// TestEditedPromptReplacesTheEntry: an edit carries the whole result, so the
// entry takes its text and attachments wholesale, keeping its place in line.
// An edit of something no longer queued changes nothing.
func TestEditedPromptReplacesTheEntry(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "first", Images: []proto.PromptImage{{ID: "a"}, {ID: "b"}}}))
	s.Apply(event(t, 2, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q2", Prompt: "second"}))
	s.Apply(event(t, 3, proto.PromptEdited, proto.PromptEditedPayload{QueueID: "q1", Prompt: "first, reworded", Images: []proto.PromptImage{{ID: "c"}}}))
	if len(s.Queued) != 2 || s.Queued[0].QueueID != "q1" || s.Queued[0].Prompt != "first, reworded" {
		t.Fatalf("queued after edit = %+v", s.Queued)
	}
	if imgs := s.Queued[0].Images; len(imgs) != 1 || imgs[0].ID != "c" {
		t.Fatalf("images after edit = %+v, want only the new set", imgs)
	}
	s.Apply(event(t, 4, proto.PromptEdited, proto.PromptEditedPayload{QueueID: "q1", Prompt: "no attachments"}))
	if len(s.Queued[0].Images) != 0 {
		t.Fatalf("an edit with no images left %+v", s.Queued[0].Images)
	}
	s.Apply(event(t, 5, proto.PromptEdited, proto.PromptEditedPayload{QueueID: "gone", Prompt: "x"}))
	if len(s.Queued) != 2 || s.Queued[1].Prompt != "second" {
		t.Fatalf("edit of an unknown id changed the queue: %+v", s.Queued)
	}
}

// TestHeldPromptsWaitUntilReleased: holding pauses the named entries and
// takes back any claim the harness had on them; release clears only the hold.
// A prompt queued behind a hold arrives held.
func TestHeldPromptsWaitUntilReleased(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "sent"}))
	s.Apply(event(t, 2, proto.PromptSent, proto.PromptSentPayload{QueueID: "q1"}))
	s.Apply(event(t, 3, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q2", Prompt: "waiting"}))
	s.Apply(event(t, 4, proto.PromptHeld, proto.PromptHeldPayload{QueueIDs: []string{"q1", "q2", "gone"}}))
	for _, q := range s.Queued {
		if !q.Held || q.Sent {
			t.Fatalf("held entry = %+v, want held and not sent", q)
		}
	}
	s.Apply(event(t, 5, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q3", Prompt: "behind", Held: true}))
	if len(s.Queued) != 3 || !s.Queued[2].Held {
		t.Fatalf("queued behind a hold = %+v", s.Queued)
	}
	s.Apply(event(t, 6, proto.PromptReleased, proto.PromptReleasedPayload{QueueIDs: []string{"q1", "q3"}}))
	if s.Queued[0].Held || !s.Queued[1].Held || s.Queued[2].Held {
		t.Fatalf("after release = %+v, want only q2 still held", s.Queued)
	}
	if s.Queued[0].Sent {
		t.Fatalf("release marked a prompt sent again: %+v", s.Queued[0])
	}
	// One the harness took after all is its own, not held.
	s.Apply(event(t, 7, proto.PromptSent, proto.PromptSentPayload{QueueID: "q2"}))
	if q := s.Queued[1]; q.Held || !q.Sent {
		t.Fatalf("held entry the harness took = %+v, want sent and not held", q)
	}
}

func TestShowingAPathAgainRevisesOneArtefact(t *testing.T) {
	s := New("s")
	show := func(seq int64, size int64, src string) {
		s.Apply(event(t, seq, proto.ArtefactShown, proto.ArtefactShownPayload{
			ArtefactID: "a1", Path: "/home/p/report.html", Name: "report.html", MediaType: "text/html", Size: size,
			Entry: "report.html", Files: 1, Source: src, TurnID: "t1",
		}))
	}
	show(1, 10, proto.ArtefactFromAgent)
	show(2, 20, proto.ArtefactFromAgent)
	s.Apply(event(t, 3, proto.ArtefactShown, proto.ArtefactShownPayload{
		ArtefactID: "u1", Path: "/home/p/uploads/brief.pdf", Name: "brief.pdf", MediaType: "application/pdf", Size: 5, Files: 1, Source: proto.ArtefactFromUpload,
	}))

	if len(s.Artefacts) != 2 {
		t.Fatalf("artefacts = %+v", s.Artefacts)
	}
	a, ok := s.ArtefactByPath("/home/p/report.html")
	if !ok || a.ID != "a1" || a.Size != 20 {
		t.Fatalf("report = %+v", a)
	}
	// One timeline card per time the agent showed it; uploads show on their
	// message instead.
	var items []Item
	for _, it := range s.Items {
		if it.Kind == ItemArtefact {
			items = append(items, it)
		}
	}
	if len(items) != 2 || items[0].ID == items[1].ID || items[1].ArtefactID != "a1" || items[1].TurnID != "t1" || items[1].Size != 20 {
		t.Fatalf("items = %+v", items)
	}

	// A clone is independent of the actor's state.
	c := s.Clone()
	show(4, 30, proto.ArtefactFromAgent)
	if got, _ := c.ArtefactByID("a1"); got.Size != 20 {
		t.Fatalf("clone shares artefacts: %+v", got)
	}
}

func plans(s *State) []Item {
	var out []Item
	for _, it := range s.Items {
		if it.Kind == ItemPlan {
			out = append(out, it)
		}
	}
	return out
}

func steps(texts ...string) []proto.PlanStep {
	out := make([]proto.PlanStep, len(texts))
	for i, t := range texts {
		out[i] = proto.PlanStep{Text: t, Status: proto.PlanPending}
	}
	return out
}

// A codex plan is the turn's: every update within the turn revises one item
// in place, wholesale, and the next turn's plan is a second item that leaves
// the first alone. Mirrored by web/src/apply.test.ts.
func TestCodexPlanRevisesOneItemPerTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.PlanUpdated, proto.PlanUpdatedPayload{
		TurnID: "t1", Explanation: "first cut", Steps: steps("read", "write"),
	}))
	s.Apply(event(t, 3, proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t1", ToolCallID: "c1", Title: "ls"}))
	revised := steps("read", "write", "test")
	revised[0].Status = proto.PlanCompleted
	revised[1].Status = proto.PlanRunning
	s.Apply(event(t, 4, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", Steps: revised}))

	ps := plans(s)
	if len(ps) != 1 {
		t.Fatalf("plan items = %+v, want one", ps)
	}
	p := ps[0]
	if p.ID != "turn-plan:t1" || p.TurnID != "t1" || len(p.Steps) != 3 || p.Steps[1].Status != proto.PlanRunning {
		t.Fatalf("plan item = %+v", p)
	}
	if p.Explanation != "first cut" {
		t.Fatalf("explanation = %q, want kept: codex sends it on the first update only", p.Explanation)
	}
	if p.ReceivedAt != 2000 {
		t.Fatalf("receivedAt = %d, want the first update's 2000", p.ReceivedAt)
	}
	if s.Items[1].ID != "turn-plan:t1" {
		t.Fatalf("plan moved from where it first appeared: items = %+v", s.Items)
	}

	s.Apply(event(t, 5, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	s.Apply(event(t, 6, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t2", Prompt: "more"}))
	s.Apply(event(t, 7, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t2", Explanation: "again", Steps: steps("ship")}))

	ps = plans(s)
	if len(ps) != 2 || ps[1].ID != "turn-plan:t2" || ps[1].Explanation != "again" || len(ps[1].Steps) != 1 {
		t.Fatalf("plan items after second turn = %+v", ps)
	}
	if len(ps[0].Steps) != 3 || ps[0].Superseded || ps[0].Steps[0].Status != proto.PlanCompleted {
		t.Fatalf("first turn's plan changed: %+v", ps[0])
	}
}

// Each TodoWrite call is a list of its own; a new one marks every earlier
// TodoWrite list superseded, and never touches a codex turn plan. Re-sending
// the same call updates it without superseding anything. Mirrored by
// web/src/apply.test.ts.
func TestTodoWriteListsSupersedeEachOther(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", Steps: steps("codex")}))
	s.Apply(event(t, 3, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", ToolCallID: "c1", Steps: steps("a")}))
	s.Apply(event(t, 4, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", ToolCallID: "c2", Steps: steps("a", "b")}))

	ps := plans(s)
	if len(ps) != 3 || ps[1].ID != "todo:c1" || ps[2].ID != "todo:c2" {
		t.Fatalf("plan items = %+v", ps)
	}
	if ps[0].Superseded || !ps[1].Superseded || ps[2].Superseded {
		t.Fatalf("superseded = %v/%v/%v, want false/true/false", ps[0].Superseded, ps[1].Superseded, ps[2].Superseded)
	}

	// Updating the newest list again is not a new list.
	s.Apply(event(t, 5, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", ToolCallID: "c2", Steps: steps("a", "b", "c")}))
	ps = plans(s)
	if len(ps) != 3 || ps[2].Superseded || len(ps[2].Steps) != 3 {
		t.Fatalf("after re-send: %+v", ps)
	}

	// A later codex plan neither supersedes nor is superseded.
	s.Apply(event(t, 6, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t2", Steps: steps("codex2")}))
	s.Apply(event(t, 7, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t2", ToolCallID: "c3", Steps: steps("z")}))
	for _, p := range plans(s) {
		want := p.ID == "todo:c1" || p.ID == "todo:c2"
		if p.Superseded != want {
			t.Fatalf("%s superseded = %v, want %v", p.ID, p.Superseded, want)
		}
	}
}

// Superseding must work on a state that came from a snapshot or a clone,
// whose item index is not yet built, and replaying the log must land the same
// state as folding it live.
func TestPlanItemsReplay(t *testing.T) {
	evs := []proto.Event{
		event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}),
		event(t, 2, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", ToolCallID: "c1", Steps: steps("a")}),
		event(t, 3, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", ToolCallID: "c1", Steps: steps("a", "b")}),
		event(t, 4, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", Explanation: "why", Steps: steps("x")}),
	}
	live := New("s1")
	for _, ev := range evs {
		live.Apply(ev)
	}

	resumed := New("s1")
	resumed.Apply(evs[0])
	resumed.Apply(evs[1])
	resumed = resumed.Clone() // drops the item index, as a loaded snapshot has none
	for _, ev := range evs[2:] {
		resumed.Apply(ev)
	}

	a, _ := json.Marshal(live)
	b, _ := json.Marshal(resumed)
	if string(a) != string(b) {
		t.Fatalf("resumed state differs:\nlive:    %s\nresumed: %s", a, b)
	}
	// Re-sending c1 after the resume is an update, not a new list: it must
	// not supersede itself.
	if resumed.Items[1].Superseded || len(resumed.Items[1].Steps) != 2 || resumed.Items[2].Explanation != "why" {
		t.Fatalf("items = %+v", resumed.Items)
	}
}

// A clone's plan steps are its own: the actor revising a plan in place must
// not reach into a copy already handed out.
func TestCloneDoesNotAliasPlanSteps(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.PlanUpdated, proto.PlanUpdatedPayload{TurnID: "t1", Steps: steps("a")}))
	c := s.Clone()
	s.Items[0].Steps[0].Status = proto.PlanCompleted
	if c.Items[0].Steps[0].Status != proto.PlanPending {
		t.Fatalf("clone shares steps: %+v", c.Items[0].Steps)
	}
}

// TestUnanchoredPlanIsDropped: a plan.updated logged in the old shape names
// no turn and no call, so replaying it must not grow a blank row.
func TestUnanchoredPlanIsDropped(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.PlanUpdated, json.RawMessage(`{"entries":[{"content":"a","status":"pending"}]}`)))
	if ps := plans(s); len(ps) != 0 {
		t.Fatalf("plan items = %+v, want none", ps)
	}
}

// A finished turn takes the requests it was blocked on with it: a harness
// that stopped waiting must not leave a card up, or the thread waiting on the
// user. Requests of other turns, untagged ones and cards stay. Mirrored by
// web/src/apply.test.ts.
func TestTurnFinishedSettlesItsRequests(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "p1", TurnID: "t1", Title: "rm -rf"}))
	s.Apply(event(t, 3, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "p-other", TurnID: "t0", Title: "ls"}))
	s.Apply(event(t, 4, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "p-untagged", Title: "cat"}))
	s.Apply(event(t, 5, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", TurnID: "t1", Prompt: "which?"}))
	s.Apply(event(t, 6, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "card1", TurnID: "t1", Card: json.RawMessage(`{"kind":"mcp"}`)}))
	if got := s.Attention(); got != AttentionNeedsPermission {
		t.Fatalf("attention before stop = %q, want %q", got, AttentionNeedsPermission)
	}

	s.Apply(event(t, 7, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopCancelled}))

	var perms []string
	for _, p := range s.Pending {
		perms = append(perms, p.RequestID)
	}
	if len(perms) != 2 || perms[0] != "p-other" || perms[1] != "p-untagged" {
		t.Fatalf("pending after finish = %v, want [p-other p-untagged]", perms)
	}
	if len(s.Elicitations) != 1 || s.Elicitations[0].RequestID != "card1" {
		t.Fatalf("elicitations after finish = %+v, want only the card", s.Elicitations)
	}
}

func TestTurnFinishedNamingNoTurnSettlesNothing(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "p-untagged", Title: "cat"}))
	s.Apply(event(t, 3, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q-untagged", Prompt: "which?"}))

	s.Apply(event(t, 4, proto.TurnFinished, proto.TurnFinishedPayload{StopReason: proto.StopCancelled}))

	if len(s.Pending) != 1 || len(s.Elicitations) != 1 {
		t.Fatalf("after a finish naming no turn: pending %+v, elicitations %+v; want both kept", s.Pending, s.Elicitations)
	}
}

// A limit holds the queue until another turn starts, and only the newest
// turn counts: an older limit the thread has moved past holds nothing.
// The limit was the old account's, and a prompt queued after it was sent by
// hand: neither waits on it.
func TestQueueHoldSparesAnotherAccountAndLaterPrompts(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "before"}))
	s.Apply(event(t, 3, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopError, Failure: proto.FailureLimit}))
	s.Apply(event(t, 4, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q2", Prompt: "by hand"}))
	if !s.HeldByLimit(s.Queued[0]) {
		t.Fatal("a prompt queued before the limit is not held")
	}
	if s.HeldByLimit(s.Queued[1]) {
		t.Fatal("a prompt queued after the limit is held")
	}
	s.Apply(event(t, 5, proto.ThreadAccountChanged, proto.ThreadAccountChangedPayload{From: "a", To: "b"}))
	if s.QueueHeld() || s.HeldByLimit(s.Queued[0]) {
		t.Fatal("still held on another account")
	}
}

func TestQueueHeldFollowsTheLastTurn(t *testing.T) {
	s := New("s1")
	if s.QueueHeld() {
		t.Fatal("a thread with no turns is held")
	}
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopError, Failure: proto.FailureLimit}))
	if !s.QueueHeld() {
		t.Fatal("not held after a limit")
	}
	s.Apply(event(t, 3, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t2", Prompt: "again"}))
	if s.QueueHeld() {
		t.Fatal("still held while the next turn runs")
	}
	s.Apply(event(t, 4, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t2", StopReason: proto.StopError, Failure: proto.FailureAuth}))
	if s.QueueHeld() {
		t.Fatal("held after a failure that is not a limit")
	}
}

// Jobs are found by id through an index. Updates land on the right job among
// many, a snapshot round trip rebuilds the index so an update merges rather
// than adding a second job, and a clone's index is its own.
func TestJobsFoundByIDAcrossSnapshotAndClone(t *testing.T) {
	s := New("s1")
	seq := int64(0)
	next := func() int64 { seq++; return seq }
	const n = 500
	for i := range n {
		s.Apply(event(t, next(), proto.JobStarted, proto.JobPayload{JobID: "j" + strconv.Itoa(i), TaskType: "local_bash"}))
	}
	for i := range n {
		s.Apply(event(t, next(), proto.JobUpdated, proto.JobPayload{JobID: "j" + strconv.Itoa(i), Usage: &proto.JobUsage{ToolUses: int64(i + 1)}}))
	}
	if len(s.Jobs) != n {
		t.Fatalf("jobs = %d, want %d", len(s.Jobs), n)
	}
	for i := range n {
		if j := s.job("j" + strconv.Itoa(i)); j == nil || j.Usage.ToolUses != int64(i+1) {
			t.Fatalf("job j%d = %+v", i, j)
		}
	}

	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	back.Apply(event(t, next(), proto.JobUpdated, proto.JobPayload{JobID: "j7", Activity: "Read"}))
	back.Apply(event(t, next(), proto.JobStarted, proto.JobPayload{JobID: "child", ParentJobID: "j7", TaskType: "local_bash"}))
	if len(back.Jobs) != n+1 || back.job("j7").Activity != "Read" || back.job("child").Depth != 1 {
		t.Fatalf("after snapshot: jobs=%d j7=%+v child=%+v", len(back.Jobs), back.job("j7"), back.job("child"))
	}

	c := back.Clone()
	c.Apply(event(t, next(), proto.JobStarted, proto.JobPayload{JobID: "only-in-clone", TaskType: "local_bash"}))
	if c.job("only-in-clone") == nil || back.job("only-in-clone") != nil {
		t.Fatal("clone shares its job index with the original")
	}
}

func modelNotices(s *State) []Item {
	var out []Item
	for _, it := range s.Items {
		if it.Kind == ItemNotice && it.NoticeKind == "model" {
			out = append(out, it)
		}
	}
	return out
}

func startTurn(t *testing.T, seq int64, id string) proto.Event {
	return event(t, seq, proto.TurnStarted, proto.TurnStartedPayload{TurnID: id, Prompt: "go"})
}

func pickModel(t *testing.T, seq int64, model string) proto.Event {
	return event(t, seq, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: model})
}

func pickEffort(t *testing.T, seq int64, effort string) proto.Event {
	return event(t, seq, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Effort: &effort})
}

func noticeSettings(items []Item) [][3]string {
	var out [][3]string
	for _, it := range items {
		out = append(out, [3]string{it.ID, it.Model, it.Effort})
	}
	return out
}

// A switch is marked where the next turn starts, just above its prompt, naming
// the settings that turn runs on: the replies below the line came from them.
// Mirrored by web/src/apply.test.ts.
func TestSwitchIsMarkedAtTheNextTurn(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus", Effort: "high"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(pickModel(t, 3, "sonnet"))
	if got := modelNotices(s); len(got) != 0 {
		t.Fatalf("notices before the next turn = %+v, want none", got)
	}
	s.Apply(startTurn(t, 4, "t2"))
	s.Apply(pickEffort(t, 5, "low"))
	s.Apply(startTurn(t, 6, "t3"))

	want := [][3]string{{"model:4", "sonnet", "high"}, {"model:6", "sonnet", "low"}}
	if got := noticeSettings(modelNotices(s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
	var order []string
	for _, it := range s.Items {
		order = append(order, it.ID)
	}
	wantOrder := []string{"prompt:t1", "model:4", "prompt:t2", "model:6", "prompt:t3"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("items = %v, want %v", order, wantOrder)
	}
	if from := s.Items[1].From; from == nil || *from != (TurnSettings{Model: "opus", Effort: "high"}) {
		t.Fatalf("first notice from = %+v, want opus/high", from)
	}
}

// The first turn has nothing above it to divide from: whatever it runs on is
// where the thread starts. Mirrored by web/src/apply.test.ts.
func TestFirstTurnIsNotMarked(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus"}))
	s.Apply(pickModel(t, 2, "sonnet"))
	s.Apply(startTurn(t, 3, "t1"))
	if got := modelNotices(s); len(got) != 0 {
		t.Fatalf("notices = %+v, want none", got)
	}
}

// Switching away and back before the next turn is no change, and several
// switches in a row are one: the line names where they ended up. Mirrored by
// web/src/apply.test.ts.
func TestSwitchesBetweenTurnsAreOneChange(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus", Effort: "high"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(pickModel(t, 3, "sonnet"))
	s.Apply(pickModel(t, 4, "opus"))
	s.Apply(startTurn(t, 5, "t2"))
	if got := modelNotices(s); len(got) != 0 {
		t.Fatalf("after switching back: notices = %+v, want none", got)
	}

	s.Apply(pickModel(t, 6, "sonnet"))
	s.Apply(pickModel(t, 7, "haiku"))
	s.Apply(pickEffort(t, 8, "low"))
	s.Apply(startTurn(t, 9, "t3"))
	want := [][3]string{{"model:9", "haiku", "low"}}
	if got := noticeSettings(modelNotices(s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
}

// The harness reporting its own settings as it starts or resumes carries its
// session id and names the bare model id for the alias that was picked. That
// is not a switch; but it must not swallow a real switch still waiting for its
// turn either. Mirrored by web/src/apply.test.ts.
func TestHarnessReportIsNotASwitch(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(event(t, 3, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: "claude-opus-5", HarnessSessionID: "h1"}))
	s.Apply(startTurn(t, 4, "t2"))
	if got := modelNotices(s); len(got) != 0 {
		t.Fatalf("after a harness report: notices = %+v, want none", got)
	}
	if s.Model != "claude-opus-5" {
		t.Fatalf("model = %q, want the reported claude-opus-5", s.Model)
	}

	s.Apply(pickModel(t, 5, "sonnet"))
	s.Apply(event(t, 6, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: "claude-sonnet-5", HarnessSessionID: "h2"}))
	s.Apply(startTurn(t, 7, "t3"))
	want := [][3]string{{"model:7", "sonnet", ""}}
	if got := noticeSettings(modelNotices(s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a switch and a restart: notices = %v, want %v", got, want)
	}
}

// Claude reports a turn's settings seconds after the turn starts, so a pick
// made in between can be followed by a report naming the model it replaced.
// The pick still stands, and the next turn is marked. Mirrored by
// web/src/apply.test.ts.
func TestReportBehindAPickKeepsThePick(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "claude-opus-5"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(pickModel(t, 3, "sonnet"))
	s.Apply(event(t, 4, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: "claude-opus-5", HarnessSessionID: "h1"}))
	if s.Model != "sonnet" {
		t.Fatalf("model = %q, want the pick to stand", s.Model)
	}
	s.Apply(startTurn(t, 5, "t2"))
	want := [][3]string{{"model:5", "sonnet", ""}}
	if got := noticeSettings(modelNotices(s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
}

// An effort switch waiting for its turn is no reason to drop a model report,
// and the report must not swallow the effort switch either. Mirrored by
// web/src/apply.test.ts.
func TestReportDuringAnEffortSwitch(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus", Effort: "high"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(pickEffort(t, 3, "low"))
	s.Apply(event(t, 4, proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{Model: "claude-opus-5", HarnessSessionID: "h1"}))
	if s.Model != "claude-opus-5" {
		t.Fatalf("model = %q, want the report's claude-opus-5", s.Model)
	}
	s.Apply(startTurn(t, 5, "t2"))
	want := [][3]string{{"model:5", "claude-opus-5", "low"}}
	if got := noticeSettings(modelNotices(s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
	if from := s.Items[1].From; from == nil || *from != (TurnSettings{Model: "claude-opus-5", Effort: "high"}) {
		t.Fatalf("from = %+v, want the reported model at the old effort", from)
	}
}

// A reload resumes from a stored snapshot, so a switch made before it must
// still be marked at the turn after it. A snapshot stored before the baseline
// existed has none, and must not mark its next turn as a switch. Mirrored by
// web/src/apply.test.ts.
func TestSwitchSurvivesASnapshot(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(pickModel(t, 3, "sonnet"))
	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	restored.Apply(startTurn(t, 4, "t2"))
	want := [][3]string{{"model:4", "sonnet", ""}}
	if got := noticeSettings(modelNotices(restored)); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored notices = %v, want %v", got, want)
	}

	s.RanWith = nil
	if blob, err = json.Marshal(s); err != nil {
		t.Fatal(err)
	}
	old, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	old.Apply(startTurn(t, 4, "t2"))
	if got := modelNotices(old); len(got) != 0 {
		t.Fatalf("old snapshot notices = %+v, want none", got)
	}
}

// Warnings and errors from an extension leave a turnless note in the
// timeline; info is a toast only and leaves nothing behind.
func TestExtensionNotifiedNoticeForWarningsAndErrors(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1"}))
	s.Apply(event(t, 2, proto.ExtensionNotified, proto.ExtensionNotifiedPayload{Level: proto.NotifyInfo, Message: "saved"}))
	s.Apply(event(t, 3, proto.ExtensionNotified, proto.ExtensionNotifiedPayload{Level: proto.NotifyWarning, Message: "Command blocked"}))
	s.Apply(event(t, 4, proto.ExtensionNotified, proto.ExtensionNotifiedPayload{Level: proto.NotifyError, Message: "Lint failed"}))

	var notices []Item
	for _, it := range s.Items {
		if it.Kind == ItemNotice {
			notices = append(notices, it)
		}
	}
	if len(notices) != 2 {
		t.Fatalf("notices = %+v, want warning and error only", notices)
	}
	want := []Item{
		{ID: "notify:3", Kind: ItemNotice, ReceivedAt: 3000, NoticeKind: "notify", Level: proto.NotifyWarning, Text: "Command blocked"},
		{ID: "notify:4", Kind: ItemNotice, ReceivedAt: 4000, NoticeKind: "notify", Level: proto.NotifyError, Text: "Lint failed"},
	}
	for i, w := range want {
		got := notices[i]
		if got.ID != w.ID || got.TurnID != "" || got.ReceivedAt != w.ReceivedAt ||
			got.NoticeKind != w.NoticeKind || got.Level != w.Level || got.Text != w.Text {
			t.Fatalf("notice %d = %+v, want %+v", i, got, w)
		}
	}
	if s.Seq != 4 {
		t.Fatalf("seq = %d, want 4", s.Seq)
	}
}

// A status key keeps its place when its text changes, a new key goes on the
// end, and empty text removes the key.
func TestExtensionStatusSetUpdateClear(t *testing.T) {
	s := New("s1")
	set := func(seq int64, key, text string) {
		s.Apply(event(t, seq, proto.ExtensionStatus, proto.ExtensionStatusPayload{Key: key, Text: text}))
	}
	check := func(want ...ExtensionStatus) {
		t.Helper()
		if len(s.ExtensionStatuses) != len(want) {
			t.Fatalf("statuses = %+v, want %+v", s.ExtensionStatuses, want)
		}
		for i := range want {
			if s.ExtensionStatuses[i] != want[i] {
				t.Fatalf("statuses = %+v, want %+v", s.ExtensionStatuses, want)
			}
		}
	}

	set(1, "a", "one")
	set(2, "b", "two")
	check(ExtensionStatus{"a", "one"}, ExtensionStatus{"b", "two"})

	set(3, "a", "uno")
	check(ExtensionStatus{"a", "uno"}, ExtensionStatus{"b", "two"})

	set(4, "c", "")
	check(ExtensionStatus{"a", "uno"}, ExtensionStatus{"b", "two"})

	// The clone is taken before the clear, so it must keep both.
	c := s.Clone()
	set(5, "a", "")
	check(ExtensionStatus{"b", "two"})
	if len(c.ExtensionStatuses) != 2 || c.ExtensionStatuses[0] != (ExtensionStatus{"a", "uno"}) || c.ExtensionStatuses[1] != (ExtensionStatus{"b", "two"}) {
		t.Fatalf("clone shares statuses: %+v", c.ExtensionStatuses)
	}

	set(6, "a", "back")
	check(ExtensionStatus{"b", "two"}, ExtensionStatus{"a", "back"})
}

// A thread can close with no process live to clear its statuses, so closing
// clears them.
func TestThreadClosedClearsExtensionStatuses(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ExtensionStatus, proto.ExtensionStatusPayload{Key: "a", Text: "watching"}))
	s.Apply(event(t, 2, proto.ThreadClosed, proto.ThreadClosedPayload{Reason: "done"}))
	if len(s.ExtensionStatuses) != 0 {
		t.Fatalf("statuses after close = %+v, want none", s.ExtensionStatuses)
	}
}

// A snapshot stored before statuses existed decodes to an empty list, not
// null, so a client never has to guard for it.
func TestFromSnapshotDefaultsExtensionStatuses(t *testing.T) {
	s, err := FromSnapshot(json.RawMessage(`{"threadId":"s1","seq":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.ExtensionStatuses == nil {
		t.Fatal("extensionStatuses decoded as nil")
	}
}

// A suggestion is offered only for the latest turn: one that names an earlier
// turn arrived after the human had moved on. The next turn starting clears
// it. Mirrored by web/src/apply.test.ts.
func TestSuggestionFollowsTheLatestTurn(t *testing.T) {
	s := New("s1")
	s.Apply(startTurn(t, 1, "t1"))
	s.Apply(event(t, 2, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}))
	s.Apply(event(t, 3, proto.TurnSuggested, proto.TurnSuggestedPayload{TurnID: "t1", Text: "run the tests"}))
	if s.Suggestion == nil || *s.Suggestion != (proto.TurnSuggestedPayload{TurnID: "t1", Text: "run the tests"}) {
		t.Fatalf("suggestion = %+v, want t1's", s.Suggestion)
	}

	s.Apply(startTurn(t, 4, "t2"))
	if s.Suggestion != nil {
		t.Fatalf("suggestion after the next turn started = %+v, want none", s.Suggestion)
	}

	// t1's suggestion turning up late, after t2 started, is stale.
	s.Apply(event(t, 5, proto.TurnSuggested, proto.TurnSuggestedPayload{TurnID: "t1", Text: "run the tests"}))
	if s.Suggestion != nil {
		t.Fatalf("stale suggestion = %+v, want ignored", s.Suggestion)
	}
	// So is one naming a turn the log never opened.
	s.Apply(event(t, 6, proto.TurnSuggested, proto.TurnSuggestedPayload{TurnID: "t-unknown", Text: "commit"}))
	if s.Suggestion != nil {
		t.Fatalf("suggestion for an unknown turn = %+v, want ignored", s.Suggestion)
	}
}

// Before any turn there is no latest turn to suggest after.
func TestSuggestionWithNoTurnIsIgnored(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnSuggested, proto.TurnSuggestedPayload{TurnID: "t1", Text: "go"}))
	if s.Suggestion != nil {
		t.Fatalf("suggestion = %+v, want none", s.Suggestion)
	}
}

func finishTurn(t *testing.T, seq int64, id, ref string) proto.Event {
	return event(t, seq, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: id, StopReason: proto.StopEndTurn, HarnessRef: ref})
}

func turnIDs(turns []Turn) []string {
	out := []string{}
	for _, t := range turns {
		out = append(out, t.ID)
	}
	return out
}

// A turn's harness anchor lands on the turn, survives a stored snapshot, and
// a later finish that carries none does not take it away.
func TestTurnFinishedKeepsItsHarnessRef(t *testing.T) {
	s := New("s1")
	s.Apply(startTurn(t, 1, "t1"))
	s.Apply(finishTurn(t, 2, "t1", "ref-1"))
	s.Apply(finishTurn(t, 3, "t1", ""))
	s.Apply(startTurn(t, 4, "t2"))
	s.Apply(finishTurn(t, 5, "t2", ""))

	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []*State{s, back, s.Clone()} {
		if st.Turns[0].HarnessRef != "ref-1" || st.Turns[1].HarnessRef != "" {
			t.Fatalf("refs = %q/%q, want ref-1/empty", st.Turns[0].HarnessRef, st.Turns[1].HarnessRef)
		}
	}
}

// rewoundThread is three finished turns: t1 with a reply, a model switch
// marked above t2's prompt, t2 with a tool call and a job, then t3. A prompt
// is queued at the end.
func rewoundThread(t *testing.T) *State {
	s := New("s1")
	s.Apply(event(t, 1, proto.ThreadCreated, proto.ThreadCreatedPayload{Model: "opus"}))
	s.Apply(startTurn(t, 2, "t1"))
	s.Apply(event(t, 3, proto.MessageChunk, proto.MessageChunkPayload{TurnID: "t1", BlockID: "m1", Role: "assistant", Kind: "text", Delta: "hi"}))
	s.Apply(event(t, 4, proto.JobStarted, proto.JobPayload{JobID: "j1", TaskType: "local_agent"}))
	s.Apply(event(t, 5, proto.JobFinished, proto.JobPayload{JobID: "j1"}))
	s.Apply(finishTurn(t, 6, "t1", "ref-1"))
	s.Apply(pickModel(t, 7, "sonnet"))
	s.Apply(startTurn(t, 8, "t2"))
	s.Apply(event(t, 9, proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: "t2", ToolCallID: "c2", Status: proto.StatusInProgress}))
	s.Apply(event(t, 10, proto.JobStarted, proto.JobPayload{JobID: "j2", ToolCallID: "c2", TaskType: "local_agent"}))
	s.Apply(event(t, 11, proto.JobFinished, proto.JobPayload{JobID: "j2"}))
	s.Apply(finishTurn(t, 12, "t2", "ref-2"))
	s.Apply(startTurn(t, 13, "t3"))
	s.Apply(finishTurn(t, 14, "t3", "ref-3"))
	s.Apply(event(t, 15, proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "q1", Prompt: "later"}))
	return s
}

// Rewinding to t2 cuts from its prompt on, keeps the switch notice above it
// (the switch still happened), drops t2 and t3 and the job t2 spawned, and
// leaves the queue alone. The thread carries on from there: a new turn's
// items are found by id where they land.
func TestRewindCutsTheThreadBackToBeforeATurn(t *testing.T) {
	s := rewoundThread(t)
	at := s.RewindAt("t2")
	if at < 0 || s.Items[at].ID != "prompt:t2" {
		t.Fatalf("RewindAt(t2) = %d, want the index of prompt:t2 in %v", at, ids(s.Items))
	}
	s.Apply(event(t, 16, proto.ThreadRewound, proto.ThreadRewoundPayload{TurnID: "t2", At: at}))

	if got, want := ids(s.Items), []string{"prompt:t1", "m1", "model:8"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	if got := turnIDs(s.Turns); !reflect.DeepEqual(got, []string{"t1"}) {
		t.Fatalf("turns = %v, want [t1]", got)
	}
	if got := jobIDs(s.Jobs); !reflect.DeepEqual(got, []string{"j1"}) {
		t.Fatalf("jobs = %v, want [j1]", got)
	}
	if _, ok := s.ItemByID("prompt:t2"); ok || s.job("j2") != nil {
		t.Fatal("the indexes still find what the rewind removed")
	}
	if len(s.Queued) != 1 || s.Queued[0].QueueID != "q1" {
		t.Fatalf("queued = %+v, want q1 left alone", s.Queued)
	}
	if s.Phase != "idle" {
		t.Fatalf("phase = %q, want idle", s.Phase)
	}

	s.Apply(startTurn(t, 17, "t4"))
	s.Apply(event(t, 18, proto.MessageChunk, proto.MessageChunkPayload{TurnID: "t4", BlockID: "m4", Role: "assistant", Kind: "text", Delta: "again"}))
	if got, want := ids(s.Items), []string{"prompt:t1", "m1", "model:8", "prompt:t4", "m4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items after the next turn = %v, want %v", got, want)
	}
	if it, ok := s.ItemByID("m4"); !ok || it.Text != "again" {
		t.Fatalf("m4 = %+v, %v", it, ok)
	}
}

// Rewinding to the first turn leaves an empty conversation.
func TestRewindToTheFirstTurnEmptiesTheThread(t *testing.T) {
	s := rewoundThread(t)
	s.Apply(event(t, 16, proto.ThreadRewound, proto.ThreadRewoundPayload{TurnID: "t1", At: s.RewindAt("t1")}))
	if len(s.Items) != 0 || len(s.Turns) != 0 || len(s.Jobs) != 0 {
		t.Fatalf("items=%v turns=%v jobs=%v, want all empty", ids(s.Items), turnIDs(s.Turns), jobIDs(s.Jobs))
	}
	if s.LastTurnFailed() || s.QueueHeld() {
		t.Fatal("an empty thread reports a failed or held last turn")
	}
}

// A rewind naming a turn the projection does not have keeps its turns, and
// an index outside the items keeps the items: nothing is cut on a guess.
func TestRewindOutOfRangeCutsNothing(t *testing.T) {
	s := rewoundThread(t)
	items, turns := ids(s.Items), turnIDs(s.Turns)
	s.Apply(event(t, 16, proto.ThreadRewound, proto.ThreadRewoundPayload{TurnID: "nope", At: len(s.Items) + 1}))
	s.Apply(event(t, 17, proto.ThreadRewound, proto.ThreadRewoundPayload{TurnID: "nope", At: -1}))
	if !reflect.DeepEqual(ids(s.Items), items) || !reflect.DeepEqual(turnIDs(s.Turns), turns) {
		t.Fatalf("items=%v turns=%v, want %v and %v", ids(s.Items), turnIDs(s.Turns), items, turns)
	}
}

// A rewind replays to the same state from a snapshot taken before it as from
// the start of the log.
func TestRewindReplaysFromASnapshot(t *testing.T) {
	s := rewoundThread(t)
	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	ev := event(t, 16, proto.ThreadRewound, proto.ThreadRewoundPayload{TurnID: "t3", At: s.RewindAt("t3")})
	s.Apply(ev)
	back.Apply(ev)
	a, _ := json.Marshal(s)
	b, _ := json.Marshal(back)
	if !bytes.Equal(a, b) {
		t.Fatalf("snapshot replay differs:\n%s\n%s", a, b)
	}
	if got := turnIDs(s.Turns); !reflect.DeepEqual(got, []string{"t1", "t2"}) {
		t.Fatalf("turns = %v, want [t1 t2]", got)
	}
}

// A turn can be rewound to when it has an anchor, whatever the turn before it
// has.
func TestCanRewindTo(t *testing.T) {
	s := New("s1")
	s.Turns = []Turn{{ID: "a", HarnessRef: "ra"}, {ID: "b"}, {ID: "c", HarnessRef: "rc"}, {ID: "d", HarnessRef: "rd"}}
	for i, want := range map[int]bool{-1: false, 0: true, 1: false, 2: true, 3: true, 4: false} {
		if got := s.CanRewindTo(i); got != want {
			t.Errorf("CanRewindTo(%d) = %v, want %v", i, got, want)
		}
	}
	if New("s").CanRewindTo(0) {
		t.Error("CanRewindTo(0) on no turns = true")
	}
}

// toolTurn opens turn t1 with tool call c1 running in it.
func toolTurn(t *testing.T) *State {
	t.Helper()
	s := New("s1")
	s.Apply(event(t, 1, proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "go"}))
	s.Apply(event(t, 2, proto.ToolCallStarted, proto.ToolCallStartedPayload{
		TurnID: "t1", ToolCallID: "c1", Kind: "execute", Title: "rm -rf build", Status: proto.StatusInProgress,
	}))
	return s
}

func itemOf(t *testing.T, s *State, id string) Item {
	t.Helper()
	it, ok := s.ItemByID(id)
	if !ok {
		t.Fatalf("no item %q in %+v", id, s.Items)
	}
	return it
}

// A permission naming a tool call marks that call, pending until it
// resolves, then with what the outcome means. No standalone item is made.
// Mirrored by web/src/apply.test.ts.
func TestApprovalMarksTheToolCall(t *testing.T) {
	for _, tc := range []struct{ outcome, want string }{
		{proto.OutcomeAllowOnce, ApprovalAllowed},
		{proto.OutcomeAllowAlways, ApprovalAllowedAlways},
		{proto.OutcomeRejectOnce, ApprovalDenied},
		{proto.OutcomeRejectAlways, ApprovalDenied},
		{proto.OutcomeCancelled, ApprovalCancelled},
		{"something-new", ApprovalCancelled},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			s := toolTurn(t)
			s.Apply(event(t, 3, proto.PermissionRequested, proto.PermissionRequestedPayload{
				RequestID: "r1", TurnID: "t1", ToolCallID: "c1", Title: "Run rm",
			}))
			if got := itemOf(t, s, "c1").Approval; got != ApprovalPending {
				t.Fatalf("approval while asked = %q, want pending", got)
			}
			if _, ok := s.ItemByID(ApprovalID("r1")); ok {
				t.Fatal("a request naming a held tool call got a standalone item")
			}
			s.Apply(event(t, 4, proto.PermissionResolved, proto.PermissionResolvedPayload{RequestID: "r1", Outcome: tc.outcome}))
			if got := itemOf(t, s, "c1").Approval; got != tc.want {
				t.Fatalf("approval after %s = %q, want %q", tc.outcome, got, tc.want)
			}
		})
	}
}

// A request this projection holds no tool call for gets a standalone item,
// titled with the request, where it was raised; it resolves like a call does.
// Mirrored by web/src/apply.test.ts.
func TestApprovalWithoutAToolCallStandsAlone(t *testing.T) {
	for name, callID := range map[string]string{"no call id": "", "unknown call": "c-missing"} {
		t.Run(name, func(t *testing.T) {
			s := toolTurn(t)
			s.Apply(event(t, 3, proto.PermissionRequested, proto.PermissionRequestedPayload{
				RequestID: "r1", TurnID: "t1", ToolCallID: callID, Title: "Grant network",
			}))
			got := itemOf(t, s, ApprovalID("r1"))
			want := Item{ID: ApprovalID("r1"), Kind: ItemApproval, TurnID: "t1", ReceivedAt: 3000, Title: "Grant network", Approval: ApprovalPending}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("standalone item = %+v, want %+v", got, want)
			}
			if s.Items[len(s.Items)-1].ID != ApprovalID("r1") {
				t.Fatal("standalone item is not where the request was raised")
			}
			if a := itemOf(t, s, "c1").Approval; a != "" {
				t.Fatalf("unrelated tool call marked %q", a)
			}
			s.Apply(event(t, 4, proto.PermissionResolved, proto.PermissionResolvedPayload{RequestID: "r1", Outcome: proto.OutcomeRejectAlways}))
			if a := itemOf(t, s, ApprovalID("r1")).Approval; a != ApprovalDenied {
				t.Fatalf("standalone approval after reject = %q, want denied", a)
			}
		})
	}
}

// A turn's finish withdraws what it was blocked on, and the records say so;
// a request already answered keeps its answer. Mirrored by
// web/src/apply.test.ts.
func TestTurnFinishedCancelsPendingRecords(t *testing.T) {
	s := toolTurn(t)
	s.Apply(event(t, 3, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "r1", TurnID: "t1", ToolCallID: "c1"}))
	s.Apply(event(t, 4, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "r2", TurnID: "t1"}))
	s.Apply(event(t, 5, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", TurnID: "t1", Prompt: "which?"}))
	s.Apply(event(t, 6, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q2", TurnID: "t1", Prompt: "and?"}))
	s.Apply(event(t, 7, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q2", Action: "decline"}))

	s.Apply(event(t, 8, proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopCancelled}))

	if a := itemOf(t, s, "c1").Approval; a != ApprovalCancelled {
		t.Fatalf("tool call approval after finish = %q, want cancelled", a)
	}
	if a := itemOf(t, s, ApprovalID("r2")).Approval; a != ApprovalCancelled {
		t.Fatalf("standalone approval after finish = %q, want cancelled", a)
	}
	if st := itemOf(t, s, QuestionID("q1")).Status; st != QuestionCancelled {
		t.Fatalf("pending question after finish = %q, want cancelled", st)
	}
	if st := itemOf(t, s, QuestionID("q2")).Status; st != QuestionDeclined {
		t.Fatalf("declined question after finish = %q, want declined kept", st)
	}
}

// Each schema field is a question, in the schema's order, labelled by its
// title, else its description, else the prompt, else its name. A schema with
// no fields is the prompt alone. Mirrored by web/src/apply.test.ts.
func TestQuestionLabels(t *testing.T) {
	for _, tc := range []struct {
		name, prompt, schema string
		want                 []QuestionAnswer
	}{
		{
			name:   "fields in document order",
			prompt: "Set up",
			schema: `{"type":"object","properties":{"zeta":{"title":"Zeta?"},"alpha":{"description":"Alpha, described"},"mid":{"type":"string"},"pw":{"title":"Token","format":"password"},"wo":{"writeOnly":true}}}`,
			want: []QuestionAnswer{
				{Key: "zeta", Question: "Zeta?"},
				{Key: "alpha", Question: "Alpha, described"},
				{Key: "mid", Question: "Set up"},
				{Key: "pw", Question: "Token", Secret: true},
				{Key: "wo", Question: "Set up", Secret: true},
			},
		},
		{
			name:   "no prompt falls back to the key",
			schema: `{"properties":{"name":{}}}`,
			want:   []QuestionAnswer{{Key: "name", Question: "name"}},
		},
		{
			name:   "no properties",
			prompt: "Continue?",
			schema: `{"type":"boolean"}`,
			want:   []QuestionAnswer{{Key: "", Question: "Continue?"}},
		},
		{
			name:   "no schema",
			prompt: "Continue?",
			want:   []QuestionAnswer{{Key: "", Question: "Continue?"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schema json.RawMessage
			if tc.schema != "" {
				schema = json.RawMessage(tc.schema)
			}
			s := New("s1")
			s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{
				RequestID: "q1", TurnID: "t1", Prompt: tc.prompt, Schema: schema,
			}))
			it := itemOf(t, s, QuestionID("q1"))
			if it.Kind != ItemQuestion || it.Status != QuestionPending || it.TurnID != "t1" || it.ReceivedAt != 1000 {
				t.Fatalf("question item = %+v", it)
			}
			if !reflect.DeepEqual(it.Questions, tc.want) {
				t.Fatalf("questions = %+v, want %+v", it.Questions, tc.want)
			}
		})
	}
}

// An accepted answer is kept as text a person reads, each capped at
// AnswerCap code points. Mirrored by web/src/apply.test.ts.
func TestQuestionAnswers(t *testing.T) {
	long := strings.Repeat("a", 300)
	runes := strings.Repeat("é", 279) + "日本"
	exact := strings.Repeat("日", AnswerCap)
	schema := `{"properties":{"s":{},"long":{},"runes":{},"exact":{},"yes":{},"no":{},"n":{},"list":{},"obj":{},"null":{},"missing":{},"nulls":{},"empty":{},"allnull":{}}}`
	value, _ := json.Marshal(map[string]any{
		"s": "plain", "long": long, "runes": runes, "exact": exact, "yes": true, "no": false,
		"n": json.Number("4.50"), "list": []any{"a", true, 3}, "obj": map[string]any{"k": []int{1}}, "null": nil,
		"nulls": []any{"a", nil, "b"}, "empty": []any{}, "allnull": []any{nil, nil},
	})
	s := New("s1")
	s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", Schema: json.RawMessage(schema)}))
	s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: "accept", Value: value}))

	it := itemOf(t, s, QuestionID("q1"))
	if it.Status != QuestionAnswered {
		t.Fatalf("status = %q, want answered", it.Status)
	}
	got := map[string]string{}
	for _, q := range it.Questions {
		got[q.Key] = q.Answer
	}
	want := map[string]string{
		"s":       "plain",
		"long":    strings.Repeat("a", AnswerCap-1) + "…",
		"runes":   strings.Repeat("é", 279) + "…",
		"exact":   exact,
		"yes":     "Yes",
		"no":      "No",
		"n":       "4.5",
		"list":    "a, Yes, 3",
		"nulls":   "a, b",
		"empty":   "",
		"allnull": "",
		"obj":     `{"k":[1]}`,
		"null":    "",
		"missing": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("answers = %v\nwant %v", got, want)
	}
	if n := utf8.RuneCountInString(got["runes"]); n != AnswerCap {
		t.Fatalf("capped multi-byte answer is %d runes, want %d", n, AnswerCap)
	}
}

// A prompt with no fields takes a scalar answer whole; anything else is no
// answer for it. Mirrored by web/src/apply.test.ts.
func TestQuestionAnswerWithoutFields(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{`"go ahead"`, "go ahead"},
		{`true`, "Yes"},
		{`7`, "7"},
		{`{"a":1}`, ""},
		{`["a"]`, ""},
	} {
		s := New("s1")
		s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", Prompt: "Proceed?"}))
		s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: "accept", Value: json.RawMessage(tc.value)}))
		if got := itemOf(t, s, QuestionID("q1")).Questions[0].Answer; got != tc.want {
			t.Errorf("answer for %s = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// Named fields read their answers off an object; any other value answers
// none of them. Mirrored by web/src/apply.test.ts.
func TestQuestionAnswerNotAnObject(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", Schema: json.RawMessage(`{"properties":{"a":{}}}`)}))
	s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: "accept", Value: json.RawMessage(`["a"]`)}))
	it := itemOf(t, s, QuestionID("q1"))
	if it.Status != QuestionAnswered || it.Questions[0].Answer != "" {
		t.Fatalf("question = %+v, want answered with no answer", it)
	}
}

// Declining or cancelling ends the question with no answers recorded.
func TestQuestionDeclinedOrCancelled(t *testing.T) {
	for action, want := range map[string]string{"decline": QuestionDeclined, "cancel": QuestionCancelled, "": QuestionCancelled} {
		s := New("s1")
		s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", Schema: json.RawMessage(`{"properties":{"a":{}}}`)}))
		s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: action, Value: json.RawMessage(`{"a":"x"}`)}))
		it := itemOf(t, s, QuestionID("q1"))
		if it.Status != want || it.Questions[0].Answer != "" {
			t.Errorf("after %q: status %q, answer %q; want %q and no answer", action, it.Status, it.Questions[0].Answer, want)
		}
	}
}

// A masked field's value goes nowhere: not on the item, not in its JSON, not
// in the state a snapshot is written from.
func TestSecretAnswerIsNeverStored(t *testing.T) {
	const secret = "hunter2-sk-live-XYZ"
	s := New("s1")
	s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{
		RequestID: "q1", Schema: json.RawMessage(`{"properties":{"user":{"title":"User"},"pw":{"title":"Password","format":"password"},"key":{"writeOnly":true}}}`),
	}))
	value, _ := json.Marshal(map[string]string{"user": "aaron", "pw": secret, "key": secret})
	s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: "accept", Value: value}))

	it := itemOf(t, s, QuestionID("q1"))
	want := []QuestionAnswer{
		{Key: "user", Question: "User", Answer: "aaron"},
		{Key: "pw", Question: "Password", Secret: true},
		{Key: "key", Question: "key", Secret: true},
	}
	if !reflect.DeepEqual(it.Questions, want) {
		t.Fatalf("questions = %+v, want %+v", it.Questions, want)
	}
	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{secret, "hunter2", "XYZ"} {
		if strings.Contains(string(blob), part) {
			t.Fatalf("state JSON holds %q of the secret: %s", part, blob)
		}
	}
}

// A reconnect gets the records from the snapshot like any other item.
func TestApprovalAndQuestionSurviveSnapshot(t *testing.T) {
	s := toolTurn(t)
	s.Apply(event(t, 3, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "r1", TurnID: "t1", ToolCallID: "c1"}))
	s.Apply(event(t, 4, proto.PermissionResolved, proto.PermissionResolvedPayload{RequestID: "r1", Outcome: proto.OutcomeAllowAlways}))
	s.Apply(event(t, 5, proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "r2", TurnID: "t1", Title: "Network"}))
	s.Apply(event(t, 6, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", TurnID: "t1", Schema: json.RawMessage(`{"properties":{"b":{"title":"B"},"a":{"title":"A"}}}`)}))
	s.Apply(event(t, 7, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: "accept", Value: json.RawMessage(`{"a":"one","b":["x","y"]}`)}))

	blob, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromSnapshot(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Items, s.Items) {
		t.Fatalf("items after round trip = %+v\nwant %+v", back.Items, s.Items)
	}
	if a := itemOf(t, back, "c1").Approval; a != ApprovalAllowedAlways {
		t.Fatalf("restored tool approval = %q", a)
	}
	// The restored state still folds onto the records it restored.
	back.Apply(event(t, 8, proto.PermissionResolved, proto.PermissionResolvedPayload{RequestID: "r2", Outcome: proto.OutcomeAllowOnce}))
	if a := itemOf(t, back, ApprovalID("r2")).Approval; a != ApprovalAllowed {
		t.Fatalf("restored standalone approval after resolve = %q, want allowed", a)
	}
	want := []QuestionAnswer{{Key: "b", Question: "B", Answer: "x, y"}, {Key: "a", Question: "A", Answer: "one"}}
	if got := itemOf(t, back, QuestionID("q1")).Questions; !reflect.DeepEqual(got, want) {
		t.Fatalf("restored questions = %+v, want %+v", got, want)
	}
}

// A card is its own record: it gets no question item.
func TestCardElicitationGetsNoQuestionItem(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{
		RequestID: "card1", Prompt: "Add server?", Card: json.RawMessage(`{"kind":"mcp"}`),
	}))
	s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "card1", Action: "accept"}))
	if _, ok := s.ItemByID(QuestionID("card1")); ok {
		t.Fatal("a card elicitation got a question item")
	}
	if len(s.Items) != 1 || s.Items[0].Kind != ItemCard {
		t.Fatalf("items = %+v, want the card alone", s.Items)
	}
}

// Clone shares no question entries with the live state.
func TestCloneCopiesQuestions(t *testing.T) {
	s := New("s1")
	s.Apply(event(t, 1, proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q1", Prompt: "which?"}))
	c := s.Clone()
	s.Apply(event(t, 2, proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: "q1", Action: "accept", Value: json.RawMessage(`"this one"`)}))
	if got := c.Items[0].Questions[0].Answer; got != "" {
		t.Fatalf("clone saw the live answer %q", got)
	}
}
