package thread

import (
	"context"
	"errors"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/ephemeral"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
)

// errRestartPrivate turns away a restart of a private chat. Its harness keeps
// nothing on disk, so the conversation is only in the running process, and a
// new process would start it over.
var errRestartPrivate = errors.New("a private chat's conversation lives only in its running harness, so restarting it would lose the conversation; start a new chat instead")

// RestartThread replaces the thread's harness process with a new one on the
// same conversation, so the agent picks up the thread's current setup: system
// prompt, MCP servers, skills and plugins. The transcript is kept. Shells and
// background jobs die with the old process.
//
// On a busy thread the restart waits for the turn, and whatever else the
// process is in the middle of, to end, and pending is true; CancelRestart
// takes it back until then. Otherwise it happens before this returns. The
// thread is not activated first: a restart starts the process itself.
func (m *Manager) RestartThread(ctx context.Context, threadID string) (pending bool, err error) {
	a, err := m.View(ctx, threadID)
	if err != nil {
		return false, err
	}
	return a.Restart(ctx)
}

// restartLive is RestartThread for a thread whose process is running; see
// Actor.restartLive.
func (m *Manager) restartLive(ctx context.Context, threadID string) (pending bool, err error) {
	a, err := m.View(ctx, threadID)
	if err != nil {
		return false, err
	}
	return a.restartLive(ctx)
}

// CancelRestart takes back a restart that is waiting for the thread to settle.
// With none waiting it does nothing.
func (m *Manager) CancelRestart(ctx context.Context, threadID string) error {
	a, err := m.View(ctx, threadID)
	if err != nil {
		return err
	}
	return a.CancelRestart(ctx)
}

// Restart is RestartThread for this actor.
func (a *Actor) Restart(ctx context.Context) (pending bool, err error) {
	return a.restartBy(ctx, cmdRestart)
}

// restartLive is Restart for a thread whose process is running. With none
// running it starts nothing and is ErrNotReady: an agent thread's next start
// restarts onto the agent's revisions by itself (agentSetupBehind).
func (a *Actor) restartLive(ctx context.Context) (pending bool, err error) {
	return a.restartBy(ctx, cmdRestartLive)
}

func (a *Actor) restartBy(ctx context.Context, kind string) (pending bool, err error) {
	v, err := a.call(ctx, command{kind: kind})
	if err != nil {
		return false, err
	}
	pending, _ = v.(bool)
	return pending, nil
}

// CancelRestart is Manager.CancelRestart for this actor.
func (a *Actor) CancelRestart(ctx context.Context) error {
	_, err := a.call(ctx, command{kind: cmdCancelRestart})
	return err
}

