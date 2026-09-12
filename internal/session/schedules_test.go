package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

func scheduled(t *testing.T, a *Actor, id string) proto.ScheduledPrompt {
	t.Helper()
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Scheduled {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("missing schedule %s", id)
	return proto.ScheduledPrompt{}
}
func scheduleInput(id string) proto.ScheduledPrompt {
	return proto.ScheduledPrompt{ID: id, Prompt: "work overnight", DueAt: time.Now().Add(time.Minute).UnixMilli(), TimeZone: "Australia/Brisbane"}
}

func TestScheduledDispatchIsDueOnceAndIndependentOfStop(t *testing.T) {
	a, fa, st := newTestActor(t)
	ctx := context.Background()
	p := scheduleInput("overnight")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	} // duplicate command after reconnect
	if err := a.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt-1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fa.session().prompts:
		t.Fatal("sent early")
	default:
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fa.session().prompts:
		if got.Text != p.Prompt {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("not delivered")
	}
	if err := a.scheduleTick(ctx, p.DueAt+1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fa.session().prompts:
		t.Fatal("sent twice")
	default:
	}
	if scheduled(t, a, p.ID).Status != "sent" {
		t.Fatal("not sent")
	}
	ids, err := st.DueScheduleSessions(ctx, p.DueAt)
	if err != nil || len(ids) != 0 {
		t.Fatalf("index still due: %v %v", ids, err)
	}
}
func TestScheduledBusyWaitDoesNotExpireAndNormalQueueIsNotBlocked(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	p := scheduleInput("later")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	turn, err := a.Prompt(ctx, "now", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	if scheduled(t, a, p.ID).Status != "ready" {
		t.Fatal("not waiting")
	}
	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turn.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
	if err := a.scheduleTick(ctx, p.DueAt+2*scheduleGrace); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fa.session().prompts:
	case <-time.After(time.Second):
		t.Fatal("busy wait expired")
	}
}
func TestScheduledCatchUpBoundaryAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		late   int64
		status string
	}{{"within", scheduleGrace, "sent"}, {"missed", scheduleGrace + 1, "missed"}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "restart.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			m := NewManager(st, t.Logf, &fakeAdapter{})
			a, err := m.Create(ctx, "fake", "", t.TempDir(), "", "")
			if err != nil {
				t.Fatal(err)
			}
			p := scheduleInput("restart")
			if err := a.Schedule(ctx, p); err != nil {
				t.Fatal(err)
			}
			id := a.ID
			m.Shutdown()
			fa := &fakeAdapter{}
			m = NewManager(st, t.Logf, fa)
			defer m.Shutdown()
			ids, err := st.DueScheduleSessions(ctx, p.DueAt+tc.late)
			if err != nil || len(ids) != 1 {
				t.Fatalf("lost schedule %v %v", ids, err)
			}
			a, err = m.View(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.scheduleTick(ctx, p.DueAt+tc.late); err != nil {
				t.Fatal(err)
			}
			if got := scheduled(t, a, p.ID).Status; got != tc.status {
				t.Fatalf("got %s want %s", got, tc.status)
			}
			if tc.status == "missed" && fa.session() != nil {
				t.Fatal("missed message started provider")
			}
		})
	}
}
func TestScheduledEditCancelAndValidation(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	p := scheduleInput("edit")
	for _, bad := range []proto.ScheduledPrompt{{ID: "empty", DueAt: p.DueAt, TimeZone: p.TimeZone}, {ID: "past", Prompt: "x", DueAt: 1, TimeZone: p.TimeZone}, {ID: "far", Prompt: "x", DueAt: time.Now().Add(25 * time.Hour).UnixMilli(), TimeZone: p.TimeZone}, {ID: "zone", Prompt: "x", DueAt: p.DueAt, TimeZone: "bad/zone"}} {
		if err := a.Schedule(ctx, bad); err == nil {
			t.Fatal("accepted invalid schedule", bad)
		}
	}
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	p = scheduled(t, a, p.ID)
	p.Prompt = "edited"
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := a.ScheduleAction(ctx, "cancel_schedule", p.ID, p.Revision); err == nil {
		t.Fatal("stale cancel accepted")
	}
	p = scheduled(t, a, p.ID)
	if err := a.ScheduleAction(ctx, "cancel_schedule", p.ID, p.Revision); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fa.session().prompts:
		t.Fatal("cancelled prompt sent")
	default:
	}
}
func TestScheduledFailureDoesNotRetry(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	p := scheduleInput("fail")
	fa.session().mu.Lock()
	fa.session().refuse = errors.New("out of tokens")
	fa.session().mu.Unlock()
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	_ = a.scheduleTick(ctx, p.DueAt)
	got := scheduled(t, a, p.ID)
	if got.Status != "failed" || got.Error != "out of tokens" {
		t.Fatal(got)
	}
	fa.session().mu.Lock()
	fa.session().refuse = nil
	fa.session().mu.Unlock()
	_ = a.scheduleTick(ctx, p.DueAt+1)
	select {
	case <-fa.session().prompts:
		t.Fatal("failure retried automatically")
	default:
	}
	if err := a.ScheduleAction(ctx, "send_schedule", p.ID, got.Revision); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fa.session().prompts:
	case <-time.After(time.Second):
		t.Fatal("explicit retry did not send")
	}
}

