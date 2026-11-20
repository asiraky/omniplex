package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/store"
)

// restartRig is an orchRig whose server can be restarted: the same database,
// opened again by a fresh store, manager, harness and server, the way
// cmd/omniplex boots.
type restartRig struct {
	*orchRig
	path string
	// holdContinuations makes the continuation of a cut turn a HOLD turn on
	// the next boot's harness, so a test can look at a thread before it ends.
	holdContinuations bool
	logf              func(string, ...any)
}

func newRestartRig(t *testing.T) *restartRig {
	t.Helper()
	// Goroutines of a server that was restarted can outlive the test; they
	// log nowhere once it is over.
	var (
		logMu sync.Mutex
		over  bool
	)
	t.Cleanup(func() { logMu.Lock(); over = true; logMu.Unlock() })
	prev := wakeWindow
	wakeWindow = 100 * time.Millisecond
	t.Cleanup(func() { wakeWindow = prev })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	r := &restartRig{orchRig: &orchRig{}, path: filepath.Join(t.TempDir(), "orch.db")}
	r.logf = func(format string, a ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		if !over {
			t.Logf(format, a...)
		}
	}
	r.up(t)
	return r
}

// up boots a server over the rig's database.
func (r *restartRig) up(t *testing.T) {
	t.Helper()
	st, err := store.Open(r.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := newOrchAdapter("orch")
	var ad adapter.Adapter = h
	if r.holdContinuations {
		ad = holdingAdapter{h}
	}
	mgr := newManager(st, r.logf, ad)
	t.Cleanup(mgr.Shutdown)
	mgr.ResumeInterrupted()
	signer := artefact.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), ArtefactSigner: signer, Logf: r.logf})
	t.Cleanup(srv.Close)
	remote := httptest.NewServer(asRemote(srv.Handler()))
	t.Cleanup(remote.Close)
	r.h = h
	r.toolRig = &toolRig{t: t, st: st, mgr: mgr, srv: srv, signer: signer, remote: remote}
}

// down stops the server as a restart does: the harnesses and the database
// go first, so a wait still in flight never gets to settle what it owes. Its
// caller's connection drops last.
func (r *restartRig) down() {
	old := r.toolRig
	old.srv.Close()
	old.mgr.Shutdown()
	old.st.Close()
	old.remote.CloseClientConnections()
	old.remote.Close()
}

func (r *restartRig) restart() {
	r.t.Helper()
	r.down()
	r.up(r.t)
}

// heldStarting waits for a HOLD turn whose prompt starts with prefix, and
// returns that prompt.
func (r *restartRig) heldStarting(prefix string) string {
	r.t.Helper()
	var got string
	eventually(r.t, prefix+" runs", func() bool {
		r.h.mu.Lock()
		defer r.h.mu.Unlock()
		for p := range r.h.held {
			if strings.HasPrefix(p, prefix) {
				got = p
				return true
			}
		}
		return false
	})
	return got
}

// wokenOnce waits for parent's first wake on this boot's harness, gives a
// second one time to come, and returns the one.
func (r *restartRig) wokenOnce(parent string) string {
	r.t.Helper()
	eventually(r.t, "the parent is woken", func() bool { return len(r.wakes(parent)) > 0 })
	time.Sleep(3 * wakeWindow)
	w := r.wakes(parent)
	if len(w) != 1 {
		r.t.Fatalf("parent woken %d times: %q", len(w), w)
	}
	return w[0]
}

// holdingAdapter turns the continuation of a cut turn into a HOLD turn.
type holdingAdapter struct{ *orchAdapter }

func (a holdingAdapter) CreateSession(ctx context.Context, hs adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	s, err := a.orchAdapter.CreateSession(ctx, hs, o)
	if err != nil {
		return nil, err
	}
	return holdingSession{s.(*orchSession)}, nil
}

type holdingSession struct{ *orchSession }

func (s holdingSession) Prompt(ctx context.Context, p adapter.PromptInput) error {
	if strings.HasPrefix(p.Text, "[omniplex]") {
		p.Text = "HOLD " + p.Text
	}
	return s.orchSession.Prompt(ctx, p)
}

