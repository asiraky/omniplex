package server

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// statusAdapter's turns never finish on their own; the test drives the
// harness stream through the last session it made.
type statusAdapter struct {
	scheduleBrowserAdapter
	mu   sync.Mutex
	last *statusThread
}

func (a *statusAdapter) CreateSession(context.Context, adapter.HostServices, adapter.CreateOptions) (adapter.Session, error) {
	s := &statusThread{events: make(chan proto.Emission, 32), prompts: make(chan string, 4)}
	a.mu.Lock()
	a.last = s
	a.mu.Unlock()
	return s, nil
}

func (a *statusAdapter) session() *statusThread {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

type statusThread struct {
	events  chan proto.Emission
	prompts chan string
	once    sync.Once
}

func (s *statusThread) Prompt(_ context.Context, p adapter.PromptInput) error {
	s.prompts <- p.TurnID
	return nil
}
func (*statusThread) Cancel(context.Context) error    { return nil }
func (s *statusThread) Events() <-chan proto.Emission { return s.events }
func (s *statusThread) Close() error                  { s.once.Do(func() { close(s.events) }); return nil }

// A phone that attaches mid-retry has no log entry to learn it from, so the
// status follows synchronized; and a change while attached is pushed live,
// with a clear as a frame that carries no status.
func TestAttachDeliversTheHeldTurnStatus(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &statusAdapter{}
	mgr := thread.NewManager(st, func(string, ...any) {}, fa)
	t.Cleanup(mgr.Shutdown)
	h := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort)}).Handler()

	ctx := context.Background()
	actor, err := mgr.Create(ctx, fa.ID(), "", t.TempDir(), "test-model", "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Prompt(ctx, "go", nil); err != nil {
		t.Fatal(err)
	}
	turnID := <-fa.session().prompts
	retrying := proto.TurnStatusPayload{State: proto.TurnRetrying, Attempt: 2, Max: 10, Reason: "overloaded", RetryAt: 1731300000000}
	fa.session().events <- proto.Emit(proto.TurnStatus, retrying)
	deadline := time.Now().Add(5 * time.Second)
	for actor.TurnStatus() == nil {
		if time.Now().After(deadline) {
			t.Fatal("status never held")
		}
		time.Sleep(2 * time.Millisecond)
	}

	c := dialWS(t, h)
	c.write(clientFrame{Type: "attach", ThreadID: actor.ID})
	for {
		if f := c.read(); f.Type == "synchronized" && f.ThreadID == actor.ID {
			break
		}
	}
	f := c.read()
	if f.Type != "turn_status" || f.ThreadID != actor.ID || f.TurnStatus == nil || *f.TurnStatus != retrying {
		t.Fatalf("frame after synchronized = %+v, want turn_status %+v", f, retrying)
	}

	fa.session().events <- proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turnID, Role: "agent", Kind: "text", BlockID: "b1", Delta: "back",
	})
	for {
		f := c.read()
		if f.Type != "turn_status" {
			continue
		}
		if f.TurnStatus != nil {
			t.Fatalf("status after the harness spoke = %+v, want cleared", f.TurnStatus)
		}
		break
	}
}
