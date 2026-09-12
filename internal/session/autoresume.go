package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/usagelimit"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// Picking work back up when the provider's window reopens.
//
// A usage limit is the only failure that repairs itself. The account is out of
// budget for the next few hours; nothing is broken, nobody has to decide
// anything, and the fix is to wait and continue. Left alone that wait is dead
// time: the window resets at 3am and the half-finished task sits there until
// somebody opens the tab and presses a button.
//
// So the session arms itself. A limited turn schedules its own continuation at
// the moment the harness named, using the same durable rails a human-scheduled
// prompt rides (see schedules.go): the entry is an event in the log, the
// scheduler finds it by index without attaching anything, and a server restart
// in the meantime changes nothing. That reuse is the point — a private timer
// in memory would be the one thing that does not survive the six hours it has
// to wait through.
//
// Two things keep it from becoming a robot that will not stop: the backoff
// below is bounded, and any turn the human starts cancels a pending resume, so
// "I will pick this up myself" always wins over a schedule armed hours ago.

// limitResumePrompt is what the session says to itself when the window
// reopens. Like the other recovery prompts it talks about the state of the
// world rather than the conversation: the agent's own transcript already shows
// where it stopped, and what it cannot know is which of its side effects
// landed before the provider cut it off.
const limitResumePrompt = "[omniplex] Your previous turn stopped because the provider's usage limit was reached. " +
	"That limit has now reset and you are being continued automatically. " +
	"Any tool call that was in flight did not report its result back, so you cannot assume it succeeded or failed. " +
	"Check the real state of the work first — the files, the diff, whatever you had just run — then continue from where you left off. " +
	"Do not redo work that is already done, and do not start over."

// resumeBuffer is added to the reset time the harness named. Providers round
// the minute they print, and a resume that arrives thirty seconds early is
// refused and burns one of the attempts below for nothing.
const resumeBuffer = 90 * time.Second

// limitBackoff is the wait when the harness named no time, by attempt. The
// windows are usually five hours, so this starts short enough to catch a limit
// that was nearly over already and then stops guessing and settles into the
// shape of a real window. Running out of the list is where auto-resume gives
// up and leaves the session for a human.
var limitBackoff = []time.Duration{
	30 * time.Minute,
	1 * time.Hour,
	2 * time.Hour,
	3 * time.Hour,
	4 * time.Hour,
	5 * time.Hour,
	10 * time.Hour,
}

// maxLimitAttempts is how many consecutive auto-resumes one piece of work may
// trigger. It is the length of the backoff because an attempt beyond the list
// has nothing left to wait for.
var maxLimitAttempts = len(limitBackoff)

// autoResumeDefault is the flag's value for an operator who has never opened
// settings. On: the failure it handles is unambiguous, the wait is otherwise
// dead time, and the resumed turn announces itself both in the transcript and
// on the phone.
const autoResumeDefault = true

// loadUserConfig is the indirection tests use to state the flag without
// writing to the developer's own ~/.omniplex/config.json.
var loadUserConfig = userconfig.Load

// autoResumeConfigured reads the global flag. A config that cannot be read is
// not a reason to strand the work, so the default stands.
func autoResumeConfigured() bool {
	cfg, err := loadUserConfig()
	if err != nil {
		return autoResumeDefault
	}
	if cfg.AutoResumeOnLimit == nil {
		return autoResumeDefault
	}
	return *cfg.AutoResumeOnLimit
}

// classifyLimit upgrades a failed turn the harness described in prose into one
// the rest of the server can act on.
//
// It runs here, on every emission on its way into the log, rather than in each
// adapter: the harnesses all report a limit the same way — a turn that ended
// with an error message meant for a human — and doing it once means a harness
// added tomorrow is covered on the day it is added. An adapter that classifies
// the failure itself is left alone.
func classifyLimit(em proto.Emission, now time.Time) proto.Emission {
	if em.Type != proto.TurnFinished {
		return em
	}
	p, ok := em.Payload.(proto.TurnFinishedPayload)
	if !ok || p.Failure != "" || p.StopReason != proto.StopError {
		return em
	}
	hit, limited := usagelimit.Detect(p.Error, now)
	if !limited {
		return em
	}
	p.Failure = proto.FailureUsageLimit
	if !hit.ResetAt.IsZero() {
		p.ResetAt = hit.ResetAt.UnixMilli()
	}
	em.Payload = p
	return em
}

// armAutoResume schedules the continuation of a turn a usage limit ended.
// Actor loop only, called from append once the turn.finished is durable.
func (a *Actor) armAutoResume(em proto.Emission, now time.Time) {
	if em.Type != proto.TurnFinished {
		return
	}
	p, ok := em.Payload.(proto.TurnFinishedPayload)
	if !ok || p.Failure != proto.FailureUsageLimit || a.state.Closed {
		return
	}
	if !autoResumeConfigured() {
		return
	}
	if err := a.armResume(p, now); err != nil && !errors.Is(err, errResumeExhausted) {
		a.logf("arm auto-resume on %s: %v", a.ID, err)
	}
}

