package server

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// restart_thread on an idle thread replaces its process at once; on a busy
// one it answers pending, and cancel_restart takes that back.
func TestRestartThreadCommands(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ad := &countingAdapter{}
	mgr := newManager(st, t.Logf, ad)
	t.Cleanup(mgr.Shutdown)
	h := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort)}).Handler()

	ctx := context.Background()
	a, err := mgr.Create(ctx, ad.ID(), "", dir, "test-model", "default")
	if err != nil {
		t.Fatal(err)
	}
	c := dialWS(t, h)

	var out struct {
		Pending bool `json:"pending"`
		OK      bool `json:"ok"`
	}
	c.ok("restart_thread", map[string]any{"threadId": a.ID}, &out)
	if out.Pending || ad.started() != 2 {
		t.Fatalf("idle restart: pending %v, %d processes started; want done on a second process", out.Pending, ad.started())
	}

	c.ok("prompt", map[string]any{"threadId": a.ID, "text": "work"}, nil)
	c.ok("restart_thread", map[string]any{"threadId": a.ID}, &out)
	if !out.Pending || ad.started() != 2 {
		t.Fatalf("busy restart: pending %v, %d processes started; want it waiting", out.Pending, ad.started())
	}
	if s, err := a.State(ctx); err != nil || !s.RestartPending {
		t.Fatalf("state after a busy restart: %v; want it pending", err)
	}
	c.ok("cancel_restart", map[string]any{"threadId": a.ID}, &out)
	if !out.OK {
		t.Fatal("cancel_restart did not say ok")
	}
	if s, err := a.State(ctx); err != nil || s.RestartPending {
		t.Fatalf("state after a cancel: %v; want nothing pending", err)
	}
}

// countingAdapter is a busyAdapter that counts the processes it starts.
type countingAdapter struct {
	busyAdapter
	mu sync.Mutex
	n  int
}

func (a *countingAdapter) CreateSession(context.Context, adapter.HostServices, adapter.CreateOptions) (adapter.Session, error) {
	a.mu.Lock()
	a.n++
	a.mu.Unlock()
	return &busySession{events: make(chan proto.Emission)}, nil
}

func (a *countingAdapter) started() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}
