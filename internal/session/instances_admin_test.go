package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// cfgAdapter is a fakeAdapter with an isolating config field, so
// defaultIsolation has something to fill.
type cfgAdapter struct{ fakeAdapter }

func (c *cfgAdapter) ConfigFields() []adapter.ConfigField {
	return []adapter.ConfigField{{Env: "FAKE_HOME", Label: "Home", Kind: adapter.FieldPath, Isolates: true}}
}

func adminTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(dir, "config.json"))
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := provider.OpenSecretStoreAt(filepath.Join(dir, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &cfgAdapter{})
	mgr.ConfigureInstances(nil, secrets)
	return mgr
}

func TestAddProviderInstanceLive(t *testing.T) {
	mgr := adminTestManager(t)
	err := mgr.AddProviderInstance(provider.Spec{ID: "fake-work", Driver: "fake", DisplayName: "Work", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	hs := mgr.Harnesses(context.Background())
	if len(hs[0].Instances) != 2 {
		t.Fatalf("instances = %+v", hs[0].Instances)
	}
	var work *InstanceMeta
	for i := range hs[0].Instances {
		if hs[0].Instances[i].ID == "fake-work" {
			work = &hs[0].Instances[i]
		}
	}
	if work == nil || !work.Configured || work.DisplayName != "Work" {
		t.Fatalf("added instance = %+v", work)
	}
	// The default stays unconfigured — it has no config entry to edit.
	for _, i := range hs[0].Instances {
		if i.ID == "fake" && i.Configured {
			t.Error("synthesised default must not present as configured")
		}
	}
}

func TestAddProviderInstanceUnknownDriver(t *testing.T) {
	mgr := adminTestManager(t)
	if err := mgr.AddProviderInstance(provider.Spec{ID: "x", Driver: "nope", Enabled: true}); err == nil {
		t.Fatal("unknown driver must be refused — the UI only offers real ones")
	}
}

func TestAddProviderInstanceDefaultsIsolation(t *testing.T) {
	mgr := adminTestManager(t)
	if err := mgr.AddProviderInstance(provider.Spec{ID: "fake-work", Driver: "fake", DisplayName: "Work", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	reg, ok := mgr.lookup("fake-work")
	if !ok {
		t.Fatal("instance not installed")
	}
	var home string
	for _, v := range reg.inst.Env {
		if v.Name == "FAKE_HOME" {
			home = v.Value
		}
	}
	if home == "" {
		t.Fatal("a new explicit instance must get an isolated home by default")
	}
	if filepath.Base(home) != "fake-work" {
		t.Errorf("isolated home should be per-instance, got %q", home)
	}

	// An explicitly set path is respected, not overwritten.
	if err := mgr.AddProviderInstance(provider.Spec{
		ID: "fake-two", Driver: "fake", DisplayName: "Two", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/custom"}},
	}); err != nil {
		t.Fatal(err)
	}
	reg2, _ := mgr.lookup("fake-two")
	for _, v := range reg2.inst.Env {
		if v.Name == "FAKE_HOME" && v.Value != "/custom" {
			t.Errorf("explicit home overwritten: %q", v.Value)
		}
	}
}

func TestListingEchoesEnvWithSecretsRedacted(t *testing.T) {
	mgr := adminTestManager(t)
	if err := mgr.AddProviderInstance(provider.Spec{
		ID: "fake-work", Driver: "fake", DisplayName: "Work", Enabled: true,
		Env: []provider.EnvVar{
			{Name: "FAKE_HOME", Value: "/custom"},
			{Name: "FAKE_TOKEN", Value: "tok-secret", Sensitive: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	hs := mgr.Harnesses(context.Background())
	var work *InstanceMeta
	for i := range hs[0].Instances {
		if hs[0].Instances[i].ID == "fake-work" {
			work = &hs[0].Instances[i]
		}
	}
	if work == nil {
		t.Fatal("instance missing from listing")
	}
	byName := map[string]provider.EnvVar{}
	for _, v := range work.Env {
		byName[v.Name] = v
	}
	if byName["FAKE_HOME"].Value != "/custom" {
		t.Errorf("plain env value must echo for prefill: %+v", work.Env)
	}
	if v := byName["FAKE_TOKEN"]; !v.Sensitive || v.Value != "" {
		t.Errorf("sensitive value must travel as a bare name: %+v", v)
	}
}

func TestSaveAndDeleteProviderInstanceLive(t *testing.T) {
	mgr := adminTestManager(t)
	spec := provider.Spec{ID: "fake-work", Driver: "fake", DisplayName: "Work", Enabled: true}
	if err := mgr.AddProviderInstance(spec); err != nil {
		t.Fatal(err)
	}
	spec.DisplayName = "Work v2"
	spec.Enabled = false
	if err := mgr.SaveProviderInstance(spec); err != nil {
		t.Fatal(err)
	}
	reg, _ := mgr.lookup("fake-work")
	if reg.inst.DisplayName != "Work v2" || reg.inst.Enabled {
		t.Fatalf("save not applied live: %+v", reg.inst)
	}

	if err := mgr.DeleteProviderInstance("fake-work"); err != nil {
		t.Fatal(err)
	}
	if _, ok := mgr.lookup("fake-work"); ok {
		t.Fatal("deleted instance still registered")
	}
	// The synthesised default survives every rebuild.
	if _, ok := mgr.lookup("fake"); !ok {
		t.Fatal("default instance vanished after delete")
	}
}
