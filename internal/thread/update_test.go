package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
)

// updAdapter is a fakeAdapter whose harness is a file on disk the test can
// replace, which counts its probes, and which can update itself. It has its
// own driver id so the package-level start gate is not shared with other
// tests' fakes.
type updAdapter struct {
	*fakeAdapter
	exe string

	// probeUpdateMu guards the probe and update fakes: their scripted
	// answers and the counts of how often each was asked.
	probeUpdateMu sync.Mutex
	probes        int
	avail         adapter.Availability
	updates       int
	update        func() (adapter.UpdateResult, error)
}

func (u *updAdapter) ID() string { return "upd" }

func (u *updAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: "upd", Name: "Upd"}
}

func (u *updAdapter) Executable() (string, bool) { return u.exe, true }

func (u *updAdapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	u.probeUpdateMu.Lock()
	defer u.probeUpdateMu.Unlock()
	u.probes++
	return u.avail
}

func (u *updAdapter) Update(ctx context.Context, env map[string]string) (adapter.UpdateResult, error) {
	u.probeUpdateMu.Lock()
	u.updates++
	fn := u.update
	u.probeUpdateMu.Unlock()
	if fn == nil {
		return adapter.UpdateResult{From: "1.0", To: "1.0"}, nil
	}
	return fn()
}

func (u *updAdapter) counts() (probes, updates int) {
	u.probeUpdateMu.Lock()
	defer u.probeUpdateMu.Unlock()
	return u.probes, u.updates
}

func (u *updAdapter) setLive(list []adapter.ModelMeta) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.live, u.liveErr = list, nil
}

func (u *updAdapter) setLiveErr(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.liveErr = err
}

// replaceBinary stands in for an install: a file of a different size where
// the old one was.
func (u *updAdapter) replaceBinary(t *testing.T, contents string) {
	t.Helper()
	if err := os.WriteFile(u.exe, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func updTestManager(t *testing.T) (*Manager, *updAdapter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ua := &updAdapter{
		fakeAdapter: &fakeAdapter{},
		exe:         filepath.Join(t.TempDir(), "harness"),
		avail:       adapter.Ready(nil),
	}
	ua.replaceBinary(t, "v1")
	mgr := NewManager(st, func(string, ...any) {}, ua)
	t.Cleanup(mgr.Shutdown)
	return mgr, ua
}

// fastUpdates makes the updater's first pass immediate and its retries quick.
func fastUpdates(mgr *Manager) {
	mgr.updatePacing = updatePacing{first: time.Millisecond, interval: time.Hour, busyRetry: 10 * time.Millisecond, busyAttempts: 200}
}

// waitRefreshed waits until no model listing is in flight, so its answer is
// in the cache before the test ages or reads it.
func waitRefreshed(t *testing.T, mgr *Manager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mgr.modelsMu.Lock()
		busy := len(mgr.refreshing) > 0
		mgr.modelsMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("a model listing never finished")
}

func waitNotified(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("no harnesses notification: %s", what)
	}
}

func listCalls(fa *fakeAdapter) int {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.listCalls
}

// An update, from whatever source, swaps the binary. The catalogue cached for
// the old one is wrong from that moment, not at the TTL: the next listing
// still answers at once with the old list, then the new one lands and
// clients are told.
func TestNewBinaryDropsCachedModelsInsideTheTTL(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.setLive([]adapter.ModelMeta{{ID: "old", Label: "Old", Default: true}})
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)

	mgr.Harnesses(context.Background())
	waitNotified(t, ch, "first listing")
	waitRefreshed(t, mgr)

	// Same binary, inside the TTL: served from cache, not re-asked.
	mgr.Harnesses(context.Background())
	waitRefreshed(t, mgr)
	if got := listCalls(ua.fakeAdapter); got != 1 {
		t.Fatalf("listings = %d with the binary unchanged, want 1", got)
	}

	ua.setLive([]adapter.ModelMeta{{ID: "new", Label: "New", Default: true}})
	ua.replaceBinary(t, "version two")

	if got := mgr.Harnesses(context.Background())[0].Models; len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("models = %v, want the old list served while the new one is fetched", got)
	}
	waitNotified(t, ch, "listing of the new binary")
	if got := mgr.Harnesses(context.Background())[0].Models; len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("models = %v, want the new binary's list", got)
	}
}

// A binary replaced while the harness was being asked: the answer came from
// the old one, and the watcher that saw the change has already fired, so the
// harness is asked again straight away and clients hear the answer without
// anyone listing again.
func TestBinaryChangedDuringAListingIsAskedAgain(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.setLive([]adapter.ModelMeta{{ID: "m", Label: "M"}})
	ua.mu.Lock()
	gate := make(chan struct{})
	ua.listGate = gate
	ua.mu.Unlock()
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)

	mgr.Harnesses(context.Background())
	waitForListing(t, ua.fakeAdapter, 1)
	ua.replaceBinary(t, "replaced mid-listing")
	close(gate)

	waitForListing(t, ua.fakeAdapter, 2)
	waitNotified(t, ch, "listing of the replaced binary")
}

