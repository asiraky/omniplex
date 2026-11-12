package thread

import (
	"context"
	"fmt"
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
		t.Fatalf("instances = %+v, want only the added one", hs[0].Instances)
	}
	if work := hs[0].Instances[0]; work.ID != "fake-work" || work.DisplayName != "Work" {
		t.Fatalf("added instance = %+v", work)
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

	// An account named for its driver is no exception: it is not the
	// machine's own home either.
	if err := mgr.AddProviderInstance(provider.Spec{ID: "fake", Driver: "fake", DisplayName: "Fake", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := envValue(mgr, "fake", "FAKE_HOME"); got != accounthome.InstanceHome(os.Getenv("HOME"), "fake") {
		t.Errorf("account named for its driver got FAKE_HOME=%q", got)
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
	// Deleting the last account leaves the driver with none, not with an
	// account made up in its place.
	if insts := mgr.Harnesses(context.Background())[0].Instances; len(insts) != 0 {
		t.Fatalf("instances after deleting the only one = %+v", insts)
	}
}

// envValue is the configured value of one variable of instance id.
func envValue(mgr *Manager, id, name string) string {
	reg, _ := mgr.lookup(id)
	for _, v := range reg.inst.Env {
		if v.Name == name {
			return v.Value
		}
	}
	return ""
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

// homeTestManager is a manager around homeAdapter on a throwaway HOME with no
// ~/.fake, and FAKE_HOME unset in the process env.
func homeTestManager(t *testing.T) (mgr *Manager, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FAKE_HOME", "")
	return adminTestManagerWith(t, &homeAdapter{}), home
}

func addAccount(t *testing.T, mgr *Manager, id string) string {
	t.Helper()
	if err := mgr.AddProviderInstance(provider.Spec{ID: id, Driver: "fake", DisplayName: id, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return envValue(mgr, id, "FAKE_HOME")
}

// shape is a home's top-level entries: each link's literal target, or "file"
// / "dir" with a file's content, for a real entry.
func shape(t *testing.T, dir string) map[string]string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, de := range des {
		p := filepath.Join(dir, de.Name())
		switch info, _ := os.Lstat(p); {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[de.Name()] = "-> " + target
		case info.IsDir():
			out[de.Name()] = "dir"
		default:
			b, _ := os.ReadFile(p)
			out[de.Name()] = "file " + string(b)
		}
	}
	return out
}

func TestTheFirstAccountOnACleanMachineGetsItsOwnHomeAndTheSharedFolders(t *testing.T) {
	mgr, home := homeTestManager(t)
	got := addAccount(t, mgr, "fake")
	if want := accounthome.InstanceHome(home, "fake"); got != want {
		t.Fatalf("first account's home = %q, want %q", got, want)
	}
	shared := filepath.Join(home, ".fake")
	if info, err := os.Lstat(filepath.Join(shared, "skills")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("shared skills folder not made as a real folder: %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(got, "skills")); err != nil || target != filepath.Join(shared, "skills") {
		t.Fatalf("account skills link = %q %v", target, err)
	}
	// A skill installed into the shared folder later is the account's too.
	if err := os.Mkdir(filepath.Join(shared, "skills", "infra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, "skills", "infra")); err != nil {
		t.Fatalf("account does not see a skill added to the shared folder: %v", err)
	}
}

func TestTheSecondAccountHasTheFirstsShape(t *testing.T) {
	mgr, home := homeTestManager(t)
	if err := os.MkdirAll(filepath.Join(home, ".fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fake", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := shape(t, addAccount(t, mgr, "fake"))
	second := shape(t, addAccount(t, mgr, "fake-two"))
	if len(first) == 0 {
		t.Fatal("first account's home is empty")
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("homes differ:\nfirst  %v\nsecond %v", first, second)
	}
}

// A sign-in from a terminal writes the machine's own home. No account sees it.
func TestATerminalSignInChangesNoAccount(t *testing.T) {
	mgr, home := homeTestManager(t)
	ids := []string{"fake", "fake-two"}
	homes := map[string]string{}
	envs := map[string]string{}
	shapes := map[string]string{}
	for _, id := range ids {
		homes[id] = addAccount(t, mgr, id)
		reg, _ := mgr.lookup(id)
		env, err := mgr.envFor(reg.inst)
		if err != nil {
			t.Fatal(err)
		}
		envs[id] = fmt.Sprint(env)
		shapes[id] = fmt.Sprint(shape(t, homes[id]))
	}
	for name, body := range map[string]string{".credentials.json": `{"token":"terminal"}`, ".fake.json": `{"account":"terminal@x"}`} {
		if err := os.WriteFile(filepath.Join(home, ".fake", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		reg, _ := mgr.lookup(id)
		env, err := mgr.envFor(reg.inst)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(env) != envs[id] {
			t.Errorf("%s env changed: %v -> %v", id, envs[id], env)
		}
		if got := fmt.Sprint(shape(t, homes[id])); got != shapes[id] {
			t.Errorf("%s home changed:\n%s\n%s", id, shapes[id], got)
		}
	}
}

// A server started from inside a harness session inherits that session's
// home variable. It decides neither where the shared configuration comes
// from nor any account's home.
func TestTheServersOwnHomeVariableIsIgnored(t *testing.T) {
	mgr, home := homeTestManager(t)
	session := t.TempDir()
	t.Setenv("FAKE_HOME", session)
	if err := os.MkdirAll(filepath.Join(session, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	account := addAccount(t, mgr, "fake")
	if target, _ := os.Readlink(filepath.Join(account, "skills")); target != filepath.Join(home, ".fake", "skills") {
		t.Errorf("account skills link to %q, want the machine's ~/.fake", target)
	}

	// An instance with no home of its own (a hand edit, or a config not yet
	// migrated) is pinned to the machine's home, not left to inherit.
	reg, _ := mgr.lookup("fake")
	bare := reg.inst
	bare.ID, bare.Env, bare.Raw = "fake-bare", nil, nil
	mgr.ConfigureInstances([]provider.Instance{reg.inst, bare}, mgr.secretStore())
	for _, id := range []string{"fake", "fake-bare"} {
		reg, _ := mgr.lookup(id)
		env, err := mgr.envFor(reg.inst)
		if err != nil {
			t.Fatal(err)
		}
		if env["FAKE_HOME"] == "" || env["FAKE_HOME"] == session {
			t.Errorf("%s runs with FAKE_HOME=%q", id, env["FAKE_HOME"])
		}
	}
	reg, _ = mgr.lookup("fake-bare")
	if env, _ := mgr.envFor(reg.inst); env["FAKE_HOME"] != filepath.Join(home, ".fake") {
		t.Errorf("unhomed instance pinned to %q, want ~/.fake", env["FAKE_HOME"])
	}
	if des, _ := os.ReadDir(filepath.Join(session, "skills")); len(des) != 0 {
		t.Errorf("session home changed: %v", des)
	}
}

func instanceMeta(t *testing.T, mgr *Manager, id string) InstanceMeta {
	t.Helper()
	mgr.ExpireProbes()
	for _, h := range mgr.Harnesses(context.Background()) {
		for _, im := range h.Instances {
			if im.ID == id {
				return im
			}
		}
	}
	t.Fatalf("instance %s not listed", id)
	return InstanceMeta{}
}

func TestAnAccountsOwnCopyIsAConflictUntilItUsesTheShared(t *testing.T) {
	mgr, home := homeTestManager(t)
	if err := os.MkdirAll(filepath.Join(home, ".fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fake", "settings.json"), []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	account := addAccount(t, mgr, "fake")
	own := filepath.Join(account, "settings.json")
	if err := os.Remove(own); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("own"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := instanceMeta(t, mgr, "fake").ConfigConflicts; fmt.Sprint(got) != "[settings.json]" {
		t.Fatalf("conflicts = %v", got)
	}
	if b, _ := os.ReadFile(own); string(b) != "own" {
		t.Fatalf("account's own copy touched: %q", b)
	}

	if _, err := mgr.UseSharedConfig("fake", "auth.json"); err == nil {
		t.Error("an entry that is not shared configuration must be refused")
	}
	backup, err := mgr.UseSharedConfig("fake", "settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(backup); err != nil || string(b) != "own" {
		t.Fatalf("backup %q = %q %v", backup, b, err)
	}
	if b, _ := os.ReadFile(own); string(b) != "shared" {
		t.Fatalf("account settings now %q, want the shared ones", b)
	}
	if got := instanceMeta(t, mgr, "fake").ConfigConflicts; len(got) != 0 {
		t.Fatalf("conflict still listed: %v", got)
	}
}

func TestAccountsInsideTheMachinesHomeAreRefused(t *testing.T) {
	mgr, home := homeTestManager(t)
	for _, value := range []string{"~/.fake", filepath.Join(home, ".fake"), filepath.Join(home, ".fake", "work"), ".fake"} {
		err := mgr.AddProviderInstance(provider.Spec{ID: "fake", Driver: "fake", DisplayName: "Fake", Enabled: true,
			Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: value}}})
		if err == nil {
			t.Errorf("FAKE_HOME=%s was accepted", value)
		}
	}
	if _, ok := mgr.lookup("fake"); ok {
		t.Fatal("a refused account was installed")
	}
	addAccount(t, mgr, "fake")
	err := mgr.SaveProviderInstance(provider.Spec{ID: "fake", Driver: "fake", DisplayName: "Fake", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "~/.fake"}}})
	if err == nil {
		t.Fatal("saving an account onto the machine's home was accepted")
	}
	if got := envValue(mgr, "fake", "FAKE_HOME"); got != accounthome.InstanceHome(home, "fake") {
		t.Errorf("refused save changed the home to %q", got)
	}
	// A sibling folder whose name merely starts the same is not inside it.
	if err := mgr.AddProviderInstance(provider.Spec{ID: "fake-near", Driver: "fake", DisplayName: "Near", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "~/.fake-near"}}}); err != nil {
		t.Errorf("a sibling of the machine's home was refused: %v", err)
	}
}
