package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
)

// ErrNothingToContinue is returned when a continue arrives for a thread whose
// last turn ended cleanly — a stale button on a screen someone left open.
var ErrNothingToContinue = errors.New("the last turn did not end in an error")

// Recovering an interrupted turn.
//
// A server restart kills the harness process mid-turn. The conversation
// survives — it lives in the log here and in the harness's own transcript —
// but nothing was driving it, so the work simply stopped. That is the freeze:
// not a hung process, an abandoned one.
//
// Resume already closes the interrupted turn and any request that was waiting
// on a human, so the thread reopens idle rather than stuck. This file is the
// other half: the thread is prompted to pick the work back up, so a restart
// costs a turn boundary rather than the task.

// maxRecoveryAttempts bounds consecutive self-started turns. A thread that
// takes the server down every time it resumes would otherwise restart itself
// forever; after this many tries it is left idle for a human to look at.
const maxRecoveryAttempts = 3

// recoveryWindow is how close together self-started turns have to be to count
// as one chain. A thread that was continued an hour ago and is interrupted
// again now is not crash-looping; it is just unlucky twice.
const recoveryWindow = 5 * time.Minute

// restartPrompt is deliberately about the state of the world rather than the
// state of the conversation. The harness repairs its own transcript on resume
// (an interrupted tool call is closed off before the next request), so what
// the agent cannot know is which of its side effects actually landed.
const restartPrompt = "[omniplex] The omniplex server restarted and interrupted your previous turn. " +
	"Any tool call that was in flight did not report its result back, so you cannot assume it succeeded or failed. " +
	"Check the real state of the work first — the files, the diff, whatever you had just run — then continue from where you left off. " +
	"Do not redo work that is already done, and do not start over."

// deadWorkIntro heads the list of background work that died with the process
// running it: a server restart, or the harness exiting. Nothing will ever
// report back, so the agent has to be told, or it waits on a result that
// never comes.
const deadWorkIntro = "This background work was cancelled before it finished, when the process running it went away. " +
	"It will not report back on its own, so resume or restart what you still need and wait for its result:"

// The dead-work list is for the agent to act on, not a transcript of the
// thread: a workflow that fanned out fifty agents needs to know that it did,
// not to read fifty lines about it.
const (
	maxDeadWork      = 10
	maxDeadWorkLabel = 160
)

// continuePrompt is the same instruction for the other way a turn is left
// unfinished: it ended in an error, and a human asked to carry on. Sending the
// restart prompt here — which is what used to happen — told the agent, and the
// screen showing it, that the server had restarted when nothing of the sort
// had happened. A turn that failed to authenticate is the common case.
const continuePrompt = "[omniplex] Your previous turn ended in an error before it finished. " +
	"Any tool call that was in flight did not report its result back, so you cannot assume it succeeded or failed. " +
	"Check the real state of the work first — the files, the diff, whatever you had just run — then continue from where you left off. " +
	"Do not redo work that is already done, and do not start over."

// recoveryPlan is the continuation a resumed thread owes itself: the turn
// record it will carry and the prompt that explains it.
type recoveryPlan struct {
	rec    proto.TurnRecovery
	prompt string
	// note is the dead-work list alone, ready to send. A human prompt that
	// gets in before the continuation supersedes it and carries this instead,
	// as does the next one after a continuation that never started.
	note string
	// phase is what the thread's row keeps saying while the continuation is
	// owed — "turn" or "background" — so a restart before it is sent finds
	// the thread again.
	phase string
}