func TestScheduledBusyWaitSurvivesHarnessExit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "harness-exit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fa := &fakeAdapter{}
	m := NewManager(st, t.Logf, fa)
	defer m.Shutdown()
	a, err := m.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	p := scheduleInput("busy-exit")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Prompt(ctx, "busy", nil, ""); err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	if scheduled(t, a, p.ID).Status != "ready" {
		t.Fatal("not picked up")
	}
	if err := fa.session().Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, ok := m.Peek(a.ID); return !ok })
	a, err = m.View(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt+2*scheduleGrace); err != nil {
		t.Fatal(err)
	}
	if got := scheduled(t, a, p.ID); got.Status != "sent" {
		t.Fatalf("host stayed up but lost busy wait: %+v", got)
	}
	select {
	case got := <-fa.session().prompts:
		if got.Text != p.Prompt {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("no scheduled delivery")
	}
}

func TestScheduledRestoreClearsStaleRequestsAndDrainsQueue(t *testing.T) {
	for _, kind := range []string{"permission", "elicitation", "queue"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "stale.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			m := NewManager(st, t.Logf, &fakeAdapter{})
			a, err := m.Create(ctx, "fake", "", t.TempDir(), "", "")
			if err != nil {
				t.Fatal(err)
			}
			p := scheduleInput("stale")
			if err := a.Schedule(ctx, p); err != nil {
				t.Fatal(err)
			}
			id := a.ID
			m.Shutdown()
			var em proto.Emission
			switch kind {
			case "permission":
				em = proto.Emit(proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "stale-permission"})
			case "elicitation":
				em = proto.Emit(proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "stale-question"})
			case "queue":
				em = proto.Emit(proto.PromptQueued, proto.PromptQueuedPayload{QueueID: "queued", Prompt: "ordinary queued work"})
			}
			if _, err := st.Append(ctx, id, em); err != nil {
				t.Fatal(err)
			}
			fa := &fakeAdapter{}
			m = NewManager(st, t.Logf, fa)
			defer m.Shutdown()
			a, err = m.View(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.scheduleTick(ctx, p.DueAt); err != nil {
				t.Fatal(err)
			}
			if fa.session() == nil {
				t.Fatal("stale request prevented activation")
			}
			if kind == "queue" {
				select {
				case got := <-fa.session().prompts:
					if got.Text != "ordinary queued work" {
						t.Fatal(got)
					}
					fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: got.TurnID, StopReason: proto.StopEndTurn}))
				case <-time.After(time.Second):
					t.Fatal("queue never drained")
				}
				waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })
				if err := a.scheduleTick(ctx, p.DueAt+1); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case got := <-fa.session().prompts:
				if got.Text != p.Prompt {
					t.Fatal(got)
				}
			case <-time.After(time.Second):
				t.Fatal("stale request blocked delivery")
			}
		})
	}
}

func TestScheduledReadyExpiresAcrossHostRestart(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "ready-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(st, t.Logf, &fakeAdapter{})
	a, err := m.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	p := scheduleInput("ready-restart")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	id := a.ID
	m.Shutdown()
	p.Revision = 2
	p.Status = "ready"
	if _, err := st.Append(ctx, id, proto.Emit(proto.PromptScheduled, p)); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{}
	m = NewManager(st, t.Logf, fa)
	defer m.Shutdown()
	a, err = m.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt+scheduleGrace+1); err != nil {
		t.Fatal(err)
	}
	if got := scheduled(t, a, p.ID).Status; got != "missed" {
		t.Fatalf("got %s", got)
	}
	if fa.session() != nil {
		t.Fatal("expired schedule started provider")
	}
}
