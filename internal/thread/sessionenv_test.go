package thread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// driverHomeAdapter is an instAdapter under its own driver id whose accounts each
// have a config home in homeVar, like Codex's CODEX_HOME.
type driverHomeAdapter struct {
	instAdapter
	id, homeVar string
}

func (h *driverHomeAdapter) ID() string { return h.id }

func (h *driverHomeAdapter) Meta() adapter.HarnessMeta {
	m := h.instAdapter.Meta()
	m.ID, m.Name = h.id, h.id
	return m
}

func (h *driverHomeAdapter) ConfigFields() []adapter.ConfigField {
	return []adapter.ConfigField{{Env: h.homeVar, Label: "Home", Kind: adapter.FieldPath, Isolates: true}}
}

func homeAccount(id, driver, homeVar, home string, enabled bool) provider.Instance {
	return provider.Instance{
		ID: id, Driver: driver, DisplayName: id, Enabled: enabled,
		Env: []provider.EnvVar{{Name: homeVar, Value: home}},
	}
}

func sessionEnvManager(t *testing.T, accounts ...provider.Instance) (*Manager, *driverHomeAdapter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fake := &driverHomeAdapter{id: "fake", homeVar: "FAKE_HOME"}
	other := &driverHomeAdapter{id: "other", homeVar: "OTHER_HOME"}
	mgr := NewManager(st, func(string, ...any) {}, fake, other)
	mgr.ConfigureInstances(accounts, nil)
	return mgr, fake
}

// A thread's shell runs other harnesses' CLIs, so it gets the home of each
// other harness's primary account: the login the user signed in to from
// Omniplex, not the machine's own. Its own harness keeps its account's home.
func TestAThreadGetsTheOtherHarnessesHomes(t *testing.T) {
	mgr, fake := sessionEnvManager(t,
		homeAccount("fake-work", "fake", "FAKE_HOME", "/work", true),
		homeAccount("fake-personal", "fake", "FAKE_HOME", "/personal", true),
		homeAccount("other-off", "other", "OTHER_HOME", "/off", false),
		homeAccount("other-on", "other", "OTHER_HOME", "/on", true),
	)
	a, err := mgr.Create(context.Background(), "fake", "fake-work", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")

	env := fake.threadEnv()
	if env["FAKE_HOME"] != "/work" {
		t.Errorf("FAKE_HOME = %q, want the thread's own account's /work", env["FAKE_HOME"])
	}
	if env["OTHER_HOME"] != "/on" {
		t.Errorf("OTHER_HOME = %q, want the enabled other account's /on", env["OTHER_HOME"])
	}
}

// With no enabled account for a harness there is no Omniplex login to point
// at, so its variable is not set at all.
func TestAThreadGetsNoHomeForAHarnessWithoutAnAccount(t *testing.T) {
	mgr, fake := sessionEnvManager(t,
		homeAccount("fake-work", "fake", "FAKE_HOME", "/work", true),
		homeAccount("other-off", "other", "OTHER_HOME", "/off", false),
	)
	a, err := mgr.Create(context.Background(), "fake", "fake-work", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")

	if v, ok := fake.threadEnv()["OTHER_HOME"]; ok {
		t.Errorf("OTHER_HOME = %q, want unset", v)
	}
}
