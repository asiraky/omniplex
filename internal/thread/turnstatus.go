package thread

import "github.com/asiraky/omniplex/internal/proto"

// TurnStatus returns why the running turn has gone quiet — retrying a model
// request, compacting context — or nil when nothing is held. It lives only in
// memory: it is never in the log, so a restarted server starts with none,
// which is right, because the turn it described died with the process. Safe
// from any goroutine.
func (a *Actor) TurnStatus() *proto.TurnStatusPayload {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.turnStatus == nil {
		return nil
	}
	st := *a.turnStatus
	return &st
}

// holdTurnStatus takes a turn.status emission in place of appending it. A
// status is only about a running turn, so one that arrives while the thread
// is idle is dropped; a payload with no state clears what is held.
func (a *Actor) holdTurnStatus(em proto.Emission) {
	p, _ := em.Payload.(proto.TurnStatusPayload)
	if p.State == "" || a.turnActive == "" {
		a.setTurnStatus(nil)
		return
	}
	a.setTurnStatus(&p)
}

// clearsTurnStatus says whether an appended event means the harness is moving
// again, so whatever status was held is stale. Adapters send their own clear;
// this is the safety net for one that forgets, or dies before it can. Output
// from inside a subagent does not count: the status is about the top-level
// conversation, which can still be retrying while a subagent it spawned works.
// A turn ending is not here: the actor clears once no turn is active, so a
// stale turn.finished for some other turn leaves the live one's status alone.
func clearsTurnStatus(em proto.Emission) bool {
	switch p := em.Payload.(type) {
	case proto.MessageChunkPayload:
		return p.ParentToolCallID == ""
	case proto.ToolCallStartedPayload:
		return p.ParentToolCallID == ""
	}
	switch em.Type {
	case proto.ContextCompacted, proto.ThreadClosed:
		return true
	}
	return false
}

// setTurnStatus replaces the held status and tells every subscriber, but only
// when it actually changed: a harness that repeats itself costs a phone
// nothing. Only the actor goroutine writes the status, so it compares before
// taking the lock, and every message chunk with nothing held costs no lock.
func (a *Actor) setTurnStatus(next *proto.TurnStatusPayload) {
	prev := a.turnStatus
	if (prev == nil && next == nil) || (prev != nil && next != nil && *prev == *next) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.turnStatus = next
	for _, sub := range a.subs {
		select {
		case sub.StatusChanged <- struct{}{}:
		default: // already pending; the reader sends whatever is current
		}
	}
}