// planRecovery decides whether a resumed thread should continue by itself,
// and under which attempt number. Everything it needs is in the log, so a
// second restart before the first one's news was delivered plans the same
// thing again: a turn the server died in is unfinished, or was closed as
// FailureRestart, and work it killed is live or interrupted.
//
// A thread wakes itself when its turn was cut, or when a subagent or a monitor
// died: those are work the agent is waiting on. A thread that lost only
// shells does not — a dev server dying is worth knowing, not worth a turn —
// so held is the note for its next human prompt instead.
func planRecovery(state *projection.State, now int64) (plan *recoveryPlan, held string) {
	if state.Closed {
		return nil, ""
	}
	// The newest turn is what a continuation resumes and counts on from.
	last, heard := lastHeard(state)
	dead := deadWork(state, heardSince(heard), true)
	list := deadWorkNote(dead)
	note := ownNote(list)
	cut := heard != nil && (!heard.Done || heard.Failure == proto.FailureRestart)
	if !cut && !awaited(dead) {
		return nil, note
	}
	attempt := 1
	if last != nil && last.Recovery != nil && now-last.StartedAt < recoveryWindow.Milliseconds() {
		attempt = last.Recovery.Attempt + 1
	}
	if attempt > maxRecoveryAttempts {
		// Left for a human, who should still hear what died.
		return nil, note
	}
	plan = &recoveryPlan{rec: proto.TurnRecovery{Attempt: attempt, Cause: proto.RecoveryRestart}, note: note, phase: "background"}
	if last != nil {
		plan.rec.ResumeOf = last.ID
	}
	if cut {
		plan.prompt = withNote(restartPrompt, list)
		plan.phase = "turn"
	} else {
		plan.prompt = note
	}
	return plan, ""
}

// owedNote is the news of dead work the agent has not heard, for a thread
// whose process is still running: what Resume or a harness exit interrupted
// since the last prompt that reached the agent. Like planRecovery it reads
// only the log, so a prompt that carried the note and was then refused — a
// continuation that ran into a usage limit — owes it again.
func owedNote(state *projection.State) string {
	_, heard := lastHeard(state)
	return ownNote(deadWorkNote(deadWork(state, heardSince(heard), false)))
}

// lastHeard is the newest turn, and the newest one whose prompt reached the
// agent: what it last heard. A prompt the harness refused told it nothing.
// Claude reports a usage limit's refusal as a message from the agent, so a
// turn the limit ended counts only what came before its last message.
func lastHeard(state *projection.State) (last, heard *projection.Turn) {
	limited := map[string]bool{}
	for _, t := range state.Turns {
		if t.Failure == proto.FailureLimit {
			limited[t.ID] = true
		}
	}
	acted := map[string]bool{}
	for i := len(state.Items) - 1; i >= 0; i-- {
		it := state.Items[i]
		if it.TurnID == "" || it.Role == "user" {
			continue
		}
		if limited[it.TurnID] {
			delete(limited, it.TurnID)
			if it.Kind == projection.ItemMessage {
				continue
			}
		}
		acted[it.TurnID] = true
	}
	for i := len(state.Turns) - 1; i >= 0; i-- {
		t := &state.Turns[i]
		if last == nil {
			last = t
		}
		if reached(t, acted) {
			return last, t
		}
	}
	return last, nil
}

// heardSince is when the agent last heard anything: dead work from before
// then it has already been told about.
func heardSince(heard *projection.Turn) int64 {
	if heard == nil {
		return 0
	}
	return heard.StartedAt
}

// ownNote marks a dead-work list sent on its own as the server's.
func ownNote(list string) string {
	if list == "" {
		return ""
	}
	return "[omniplex] " + list
}

// reached says whether the agent saw a turn's prompt. One still running, or
// cut by a restart, was under way; one that ended in an error with nothing
// from the agent in it was refused, so whatever it carried is still owed.
func reached(t *projection.Turn, acted map[string]bool) bool {
	return !t.Done || t.Failure == proto.FailureRestart || t.StopReason != proto.StopError || acted[t.ID]
}

// awaited is whether dead work includes something the agent was waiting on.
func awaited(dead []projection.Job) bool {
	for _, j := range dead {
		if j.Kind == proto.JobAgent || j.Kind == proto.JobMonitor {
			return true
		}
	}
	return false
}

