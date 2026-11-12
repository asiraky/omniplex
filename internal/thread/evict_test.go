package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// later is a sweep time past any idle threshold the tests use.
func later() time.Time { return time.Now().Add(time.Hour) }

// seedThread stores a thread with the given phase and log, and a manager over
// it. Nothing is opened yet.
func seedThread(t *testing.T, phase string, ems ...proto.Emission) (*Manager, *store.Store, *fakeAdapter, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "evict.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	meta := store.ThreadMeta{ID: "seeded", Cwd: t.TempDir(), Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: phase}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	ems = append([]proto.Emission{proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness})}, ems...)
	for _, em := range ems {
		if _, err := st.Append(ctx, meta.ID, em); err != nil {
			t.Fatal(err)
		}
	}
	fa := &fakeAdapter{}
	mgr := newManager(st, func(string, ...any) {}, fa)
	t.Cleanup(mgr.Shutdown)
	return mgr, st, fa, meta.ID
}

func finishedTurn() []proto.Emission {
	return []proto.Emission{
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "look"}),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t1", StopReason: proto.StopEndTurn}),
	}
}

// settle waits until a has handled everything already in its inbox, so a
// test can assert that an eviction was refused rather than not yet run.
func settle(t *testing.T, a *Actor) {
	t.Helper()
	if _, err := a.State(context.Background()); err != nil {
		t.Fatalf("actor stopped: %v", err)
	}
}

func evicted(mgr *Manager, a *Actor) bool {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.actors[a.ID] != a
}

