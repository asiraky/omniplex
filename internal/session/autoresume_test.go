package session

import (
	"context"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// autoResume pins the global flag for one test, so a run never depends on the
// developer's own ~/.omniplex/config.json.
func autoResume(t *testing.T, on bool) {
	t.Helper()
	prev := loadUserConfig
	loadUserConfig = func() (userconfig.Config, error) {
		cfg := userconfig.Default()
		cfg.AutoResumeOnLimit = &on
		return cfg, nil
	}
	t.Cleanup(func() { loadUserConfig = prev })
}

// limitedTurn runs a turn and has the harness end it the way a provider out of
// budget does: an ordinary error, phrased for a human.
func limitedTurn(t *testing.T, a *Actor, fa *fakeAdapter, message string) adapter.PromptInput {
	t.Helper()
	ctx := context.Background()
	if _, err := a.Prompt(ctx, "do the work", nil, ""); err != nil {
		t.Fatal(err)
	}
	in := <-fa.session().prompts
	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: in.TurnID, StopReason: proto.StopError, Error: message,
	}))
	waitFor(t, func() bool {
		s, err := a.State(ctx)
		return err == nil && len(s.Turns) > 0 && s.Turns[len(s.Turns)-1].Done
	})
	return in
}

func armedResume(t *testing.T, a *Actor) *proto.ScheduledPrompt {
	t.Helper()
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := len(s.Scheduled) - 1; i >= 0; i-- {
		if p := s.Scheduled[i]; p.Kind == proto.ScheduleResume && (p.Status == "pending" || p.Status == "ready") {
			return &p
		}
	}
	return nil
}

// The message the harness prints is the only evidence that this failure fixes
// itself, so the turn it lands on says so — and the session arms itself for
// the moment the provider named rather than waiting to be asked.
func TestUsageLimitClassifiesTheTurnAndArmsAResume(t *testing.T) {
	autoResume(t, true)
	a, fa, _ := newTestActor(t)
	limitedTurn(t, a, fa, "You've hit your session limit · resets 11:10am (Australia/Brisbane)")

	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	turn := state.Turns[len(state.Turns)-1]
	if turn.Failure != proto.FailureUsageLimit {
		t.Fatalf("failure = %q, want %q", turn.Failure, proto.FailureUsageLimit)
	}
	if turn.ResetAt == 0 {
		t.Fatal("the turn kept no reset time, so nothing can wait for it")
	}
	armed := armedResume(t, a)
	if armed == nil {
		t.Fatal("no resume was armed")
	}
	if armed.ResumeOf != turn.ID || armed.Attempt != 1 {
		t.Fatalf("armed = %+v, want a first attempt at %s", armed, turn.ID)
	}
	// The buffer keeps the resume from arriving before the window reopens.
	if want := turn.ResetAt + int64(resumeBuffer/time.Millisecond); armed.DueAt != want {
		t.Fatalf("due = %d, want %d", armed.DueAt, want)
	}
	// Nobody authored this entry, so it names no zone — and it must not name
	// Go's "Local", which is not an IANA zone: Intl throws on it, and the
	// throw took the whole scheduled list down with it.
	if armed.TimeZone != "" {
		t.Fatalf("timezone = %q, want none: an armed resume has no authoring zone", armed.TimeZone)
	}
}

// A limit with no time is still a limit: it backs off rather than giving up,
// because the alternative is work that sits there until somebody notices.
func TestUsageLimitWithoutATimeBacksOff(t *testing.T) {
	autoResume(t, true)
	a, fa, _ := newTestActor(t)
	before := time.Now()
	limitedTurn(t, a, fa, "You've hit your usage limit.")

	armed := armedResume(t, a)
	if armed == nil {
		t.Fatal("no resume was armed")
	}
	due := time.UnixMilli(armed.DueAt)
	if due.Before(before.Add(limitBackoff[0]-time.Second)) || due.After(time.Now().Add(limitBackoff[0]+time.Minute)) {
		t.Fatalf("due %s is not the first backoff step after %s", due, before)
	}
}

