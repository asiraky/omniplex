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
// held queue; once it, or anything else, starts a turn, the queue moves again.

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
const limitGrace = 24 * scheduleGrace

func limitScheduleID(turnID string) string { return "limit-" + turnID }

// followLimit keeps the continuation in step with the log. append calls it
// once the event is folded and fanned out, so every way a turn ends or starts
// — the harness, a refused prompt, the exit backstop — passes through here.
func (a *Actor) followLimit(ev proto.Event) {
	switch ev.Type {
	case proto.TurnFinished:
		var p proto.TurnFinishedPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			a.armLimit(p, ev.Timestamp)
		}
	case proto.TurnStarted:
		// The continuation's own start has already marked it sent, so this
		// only ever cancels one the conversation has moved past.
		a.disarmLimit("")
	case proto.ThreadAccountChanged:
		// The reset it waits for belongs to the account the thread left.
		a.disarmLimit("")
	}
}

// armLimit schedules the continuation for a turn that stopped at a limit
// with a known reset.
func (a *Actor) armLimit(p proto.TurnFinishedPayload, finishedAt int64) {
	// A reset at or before the finish is a window the harness reported stale;
	// sending at it would only hit the limit again. Only the finish that
	// left the thread limited arms: a stale close of an older turn does not.
	last := a.lastTurn()
	if a.state.Closed || p.Failure != proto.FailureLimit || p.ResetAt <= finishedAt ||
		last == nil || last.ID != p.TurnID || !a.state.QueueHeld() {
		return
	}
	id := limitScheduleID(p.TurnID)
	for _, s := range a.state.Scheduled {
		if s.ID == id {
			return // a repeated finish for the same turn
		}
	}
	a.disarmLimit(id)
	// No 24-hour cap as a human schedule has: a weekly limit resets days out.
	_ = a.append(proto.Emit(proto.PromptScheduled, proto.ScheduledPrompt{
		ID: id, Kind: proto.ScheduleLimit, Prompt: limitContinuePrompt,
		DueAt: p.ResetAt + limitDelay, TimeZone: "UTC",
		Model: a.state.Model, Mode: a.state.Mode, Effort: a.state.Effort,
		Status: "pending", Revision: 1,
	}))
}

// disarmLimit cancels every continuation still waiting to go out, except
// keep. Called when the thread moves on without it: a later "continue" would
// land in a conversation that is no longer where the limit left it.
func (a *Actor) disarmLimit(keep string) {
	for _, s := range append([]proto.ScheduledPrompt(nil), a.state.Scheduled...) {
		if s.Kind != proto.ScheduleLimit || s.ID == keep || (s.Status != "pending" && s.Status != "ready") {
			continue
		}
		s.Status = "cancelled"
		s.TurnID = ""
		s.Revision++
		_ = a.append(proto.Emit(proto.PromptScheduled, s))
	}
}