func stateJSON(t *testing.T, a *Actor) []byte {
	t.Helper()
	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIdleViewedThreadIsEvictedAndReopensFromItsSnapshot(t *testing.T) {
	mgr, st, fa, id := seedThread(t, "idle", finishedTurn()...)
	ctx := context.Background()
	a, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	before := stateJSON(t, a)
	head := a.Head()

	mgr.evictIdle(later())
	waitFor(t, func() bool { return evicted(mgr, a) })
	select {
	case <-a.quit:
	case <-time.After(5 * time.Second):
		t.Fatal("evicted actor's loop did not stop")
	}
	if fa.thread() != nil {
		t.Fatal("eviction started a harness")
	}
	if seq, _, err := st.LatestSnapshot(ctx, id); err != nil || seq != head {
		t.Fatalf("snapshot seq = %d, err = %v; want %d so reopening replays nothing", seq, err, head)
	}

	again, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if again == a {
		t.Fatal("view handed out the evicted actor")
	}
	if after := stateJSON(t, again); !bytes.Equal(before, after) {
		t.Fatalf("reopened state differs:\nbefore %s\nafter  %s", before, after)
	}
}

func TestEvictionWritesNoSnapshotWhenOneIsCurrent(t *testing.T) {
	mgr, st, _, id := seedThread(t, "idle", finishedTurn()...)
	ctx := context.Background()
	a, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(later())
	waitFor(t, func() bool { return evicted(mgr, a) })
	<-a.quit

	// Reopened from that snapshot with nothing after it: evicting again has
	// nothing new to write, and must not rewrite the same blob. The marker
	// stands in for it at the same seq, so a rewrite would replace it.
	a, err = mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSnapshot(ctx, id, a.Head(), map[string]string{"marker": "untouched"}); err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(later())
	waitFor(t, func() bool { return evicted(mgr, a) })
	<-a.quit
	_, blob, _ := st.LatestSnapshot(ctx, id)
	if !bytes.Contains(blob, []byte("untouched")) {
		t.Fatalf("eviction rewrote a current snapshot: %s", blob)
	}
}

// A snapshot from before tool output was capped loads small but is still big
// on disk, so evicting the actor holding it writes the small one even with
// no new events.
func TestEvictionRewritesAnUncappedSnapshot(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	mgr, st, _, id := seedThread(t, "idle", finishedTurn()...)
	ctx := context.Background()
	head, err := st.Thread(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	old := projection.New(id)
	old.Items = append(old.Items, projection.Item{ID: "c1", Kind: projection.ItemTool, Content: []proto.ToolContent{{Type: "text", Text: big}}})
	if err := st.PutSnapshot(ctx, id, head.HeadSeq, old); err != nil {
		t.Fatal(err)
	}

	a, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(later())
	waitFor(t, func() bool { return evicted(mgr, a) })
	<-a.quit
	_, blob, _ := st.LatestSnapshot(ctx, id)
	if len(blob) >= len(big) {
		t.Fatalf("stored snapshot is still %d bytes", len(blob))
	}
}

func TestEvictionLeavesStoredPhaseAlone(t *testing.T) {
	for _, phase := range []string{"ready", "idle", "closed"} {
		t.Run(phase, func(t *testing.T) {
			mgr, st, _, id := seedThread(t, phase, finishedTurn()...)
			ctx := context.Background()
			a, err := mgr.View(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			mgr.evictIdle(later())
			waitFor(t, func() bool { return evicted(mgr, a) })
			<-a.quit
			meta, err := st.Thread(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if meta.Phase != phase {
				t.Fatalf("stored phase = %q after eviction, want %q", meta.Phase, phase)
			}
		})
	}
}

func TestPendingWorkspaceActorIsNeverEvicted(t *testing.T) {
	mgr, st, _, id := seedThread(t, "provision_failed")
	ctx := context.Background()
	a, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(later())
	settle(t, a)
	if evicted(mgr, a) {
		t.Fatal("pending-workspace actor was evicted")
	}
	if meta, _ := st.Thread(ctx, id); meta.Phase != "provision_failed" {
		t.Fatalf("stored phase = %q, want provision_failed", meta.Phase)
	}
}

func TestSubscribedActorIsNotEvicted(t *testing.T) {
	mgr, _, _, id := seedThread(t, "idle", finishedTurn()...)
	a, err := mgr.View(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sub := a.Subscribe()
	mgr.evictIdle(later())
	settle(t, a)
	if evicted(mgr, a) {
		t.Fatal("actor with a subscriber was evicted")
	}

	a.Unsubscribe(sub)
	mgr.evictIdle(later())
	waitFor(t, func() bool { return evicted(mgr, a) })
}

func TestRecentlyUsedActorIsNotEvicted(t *testing.T) {
	mgr, _, _, id := seedThread(t, "idle", finishedTurn()...)
	mgr.idleEvictAfter = time.Hour
	a, err := mgr.View(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(time.Now())
	settle(t, a)
	if evicted(mgr, a) {
		t.Fatal("recently used actor was evicted")
	}
}

func TestActorReachedAfterTheSweepIsNotEvicted(t *testing.T) {
	mgr, _, _, id := seedThread(t, "idle", finishedTurn()...)
	mgr.idleEvictAfter = 0
	ctx := context.Background()
	a, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// The sweep judged the actor idle, then someone opened it before its
	// loop got to the command. Their touch is what the commit re-checks.
	sweptAt := time.Now()
	if got, err := mgr.View(ctx, id); err != nil || got != a {
		t.Fatalf("view = %p, %v; want the live actor", got, err)
	}
	a.inbox <- mgr.evictCmd(a, sweptAt)
	settle(t, a)
	if evicted(mgr, a) {
		t.Fatal("actor handed out after the sweep was evicted")
	}
}

func TestActorWithHarnessIsNotEvicted(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		st, err := store.Open(filepath.Join(t.TempDir(), "evict-live.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
		defer mgr.Shutdown()
		a, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
		if err != nil {
			t.Fatal(err)
		}
		mgr.evictIdle(later())
		settle(t, a)
		if evicted(mgr, a) {
			t.Fatal("created thread with a running harness was evicted")
		}
	})
	t.Run("activated after view", func(t *testing.T) {
		mgr, _, fa, id := seedThread(t, "idle", finishedTurn()...)
		ctx := context.Background()
		a, err := mgr.View(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := mgr.Get(ctx, id); err != nil || got != a || fa.thread() == nil {
			t.Fatalf("get = %p, %v; want the viewed actor, activated", got, err)
		}
		mgr.evictIdle(later())
		settle(t, a)
		if evicted(mgr, a) {
			t.Fatal("activated actor was evicted")
		}
	})
}

func TestEvictionRefusedWhenTheListWouldChange(t *testing.T) {
	// A permission left pending in the log: the live actor's list entry says
	// it needs one, while a thread with no actor would read as idle.
	mgr, _, _, id := seedThread(t, "idle", proto.Emit(proto.PermissionRequested, proto.PermissionRequestedPayload{
		RequestID: "r1", TurnID: "t0", ToolCallID: "c1", ToolName: "Bash", Title: "run",
	}))
	a, err := mgr.View(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(later())
	settle(t, a)
	if evicted(mgr, a) {
		t.Fatal("eviction would have changed the thread list")
	}
}

func TestInterruptedTurnIsNotEvicted(t *testing.T) {
	mgr, _, _, id := seedThread(t, "turn",
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "work"}))
	a, err := mgr.View(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	mgr.evictIdle(later())
	settle(t, a)
	if evicted(mgr, a) {
		t.Fatal("actor holding an interrupted turn was evicted")
	}
}

func TestViewReplacesAStoppedActor(t *testing.T) {
	mgr, _, _, id := seedThread(t, "idle", finishedTurn()...)
	ctx := context.Background()
	a, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// The window between an actor's loop ending and onExit forgetting it.
	a.mu.Lock()
	a.onExit = nil
	a.mu.Unlock()
	a.Dispose("gone")

	again, err := mgr.View(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if again == a {
		t.Fatal("view handed out a stopped actor")
	}
	if _, err := again.State(ctx); err != nil {
		t.Fatalf("replacement actor: %v", err)
	}
}
