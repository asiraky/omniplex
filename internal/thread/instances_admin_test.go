package thread

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// cfgAdapter is a fakeAdapter with an isolating config field, so
// defaultIsolation has something to fill.
type cfgAdapter struct{ fakeAdapter }

func (c *cfgAdapter) ConfigFields() []adapter.ConfigField {
	return []adapter.ConfigField{
		{Env: "FAKE_HOME", Label: "Home", Kind: adapter.FieldPath, Isolates: true},
		{Env: "FAKE_KEY", Label: "API key", Kind: adapter.FieldSecret},
	}
}

func adminTestManager(t *testing.T) *Manager {
	t.Helper()
	return adminTestManagerWith(t, &cfgAdapter{})
}

// adminTestManagerWith builds the same manager around a specific driver, for
// tests about an optional capability the plain fake does not implement.
func adminTestManagerWith(t *testing.T, ad adapter.Adapter) *Manager {
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
	mgr := NewManager(st, func(string, ...any) {}, ad)
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
	if len(hs[0].Instances) != 1 {
		t.Fatalf("instances = %+v", hs[0].Instances)
	}
	if work := hs[0].Instances[0]; work.ID != "fake-work" || work.DisplayName != "Work" {
		t.Fatalf("added instance = %+v", work)
	}
	if !hs[0].Availability.OK() {
		t.Errorf("the driver is usable once it has an account: %+v", hs[0].Availability)
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
	// Nothing stands in for the deleted account.
	if insts := mgr.Harnesses(context.Background())[0].Instances; len(insts) != 0 {
		t.Fatalf("instances after delete = %+v", insts)
	}
}

// The server, not the client, decides what is secret: a variable matching a
// secret-kind config field is stored as a secret even when the client claims
// it is not sensitive.
func TestSensitiveEnforcedFromConfigFields(t *testing.T) {
	mgr := adminTestManager(t)
	secrets := mgr.secretStore()
	err := mgr.AddProviderInstance(provider.Spec{
		ID: "fake-key", Driver: "fake", DisplayName: "Forged", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_KEY", Value: "sk-forged", Sensitive: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := secrets.Get("fake-key", "FAKE_KEY"); !ok || v != "sk-forged" {
		t.Fatalf("secret store should hold the value: %q %v", v, ok)
	}
	for _, h := range mgr.Harnesses(context.Background()) {
		for _, inst := range h.Instances {
			for _, v := range inst.Env {
				if v.Value == "sk-forged" {
					t.Errorf("plaintext secret echoed in listing: %+v", inst.Env)
				}
			}
		}
	}
}

// homeAdapter is a cfgAdapter whose isolating home shares a skills folder
// and a settings file, like Claude's does.
type homeAdapter struct{ cfgAdapter }

func (h *homeAdapter) DefaultHome(userHome string) string { return filepath.Join(userHome, ".fake") }

func (h *homeAdapter) SharedEntries() []accounthome.Entry {
	return []accounthome.Entry{{Name: "settings.json"}, {Name: "skills", Dir: true}}
}

func TestEveryAddedAccountSharesTheMachinesConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FAKE_HOME", "")
	shared := filepath.Join(home, ".fake")
	if err := os.MkdirAll(filepath.Join(shared, "skills", "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := adminTestManagerWith(t, &homeAdapter{})
	ids := []string{"fake-work", "fake-personal", "fake-client"}
	for _, id := range ids {
		if err := mgr.AddProviderInstance(provider.Spec{ID: id, Driver: "fake", DisplayName: id, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		account := filepath.Join(home, ".omniplex", "instances", id)
		if _, err := os.Stat(filepath.Join(account, "skills", "infra")); err != nil {
			t.Fatalf("%s does not see the shared skill: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(account, "settings.json")); err != nil {
			t.Fatalf("%s does not see the shared settings: %v", id, err)
		}
	}
	// The machine's own home is the shared one, not a linked copy of it.
	if info, err := os.Lstat(filepath.Join(shared, "skills")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("shared skills: %v %v", info, err)
	}
}

func TestARelativeAccountHomeIsSavedAsTheFolderItIsSharedInto(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FAKE_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".fake", "skills", "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := adminTestManagerWith(t, &homeAdapter{})
	for id, value := range map[string]string{"fake-rel": "accounts/rel", "fake-tilde": "~/accounts/tilde"} {
		spec := provider.Spec{ID: id, Driver: "fake", DisplayName: id, Enabled: true,
			Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: value}}}
		if err := mgr.AddProviderInstance(spec); err != nil {
			t.Fatal(err)
		}
		reg, _ := mgr.lookup(id)
		want := filepath.Join(home, "accounts", id[len("fake-"):])
		if got := reg.inst.Env[0].Value; got != want {
			t.Fatalf("%s saved FAKE_HOME=%q, want %q", id, got, want)
		}
		if _, err := os.Stat(filepath.Join(want, "skills", "infra")); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
}

func TestAnAccountWithNoHomeOfItsOwnIsNotLinked(t *testing.T) {
	home, ambient := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FAKE_HOME", ambient)
	if err := os.WriteFile(filepath.Join(home, "settings.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := adminTestManagerWith(t, &homeAdapter{})
	// No home variable: the harness runs ambient. Nothing is linked, and the
	// server's own FAKE_HOME is not taken for the account's.
	mgr.shareHome(provider.Instance{ID: "fake", Driver: "fake"}, nil)
	if _, err := os.Lstat(filepath.Join(home, ".fake")); !os.IsNotExist(err) {
		t.Fatalf("~/.fake was prepared: %v", err)
	}
	if des, _ := os.ReadDir(ambient); len(des) != 0 {
		t.Fatalf("server's home changed: %v", des)
	}
}