// deadWork is the background work that died with its process and that the
// agent has not heard about since: interrupted after the last prompt it read,
// or — when gone says the process that ran it is gone — still live. A job
// started inside another — a shell a subagent ran — is reported under its
// parent: restarting the subagent is what brings it back.
func deadWork(state *projection.State, since int64, gone bool) []projection.Job {
	dead := map[string]bool{}
	byToolCall := map[string]string{}
	for _, j := range state.Jobs {
		unheard := (gone && !proto.JobDone(j.Status)) || (j.Status == proto.JobInterrupted && j.FinishedAt > since)
		if unheard && j.Kind != proto.JobInert {
			dead[j.ID] = true
			if j.ToolCallID != "" {
				byToolCall[j.ToolCallID] = j.ID
			}
		}
	}
	// A harness does not always name a job's parent job, but the tool call
	// that started it still sits inside the parent's: Claude reports a
	// subagent's background shell that way.
	calledFrom := map[string]string{}
	for _, it := range state.Items {
		if it.Kind == projection.ItemTool && it.ParentID != "" {
			calledFrom[it.ID] = it.ParentID
		}
	}
	var out []projection.Job
	for _, j := range state.Jobs {
		if !dead[j.ID] || dead[j.ParentJobID] || dead[byToolCall[calledFrom[j.ToolCallID]]] {
			continue
		}
		out = append(out, j)
	}
	return out
}

// deadWorkNote lists dead work for the agent, or is empty when there is none.
func deadWorkNote(dead []projection.Job) string {
	if len(dead) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(deadWorkIntro)
	for i, j := range dead {
		if i == maxDeadWork {
			fmt.Fprintf(&b, "\n- and %d more", len(dead)-maxDeadWork)
			break
		}
		fmt.Fprintf(&b, "\n- %s: %s", j.Kind, jobLabel(j))
	}
	return b.String()
}

// jobLabel is the most specific name the harness gave a job.
func jobLabel(j projection.Job) string {
	label := j.ID
	for _, s := range []string{j.Name, j.WorkflowName, j.Role, j.TaskType} {
		if s = strings.TrimSpace(s); s != "" {
			label = s
			break
		}
	}
	if r := []rune(label); len(r) > maxDeadWorkLabel {
		label = string(r[:maxDeadWorkLabel-1]) + "…"
	}
	return label
}

// withNote puts a note the agent is owed in front of a prompt.
func withNote(note, prompt string) string {
	switch {
	case note == "":
		return prompt
	case prompt == "":
		return note
	}
	return note + "\n\n" + prompt
}

// lastTurn is the newest turn, or nil on a thread that has never run one.
// Actor-loop only, like everything else that touches the projection.
func (a *Actor) lastTurn() *projection.Turn {
	if len(a.state.Turns) == 0 {
		return nil
	}
	return &a.state.Turns[len(a.state.Turns)-1]
}

// Recover continues an interrupted turn, or wakes a thread whose background
// work a restart killed, if this actor was resumed from one. It is a no-op on
// every other actor, so callers need not ask first.
func (a *Actor) Recover(ctx context.Context) error {
	// The plan stays set until the actor loop takes the prompt: it is what
	// keeps a queued prompt from starting ahead of the continuation in the
	// gap between here and the command being handled.
	a.mu.Lock()
	plan := a.recovery
	a.mu.Unlock()
	if plan == nil {
		return nil
	}
	_, err := a.call(ctx, command{kind: cmdPrompt, plan: plan})
	return err
}

// dropRecovery gives up on a continuation that will not start, so the queue
// it was holding back can run.
func (a *Actor) dropRecovery(ctx context.Context) {
	_, _ = a.call(ctx, command{kind: cmdDropRecovery})
}