func TestAutoResumeOffArmsNothing(t *testing.T) {
	autoResume(t, false)
	a, fa, _ := newTestActor(t)
	limitedTurn(t, a, fa, "You've hit your session limit · resets 11:10am (Australia/Brisbane)")
	if armed := armedResume(t, a); armed != nil {
		t.Fatalf("armed %+v with the flag off", armed)
	}

	// The switch on the card is the override: it arms this one failure
	// without touching the flag.
	p, err := a.SetAutoResume(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || armedResume(t, a) == nil {
		t.Fatal("the override armed nothing")
	}

	// And off again cancels it, which is what the switch does the other way.
	if _, err := a.SetAutoResume(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if armed := armedResume(t, a); armed != nil {
		t.Fatalf("still armed after disarming: %+v", armed)
	}
}

// The turn it starts when the window reopens is a continuation, not a prompt
// out of nowhere, and it counts the chain so a limit that keeps coming back
// backs off instead of being polled.
func TestArmedResumeContinuesTheWorkAndCountsAttempts(t *testing.T) {
	autoResume(t, true)
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	limitedTurn(t, a, fa, "You've hit your session limit · resets 11:10am (Australia/Brisbane)")

	armed := armedResume(t, a)
	if armed == nil {
		t.Fatal("no resume was armed")
	}
	if err := a.scheduleTick(ctx, armed.DueAt); err != nil {
		t.Fatal(err)
	}
	var second adapter.PromptInput
	select {
	case second = <-fa.session().prompts:
	case <-time.After(time.Second):
		t.Fatal("the armed resume never ran")
	}
	if second.Text != limitResumePrompt {
		t.Fatalf("resume prompt = %q", second.Text)
	}
	state, err := a.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	turn := state.Turns[len(state.Turns)-1]
	if turn.Recovery == nil || turn.Recovery.Cause != proto.RecoveryLimit || turn.Recovery.Attempt != 1 {
		t.Fatalf("recovery = %+v, want a first limit recovery", turn.Recovery)
	}

	// The same limit again: the second wait is the next step of the backoff,
	// not another go at the same moment.
	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: second.TurnID, StopReason: proto.StopError, Error: "You've hit your usage limit.",
	}))
	waitFor(t, func() bool {
		p := armedResume(t, a)
		return p != nil && p.Attempt == 2
	})
	next := armedResume(t, a)
	if due := time.UnixMilli(next.DueAt); due.Before(time.Now().Add(limitBackoff[1] - time.Minute)) {
		t.Fatalf("second wait %s is shorter than the second backoff step", time.Until(due))
	}
}

// A human who picks the work up themselves has taken it back. A resume armed
// hours ago must not land in the middle of what they are doing.
func TestStartingWorkCancelsAnArmedResume(t *testing.T) {
	autoResume(t, true)
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	limitedTurn(t, a, fa, "You've hit your session limit · resets 11:10am (Australia/Brisbane)")
	if armedResume(t, a) == nil {
		t.Fatal("no resume was armed")
	}

	if _, err := a.Prompt(ctx, "never mind, do this instead", nil, ""); err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	if armed := armedResume(t, a); armed != nil {
		t.Fatalf("a resume survived a human prompt: %+v", armed)
	}
}

// Everything else that fails is left alone: the card, the button, and no
// schedule that would retry a failure retrying cannot fix.
func TestOtherFailuresAreNotResumed(t *testing.T) {
	autoResume(t, true)
	a, fa, _ := newTestActor(t)
	limitedTurn(t, a, fa, "claude exited: connect ECONNREFUSED")

	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if turn := state.Turns[len(state.Turns)-1]; turn.Failure != "" {
		t.Fatalf("failure = %q, want unclassified", turn.Failure)
	}
	if armed := armedResume(t, a); armed != nil {
		t.Fatalf("armed %+v for a failure waiting cannot fix", armed)
	}
}
