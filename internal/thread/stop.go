package thread

import (
	"context"
	"errors"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
)

// stopGrace is how long Stop waits for the harness to end the turn before it
// kills the process. Every harness has some way to ignore an interrupt — a
// promise it never settles, a compaction that does not listen — and a Stop
// button that does nothing is worse than one that costs the process. A var so
// tests need not wait it out.
var stopGrace = 10 * time.Second

// errHarnessRestarted is a setting the harness took just before its process
// was replaced. The new process never heard of it, so it is not recorded.
var errHarnessRestarted = errors.New("the harness restarted before the change took effect; try again")

// stopRequest is one Stop: the turn it is meant to end, and the timer that
// kills the harness if that turn is still running when the grace runs out.
type stopRequest struct {
	turnID string
	timer  *time.Timer
}

// offLoop runs a call to the harness on its own goroutine and hands the
// result back to the loop as a command, so a harness that is slow to answer
// holds up only the caller waiting on it. Harness events, questions for a
// human and every other command carry on meanwhile. then runs on the loop;
// it is where the caller's reply is sent, and it must check that whatever it
// is about to act on is still so.
func (a *Actor) offLoop(timeout time.Duration, call func(context.Context) (any, error), then func(v any, err error)) {
	sess := a.sess
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		v, err := call(ctx)
		cancel()
		select {
		case a.inbox <- command{kind: cmdHarnessReply, from: sess, done: func() { then(v, err) }}:
		case <-a.quit:
		}
	}()
}

// setting changes one of the thread's settings in the harness and, once the
// harness has taken it, records it. A process replaced while it was being
// asked took the change with it, so that is an error to try again rather
// than a setting the log would claim and the new process does not have.
func (a *Actor) setting(call func(context.Context) error, changed proto.ThreadConfigChangedPayload, reply chan cmdResult) {
	sess := a.sess
	a.offLoop(30*time.Second, func(ctx context.Context) (any, error) {
		return nil, call(ctx)
	}, func(_ any, err error) {
		switch {
		case err != nil:
			reply <- cmdResult{err: err}
		case sess != a.sess:
			reply <- cmdResult{err: errHarnessRestarted}
		default:
			a.append(proto.Emit(proto.ThreadConfigChanged, changed))
			reply <- cmdResult{}
		}
	})
}

// steer hands a queued prompt to the running harness. It counts as sent from
// the moment it is on its way: the queue must not start it, nor a human take
// it back, while the harness may be reading it. If the hand-over fails it is
// an ordinary queued prompt again, which the queue starts once the thread is
// idle.
func (a *Actor) steer(st adapter.Steerer, queueID string, in adapter.PromptInput, reply chan cmdResult) {
	sess := a.sess
	a.markSent(queueID)
	a.offLoop(30*time.Second, func(ctx context.Context) (any, error) {
		return nil, st.Steer(ctx, in)
	}, func(_ any, err error) {
		switch {
		case err != nil:
			delete(a.sent, queueID)
			a.logf("steer on %s: %v", a.ID, err)
		case sess != a.sess:
			// The process that took it is gone, and the next one never saw
			// it.
			delete(a.sent, queueID)
			a.logf("steer on %s: the harness was replaced before it read the prompt", a.ID)
		case a.isQueued(queueID):
			a.append(proto.Emit(proto.PromptSent, proto.PromptSentPayload{QueueID: queueID}))
		}
		reply <- cmdResult{value: PromptResult{QueueID: queueID}}
	})
}

// stop asks the harness to interrupt the running turn, and arranges for the
// process to be killed if the turn has not ended stopGrace later. The caller
// has checked that there is a turn and a harness to ask.
func (a *Actor) stop() {
	req := &stopRequest{turnID: a.turnActive}
	req.timer = time.AfterFunc(stopGrace, func() {
		select {
		case a.inbox <- command{kind: cmdStopExpired, stop: req}:
		case <-a.quit:
		}
	})
	a.stopping = req
	sess := a.sess
	a.offLoop(stopGrace, func(ctx context.Context) (any, error) {
		return nil, sess.Cancel(ctx)
	}, func(_ any, err error) {
		if err != nil {
			a.logf("stop on %s: %v", a.ID, err)
		}
		// The queue goes after the harness has been told, not before: a steer
		// it read in the moment between would otherwise vanish from the log
		// although the model saw it. A turn that already ended took the
		// queue with it (settleStop), and a killed harness took everything.
		if a.stopping == req {
			a.dropQueue()
		}
	})
}

// settleStop closes a Stop whose turn has ended, by the harness's own hand
// or anyone else's. That turn was the last thing the queue waited on.
func (a *Actor) settleStop() {
	if a.stopping == nil || a.turnActive == a.stopping.turnID {
		return
	}
	a.clearStopping()
	a.dropQueue()
}

func (a *Actor) clearStopping() {
	if a.stopping != nil {
		a.stopping.timer.Stop()
		a.stopping = nil
	}
}

// forceStop ends the running turn by killing the harness, for a harness that
// did not stop when asked. The turn is closed here rather than by the
// process's exit: the adapter would close it as a crash, and this was a Stop.
// The actor stays, idle and with no process, as an account switch leaves it,
// and the next command that needs a harness resumes the conversation in a
// new one.
func (a *Actor) forceStop() {
	turn := a.turnActive
	old := a.sess
	// Dropping the session first is what marks the old process's last events
	// and its exit, still on their way through the inbox, as stale.
	a.sess = nil
	a.clearStopping()
	a.dropQueue()
	a.cancelProcessAsks()
	if turn != "" {
		a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turn, StopReason: proto.StopCancelled}))
	}
	for _, em := range interruptedJobs(a.state) {
		a.append(em)
	}
	if old != nil {
		// Close kills the process tree and waits for it, which is not
		// something the loop can sit through.
		go func() { _ = old.Close() }()
	}
	a.logf("stop on %s: killed the harness to end turn %s", a.ID, turn)
}

// releaseAsks unblocks every adapter goroutine waiting on a human, for a
// process that will never hear the answer.
func (a *Actor) releaseAsks() {
	for id, ch := range a.pendingPerm {
		select {
		case ch <- adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}:
		default:
		}
		delete(a.pendingPerm, id)
	}
	for id, ch := range a.pendingElicit {
		select {
		case ch <- adapter.ElicitationResult{Action: "cancel"}:
		default:
		}
		delete(a.pendingElicit, id)
	}
}

// cancelProcessAsks closes every question a harness process that is gone was
// waiting on, both for the adapter goroutine still blocked on it and in the
// log. A card the server still holds is not the process's: it stays
// answerable. One the server lost is cancelled with the rest.
func (a *Actor) cancelProcessAsks() {
	a.releaseAsks()
	for _, pending := range append([]projection.PendingPermission(nil), a.state.Pending...) {
		a.append(proto.Emit(proto.PermissionResolved, proto.PermissionResolvedPayload{RequestID: pending.RequestID, Outcome: proto.OutcomeCancelled}))
	}
	for _, pending := range append([]projection.PendingElicitation(nil), a.state.Elicitations...) {
		if pending.IsCard() && a.liveCard(pending.RequestID) {
			continue
		}
		a.append(proto.Emit(proto.ElicitationResolved, proto.ElicitationResolvedPayload{RequestID: pending.RequestID, Action: "cancel"}))
	}
}
