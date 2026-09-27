package session

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

func (f *instAdapter) sessionEnv() map[string]string {
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
	return NewManager(st, func(string, ...any) {}, fa), fa, st
}

func workInstance() provider.Instance {
	return provider.Instance{
		ID: "fake-work", Driver: "fake", DisplayName: "Fake Work", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/work"}},
	}
}

func TestDefaultInstanceSynthesisedPerAdapter(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	hs := mgr.Harnesses(context.Background())
	if len(hs) != 1 {
		t.Fatalf("harnesses = %d, want 1", len(hs))
	}
	if len(hs[0].Instances) != 1 {
		t.Fatalf("instances = %d, want the synthesised default", len(hs[0].Instances))
	}
	inst := hs[0].Instances[0]
	if inst.ID != "fake" || inst.Driver != "fake" || !inst.Enabled {
		t.Errorf("default instance = %+v", inst)
	}
	if len(inst.Models) == 0 {
		t.Error("instance should carry its own model list")
	}
}

func TestInstancesReportIndependentAvailability(t *testing.T) {
	mgr, fa, _ := instTestManager(t)
	// The work account is broken; the ambient default is fine. One being
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
		t.Errorf("default instance must stay ready: %+v", byID["fake"])
	}
	if byID["fake-work"].OK() {
		t.Error("broken instance must report unavailable")
	}
	if !hs[0].Availability.OK() {
		t.Error("driver-level availability mirrors the default instance")
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
	meta, err := st.Session(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ProviderInstance != "fake-work" {
		t.Errorf("ProviderInstance = %q, want fake-work", meta.ProviderInstance)
	}
	if meta.Harness != "fake" {
		t.Errorf("Harness stays the driver id, got %q", meta.Harness)
	}
	if env := fa.sessionEnv(); env["FAKE_HOME"] != "/work" {
		t.Errorf("adapter did not receive the instance overlay: %v", env)
	}

	// Creating on the bare harness id uses the default instance and ambient env.
	b, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Dispose("test done")
	metaB, _ := st.Session(ctx, b.ID)
	if metaB.ProviderInstance != "fake" {
		t.Errorf("default create ProviderInstance = %q, want fake", metaB.ProviderInstance)
	}
	if env := fa.sessionEnv(); len(env) != 0 {
		t.Errorf("default instance must use ambient credentials, got %v", env)
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
	if env := fa.sessionEnv(); env["FAKE_TOKEN"] != "tok-123" {
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
	if env := fa.sessionEnv(); env["FAKE_HOME"] != "/work" {
		t.Errorf("resume must re-materialise the original instance's env, got %v", env)
	}
}

func TestLegacySessionResolvesToDefaultInstance(t *testing.T) {
	mgr, _, st := instTestManager(t)
	ctx := context.Background()
	// A row written before provider instances existed: harness only.
	meta := store.SessionMeta{ID: "legacy-1", Cwd: t.TempDir(), Harness: "fake", Phase: "idle"}
	if err := st.CreateSession(ctx, meta); err != nil {
		t.Fatal(err)
	}
	a, err := mgr.Get(ctx, "legacy-1")
	if err != nil {
		t.Fatalf("legacy session must resolve to the default instance: %v", err)
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
	// instance's session on the ambient account.
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
	// "fake" is the synthesised default for the fake driver; an instance of a
	// *different* driver must not be allowed to take its id.
	mgr.ConfigureInstances([]provider.Instance{{ID: "fake", Driver: "ollama", DisplayName: "Impostor", Enabled: true}}, nil)

	reg := mgr.instances["fake"]
	if reg.inst.Driver != "fake" {
		t.Fatalf("default instance was replaced by a %q-driver entry", reg.inst.Driver)
	}
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err != nil {
		t.Errorf("default instance must keep working after a rejected collision: %v", err)
	}
}

func TestVanishedInstanceStillRestoresPendingSessions(t *testing.T) {
	mgr, _, st := instTestManager(t)
	ctx := context.Background()
	// A session created under an instance that has since been removed from the
	// config, caught mid-provisioning. It must stay attachable so cleanup can
	// run; requiring the instance would strand it forever.
	meta := store.SessionMeta{ID: "orphan-1", Cwd: t.TempDir(), Harness: "fake", ProviderInstance: "gone-instance", Phase: "provision_failed"}
	if err := st.CreateSession(ctx, meta); err != nil {
		t.Fatal(err)
	}
	a, err := mgr.Get(ctx, "orphan-1")
	if err != nil {
		t.Fatalf("pending session with a vanished instance must restore: %v", err)
	}
	a.Dispose("test done")

	// An idle session, by contrast, would spawn a harness on resume, and must
	// be refused legibly rather than run under the wrong account.
	meta2 := store.SessionMeta{ID: "orphan-2", Cwd: t.TempDir(), Harness: "fake", ProviderInstance: "gone-instance", Phase: "idle"}
	if err := st.CreateSession(ctx, meta2); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Get(ctx, "orphan-2"); err == nil {
		t.Error("resuming a live session on a vanished instance must fail legibly")
	}
}

func TestDisabledInstanceRefusesCreate(t *testing.T) {
	mgr, _, _ := instTestManager(t)
	inst := workInstance()
	inst.Enabled = false
	mgr.ConfigureInstances([]provider.Instance{inst}, nil)

	if _, err := mgr.Create(context.Background(), "fake", "fake-work", t.TempDir(), "", ""); err == nil {
		t.Error("a disabled instance must refuse to start sessions")
	}
	hs := mgr.Harnesses(context.Background())
	for _, i := range hs[0].Instances {
		if i.ID == "fake-work" && i.Availability.OK() {
			t.Error("a disabled instance must list as unavailable")
		}
	}
}
