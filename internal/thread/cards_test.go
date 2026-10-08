package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

func cardItem(t *testing.T, a *Actor, requestID string) projection.Item {
	t.Helper()
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	it, ok := s.ItemByID(projection.CardID(requestID))
	if !ok {
		t.Fatalf("no card item for %s", requestID)
	}
	return it
}

// A card is the server's, not the turn's: stopping the turn leaves it
// answerable, and while it waits the thread is neither busy nor blocking a
// scheduled prompt.
func TestCardOutlivesItsTurnAndDoesNotBlockSchedules(t *testing.T) {
	a, fa, _ := newTestActor(t)
	ctx := context.Background()
	res, err := a.Prompt(ctx, "add a server", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-fa.thread().prompts
	if err := a.RaiseCard(ctx, "c1", "Add server linear", json.RawMessage(`{"kind":"add_mcp_server"}`)); err != nil {
		t.Fatal(err)
	}
	if err := a.RaiseCard(ctx, "c1", "again", json.RawMessage(`{}`)); err == nil {
		t.Fatal("the same card raised twice")
	}
	if err := a.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	fa.thread().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopCancelled}))
	waitFor(t, func() bool { s, _ := a.State(ctx); return s.Phase == "idle" })

	s, _ := a.State(ctx)
	if len(s.Elicitations) != 1 || s.Attention() != projection.AttentionNeedsAnswer {
		t.Fatalf("card after stop: elicitations %+v attention %s", s.Elicitations, s.Attention())
	}
	if it := cardItem(t, a, "c1"); it.Status != projection.CardPending || it.TurnID != res.TurnID {
		t.Fatalf("card item = %+v", it)
	}

	p := scheduleInput("while-waiting")
	if err := a.Schedule(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := a.scheduleTick(ctx, p.DueAt); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fa.thread().prompts:
		if got.Text != p.Prompt {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("a waiting card held back a scheduled prompt")
	}
}

// The harness can be long gone by the time the user answers.
func TestCardResolvesOnViewedThreadAfterHarnessExit(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "cards.db"))
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
	if err := a.RaiseCard(ctx, "c1", "Install skill", json.RawMessage(`{"kind":"install_skill"}`)); err != nil {
		t.Fatal(err)
	}
	_ = fa.thread().Close()
	waitFor(t, func() bool { _, ok := m.Peek(a.ID); return !ok })

	a, err = m.View(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The harness's own answer path cannot settle a card.
	_ = a.ResolveElicitation(ctx, "c1", adapter.ElicitationResult{Action: "accept"})
	if it := cardItem(t, a, "c1"); it.Status != projection.CardPending {
		t.Fatalf("resolve_elicitation answered a card: %+v", it)
	}

	outcome := json.RawMessage(`{"result":"saved","edited":true}`)
	if err := a.ResolveCard(ctx, "c1", "accept", outcome); err != nil {
		t.Fatal(err)
	}
	it := cardItem(t, a, "c1")
	if it.Status != projection.CardSaved || !bytes.Equal(it.Outcome, outcome) {
		t.Fatalf("answered card = %+v", it)
	}
	if s, _ := a.State(ctx); len(s.Elicitations) != 0 || s.Attention() != projection.AttentionNeedsPrompt {
		t.Fatalf("still waiting: %+v", s.Elicitations)
	}
	if err := a.ResolveCard(ctx, "c1", "decline", nil); !errors.Is(err, ErrNoCard) {
		t.Fatalf("second answer = %v", err)
	}
	if err := a.ResolveCard(ctx, "never", "accept", nil); !errors.Is(err, ErrNoCard) {
		t.Fatalf("unknown card = %v", err)
	}
	if _, err := m.Get(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if fa.thread() == nil {
		t.Fatal("not resumed")
	}
}

func TestCardDeclineAndCancelResults(t *testing.T) {
	a, _, _ := newTestActor(t)
	ctx := context.Background()
	for id, action := range map[string]string{"d": "decline", "x": "cancel"} {
		if err := a.RaiseCard(ctx, id, id, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		if err := a.ResolveCard(ctx, id, action, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := cardItem(t, a, "d").Status; got != projection.CardDeclined {
		t.Fatal(got)
	}
	if got := cardItem(t, a, "x").Status; got != projection.CardCancelled {
		t.Fatal(got)
	}
}

// Resuming after a restart cancels what the old process was waiting on. A
// card the server still holds is not the old process's, so it stays; one the
// server lost cannot be answered, so it goes.
func TestResumeKeepsHeldCardsAndCancelsLostOnes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "resume-cards.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(st, t.Logf, &fakeAdapter{})
	a, err := m.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"held", "lost"} {
		if err := a.RaiseCard(ctx, id, id, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	id := a.ID
	m.Shutdown()

	m = NewManager(st, t.Logf, &fakeAdapter{})
	defer m.Shutdown()
	m.SetCardLive(func(requestID string) bool { return requestID == "held" })
	a, err = m.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := a.State(ctx); len(s.Elicitations) != 2 {
		t.Fatalf("viewing rewrote history: %+v", s.Elicitations)
	}
	if a, err = m.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := cardItem(t, a, "held").Status; got != projection.CardPending {
		t.Fatalf("held card = %s", got)
	}
	if got := cardItem(t, a, "lost").Status; got != projection.CardCancelled {
		t.Fatalf("lost card = %s", got)
	}
}

// A secret an agent passes to a tool is rewritten before the call is stored,
// in the started and updated inputs and in the permission asked for it.
func TestRedactToolInputBeforeStoring(t *testing.T) {
	RedactToolInput = func(name string, in json.RawMessage) json.RawMessage {
		if name != "omniplex/add" {
			return in
		}
		return bytes.ReplaceAll(in, []byte("hunter2"), []byte("****"))
	}
	t.Cleanup(func() { RedactToolInput = nil })

	a, fa, st := newTestActor(t)
	ctx := context.Background()
	res, err := a.Prompt(ctx, "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-fa.thread().prompts
	secret := json.RawMessage(`{"config":"TOKEN=hunter2"}`)
	fa.thread().emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: res.TurnID, ToolCallID: "t1", Kind: "other", Title: "omniplex/add", Status: "pending", RawInput: secret}))
	// An update's title can summarise its input; it is still the same tool.
	fa.thread().emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{ToolCallID: "t1", Title: "Adding a server", RawInput: secret}))
	fa.thread().emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{TurnID: res.TurnID, ToolCallID: "t2", Kind: "other", Title: "other/tool", Status: "pending", RawInput: json.RawMessage(`{"kept":"hunter2"}`)}))

	askCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = fa.thread().host.RequestPermission(askCtx, adapter.PermissionRequest{TurnID: res.TurnID, ToolCallID: "t1", ToolName: "omniplex/add", Title: "Add", RawInput: secret})
	}()
	waitFor(t, func() bool { s, _ := a.State(ctx); return len(s.Pending) == 1 })

	events, err := st.ReadEvents(ctx, a.ID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var redacted, kept int
	for _, ev := range events {
		raw, _ := json.Marshal(ev)
		switch {
		case bytes.Contains(raw, []byte("TOKEN=hunter2")):
			t.Fatalf("secret stored in %s: %s", ev.Type, raw)
		case bytes.Contains(raw, []byte("TOKEN=****")):
			redacted++
		case bytes.Contains(raw, []byte(`hunter2`)):
			kept++
		}
	}
	if redacted != 3 || kept != 1 {
		t.Fatalf("redacted %d events, left %d alone", redacted, kept)
	}
}