// requestRestart restarts an idle thread now, and marks a busy one to restart
// once it settles (restartIfDue). Asking again while one waits changes
// nothing. A thread with no process restarts now whatever its state says: a
// turn a server restart cut off will never end on its own, and the restart's
// start settles it as activation would.
func (a *Actor) requestRestart(ctx context.Context) (pending bool, err error) {
	if err := a.restartRefusal(); err != nil {
		return false, err
	}
	if a.sess != nil && a.restartBusy() {
		if !a.state.RestartPending {
			if err := a.append(proto.Emit(proto.ThreadRestartRequested, proto.ThreadRestartRequestedPayload{})); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	return false, a.restart(ctx)
}

func (a *Actor) cancelRestart() error {
	if a.state.Closed {
		return ErrClosed
	}
	if !a.state.RestartPending {
		return nil
	}
	return a.append(proto.Emit(proto.ThreadRestartCancelled, proto.ThreadRestartCancelledPayload{}))
}

// restartRefusal says why this thread cannot be restarted at all, busy or
// not, or nil.
func (a *Actor) restartRefusal() error {
	switch {
	case a.state.Closed:
		return ErrClosed
	case ephemeral.Is(a.ID):
		return errRestartPrivate
	case a.preparing || (a.state.Phase != "idle" && a.state.Phase != "turn"):
		// Provisioning, cleaning up, or failed at either: there is no
		// conversation running to restart.
		return ErrNotReady
	case a.adapter == nil:
		return a.noAdapter()
	}
	return nil
}

// restartBusy says the process is in the middle of something a restart would
// cut short: a turn, a Stop, a change on its way to the harness, a rewind, a
// prompt the harness was handed and has yet to read, or the continuation of
// work a server restart interrupted, not yet sent. Shells and background jobs
// do not count. They die with the process, and that is accepted.
func (a *Actor) restartBusy() bool {
	if a.turnActive != "" || a.state.Phase != "idle" || a.stopping != nil || a.controlPending > 0 || a.rewinding || len(a.sent) > 0 {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.recovery != nil
}

// restartIfDue carries out a restart that was waiting for the thread to
// settle, once it has. The loop calls it after every command, before the
// queue is dispatched, so a prompt queued behind the restart runs on the new
// process. An actor with no process leaves it waiting: one restored only to
// be looked at must not start a harness on its own, and the next start of
// its process carries the restart out (cmdActivate).
func (a *Actor) restartIfDue(ctx context.Context) {
	if !a.state.RestartPending || a.sess == nil || a.state.Closed || a.restartBusy() {
		return
	}
	// A failure is in the log as the restart's note, for whoever looks.
	_ = a.restart(ctx)
}

// restart replaces the harness process with a new one resuming the same
// conversation, built from the thread's setup as it is now. A process that
// fails to start leaves the thread with none, as an account switch does: the
// next command that needs a harness resumes the conversation the ordinary
// way. Either way the outcome is logged, which also ends a requested
// restart.
func (a *Actor) restart(ctx context.Context) error {
	fresh := a.sess == nil
	if !fresh {
		// Dropping the process first is what marks its exit, still on its way
		// through the inbox, as stale. It goes synchronously: the harness
		// writes its conversation as it goes, and has to be gone before the
		// adapter rewrites what it pinned there.
		old := a.dropProcess()
		a.cancelProcessAsks()
		_ = old.Close()
		for _, em := range interruptedJobs(a.state) {
			a.append(em)
		}
		for _, em := range clearedStatuses(a.state) {
			a.append(em)
		}
	}
	err := a.startProcess(ctx, adapter.CreateOptions{
		Cwd: a.Cwd, Model: a.state.Model, Mode: a.state.Mode, Effort: a.state.Effort, ServiceTier: a.state.ServiceTier,
		Resume: true, Restart: hasRunATurn(a.state),
	})
	if err != nil {
		a.noteRestart(err)
		return err
	}
	if fresh {
		// The process this replaces is the one a server restart took, so
		// this is the thread's first start since: it settles what that
		// process left, as activation would have.
		a.settleResumed()
	}
	a.holdRestored()
	a.noteRestart(nil)
	return nil
}

// noteRestart logs a restart's outcome as the timeline's note, which also ends
// a restart that was waiting. A failure goes to the server log too.
func (a *Actor) noteRestart(err error) {
	if err == nil {
		a.append(proto.Emit(proto.ThreadRestarted, proto.ThreadRestartedPayload{PromptRev: a.agentPromptRev}))
		return
	}
	a.logf("restart %s: %v", a.ID, err)
	a.append(proto.Emit(proto.ThreadRestarted, proto.ThreadRestartedPayload{Error: err.Error()}))
}

// hasRunATurn says the conversation has run a turn, so its harness may have
// pinned the setup it ran on. Not the session id: a harness can report one
// as it starts, before it has written anything.
func hasRunATurn(s *projection.State) bool {
	return len(s.Turns) > 0
}

// noAdapter is why a thread with no adapter cannot start a harness. A pending
// actor can be restored leniently, for cleanup, with none behind it.
func (a *Actor) noAdapter() error {
	if a.activationError != nil {
		return a.activationError
	}
	return errors.New("this thread's provider instance is no longer configured")
}
