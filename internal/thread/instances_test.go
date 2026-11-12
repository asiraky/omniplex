package thread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// instAdapter is a fakeAdapter that records the env overlay it is given and
// can report per-env availability, so instance independence is observable.
type instAdapter struct {
	fakeAdapter
	envMu    sync.Mutex
	lastEnv  map[string]string
	availFor func(env map[string]string) adapter.Availability
}

func (f *instAdapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	if f.availFor != nil {
		return f.availFor(env)
	}
	return adapter.Ready(nil)
}

func (f *instAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	f.envMu.Lock()
	f.lastEnv = o.Env
	f.envMu.Unlock()
	return f.fakeAdapter.CreateSession(ctx, host, o)
}

func (f *instAdapter) threadEnv() map[string]string {
	f.envMu.Lock()
	defer f.envMu.Unlock()
	return f.lastEnv
}

func instTestManager(t *testing.T) (*Manager, *instAdapter, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &instAdapter{}
	return newManager(st, func(string, ...any) {}, fa), fa, st
}

func workInstance() provider.Instance {
	return provider.Instance{
		ID: "fake-work", Driver: "fake", DisplayName: "Fake Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/work"}},
	}
}

// A driver is not an account: with nothing configured it lists no instance,
// says there is no account, and offers to add one, and a bare harness id has
// nothing to start a thread on.
func TestADriverWithNoAccountsHasNone(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &instAdapter{})

	hs := mgr.Harnesses(context.Background())
	if len(hs) != 1 {
		t.Fatalf("harnesses = %d, want 1", len(hs))
	}
	if hs[0].Instances == nil || len(hs[0].Instances) != 0 {
		t.Fatalf("instances = %#v, want an empty list", hs[0].Instances)
	}
	avail := hs[0].Availability
	if avail.OK() {
		t.Fatal("a driver with no account must not report ready")
	}
	if len(avail.Remedy) == 0 || avail.Remedy[0].Action != adapter.RemedyAddAccount {
		t.Errorf("remedy = %+v, want add-account", avail.Remedy)
	}

	_, err = mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err == nil || !strings.Contains(err.Error(), "add one") {
		t.Fatalf("create with no account = %v, want a legible no-account error", err)
	}
}

func TestEachAccountCarriesItsOwnModels(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	hs := mgr.Harnesses(context.Background())
	if len(hs) != 1 || len(hs[0].Instances) != 1 {
		t.Fatalf("harnesses = %+v", hs)
	}
	if len(hs[0].Instances[0].Models) == 0 {
		t.Error("instance should carry its own model list")
	}
}

// With no account under the harness's own id, a bare harness id starts on the
// first enabled account, and the driver-level availability is that one's.
func TestABareHarnessIDUsesTheFirstEnabledAccount(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &instAdapter{availFor: func(env map[string]string) adapter.Availability {
		if env["FAKE_HOME"] == "/off" {
			return adapter.Unavailable("disabled anyway")
		}
		return adapter.Ready(nil)
	}}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	off := workInstance()
	off.ID, off.Enabled, off.Env = "fake-off", false, []provider.EnvVar{{Name: "FAKE_HOME", Value: "/off"}}
	mgr.ConfigureInstances([]provider.Instance{off, workInstance()}, nil)

	a, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, _ := st.Thread(context.Background(), a.ID)
	if meta.ProviderInstance != "fake-work" {
		t.Errorf("bare harness id ran on %q, want fake-work", meta.ProviderInstance)
	}
	if h := mgr.Harnesses(context.Background())[0]; !h.Availability.OK() {
		t.Errorf("driver availability = %+v, want fake-work's", h.Availability)
	}
}

func TestInstancesReportIndependentAvailability(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	// The work account is broken; the primary one is fine. One being
	// unhealthy must not mark the other.
	fa.availFor = func(env map[string]string) adapter.Availability {
		if env["FAKE_HOME"] != "" {
			return adapter.Unavailable("logged out")
		}
		return adapter.Ready(nil)
	}
	mgr.ConfigureInstances([]provider.Instance{workInstance()}, nil)

	hs := mgr.Harnesses(context.Background())
	if len(hs) != 1 || len(hs[0].Instances) != 2 {
		t.Fatalf("want one driver with two instances, got %+v", hs)
	}
	byID := map[string]adapter.Availability{}
	for _, i := range hs[0].Instances {
		byID[i.ID] = i.Availability
	}
	if !byID["fake"].OK() {
		t.Errorf("primary instance must stay ready: %+v", byID["fake"])
	}
	if byID["fake-work"].OK() {
		t.Error("broken instance must report unavailable")
	}
	if !hs[0].Availability.OK() {
		t.Error("driver-level availability mirrors the primary instance")
	}
}

