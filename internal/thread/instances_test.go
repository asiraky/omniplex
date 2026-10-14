package thread

import (
	"context"
	"path/filepath"
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
	return testManager(st, func(string, ...any) {}, fa), fa, st
}

func workInstance() provider.Instance {
	return provider.Instance{
		ID: "fake-work", Driver: "fake", DisplayName: "Fake Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/work"}},
	}
}

func TestADriverWithNoAccountIsUnavailable(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &instAdapter{})

	hs := mgr.Harnesses(context.Background())
	if len(hs) != 1 {
		t.Fatalf("harnesses = %d, want the driver listed", len(hs))
	}
	if len(hs[0].Instances) != 0 {
		t.Fatalf("instances = %+v, want none synthesised", hs[0].Instances)
	}
	if hs[0].Availability.OK() || hs[0].Availability.Reason == "" {
		t.Errorf("a driver with no account must say it cannot start: %+v", hs[0].Availability)
	}
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err == nil {
		t.Error("creating with no account must fail")
	}

	// Every account disabled is the same as none.
	mgr.ConfigureInstances([]provider.Instance{{ID: "off", Driver: "fake", DisplayName: "Off", Enabled: false}}, nil)
	if mgr.Harnesses(context.Background())[0].Availability.OK() {
		t.Error("a driver whose only account is disabled must be unavailable")
	}
}

func TestCreateWithoutAnInstancePicksTheFirstEnabledAccount(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &instAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	mgr.ConfigureInstances([]provider.Instance{
		{ID: "off", Driver: "fake", DisplayName: "Off", Enabled: false},
		{ID: "first", Driver: "fake", DisplayName: "First", Enabled: true, Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/first"}}},
		{ID: "second", Driver: "fake", DisplayName: "Second", Enabled: true},
	}, nil)
	ctx := context.Background()

	a, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, _ := st.Thread(ctx, a.ID)
	if meta.ProviderInstance != "first" {
		t.Errorf("ProviderInstance = %q, want first", meta.ProviderInstance)
	}
	if env := fa.threadEnv(); env["FAKE_HOME"] != "/first" {
		t.Errorf("thread ran with %v, want the first account's env", env)
	}
	if h := mgr.Harnesses(ctx)[0]; !h.Availability.OK() {
		t.Errorf("driver-level availability mirrors the first enabled account: %+v", h.Availability)
	}
}

func TestALegacyThreadIsNeverHandedToAnotherAccount(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &instAdapter{})
	mgr.ConfigureInstances([]provider.Instance{workInstance()}, nil)
	ctx := context.Background()
	// Harness only, and no account with the harness's id.
	if err := st.CreateThread(ctx, store.ThreadMeta{ID: "legacy-2", Cwd: t.TempDir(), Harness: "fake", Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Get(ctx, "legacy-2"); err == nil {
		t.Error("a legacy thread must not resume on whichever account the harness has")
	}
}

func TestInstancesReportIndependentAvailability(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	// The work account is broken; the first account is fine. One being
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
		t.Errorf("the healthy account must stay ready: %+v", byID["fake"])
	}
	if byID["fake-work"].OK() {
		t.Error("broken instance must report unavailable")
	}
	if !hs[0].Availability.OK() {
		t.Error("driver-level availability mirrors the first enabled account")
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

	// Creating on the bare harness id uses the first account, here one with no
	// env of its own.
	b, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Dispose("test done")
	metaB, _ := st.Thread(ctx, b.ID)
	if metaB.ProviderInstance != "fake" {
		t.Errorf("bare-harness create ProviderInstance = %q, want fake", metaB.ProviderInstance)
	}
	if env := fa.threadEnv(); len(env) != 0 {
		t.Errorf("the account has no env, got %v", env)
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

func TestLegacyThreadResolvesToTheAccountWithItsHarnessID(t *testing.T) {
	mgr, _, st := instTestManager(t)
	ctx := context.Background()
	// A row written before provider instances existed: harness only.
	meta := store.ThreadMeta{ID: "legacy-1", Cwd: t.TempDir(), Harness: "fake", Phase: "idle"}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	a, err := mgr.Get(ctx, "legacy-1")
	if err != nil {
		t.Fatalf("legacy thread must resolve to the account with its harness's id: %v", err)
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
	// "fake" is a fake-driver account; an instance of a *different* driver
	// must not be allowed to take its id.
	mgr.ConfigureInstances([]provider.Instance{{ID: "fake", Driver: "ollama", DisplayName: "Impostor", Enabled: true}}, nil)

	reg := mgr.instances["fake"]
	if reg.inst.Driver != "fake" {
		t.Fatalf("account was replaced by a %q-driver entry", reg.inst.Driver)
	}
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err != nil {
		t.Errorf("the account must keep working after a rejected collision: %v", err)
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
