package thread

import (
	"encoding/json"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// Continuing after a usage limit.
//
// A turn that stops at the account's usage limit is work cut off, not work
// finished, and the harness usually says when the window reopens. Rather than
// wait for a human to come back and type "continue", the server arms a one-off
// scheduled prompt for just after the reset. It is an ordinary schedule — in
// the log, in the due index, cancellable from the chip — so it survives a
// restart and the scheduler sends it like any other.
//
// While the thread is limited its queue is held (projection.QueueHeld): every
// queued prompt would only hit the limit again. The continuation jumps the
// held queue, and so does a human's schedule while none is armed; once a turn
// starts, the queue moves again.
//
// A Codex turn can finish before the harness knows the reset. The adapter
// back-fills it with turn.limit_reset, and the continuation is armed then.
//
// A turn the limit refused never reached the agent, so news it carried of
// work a restart killed is still owed. Whatever goes past the held queue
// next — the continuation, a prompt sent by hand — carries it; the queue
// waits behind that turn and gets none of it.

// limitContinuePrompt is the continuation's text, in the style of the
// recovery prompts: what the agent cannot know is which side effects landed.
const limitContinuePrompt = "[omniplex] Your previous turn stopped because the account hit its usage limit, which has now reset. " +
	"Any tool call that was in flight did not report its result back, so you cannot assume it succeeded or failed. " +
	"Check the real state of the work first — the files, the diff, whatever you had just run — then continue from where you left off. " +
	"Do not redo work that is already done, and do not start over."

// limitDelay pads the reset, so the continuation does not land the moment
// the window reopens and find the provider's clock a little behind ours.
const limitDelay = int64(time.Minute / time.Millisecond)

// limitGrace is how late a continuation may still go out. A weekly window
// can reset while the host is down; a day late is still worth continuing,
// and after that the conversation has gone stale.
const limitGrace = int64(24 * time.Hour / time.Millisecond)

func limitScheduleID(turnID string) string { return "limit-" + turnID }

// followLimit keeps the continuation in step with the log. append calls it
// once the event is folded and fanned out, so every way a turn ends or starts
// — the harness, a refused prompt, the exit backstop — passes through here.
func (a *Actor) followLimit(ev proto.Event) {
	switch ev.Type {
	case proto.TurnFinished:
		var p proto.TurnFinishedPayload
		if json.Unmarshal(ev.Payload, &p) == nil && p.Failure == proto.FailureLimit {
			if a.state.QueueHeld() {
				// Read from the log, so it replaces rather than adds to
				// a note already held: that is in it too.
				a.heldNote = owedNote(a.state)
			}
			a.armLimit(p.TurnID, p.ResetAt, ev.Timestamp)
		}
	case proto.TurnLimitReset:
		// A back-fill only fills a gap: a continuation already waiting
		// stands, whoever armed it.
		var p proto.TurnLimitResetPayload
		if json.Unmarshal(ev.Payload, &p) == nil && !a.limitArmed() {
			a.armLimit(p.TurnID, p.ResetAt, ev.Timestamp)
		}
	case proto.TurnStarted:
		// The continuation's own start has already marked it sent, so this
		// only ever cancels one the conversation has moved past.
		a.disarmLimit()
	case proto.ThreadAccountChanged:
		// The reset it waits for belongs to the account the thread left.
		a.disarmLimit()
	}
}

// armLimit schedules the continuation for a turn that stopped at a limit
// with a known reset, as of at.
func (a *Actor) armLimit(turnID string, resetAt, at int64) {
	// A reset at or before now is a window the harness reported stale;
	// sending at it would only hit the limit again. Only the turn that left
	// the thread limited arms: a stale close of an older turn does not. Nor
	// does a turn the human stopped — Codex finishes one late, after Stop
	// has come and gone.
	last := a.lastTurn()
	if a.state.Closed || resetAt <= at || last == nil || last.ID != turnID ||
		!a.state.QueueHeld() || turnID == a.stoppedTurn {
		return
	}
	id := limitScheduleID(turnID)
	for _, s := range a.state.Scheduled {
		if s.ID == id {
			return // a repeated finish for the same turn
		}
	}
	a.disarmLimit()
	// No 24-hour cap as a human schedule has: a weekly limit resets days out.
	_ = a.append(proto.Emit(proto.PromptScheduled, proto.ScheduledPrompt{
		ID: id, Kind: proto.ScheduleLimit, Prompt: limitContinuePrompt,
		DueAt: resetAt + limitDelay, TimeZone: "UTC",
		Model: a.state.Model, Mode: a.state.Mode, Effort: a.state.Effort,
		Status: "pending", Revision: 1,
	}))
}

// limitArmed reports whether a continuation is still waiting to go out.
func (a *Actor) limitArmed() bool {
	for _, s := range a.state.Scheduled {
		if liveLimit(s) {
			return true
		}
	}
	return false
}

func liveLimit(s proto.ScheduledPrompt) bool {
	return s.Kind == proto.ScheduleLimit && (s.Status == "pending" || s.Status == "ready")
}

// disarmLimit cancels every continuation still waiting to go out. Called
// when the thread moves on without it: a later "continue" would land in a
// conversation that is no longer where the limit left it.
func (a *Actor) disarmLimit() {
	for _, s := range append([]proto.ScheduledPrompt(nil), a.state.Scheduled...) {
		if !liveLimit(s) {
			continue
		}
		s.Status = "cancelled"
		s.TurnID = ""
		s.Revision++
		_ = a.append(proto.Emit(proto.PromptScheduled, s))
	}
}
