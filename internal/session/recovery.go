package session

import (
	"context"
	"errors"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
)

// ErrNothingToContinue is returned when a continue arrives for a session whose
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
// on a human, so the session reopens idle rather than stuck. This file is the
// other half: the session is prompted to pick the work back up, so a restart
// costs a turn boundary rather than the task.

// maxRecoveryAttempts bounds consecutive self-started turns. A session that
// takes the server down every time it resumes would otherwise restart itself
// forever; after this many tries it is left idle for a human to look at.
const maxRecoveryAttempts = 3

// restartPrompt is deliberately about the state of the world rather than the
// state of the conversation. The harness repairs its own transcript on resume
// (an interrupted tool call is closed off before the next request), so what
// the agent cannot know is which of its side effects actually landed.
const restartPrompt = "[omniplex] The omniplex server restarted and interrupted your previous turn. " +
	"Any tool call that was in flight did not report its result back, so you cannot assume it succeeded or failed. " +
	"Check the real state of the work first — the files, the diff, whatever you had just run — then continue from where you left off. " +
	"Do not redo work that is already done, and do not start over."

// continuePrompt is the same instruction for the other way a turn is left
// unfinished: it ended in an error, and a human asked to carry on. Sending the
// restart prompt here — which is what used to happen — told the agent, and the
// screen showing it, that the server had restarted when nothing of the sort
// had happened. A turn that failed to authenticate is the common case.
const continuePrompt = "[omniplex] Your previous turn ended in an error before it finished. " +
	"Any tool call that was in flight did not report its result back, so you cannot assume it succeeded or failed. " +
	"Check the real state of the work first — the files, the diff, whatever you had just run — then continue from where you left off. " +
	"Do not redo work that is already done, and do not start over."

// planRecovery decides whether a resumed session should continue by itself,
// and under which attempt number. It reads the state as of the moment before
// Resume closed the interrupted turn, so an unfinished turn there is exactly
// "the server died while this was running".
func planRecovery(state *projection.State) *proto.TurnRecovery {
	if state.Closed {
		return nil
	}
	// Only the newest unfinished turn matters. Older ones, if any survived an
	// earlier crash, were already closed by the resume that followed it.
	for i := len(state.Turns) - 1; i >= 0; i-- {
		turn := state.Turns[i]
		if turn.Done {
			return nil
		}
		attempt := 1
		if turn.Recovery != nil {
			attempt = turn.Recovery.Attempt + 1
		}
		if attempt > maxRecoveryAttempts {
			return nil
		}
		return &proto.TurnRecovery{ResumeOf: turn.ID, Attempt: attempt, Cause: proto.RecoveryRestart}
	}
	return nil
}

// lastTurn is the newest turn, or nil on a session that has never run one.
// Actor-loop only, like everything else that touches the projection.
func (a *Actor) lastTurn() *projection.Turn {
	if len(a.state.Turns) == 0 {
		return nil
	}
	return &a.state.Turns[len(a.state.Turns)-1]
}

// Recover continues an interrupted turn, if this actor was resumed from one.
// It is a no-op on every other actor, so callers need not ask first.
func (a *Actor) Recover(ctx context.Context) error {
	a.mu.Lock()
	rec := a.recovery
	a.recovery = nil
	a.mu.Unlock()
	if rec == nil {
		return nil
	}
	_, err := a.call(ctx, command{kind: cmdPrompt, prompt: restartPrompt, recovery: rec})
	return err
}

// recoverAll resumes every session that was mid-turn when the server stopped.
//
// Resume is otherwise lazy — idle sessions come back when a command needs the
// harness, while read-only presenter attachment restores only their projection.
// Work in flight is different: an agent that was three tool calls into a task
// should not wait for a command or an open browser tab before it continues.
func (m *Manager) recoverAll(ctx context.Context) {
	metas, err := m.store.ListSessions(ctx)
	if err != nil {
		m.logf("recover interrupted sessions: %v", err)
		return
	}
	for _, meta := range metas {
		if meta.Phase != "turn" {
			continue
		}
		// Get resumes the session, which closes the interrupted turn and
		// schedules the continuation prompt.
		if _, err := m.Get(ctx, meta.ID); err != nil {
			m.logf("recover %s: %v", meta.ID, err)
			continue
		}
		m.logf("session %s was mid-turn when the server stopped; resumed it", meta.ID)
	}
}

// ResumeInterrupted brings back every session that a restart caught mid-turn.
// It runs in the background: a slow harness start must not hold up the server
// binding its listeners.
func (m *Manager) ResumeInterrupted() {
	go m.recoverAll(context.Background())
}