// The probe is keyed on the binary as well: a cached readiness answer about a
// program that has been replaced is not reused, however recent.
func TestNewBinaryDropsCachedProbe(t *testing.T) {
	mgr, ua := updTestManager(t)

	mgr.Harnesses(context.Background())
	mgr.Harnesses(context.Background())
	if probes, _ := ua.counts(); probes != 1 {
		t.Fatalf("probes = %d, want 1 inside the probe TTL", probes)
	}

	ua.replaceBinary(t, "a newer binary")
	mgr.Harnesses(context.Background())
	if probes, _ := ua.counts(); probes != 2 {
		t.Fatalf("probes = %d, want a fresh probe of the new binary", probes)
	}
}

// A failed listing keeps the last good list but is retried after the short
// backoff, not the full TTL; a successful one is not.
func TestFailedRefreshRetriesAfterTheBackoff(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.setLiveErr(errors.New("harness is restarting"))
	past := modelRetryTTL + time.Second

	mgr.Harnesses(context.Background())
	waitForListing(t, ua.fakeAdapter, 1)
	waitRefreshed(t, mgr)

	mgr.ageModelsForTest(past)
	ua.setLive([]adapter.ModelMeta{{ID: "live", Label: "Live"}})
	mgr.Harnesses(context.Background())
	waitForListing(t, ua.fakeAdapter, 2)
	waitRefreshed(t, mgr)

	mgr.ageModelsForTest(past)
	mgr.Harnesses(context.Background())
	time.Sleep(50 * time.Millisecond)
	if got := listCalls(ua.fakeAdapter); got != 2 {
		t.Fatalf("listings = %d, want a good list kept until the TTL", got)
	}

	// And a failure after a good list keeps serving that list meanwhile.
	ua.setLiveErr(errors.New("blip"))
	mgr.expireModelsForTest()
	mgr.Harnesses(context.Background())
	waitForListing(t, ua.fakeAdapter, 3)
	waitRefreshed(t, mgr)
	if got := mgr.Harnesses(context.Background())[0].Models; len(got) != 1 || got[0].ID != "live" {
		t.Fatalf("models = %v, want the last good list after a failure", got)
	}
	mgr.ageModelsForTest(past)
	mgr.Harnesses(context.Background())
	waitForListing(t, ua.fakeAdapter, 4)
}

// An update that installs a new version re-lists the catalogue and tells every
// client, without anyone pressing "Check again". Until the new list lands the
// old live one is served, not the built-in fallback.
func TestUpdateThatChangesVersionRelistsAndNotifies(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.setLive([]adapter.ModelMeta{{ID: "old", Label: "Old"}})
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)
	mgr.Harnesses(context.Background())
	waitNotified(t, ch, "first listing")
	waitRefreshed(t, mgr)

	ua.update = func() (adapter.UpdateResult, error) {
		ua.setLive([]adapter.ModelMeta{{ID: "new", Label: "New"}})
		return adapter.UpdateResult{From: "1.0", To: "1.1"}, nil
	}
	fastUpdates(mgr)
	mgr.StartUpdater(func() bool { return true })
	waitNotified(t, ch, "update installed")

	h := mgr.Harnesses(context.Background())[0]
	if h.Update == nil || h.Update.From != "1.0" || h.Update.To != "1.1" || h.Update.Error != "" || h.Update.At == 0 {
		t.Fatalf("update status = %+v, want 1.0 -> 1.1", h.Update)
	}
	if len(h.Models) != 1 || h.Models[0].ID != "old" {
		t.Fatalf("models = %v right after the update, want the cached live list", h.Models)
	}
	waitNotified(t, ch, "new catalogue")
	if got := mgr.Harnesses(context.Background())[0].Models; len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("models = %v, want the updated catalogue", got)
	}
}

// A failing updater is visible next to the harness, not only in a log.
func TestFailedUpdateIsReported(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.update = func() (adapter.UpdateResult, error) {
		return adapter.UpdateResult{From: "1.0", To: "1.0"}, errors.New("no permission to write the install directory")
	}
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)
	fastUpdates(mgr)
	mgr.StartUpdater(func() bool { return true })
	waitNotified(t, ch, "update failed")

	h := mgr.Harnesses(context.Background())[0]
	if h.Update == nil || !strings.Contains(h.Update.Error, "no permission") {
		t.Fatalf("update status = %+v, want the failure", h.Update)
	}
}

// A status recorded while updates were on is not shown once they are off: it
// would read as current when nothing is updating any more.
func TestUpdateStatusHiddenWhileUpdatesAreOff(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.update = func() (adapter.UpdateResult, error) {
		return adapter.UpdateResult{From: "1.0", To: "1.0"}, errors.New("offline")
	}
	var enabled atomic.Bool
	enabled.Store(true)
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)
	fastUpdates(mgr)
	mgr.StartUpdater(enabled.Load)
	waitNotified(t, ch, "update failed")
	if h := mgr.Harnesses(context.Background())[0]; h.Update == nil {
		t.Fatal("no update status with updates on")
	}

	enabled.Store(false)
	if h := mgr.Harnesses(context.Background())[0]; h.Update != nil {
		t.Fatalf("update status = %+v with updates off, want none", h.Update)
	}
}

