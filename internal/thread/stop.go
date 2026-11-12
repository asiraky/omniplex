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

// stopRequest is one Stop: the turn it is meant to end, the prompts that were
// waiting behind it, and the timer that kills the harness if that turn is
// still running when the grace runs out. A prompt sent after Stop is not one
// of them: it was written knowing the turn was being stopped, and starts once
// it has.
type stopRequest struct {
	turnID string
	queued []string
	timer  *time.Timer
}

// offLoop runs a call to the harness on its own goroutine and hands the
// result back to the loop as a command, so a harness that is slow to answer
// holds up only the caller waiting on it. Harness events, questions for a
// human and every other command carry on meanwhile. then runs on the loop;
// it is where the caller's reply is sent, and it must check that whatever it
// is about to act on is still so. Only a read goes this way: it changes
// nothing, so it does not matter what it overtakes.
func (a *Actor) offLoop(timeout time.Duration, call func(context.Context) (any, error), then func(v any, err error)) {
	a.harnessCall(nil, nil, timeout, call, then)
}

// inOrder is offLoop for a call that changes what the harness does: Stop, a
// steer, a setting, a job stopped, a composer action. These reach the harness
// one at a time, in the order they were asked for, and their results come
// back to the loop in that order too. Side by side, a steer still on its way
// when Stop is pressed could land after the interrupt, and the harness would
// start a turn for a prompt the user stopped; two model changes could be
// taken A then B and come back B then A, leaving the log saying A while the
// harness runs B. A call stuck behind one the harness never answers waits
// with it, which for Stop is what the kill timer is for.
func (a *Actor) inOrder(timeout time.Duration, call func(context.Context) (any, error), then func(v any, err error)) {
	prev, next := a.controlTail, make(chan struct{})
	a.controlTail = next
	process := a.process
	a.controlPending++
	a.harnessCall(prev, next, timeout, call, func(v any, err error) {
		// A dropped process took its pending count with it (dropProcess).
		if process == a.process {
			a.controlPending--
		}
		then(v, err)
	})
}

