package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
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

// deadWorkIntro heads the list of background work a restart killed. The
// harness that started it is gone, so nothing will ever report back: the
// agent has to be told, or it waits on a result that never comes.
const deadWorkIntro = "The omniplex server restarted, and this background work was cancelled before it finished. " +
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
	// note is the dead-work list alone. A human prompt that gets in before
	// the continuation supersedes it, and carries this instead.
	note string
}

// planRecovery decides whether a resumed thread should continue by itself,
// and under which attempt number. It reads the state as of the moment before
// Resume closed the interrupted turn and its jobs, so an unfinished turn there
// is exactly "the server died while this was running", and a live job is work
// that died with it.
//
// A thread wakes itself when its turn was cut, or when a subagent or a monitor
// was still running: those are work the agent is waiting on. A thread that had
// only shells running does not — a dev server dying is worth knowing, not
// worth a turn — so held is the note for its next human prompt instead.
func planRecovery(state *projection.State, now int64) (plan *recoveryPlan, held string) {
	if state.Closed {
		return nil, ""
	}
	dead := deadWork(state)
	note := deadWorkNote(dead)
	var last *projection.Turn
	if len(state.Turns) > 0 {
		last = &state.Turns[len(state.Turns)-1]
	}
	cut := last != nil && !last.Done
	if !cut && !state.LiveJobs().Working() {
		if note == "" {
			return nil, ""
		}
		return nil, "[omniplex] " + note
	}
	attempt := 1
	if last != nil && last.Recovery != nil && now-last.StartedAt < recoveryWindow.Milliseconds() {
		attempt = last.Recovery.Attempt + 1
	}
	if attempt > maxRecoveryAttempts {
		return nil, ""
	}
	plan = &recoveryPlan{rec: proto.TurnRecovery{Attempt: attempt, Cause: proto.RecoveryRestart}, note: note}
	if last != nil {
		plan.rec.ResumeOf = last.ID
	}
	switch {
	case !cut:
		plan.prompt = "[omniplex] " + note
	case note != "":
		plan.prompt = restartPrompt + "\n\n" + note
	default:
		plan.prompt = restartPrompt
	}
	return plan, ""
}

// deadWork is the background work still live in a state whose process is
// gone, as the agent should hear about it. A job started inside another — a
// shell a subagent ran — is reported under its parent: restarting the
// subagent is what brings it back.
func deadWork(state *projection.State) []projection.Job {
	live := map[string]bool{}
	for _, j := range state.Jobs {
		if !proto.JobDone(j.Status) && j.Kind != proto.JobInert {
			live[j.ID] = true
		}
	}
	var out []projection.Job
	for _, j := range state.Jobs {
		if live[j.ID] && !live[j.ParentJobID] {
			out = append(out, j)
		}
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
// quota will refuse again until a human acts, and a thread that is closed or
// already busy has nothing left to continue.
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