// Updates are opt-out, and the switch is read on every pass.
func TestUpdaterSkipsWhenDisabled(t *testing.T) {
	mgr, ua := updTestManager(t)
	mgr.updatePacing = updatePacing{first: time.Millisecond, interval: 5 * time.Millisecond, busyRetry: time.Millisecond, busyAttempts: 3}
	var mu sync.Mutex
	asked := 0
	mgr.StartUpdater(func() bool {
		mu.Lock()
		defer mu.Unlock()
		asked++
		return false
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := asked
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the updater never ran a pass")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, updates := ua.counts(); updates != 0 {
		t.Fatalf("updates = %d with updates disabled", updates)
	}
	if h := mgr.Harnesses(context.Background())[0]; h.Update != nil {
		t.Fatalf("update status = %+v, want none", h.Update)
	}
}

// A session caught mid-start must not have its binary replaced under it. The
// updater waits for the start to finish and then runs, rather than giving up
// until the next hour.
func TestUpdaterDefersWhileASessionIsStarting(t *testing.T) {
	mgr, ua := updTestManager(t)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	ua.createGate, ua.createStarted = gate, started

	created := make(chan error, 1)
	go func() {
		_, err := mgr.Create(context.Background(), "upd", "", t.TempDir(), "", "")
		created <- err
	}()
	<-started

	fastUpdates(mgr)
	mgr.StartUpdater(func() bool { return true })
	time.Sleep(100 * time.Millisecond)
	if _, updates := ua.counts(); updates != 0 {
		t.Fatalf("updated %d times while a session was starting", updates)
	}

	close(gate)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, updates := ua.counts(); updates == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the deferred update never ran once the start finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A harness with no ready instance (not installed, signed out) is left alone,
// and that is not reported as a failed update.
func TestUpdaterSkipsADriverWithNoAvailableInstance(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.avail = adapter.Unavailable("not installed")
	fastUpdates(mgr)
	mgr.StartUpdater(func() bool { return true })

	deadline := time.Now().Add(3 * time.Second)
	for {
		if probes, _ := ua.counts(); probes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the updater never looked at the driver")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if _, updates := ua.counts(); updates != 0 {
		t.Fatalf("updates = %d for a driver with nothing available", updates)
	}
	if h := mgr.Harnesses(context.Background())[0]; h.Update != nil {
		t.Fatalf("update status = %+v, want none", h.Update)
	}
}

// watchFast starts the binary watcher on a short interval and waits until it
// has taken its first look, so a change made after this is one it can see.
func watchFast(t *testing.T, mgr *Manager) {
	t.Helper()
	mgr.watchInterval = 10 * time.Millisecond
	mgr.StartBinaryWatch()
	deadline := time.Now().Add(3 * time.Second)
	for {
		mgr.binaryMu.Lock()
		_, seen := mgr.binarySeen["upd"]
		mgr.binaryMu.Unlock()
		if seen {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher never looked at the binary")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A binary replaced out of band reaches a client that stays connected: it is
// told, and the listing it then asks for re-probes and re-lists the new
// binary.
func TestBinaryWatchTellsClientsAboutANewBinary(t *testing.T) {
	mgr, ua := updTestManager(t)
	ua.setLive([]adapter.ModelMeta{{ID: "old", Label: "Old"}})
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)
	mgr.Harnesses(context.Background())
	waitNotified(t, ch, "first listing")
	waitRefreshed(t, mgr)
	watchFast(t, mgr)

	ua.setLive([]adapter.ModelMeta{{ID: "new", Label: "New"}})
	ua.replaceBinary(t, "installed by hand")
	waitNotified(t, ch, "binary replaced")

	probesBefore, _ := ua.counts()
	if got := mgr.Harnesses(context.Background())[0].Models; len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("models = %v, want the old list served while the new one is fetched", got)
	}
	if probes, _ := ua.counts(); probes != probesBefore+1 {
		t.Fatalf("probes = %d, want one fresh probe of the new binary", probes-probesBefore)
	}
	waitNotified(t, ch, "listing of the new binary")
	if got := mgr.Harnesses(context.Background())[0].Models; len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("models = %v, want the new binary's list", got)
	}
}

// An unchanged binary is not news.
func TestBinaryWatchIsQuietWhileTheBinaryIsUnchanged(t *testing.T) {
	mgr, _ := updTestManager(t)
	id, ch := mgr.SubscribeHarnesses()
	defer mgr.UnsubscribeHarnesses(id)
	watchFast(t, mgr)
	time.Sleep(100 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("notified with the binary unchanged")
	default:
	}
}