// harnessCall waits for after, makes the call, posts its result, and then
// closes done. Closing done only once the result is in the inbox is what
// keeps the next call's result behind it.
func (a *Actor) harnessCall(after <-chan struct{}, done chan struct{}, timeout time.Duration, call func(context.Context) (any, error), then func(v any, err error)) {
	go func() {
		if done != nil {
			defer close(done)
		}
		if after != nil {
			select {
			case <-after:
			case <-a.quit:
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		v, err := call(ctx)
		cancel()
		select {
		case a.inbox <- command{kind: cmdHarnessReply, onReply: func() { then(v, err) }}:
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
	a.inOrder(30*time.Second, func(ctx context.Context) (any, error) {
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
// idle. A new prompt is answered as queued either way; one sent ahead of its
// turn by hand (refuse) hears why it was not.
func (a *Actor) steer(st adapter.Steerer, queueID string, in adapter.PromptInput, reply chan cmdResult, refuse bool) {
	sess := a.sess
	a.markSent(queueID)
	a.inOrder(30*time.Second, func(ctx context.Context) (any, error) {
		return nil, st.Steer(ctx, in)
	}, func(_ any, err error) {
		switch {
		case err != nil:
			delete(a.sent, queueID)
			a.logf("steer on %s: %v", a.ID, err)
			a.holdBehind(queueID)
		case sess != a.sess:
			// The process that took it is gone, and the next one never saw
			// it.
			err = errHarnessRestarted
			delete(a.sent, queueID)
			a.logf("steer on %s: the harness was replaced before it read the prompt", a.ID)
			a.holdBehind(queueID)
		case a.queuedEntry(queueID) != nil:
			a.append(proto.Emit(proto.PromptSent, proto.PromptSentPayload{QueueID: queueID}))
		}
		if err != nil && refuse {
			reply <- cmdResult{err: err}
			return
		}
		reply <- cmdResult{value: PromptResult{QueueID: queueID}}
	})
}

// requestStop asks the harness to interrupt the running turn, and arranges
// for the process to be killed if the turn has not ended stopGrace later. The
// caller has checked that there is a turn and a harness to ask.
func (a *Actor) requestStop() {
	req := &stopRequest{turnID: a.turnActive}
	for _, q := range a.state.Queued {
		req.queued = append(req.queued, q.QueueID)
	}
	req.timer = time.AfterFunc(stopGrace, func() {
		select {
		case a.inbox <- command{kind: cmdStopExpired, stop: req}:
		case <-a.quit:
		}
	})
	a.stopping = req
	sess := a.sess
	a.inOrder(stopGrace, func(ctx context.Context) (any, error) {
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
			a.dropStopped(req)
		}
	})
}

// settleStop closes a Stop whose turn has ended, by the harness's own hand
// or anyone else's. That turn was the last thing the queue waited on.
func (a *Actor) settleStop() {
	if a.stopping == nil || a.turnActive == a.stopping.turnID {
		return
	}
	req := a.stopping
	a.clearStopping()
	a.dropStopped(req)
}

// dropStopped cancels the prompts that were waiting when Stop was pressed.
// The harness discards whatever of them it was holding (adapter.Steerer).
func (a *Actor) dropStopped(req *stopRequest) {
	for _, id := range req.queued {
		if a.queuedEntry(id) != nil {
			a.append(proto.Emit(proto.PromptDequeued, proto.PromptDequeuedPayload{QueueID: id, Reason: proto.DequeueCancelled}))
		}
		delete(a.sent, id)
	}
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
	// Dropping the process first is what marks its last events, its exit and
	// its last questions, still on their way through the inbox, as stale.
	old := a.dropProcess()
	if req := a.stopping; req != nil {
		a.clearStopping()
		a.dropStopped(req)
	}
	a.cancelProcessAsks()
	if turn != "" {
		a.append(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turn, StopReason: proto.StopCancelled}))
	}
	for _, em := range interruptedJobs(a.state) {
		a.append(em)
	}
	// Its exit is dropped as stale, so its extensions' statuses go here.
	for _, em := range clearedStatuses(a.state) {
		a.append(em)
	}
	if old != nil {
		// Close kills the process tree and waits for it, which is not
		// something the loop can sit through.
		go func() { _ = old.Close() }()
	}
	a.logf("stop on %s: killed the harness to end turn %s", a.ID, turn)
}

// dropProcess forgets the harness process, for one about to be killed or
// replaced, and returns it. Its process generation is retired with it, and
// so is its line of control calls: the next process's calls do not wait on
// the old one's, which fail fast once it is killed.
func (a *Actor) dropProcess() adapter.Session {
	old := a.sess
	a.sess, a.sent, a.controlTail, a.controlPending = nil, nil, nil, 0
	a.process++
	return old
}

// newHost is the host services for a harness process about to be started,
// under a generation of its own. Only for a thread with no process: one still
// live must go through dropProcess first, or its control calls would never be
// counted off controlPending and the queue would wait on them for ever.
func (a *Actor) newHost() hostServices {
	a.process++
	return hostServices{a: a, process: a.process}
}

// releaseAsks unblocks every adapter goroutine waiting on a human, for a
// process that will never hear the answer.
func (a *Actor) releaseAsks() { a.releaseAsksExcept(nil) }

// releaseAsksExcept answers cancelled every ask whose request id open does
// not hold.
func (a *Actor) releaseAsksExcept(open map[string]bool) {
	for id, ch := range a.pendingPerm {
		if open[id] {
			continue
		}
		select {
		case ch <- adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}:
		default:
		}
		delete(a.pendingPerm, id)
	}
	for id, ch := range a.pendingElicit {
		if open[id] {
			continue
		}
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