// A parent owed a child that a restart caught mid-turn is still owed it:
// not finished while the child's continuation runs, and woken once with
// what that continuation said.
func TestARestartKeepsAChildsResultOwed(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	child := r.delegate(parent, "HOLD child")
	r.running("HOLD child")

	r.holdContinuations = true
	r.restart()
	cont := r.heldStarting("HOLD [omniplex]")
	if got := r.h.promptsOf(child); len(got) != 1 || got[0] != cont {
		t.Fatalf("child prompted %q after the restart, want only its continuation", got)
	}
	time.Sleep(3 * wakeWindow)
	if w := r.wakes(parent); len(w) != 0 {
		t.Fatalf("parent woken with %q before the child's continuation finished", w)
	}
	if !r.srv.orch.busy(parent) {
		t.Fatal("parent reads as finished while its child's result is still owed")
	}

	r.h.release(t, cont)
	w := r.wokenOnce(parent)
	if !strings.Contains(w, child) || !strings.Contains(w, "finished "+cont) {
		t.Fatalf("wake = %q, want child %s's continuation", w, child)
	}
	eventually(t, "the parent is no longer owed anything", func() bool { return !r.srv.orch.busy(parent) })
}

// A child's result still inside its wake's window when the server went down
// wakes the parent once after it comes back.
func TestARestartKeepsAWakeNotYetSent(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	wakeWindow = time.Hour // set before this boot's server reads it
	r.delegate(parent, "quick")
	eventually(t, "the wake is recorded", func() bool {
		ws, err := r.st.PendingWakes(context.Background())
		return err == nil && len(ws) == 1
	})

	r.down()
	wakeWindow = 100 * time.Millisecond
	r.up(t)
	if w := r.wokenOnce(parent); !strings.Contains(w, "done: quick") {
		t.Fatalf("wake = %q, want the child's result", w)
	}
}

