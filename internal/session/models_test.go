package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
)

func modelsTestManager(t *testing.T, fa *fakeAdapter) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewManager(st, func(string, ...any) {}, fa)
}

// Listing harnesses runs on every connection. Asking a harness for its
// catalogue costs a process start, so the first listing must answer from the
// adapter's fallback rather than waiting for one.
func TestHarnessesServeFallbackModelsWithoutBlocking(t *testing.T) {
	fa := &fakeAdapter{live: []adapter.ModelMeta{{ID: "live", Label: "Live"}}}
	fa.listGate = make(chan struct{})
	mgr := modelsTestManager(t, fa)

	done := make(chan []adapter.ModelMeta, 1)
	go func() { done <- mgr.Harnesses(context.Background())[0].Models }()

	select {
	case got := <-done:
		if len(got) != 1 || got[0].ID != "fallback" {
			t.Fatalf("models = %v, want the adapter's fallback list", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Harnesses blocked on a live model listing")
	}
	close(fa.listGate)
}

// Once the background listing lands, clients are told — a picker opened after
// connecting must show the live catalogue, not the fallback forever.
func TestLiveModelsReplaceTheFallbackAndNotify(t *testing.T) {
	fa := &fakeAdapter{live: []adapter.ModelMeta{
		{ID: "live-default", Label: "Live", Default: true},
		{ID: "live-old", Label: "Old", Group: adapter.GroupLegacy},
	}}
	mgr := modelsTestManager(t, fa)

	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)

	mgr.Harnesses(context.Background()) // kicks the refresh

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("a landed model listing never reached subscribers")
	}

	h := mgr.Harnesses(context.Background())[0]
	if len(h.Models) != 2 || h.Models[0].ID != "live-default" {
		t.Fatalf("harness models = %v, want the live list", h.Models)
	}
	if len(h.Instances) != 1 || len(h.Instances[0].Models) != 2 {
		t.Fatalf("instance models = %v, want the live list per instance", h.Instances)
	}
}

// A harness that cannot answer must not be re-asked on every listing: the
// failure is cached, and the fallback keeps the picker populated.
func TestFailedListingIsNotRetriedEveryListing(t *testing.T) {
	fa := &fakeAdapter{liveErr: errors.New("no")}
	mgr := modelsTestManager(t, fa)

	for i := 0; i < 5; i++ {
		h := mgr.Harnesses(context.Background())[0]
		if len(h.Models) == 0 || h.Models[0].ID != "fallback" {
			t.Fatalf("models = %v, want the fallback after a failed listing", h.Models)
		}
		time.Sleep(20 * time.Millisecond)
	}

	fa.mu.Lock()
	calls := fa.listCalls
	fa.mu.Unlock()
	if calls != 1 {
		t.Fatalf("listing attempts = %d, want exactly one until the cache expires", calls)
	}
}

// A harness that answered once and then blips must not have its catalogue
// quietly downgraded to the built-in fallback until the next TTL.
func TestAFailedRefreshKeepsTheLastGoodList(t *testing.T) {
	fa := &fakeAdapter{live: []adapter.ModelMeta{{ID: "live", Label: "Live", Default: true}}}
	mgr := modelsTestManager(t, fa)

	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)
	mgr.Harnesses(context.Background())
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("the first listing never landed")
	}

	// Expire the cache, then make the harness unanswerable.
	fa.mu.Lock()
	fa.liveErr = errors.New("harness is restarting")
	fa.mu.Unlock()
	mgr.expireModelsForTest()

	mgr.Harnesses(context.Background())
	waitForListing(t, fa, 2)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h := mgr.Harnesses(context.Background())[0]
		if len(h.Models) != 1 || h.Models[0].ID != "live" {
			t.Fatalf("models = %v, want the last good live list kept", h.Models)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// One click of "Check again" has to produce a fresh answer even when a listing
// was already in flight, or the user has no way to force the question.
func TestRecheckDuringAListingStillReasks(t *testing.T) {
	fa := &fakeAdapter{live: []adapter.ModelMeta{{ID: "live", Label: "Live"}}}
	fa.listGate = make(chan struct{})
	mgr := modelsTestManager(t, fa)

	mgr.Harnesses(context.Background()) // starts a listing, which now blocks
	waitForListing(t, fa, 1)

	mgr.RecheckHarnesses()
	close(fa.listGate) // the in-flight listing returns its pre-recheck answer

	// That answer predates the recheck, so it is dropped and another asked for.
	waitForListing(t, fa, 2)
}

// A recheck is a user saying "I just installed something": it re-asks for
// models as well as readiness.
func TestRecheckDropsCachedModels(t *testing.T) {
	fa := &fakeAdapter{liveErr: errors.New("no")}
	mgr := modelsTestManager(t, fa)

	mgr.Harnesses(context.Background())
	waitForListing(t, fa, 1)

	mgr.RecheckHarnesses()
	mgr.Harnesses(context.Background())
	waitForListing(t, fa, 2)
}

func waitForListing(t *testing.T, fa *fakeAdapter, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fa.mu.Lock()
		got := fa.listCalls
		fa.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listing attempts never reached %d", want)
}

// A harness that starts answering as a different account — signed in after
// being signed out, above all — offers a different catalogue. The listing
// cached under the old identity is wrong, not stale, and must go at once:
// waiting out the TTL is how a fresh login kept showing the signed-out list.
func TestAccountChangeDropsCachedModels(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	fa.liveErr = errors.New("no")
	account := "unavailable"
	var mu sync.Mutex
	fa.availFor = func(map[string]string) adapter.Availability {
		mu.Lock()
		defer mu.Unlock()
		if account == "unavailable" {
			return adapter.Unavailable("not signed in")
		}
		return adapter.Ready(map[string]string{"account": account})
	}

	mgr.Harnesses(context.Background())
	// Signed out: no listing is attempted at all.
	time.Sleep(20 * time.Millisecond)
	if fa.listCalls != 0 {
		t.Fatalf("listed while unavailable: %d", fa.listCalls)
	}

	mu.Lock()
	account = "a@b.c"
	mu.Unlock()
	mgr.expireProbesForTest()
	mgr.Harnesses(context.Background())
	waitForListing(t, &fa.fakeAdapter, 1)

	mu.Lock()
	account = "z@b.c"
	mu.Unlock()
	mgr.expireProbesForTest()
	mgr.Harnesses(context.Background())
	waitForListing(t, &fa.fakeAdapter, 2)
}
