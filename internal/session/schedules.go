package session

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

const scheduleGrace = int64(time.Hour / time.Millisecond)

func (a *Actor) Schedule(ctx context.Context, p proto.ScheduledPrompt) error {
	_, err := a.call(ctx, command{kind: "schedule_prompt", schedule: &p})
	return err
}
func (a *Actor) ScheduleAction(ctx context.Context, action, id string, revision int) error {
	if action != "cancel_schedule" && action != "send_schedule" {
		return errors.New("unknown schedule action")
	}
	_, err := a.call(ctx, command{kind: action, schedule: &proto.ScheduledPrompt{ID: id, Revision: revision}})
	return err
}
func (a *Actor) scheduleTick(ctx context.Context, now int64) error {
	_, err := a.call(ctx, command{kind: "schedule_tick", now: now})
	return err
}

func (a *Actor) handleSchedule(c command) error {
	now := c.now
	if now == 0 {
		now = time.Now().UnixMilli()
	}
	if a.state.Closed {
		return ErrClosed
	}
	if c.kind == "schedule_tick" {
		return a.dispatchScheduled(now)
	}
	p := *c.schedule
	var old *proto.ScheduledPrompt
	for i := range a.state.Scheduled {
		if a.state.Scheduled[i].ID == p.ID {
			copy := a.state.Scheduled[i]
			old = &copy
			break
		}
	}
	if p.ID == "" {
		return errors.New("schedule id is required")
	}
	if c.kind == "schedule_prompt" && p.Revision == 0 && old != nil {
		return nil
	} // retried creation
	if old != nil && (old.Revision != p.Revision || old.Status == "sent" || old.Status == "cancelled") {
		return errors.New("schedule changed or already sent; refresh and try again")
	}
	if old == nil && (p.Revision != 0 || c.kind != "schedule_prompt") {
		return errors.New("schedule no longer exists")
	}
	if c.kind == "schedule_prompt" {
		if strings.TrimSpace(p.Prompt) == "" && len(p.Images) == 0 {
			return errors.New("write a message first")
		}
		if p.DueAt <= now || p.DueAt-now > 24*scheduleGrace {
			return errors.New("choose a time in the next 24 hours")
		}
		if _, err := time.LoadLocation(p.TimeZone); err != nil || p.TimeZone == "" || p.TimeZone == "Local" {
			return errors.New("choose a valid IANA timezone")
		}
		if old == nil {
			p.Model, p.Mode, p.Effort = a.state.Model, a.state.Mode, a.state.Effort
		} else {
			p.Model, p.Mode, p.Effort = old.Model, old.Mode, old.Effort
		}
		p.Status = "pending"
		p.Error = ""
		p.TurnID = ""
	} else {
		p = *old
		if c.kind == "cancel_schedule" {
			p.Status = "cancelled"
			p.TurnID = ""
		} else {
			p.Status = "ready"
			p.DueAt = now
			p.Error = ""
			p.TurnID = ""
		}
	}
	p.Revision++
	return a.append(proto.Emit(proto.PromptScheduled, p))
}

func (a *Actor) dispatchScheduled(now int64) error {
	if a.scheduleReady == nil {
		a.scheduleReady = &sync.Map{}
	}
	schedules := append([]proto.ScheduledPrompt(nil), a.state.Scheduled...)
	sort.SliceStable(schedules, func(i, j int) bool { return schedules[i].DueAt < schedules[j].DueAt })
	for _, p := range schedules {
		if (p.Status != "pending" && p.Status != "ready") || p.DueAt > now {
			continue
		}
		_, pickedUp := a.scheduleReady.Load(a.ID + ":" + p.ID)
		if p.Status == "pending" || !pickedUp {
			p.Revision++
			if now-p.DueAt > graceFor(p) {
				p.Status = "missed"
				p.Error = "The host did not pick up this message within one hour."
			} else {
				p.Status = "ready"
			}
			if err := a.append(proto.Emit(proto.PromptScheduled, p)); err != nil {
				return err
			}
			if p.Status == "missed" {
				continue
			}
			a.scheduleReady.Store(a.ID+":"+p.ID, true)
		}

		// Resume in the actor, without racing another activation or human command.
		if a.sess == nil {
			reply := make(chan cmdResult, 1)
			a.handle(command{kind: cmdActivate, resume: true, reply: reply})
			if result := <-reply; result.err != nil {
				return a.failSchedule(p, result.err)
			}
		}
		if a.turnActive != "" || len(a.state.Pending) > 0 || len(a.state.Elicitations) > 0 || len(a.state.Queued) > 0 {
			continue
		}
		a.mu.Lock()
		recovering := a.recovery != nil
		a.mu.Unlock()
		if recovering {
			continue
		}
		images := a.resolveImages(p.Images)
		for _, img := range images {
			if img.Path == "" {
				return a.failSchedule(p, errors.New("a scheduled attachment is missing"))
			}
		}
		// Restore this instruction's saved settings before sending. They become the
		// session's active settings, visibly, just like a manual settings change.
		if err := a.scheduleSettings(p); err != nil {
			return a.failSchedule(p, err)
		}
		// A resume the server armed itself says so on the turn it starts, so
		// the transcript reads as a continuation rather than as a prompt
		// nobody remembers writing — and so the next limit can count the
		// chain (see autoresume.go).
		var recovery *proto.TurnRecovery
		if p.Kind == proto.ScheduleResume {
			recovery = &proto.TurnRecovery{ResumeOf: p.ResumeOf, Attempt: p.Attempt, Cause: proto.RecoveryLimit}
		}
		_, err := a.startTurn(context.Background(), p.Prompt, images, recovery, p.ID)
		if err != nil {
			// A durable turn already records a provider failure. Never resend it.
			for _, current := range a.state.Scheduled {
				if current.ID == p.ID && current.TurnID != "" {
					return err
				}
			}
			return a.failSchedule(p, err)
		}
		// Worth a buzz: this is work restarting hours after the person who
		// asked for it walked away, and the phone in their pocket is the only
		// thing that will tell them it is moving again.
		if p.Kind == proto.ScheduleResume {
			a.notify(Notice{Kind: NoticeResumed, Body: "Usage limit lifted — picked the work back up"})
		}
		return nil
	}
	return nil
}