func TestCreatePersistsInstanceAndAppliesEnv(t *testing.T) {
	mgr, fa, st := instTestManager(t)
	mgr.ConfigureInstances([]provider.Instance{workInstance()}, nil)
	ctx := context.Background()

	a, err := mgr.Create(ctx, "fake", "fake-work", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, err := st.Thread(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ProviderInstance != "fake-work" {
		t.Errorf("ProviderInstance = %q, want fake-work", meta.ProviderInstance)
	}
	if meta.Harness != "fake" {
		t.Errorf("Harness stays the driver id, got %q", meta.Harness)
	}
	if env := fa.threadEnv(); env["FAKE_HOME"] != "/work" {
		t.Errorf("adapter did not receive the instance overlay: %v", env)
	}

	// Creating on the bare harness id uses the account whose id it is.
	b, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Dispose("test done")
	metaB, _ := st.Thread(ctx, b.ID)
	if metaB.ProviderInstance != "fake" {
		t.Errorf("bare create ProviderInstance = %q, want fake", metaB.ProviderInstance)
	}
	if env := fa.threadEnv(); len(env) != 0 {
		t.Errorf("fake's account has no overlay, got %v", env)
	}
}

func TestCreateMaterialisesSecretsAtSpawn(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	secrets, err := provider.OpenSecretStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Put("fake-work", "FAKE_TOKEN", "tok-123"); err != nil {
		t.Fatal(err)
	}
	inst := workInstance()
	inst.Env = append(inst.Env, provider.EnvVar{Name: "FAKE_TOKEN", Sensitive: true})
	mgr.ConfigureInstances([]provider.Instance{inst}, secrets)

	a, err := mgr.Create(context.Background(), "fake", "fake-work", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	if env := fa.threadEnv(); env["FAKE_TOKEN"] != "tok-123" {
		t.Errorf("secret not materialised at spawn: %v", env)
	}
}

func TestResumeReusesInstance(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	mgr.ConfigureInstances([]provider.Instance{workInstance()}, nil)
	ctx := context.Background()

	a, err := mgr.Create(ctx, "fake", "fake-work", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := a.ID
	a.Dispose("simulate restart")

	fa.envMu.Lock()
	fa.lastEnv = nil
	fa.envMu.Unlock()

	b, err := mgr.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Dispose("test done")
	if env := fa.threadEnv(); env["FAKE_HOME"] != "/work" {
		t.Errorf("resume must re-materialise the original instance's env, got %v", env)
	}
}

func TestLegacyThreadResolvesToTheAccountNamedForItsHarness(t *testing.T) {
	mgr, _, st := instTestManager(t)
	ctx := context.Background()
	// A row written before provider instances existed: harness only.
	meta := store.ThreadMeta{ID: "legacy-1", Cwd: t.TempDir(), Harness: "fake", Phase: "idle"}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	a, err := mgr.Get(ctx, "legacy-1")
	if err != nil {
		t.Fatalf("legacy thread must resolve to the instance named for its harness: %v", err)
	}
	a.Dispose("test done")
}

func TestUnknownDriverLoadsAndPresentsUnavailable(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	mgr.ConfigureInstances([]provider.Instance{{ID: "local-llm", Driver: "ollama", DisplayName: "Local", Enabled: true}}, nil)

	hs := mgr.Harnesses(context.Background())
	var found *Harness
	for i := range hs {
		if hs[i].ID == "ollama" {
			found = &hs[i]
		}
	}
	if found == nil {
		t.Fatal("an instance with an unknown driver must still be listed")
	}
	if found.Availability.OK() || len(found.Instances) != 1 || found.Instances[0].Availability.OK() {
		t.Errorf("unknown driver must present as unavailable: %+v", found)
	}

	if _, err := mgr.Create(context.Background(), "ollama", "local-llm", t.TempDir(), "", ""); err == nil {
		t.Error("creating on an unknown driver must fail legibly")
	}
}

func TestMissingSecretFailsClosed(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	secrets, err := provider.OpenSecretStoreAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inst := workInstance()
	inst.Env = append(inst.Env, provider.EnvVar{Name: "FAKE_TOKEN", Sensitive: true})
	// No secret stored: creating must refuse rather than silently run this
	// instance's thread on the ambient account.
	mgr.ConfigureInstances([]provider.Instance{inst}, secrets)

	if _, err := mgr.Create(context.Background(), "fake", "fake-work", t.TempDir(), "", ""); err == nil {
		t.Error("a missing secret must refuse creation, not fall back to ambient credentials")
	}
	for _, i := range mgr.Harnesses(context.Background())[0].Instances {
		if i.ID == "fake-work" && i.Availability.OK() {
			t.Error("an instance with a missing secret must list as unavailable")
		}
	}
}

func TestCrossDriverIDCollisionIsRejected(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	// "fake" is an account of the fake driver; an instance of a *different*
	// driver must not be allowed to take its id.
	mgr.ConfigureInstances([]provider.Instance{{ID: "fake", Driver: "ollama", DisplayName: "Impostor", Enabled: true}}, nil)

	reg := mgr.instances["fake"]
	if reg.inst.Driver != "fake" {
		t.Fatalf("instance was replaced by a %q-driver entry", reg.inst.Driver)
	}
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err != nil {
		t.Errorf("instance must keep working after a rejected collision: %v", err)
	}
}

func TestVanishedInstanceStillRestoresPendingThreads(t *testing.T) {
	mgr, _, st := instTestManager(t)
	ctx := context.Background()
	// A thread created under an instance that has since been removed from the
	// config, caught mid-provisioning. It must stay attachable so cleanup can
	// run; requiring the instance would strand it forever.
	meta := store.ThreadMeta{ID: "orphan-1", Cwd: t.TempDir(), Harness: "fake", ProviderInstance: "gone-instance", Phase: "provision_failed"}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	a, err := mgr.Get(ctx, "orphan-1")
	if err != nil {
		t.Fatalf("pending thread with a vanished instance must restore: %v", err)
	}
	a.Dispose("test done")

	// An idle thread, by contrast, would spawn a harness on resume, and must
	// be refused legibly rather than run under the wrong account.
	meta2 := store.ThreadMeta{ID: "orphan-2", Cwd: t.TempDir(), Harness: "fake", ProviderInstance: "gone-instance", Phase: "idle"}
	if err := st.CreateThread(ctx, meta2); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Get(ctx, "orphan-2"); err == nil {
		t.Error("resuming a live thread on a vanished instance must fail legibly")
	}
}

func TestDisabledInstanceRefusesCreate(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	inst := workInstance()
	inst.Enabled = false
	mgr.ConfigureInstances([]provider.Instance{inst}, nil)

	if _, err := mgr.Create(context.Background(), "fake", "fake-work", t.TempDir(), "", ""); err == nil {
		t.Error("a disabled instance must refuse to start threads")
	}
	hs := mgr.Harnesses(context.Background())
	for _, i := range hs[0].Instances {
		if i.ID == "fake-work" && i.Availability.OK() {
			t.Error("a disabled instance must list as unavailable")
		}
	}
}

// Two accounts signed in to one login share one set of limits, which reads as
// data mixing between them. Both listings say so; different logins do not.
// Emails decide when both accounts have one, even if their account ids match
// (a team's shared workspace); an account id decides only when one is missing.
func TestAccountsOnTheSameLoginNameEachOther(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	emails := map[string]string{"/a": "me@example.com", "/b": "Me@Example.com", "/c": "other@example.com",
		"/f": "one@team.com", "/g": "two@team.com", "/h": "h@example.com"}
	fa.availFor = func(env map[string]string) adapter.Availability {
		if e := emails[env["FAKE_HOME"]]; e != "" {
			return adapter.Ready(map[string]string{adapter.FactAccount: e})
		}
		return adapter.Ready(nil)
	}
	var insts []provider.Instance
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		insts = append(insts, provider.Instance{ID: id, Driver: "fake", DisplayName: id, Enabled: true,
			Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/" + id}}})
	}
	mgr.ConfigureInstances(insts, nil)
	// d and e report no email, but their limits carry one account id.
	mgr.reportQuota("fake", "d", adapter.QuotaSnapshot{AccountID: "acct-1"}, true)
	mgr.reportQuota("fake", "e", adapter.QuotaSnapshot{AccountID: "acct-1"}, true)
	// h has an email and d's account id: d has none, so the id decides.
	mgr.reportQuota("fake", "h", adapter.QuotaSnapshot{AccountID: "acct-1"}, true)
	// f and g are two people in one workspace.
	mgr.reportQuota("fake", "f", adapter.QuotaSnapshot{AccountID: "ws-1"}, true)
	mgr.reportQuota("fake", "g", adapter.QuotaSnapshot{AccountID: "ws-1"}, true)

	want := map[string]string{"a": "[b]", "b": "[a]", "c": "[]", "d": "[e h]", "e": "[d h]",
		"f": "[]", "g": "[]", "h": "[d e]", "fake": "[]"}
	got := map[string]string{}
	for _, im := range mgr.Harnesses(context.Background())[0].Instances {
		got[im.ID] = fmt.Sprint(im.SameLoginAs)
		if im.ID == "c" && im.Account != "other@example.com" {
			t.Errorf("c's account = %q", im.Account)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("instances SameLoginAs = %v, want %v", got, want)
	}
	got = map[string]string{}
	for _, q := range mgr.Quotas() {
		got[q.Instance] = fmt.Sprint(q.SameLoginAs)
		if q.Instance == "a" && q.Account != "me@example.com" {
			t.Errorf("a's quota account = %q", q.Account)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("quotas SameLoginAs = %v, want %v", got, want)
	}
}