// retryableRecovery says whether a continuation that failed to start is worth
// trying again later. A harness that is not up yet, or that refused for some
// passing reason, may take it in a minute. One that needs a login or is out of
// quota will refuse again until a human acts, as will one that rejected the
// request as invalid, and a thread that is closed or already busy has nothing
// left to continue.
func retryableRecovery(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotReady):
		return true
	case errors.Is(err, ErrClosed), errors.Is(err, ErrBusy), errors.Is(err, context.Canceled):
		return false
	}
	switch adapter.FailureOf(err) {
	case proto.FailureAuth, proto.FailureLimit:
		return false
	}
	var rpc *jsonrpc.RPCError
	if errors.As(err, &rpc) {
		switch rpc.Code {
		case -32600, -32601, -32602:
			// The harness rejected the request itself: invalid, unknown or
			// malformed. Asking again asks the same thing.
			return false
		}
	}
	return true
}

// recoveryRetries is how long a continuation that failed to start waits
// before each further try.
var recoveryRetries = []time.Duration{60 * time.Second, 180 * time.Second}

// retryRecovery takes the error a first try ended in and runs try again
// after each of the manager's retry delays, while it keeps failing for a
// reason that might pass. It returns the last error, or nil once a try
// succeeds.
func (m *Manager) retryRecovery(id string, err error, try func() error) error {
	for _, wait := range m.recoveryRetries {
		if !retryableRecovery(err) {
			return err
		}
		m.logf("continue %s: %v; trying again in %s", id, err, wait)
		select {
		case <-time.After(wait):
		case <-m.recoverCtx.Done():
			return err
		}
		err = try()
	}
	return err
}

// recoverActor sends a resumed actor its continuation, retrying a start that
// failed. One that never starts is dropped, so the thread is left idle for a
// human rather than holding its queue back forever.
func (m *Manager) recoverActor(a *Actor) {
	try := func() error { return a.Recover(m.recoverCtx) }
	err := m.retryRecovery(a.ID, try(), try)
	if m.recoverCtx.Err() != nil {
		// The server is stopping: the plan is still owed, and the actor's
		// shutdown leaves its row saying so for the next start.
		return
	}
	if err != nil {
		m.logf("continue interrupted work on %s: %v", a.ID, err)
		a.dropRecovery(m.recoverCtx)
	}
}

// recoverAll resumes every thread that was mid-turn, or waiting on background
// work, when the server stopped.
//
// Resume is otherwise lazy — idle threads come back when a command needs the
// harness, while read-only presenter attachment restores only their projection.
// Work in flight is different: an agent that was three tool calls into a task,
// or waiting on a subagent, should not wait for a command or an open browser
// tab before it continues.
func (m *Manager) recoverAll(ctx context.Context) {
	metas, err := m.store.ListThreads(ctx)
	if err != nil {
		m.logf("recover interrupted threads: %v", err)
		return
	}
	for _, meta := range metas {
		if !interruptedPhase(meta.Phase) {
			continue
		}
		// Get resumes the thread, which closes the interrupted turn and its
		// jobs, and schedules the continuation prompt. A harness that would
		// not start is tried again in the background, so one stuck thread
		// does not hold up the rest.
		id := meta.ID
		get := func() error { _, err := m.Get(m.recoverCtx, id); return err }
		if err := get(); err != nil {
			if !retryableRecovery(err) {
				m.logf("recover %s: %v", id, err)
				continue
			}
			go func() {
				if err := m.retryRecovery(id, err, get); err != nil {
					m.logf("recover %s: %v", id, err)
				}
			}()
			continue
		}
		m.logf("thread %s had work in flight when the server stopped; resumed it", id)
	}
}

// interruptedPhase says whether a thread row was left saying work was in
// flight: a turn, or background work the agent was waiting on.
func interruptedPhase(phase string) bool {
	return phase == "turn" || phase == "background"
}

// ResumeInterrupted brings back every thread that a restart caught mid-turn.
// It runs in the background: a slow harness start must not hold up the server
// binding its listeners.
func (m *Manager) ResumeInterrupted() {
	go m.recoverAll(context.Background())
}
