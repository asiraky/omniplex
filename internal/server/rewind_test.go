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
)

// rewind_thread reaches the thread's harness with the turn's anchor, and its
// restoreFiles flag reaches the thread: a folder that is not a checkout has
// no files to put back, so that rewind is refused and the turn kept.
func TestRewindThreadCommand(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "rewind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ad := &rewindAdapter{}
	mgr := newManager(st, t.Logf, ad)
	t.Cleanup(mgr.Shutdown)
	h := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort)}).Handler()

	ctx := context.Background()
	a, err := mgr.Create(ctx, ad.ID(), "", dir, "test-model", "default")
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Prompt(ctx, "one", nil)
	if err != nil {
		t.Fatal(err)
	}
	ad.session().events <- proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn, HarnessRef: "r1"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := a.State(ctx)
		if err == nil && s.Phase == "idle" && len(s.Turns) == 1 && s.Turns[0].Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := dialWS(t, h)

	if _, e := c.do("rewind_thread", map[string]any{"threadId": a.ID, "turnId": res.TurnID, "restoreFiles": true}); e == "" {
		t.Fatal("restoring files outside a checkout was accepted")
	}
	if got := ad.session().targets(); len(got) != 0 {
		t.Fatalf("a refused restore still rewound the harness: %+v", got)
	}

	var out struct {
		OK bool `json:"ok"`
	}
	c.ok("rewind_thread", map[string]any{"threadId": a.ID, "turnId": res.TurnID, "restoreFiles": false}, &out)
	if !out.OK {
		t.Fatal("the rewind did not say ok")
	}
	if got := ad.session().targets(); len(got) != 1 || got[0] != (adapter.RewindTarget{Turn: "r1"}) {
		t.Fatalf("harness asked for %+v, want r1", got)
	}
	if s, err := a.State(ctx); err != nil || len(s.Turns) != 0 {
		t.Fatalf("turns after the rewind: %+v, %v", s.Turns, err)
	}
}

// rewindAdapter's sessions take every prompt, never finish a turn on their
// own, and can rewind, keeping what they were asked to rewind to.
type rewindAdapter struct {
	busyAdapter
	mu   sync.Mutex
	sess *rewindSession
}

func (*rewindAdapter) ID() string { return "rewind-test" }
func (a *rewindAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: a.ID(), Name: "Rewind harness", Rewinds: true}
}
func (a *rewindAdapter) CreateSession(context.Context, adapter.HostServices, adapter.CreateOptions) (adapter.Session, error) {
	s := &rewindSession{busySession: busySession{events: make(chan proto.Emission)}}
	a.mu.Lock()
	a.sess = s
	a.mu.Unlock()
	return s, nil
}

func (a *rewindAdapter) session() *rewindSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sess
}

type rewindSession struct {
	busySession
	mu   sync.Mutex
	asks []adapter.RewindTarget
}

func (s *rewindSession) Rewind(_ context.Context, target adapter.RewindTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asks = append(s.asks, target)
	return nil
}

func (s *rewindSession) targets() []adapter.RewindTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]adapter.RewindTarget(nil), s.asks...)
}