// errResumeExhausted is the honest end of the road: the work has been resumed
// as often as it is going to be, and the session is left for a human.
var errResumeExhausted = errors.New("the usage limit kept coming back; auto-resume gave up")

// armResume writes the scheduled continuation for a limited turn. It is also
// what the switch on the interrupted card calls when the human arms one by
// hand, which is why it is separate from the policy check above.
func (a *Actor) armResume(p proto.TurnFinishedPayload, now time.Time) error {
	// The attempt count rides on the turn being resumed rather than on a scan
	// of old schedules: an auto-resumed turn carries the recovery that started
	// it, so the chain counts itself. A turn the human started resets it,
	// because they have seen the problem.
	attempt := 1
	for i := len(a.state.Turns) - 1; i >= 0; i-- {
		turn := a.state.Turns[i]
		if turn.ID != p.TurnID {
			continue
		}
		if turn.Recovery != nil && turn.Recovery.Cause == proto.RecoveryLimit {
			attempt = turn.Recovery.Attempt + 1
		}
		break
	}
	if attempt > maxLimitAttempts {
		return errResumeExhausted
	}

	due := time.UnixMilli(p.ResetAt).Add(resumeBuffer)
	// A reset time in the past — a clock that disagrees with the provider's,
	// or a message read hours after the fact — is not a reason to hammer the
	// provider on the next tick, but it is a reason to try soon.
	if p.ResetAt == 0 || !due.After(now) {
		due = now.Add(limitBackoff[attempt-1])
	}

	// Only one resume is ever pending: re-arming replaces the entry rather
	// than stacking a second continuation behind the first.
	if err := a.cancelPendingResumes(""); err != nil {
		return err
	}
	return a.append(proto.Emit(proto.PromptScheduled, proto.ScheduledPrompt{
		ID:       uuid.NewString(),
		Kind:     proto.ScheduleResume,
		Attempt:  attempt,
		ResumeOf: p.TurnID,
		Prompt:   limitResumePrompt,
		DueAt:    due.UnixMilli(),
		// No timezone. A human's schedule is anchored to the zone they wrote
		// it in — "9am, my time, wherever I am". This one is anchored to an
		// instant the provider chose, so there is no authoring zone to record,
		// and the reader's own is the honest one to show it in. (Writing
		// time.Local here put the literal string "Local" in the log, which is
		// not an IANA zone: Intl threw on it and took the whole list down.)
		Model:  a.state.Model,
		Mode:   a.state.Mode,
		Effort: a.state.Effort,
		Status: "pending",
	}))
}

// pendingResume is the armed continuation, if there is one. The UI reads it to
// decide what the switch beside the Continue button says.
func (a *Actor) pendingResume() *proto.ScheduledPrompt {
	for i := len(a.state.Scheduled) - 1; i >= 0; i-- {
		p := a.state.Scheduled[i]
		if p.Kind == proto.ScheduleResume && (p.Status == "pending" || p.Status == "ready") {
			return &p
		}
	}
	return nil
}

// cancelPendingResumes cancels every armed continuation except one, named by
// id. A human who starts a turn has taken the work back; a resume armed hours
// ago must not land in the middle of it.
func (a *Actor) cancelPendingResumes(except string) error {
	for {
		p := a.pendingResume()
		if p == nil || p.ID == except {
			return nil
		}
		p.Status = "cancelled"
		p.Revision++
		p.TurnID = ""
		if err := a.append(proto.Emit(proto.PromptScheduled, *p)); err != nil {
			return err
		}
	}
}

// notify hands a notice to the manager's watcher, if one is installed. Actor
// loop only, like everything else that reads the projection.
func (a *Actor) notify(n Notice) {
	a.mu.Lock()
	fn := a.onNotice
	a.mu.Unlock()
	if fn != nil {
		fn(n)
	}
}

// SetAutoResume arms or disarms the continuation for the turn a usage limit
// just ended — the switch beside the Continue button, which overrides the
// global flag for this session and this failure only.
func (a *Actor) SetAutoResume(ctx context.Context, on bool) (*proto.ScheduledPrompt, error) {
	v, err := a.call(ctx, command{kind: cmdAutoResume, resume: on})
	if err != nil {
		return nil, err
	}
	p, _ := v.(*proto.ScheduledPrompt)
	return p, nil
}

// handleAutoResume runs SetAutoResume on the actor loop.
func (a *Actor) handleAutoResume(c command) (any, error) {
	if !c.resume {
		return nil, a.cancelPendingResumes("")
	}
	last := a.lastTurn()
	if last == nil || !last.Done || last.Failure != proto.FailureUsageLimit {
		return nil, errors.New("the last turn did not stop on a usage limit")
	}
	now := time.Now()
	if err := a.armResume(proto.TurnFinishedPayload{
		TurnID:  last.ID,
		ResetAt: last.ResetAt,
	}, now); err != nil {
		return nil, err
	}
	return a.pendingResume(), nil
}