// A wake queued on a parent mid-turn when the server went down is held with
// the rest of its queue after the restart. It is taken back and sent again:
// once, after the parent's own continuation, with nothing left held.
func TestARestartRedeliversAQueuedWakeOnce(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	ctx := context.Background()
	a, err := r.mgr.Get(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Prompt(ctx, "HOLD parent", nil); err != nil {
		t.Fatal(err)
	}
	r.running("HOLD parent")
	r.delegate(parent, "quick")
	eventually(t, "the wake waits in the parent's queue", func() bool {
		s, err := a.State(ctx)
		return err == nil && len(s.Queued) == 1 && strings.HasPrefix(s.Queued[0].Prompt, "[Omniplex]")
	})

	r.restart()
	w := r.wokenOnce(parent)
	if !strings.Contains(w, "done: quick") {
		t.Fatalf("wake = %q, want the child's result", w)
	}
	prompts := r.h.promptsOf(parent)
	cont := slices.IndexFunc(prompts, func(p string) bool { return strings.HasPrefix(p, "[omniplex]") })
	wake := slices.Index(prompts, w)
	if cont < 0 || cont > wake {
		t.Fatalf("parent prompted %q, want its continuation before the wake", prompts)
	}
	eventually(t, "the parent's queue is empty", func() bool {
		a, err := r.mgr.View(ctx, parent)
		if err != nil {
			return false
		}
		s, err := a.State(ctx)
		return err == nil && len(s.Queued) == 0
	})
}

// A delegate_task that waits, in flight when the server went down, owes its
// result to the caller: the restart cut the wait short, so a wake brings it.
func TestARestartDuringAWaitStillDeliversTheResult(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	ctx := context.Background()
	res := r.start(ctx, parent, "delegate_task", map[string]any{"prompt": "HOLD child", "wait": true})
	r.running("HOLD child")
	var child string
	eventually(t, "the child is recorded", func() bool {
		list, err := r.st.ListThreads(ctx)
		if err != nil {
			return false
		}
		for _, m := range list {
			if m.ParentID == parent {
				child = m.ID
				return true
			}
		}
		return false
	})

	r.restart()
	<-res
	w := r.wokenOnce(parent)
	if !strings.Contains(w, child) || !strings.Contains(w, "done: [omniplex]") {
		t.Fatalf("wake = %q, want child %s's continuation", w, child)
	}
}

// A child that finished as the server went down, before its parent was
// told, wakes the parent once on the next start.
func TestARestartWakesForAResultThatLandedAsItWentDown(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	wakeWindow = time.Hour // set before this boot's server reads it
	child := r.delegate(parent, "quick")
	eventually(t, "the wake is recorded", func() bool {
		ws, err := r.st.PendingWakes(context.Background())
		return err == nil && len(ws) == 1
	})
	r.down()

	// What the store holds when the server stops between the child
	// finishing and the scan that would have told its parent.
	ctx := context.Background()
	st, err := store.Open(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteWake(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveOwed(ctx, store.OwedResult{Child: child, Parent: parent}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	wakeWindow = 100 * time.Millisecond
	r.up(t)
	if w := r.wokenOnce(parent); !strings.Contains(w, child) || !strings.Contains(w, "done: quick") {
		t.Fatalf("wake = %q, want child %s's result", w, child)
	}
	eventually(t, "nothing is left owed", func() bool {
		owed, err := r.st.OwedResults(ctx)
		return err == nil && len(owed) == 0 && !r.srv.orch.busy(parent)
	})
}

// A wake that ran before the server went down is settled on the next start
// from the parent's record: its harness is not started to look, and it is
// not woken again.
func TestARestartLeavesAWakeThatRanAlone(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	ctx := context.Background()
	a, err := r.mgr.Get(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Prompt(ctx, "HOLD parent", nil); err != nil {
		t.Fatal(err)
	}
	r.running("HOLD parent")
	r.delegate(parent, "quick")
	eventually(t, "the wake waits in the parent's queue", func() bool {
		s, err := a.State(ctx)
		return err == nil && len(s.Queued) == 1
	})
	r.h.release(t, "HOLD parent")
	eventually(t, "the wake runs", func() bool { return len(r.wakes(parent)) == 1 })
	eventually(t, "the parent is idle", func() bool {
		s, err := a.State(ctx)
		return err == nil && len(s.Queued) == 0 && s.Phase == "idle"
	})

	r.restart()
	eventually(t, "the wake is settled", func() bool {
		ws, err := r.st.PendingWakes(ctx)
		return err == nil && len(ws) == 0
	})
	time.Sleep(3 * wakeWindow)
	r.h.mu.Lock()
	started := r.h.sessions[parent]
	r.h.mu.Unlock()
	if started != 0 || len(r.wakes(parent)) != 0 {
		t.Fatalf("parent's harness started %d times and woken %q after the restart, want neither", started, r.wakes(parent))
	}
}

// A wait that fails leaves nothing owed for its threads that were not owed
// before it: after a restart, one of them finishing wakes nobody.
func TestAFailedWaitOwesNothingAfterARestart(t *testing.T) {
	r := newRestartRig(t)
	parent, _ := r.parent()
	ctx := context.Background()
	done := r.delegate(parent, "quick")
	eventually(t, "the parent is woken with its result", func() bool { return len(r.wakes(parent)) == 1 })
	a, err := r.mgr.Get(ctx, done)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Prompt(ctx, "HOLD again", nil); err != nil {
		t.Fatal(err)
	}
	r.running("HOLD again")
	gone := r.delegate(parent, "HOLD gone")
	r.running("HOLD gone")

	res := r.start(ctx, parent, "thread_wait", map[string]any{"ids": []string{done, gone}, "timeout_seconds": 600})
	eventually(t, "the wait holds the thread it does not owe", func() bool {
		owed, err := r.st.OwedResults(ctx)
		return err == nil && slices.ContainsFunc(owed, func(o store.OwedResult) bool { return o.Child == done })
	})
	if err := r.mgr.Delete(ctx, gone, false); err != nil {
		t.Fatal(err)
	}
	if got := <-res; got.code == http.StatusOK {
		t.Fatalf("thread_wait on a deleted thread = %q, want an error", got.text)
	}

	r.restart()
	eventually(t, "the thread's cut turn is continued", func() bool {
		return slices.ContainsFunc(r.h.promptsOf(done), func(p string) bool { return strings.HasPrefix(p, "[omniplex]") })
	})
	time.Sleep(3 * wakeWindow)
	if w := r.wakes(parent); len(w) != 0 || r.srv.orch.busy(parent) {
		t.Fatalf("parent woken with %q after a failed wait and a restart, want nothing owed", w)
	}
}