// graceFor is how late a due schedule may be picked up. An instruction a human
// wrote for 9am is stale by lunchtime — they meant 9am. A resume the server
// armed is not: it exists precisely because nobody was watching, and a laptop
// that was shut for the evening is the ordinary case rather than the strange
// one, so it stays good for most of a day.
func graceFor(p proto.ScheduledPrompt) int64 {
	if p.Kind == proto.ScheduleResume {
		return 12 * scheduleGrace
	}
	return scheduleGrace
}
func (a *Actor) failSchedule(p proto.ScheduledPrompt, err error) error {
	p.Status = "failed"
	p.Error = err.Error()
	p.Revision++
	return a.append(proto.Emit(proto.PromptScheduled, p))
}
func (a *Actor) scheduleSettings(p proto.ScheduledPrompt) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if p.Model != a.state.Model {
		s, ok := a.sess.(adapter.ModelSwitcher)
		if !ok {
			return errors.New("provider cannot restore the scheduled model")
		}
		if err := s.SetModel(ctx, p.Model); err != nil {
			return err
		}
		if err := a.append(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{ReplaceSettings: true, Model: p.Model, Mode: a.state.Mode})); err != nil {
			return err
		}
	}
	if p.Mode != a.state.Mode {
		s, ok := a.sess.(adapter.ModeSwitcher)
		if !ok {
			return errors.New("provider cannot restore scheduled permissions")
		}
		if err := s.SetMode(ctx, p.Mode); err != nil {
			return err
		}
		if err := a.append(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{ReplaceSettings: true, Model: a.state.Model, Mode: p.Mode})); err != nil {
			return err
		}
	}
	if p.Effort != a.state.Effort {
		s, ok := a.sess.(adapter.EffortSwitcher)
		if !ok {
			return errors.New("provider cannot restore scheduled effort")
		}
		if err := s.SetEffort(ctx, p.Effort); err != nil {
			return err
		}
		if err := a.append(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{Effort: &p.Effort})); err != nil {
			return err
		}
	}
	return nil
}

// StartScheduler is called once after providers and attachments are configured.
// The indexed lookup is cheap; no transcript needs to be attached or replayed
// until its schedule is due. Slow providers do not delay other sessions.
func (m *Manager) StartScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	m.schedulerCancel = cancel
	m.schedulerWG.Add(1)
	go func() {
		defer m.schedulerWG.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var workers sync.WaitGroup
		defer workers.Wait()
		var active sync.Map
		for {
			ids, err := m.store.DueScheduleSessions(ctx, time.Now().UnixMilli())
			if err != nil && ctx.Err() == nil {
				m.logf("schedule lookup: %v", err)
			}
			for _, id := range ids {
				if _, loaded := active.LoadOrStore(id, true); loaded {
					continue
				}
				workers.Add(1)
				go func(id string) {
					defer workers.Done()
					defer active.Delete(id)
					a, err := m.View(ctx, id)
					if err == nil {
						err = a.scheduleTick(ctx, time.Now().UnixMilli())
					}
					if err != nil && ctx.Err() == nil {
						m.logf("scheduled prompt on %s: %v", id, fmt.Errorf("dispatch: %w", err))
					}
				}(id)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
