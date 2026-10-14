package thread

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/provider"
)

// This file is live provider-instance management: the manager-side half of
// the Providers settings surface. The write path goes through
// provider.Add/Save/DeleteInstance — which persist to the user config and the
// secret store — and then rebuilds the in-memory registry, so a crash between
// the two leaves the durable state authoritative and the next startup
// consistent.

// lookup reads one instance under the registry lock.
func (m *Manager) lookup(id string) (registered, bool) {
	m.instMu.RLock()
	defer m.instMu.RUnlock()
	reg, ok := m.instances[id]
	return reg, ok
}

// orderedInstances snapshots the registry in listing order.
func (m *Manager) orderedInstances() []registered {
	m.instMu.RLock()
	defer m.instMu.RUnlock()
	out := make([]registered, 0, len(m.instanceOrder))
	for _, id := range m.instanceOrder {
		out = append(out, m.instances[id])
	}
	return out
}

func (m *Manager) secretStore() *provider.SecretStore {
	m.instMu.RLock()
	defer m.instMu.RUnlock()
	return m.secrets
}

// applyInstances rebuilds the registry from a full configured-instance list,
// preserving the synthesised defaults exactly as startup does. It is
// ConfigureInstances for a running server.
func (m *Manager) applyInstances(instances []provider.Instance) {
	m.instMu.Lock()
	m.instances = map[string]registered{}
	m.instanceOrder = nil
	for _, id := range m.driverOrder {
		ad := m.drivers[id]
		m.register(registered{inst: provider.Default(ad.ID(), ad.Meta().Name), ad: ad})
	}
	m.configureLocked(instances)
	m.instMu.Unlock()
	m.notifyHarnesses()
}

// AddProviderInstance creates a configured instance and installs it live. The
// driver must exist in this build — the UI offers only real drivers — and new
// explicit instances get an isolated credential home by default, so a second
// account can never silently share state with the first.
func (m *Manager) AddProviderInstance(spec provider.Spec) error {
	ad, ok := m.drivers[spec.Driver]
	if !ok {
		return fmt.Errorf("no %q driver in this build", spec.Driver)
	}
	if reg, exists := m.lookup(spec.ID); exists && reg.inst.Driver != spec.Driver {
		return fmt.Errorf("instance id %q already belongs to driver %q", spec.ID, reg.inst.Driver)
	}
	spec = defaultIsolation(spec, ad)
	spec = enforceSensitive(spec, ad)
	spec = absolutePaths(spec, ad)
	instances, err := provider.AddInstance(spec, m.secretStore(), m.logf)
	if err != nil {
		return err
	}
	m.applyInstances(instances)
	m.prepareHome(spec.ID)
	return nil
}

// SaveProviderInstance updates a configured instance in place. Editing the
// implicit default instance works by creating a config entry with the
// driver's id, which Add covers; Save requires the entry to exist.
func (m *Manager) SaveProviderInstance(spec provider.Spec) error {
	if ad, ok := m.drivers[spec.Driver]; ok {
		spec = enforceSensitive(spec, ad)
		spec = absolutePaths(spec, ad)
	}
	instances, err := provider.SaveInstance(spec, m.secretStore(), m.logf)
	if err != nil {
		return err
	}
	m.applyInstances(instances)
	m.forgetInstance(spec.ID)
	m.prepareHome(spec.ID)
	return nil
}

// prepareHome shares the configuration into an instance's home as soon as
// it is added or repointed, before the harness first runs there: a sign-in
// started next must find the shared settings, not make the account its own.
func (m *Manager) prepareHome(id string) {
	if reg, ok := m.lookup(id); ok {
		_, _ = m.envFor(reg.inst)
	}
}

// DeleteProviderInstance removes a configured instance and its secrets.
// Threads that ran on it keep their history; resuming one reports the
// missing instance legibly rather than falling through to another account.
func (m *Manager) DeleteProviderInstance(id string) error {
	instances, err := provider.DeleteInstance(id, m.secretStore(), m.logf)
	if err != nil {
		return err
	}
	m.applyInstances(instances)
	m.forgetInstance(id)
	return nil
}

// defaultIsolation fills the adapter's isolating config field for a new
// explicit instance that did not set it: each account gets its own directory
// under ~/.omniplex/instances, so two Pi (or Codex, or Claude) accounts never
// overwrite each other's credentials. The user's configuration in it is linked
// from the machine's own home (shareHome). The default instance never passes
// through here and keeps ambient behaviour.
func defaultIsolation(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" || spec.ID == ad.ID() {
		return spec
	}
	for _, v := range spec.Env {
		if v.Name == homeEnv {
			return spec // explicitly set; respect it
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return spec
	}
	spec.Env = append(spec.Env, provider.EnvVar{
		Name:  homeEnv,
		Value: filepath.Join(home, ".omniplex", "instances", spec.ID),
	})
	return spec
}

// enforceSensitive marks every variable the adapter declares as a secret
// field sensitive, whatever the client said. The secrecy boundary is the
// server's: a forged or stale client must not be able to talk a key into the
// plaintext config, where redactedEnv would then echo it to every client.
func enforceSensitive(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	cfg, ok := ad.(adapter.Configurer)
	if !ok {
		return spec
	}
	secret := map[string]bool{}
	for _, field := range cfg.ConfigFields() {
		if field.Kind == adapter.FieldSecret {
			secret[field.Env] = true
		}
	}
	for i := range spec.Env {
		if secret[spec.Env[i].Name] {
			spec.Env[i].Sensitive = true
		}
	}
	return spec
}

// shareHome links the user's configuration from the machine's own config
// home into the instance's, when the instance has a home of its own. The
// default instance, and any instance pointed at the machine's home, is the
// shared home and has nothing to do. Problems are logged, once each, and never
// stop the account from running: an account whose home could not be linked
// still has its login.
func (m *Manager) shareHome(inst provider.Instance, env map[string]string) {
	ad := m.drivers[inst.Driver]
	sh, ok := ad.(adapter.SharedHome)
	if !ok {
		return
	}
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" {
		return
	}
	serverHome, err := os.UserHomeDir()
	if err != nil {
		return
	}
	shared := accounthome.Resolve(serverHome, os.Getenv(homeEnv))
	if shared == "" {
		shared = sh.DefaultHome(serverHome)
	}
	// The adapters' rule for an account's home folder: any non-blank HOME.
	userHome := serverHome
	if h := strings.TrimSpace(env["HOME"]); h != "" {
		userHome = h
	}
	// No override means the harness inherits the server's own value.
	raw, set := env[homeEnv]
	if !set {
		raw = os.Getenv(homeEnv)
	}
	account := sh.DefaultHome(userHome)
	if raw = strings.TrimSpace(raw); raw != "" {
		// A relative value is resolved by the harness against the session's
		// working directory, so there is no one home to prepare. Saved
		// instances are made absolute (absolutePaths); this is a hand edit.
		if !filepath.IsAbs(raw) {
			m.warnHome(inst.ID, fmt.Errorf("%s=%q is not an absolute path; not sharing configuration into it", homeEnv, raw))
			return
		}
		account = filepath.Clean(raw)
	}
	if err := accounthome.Link(shared, account, sh.SharedEntries()); err != nil {
		m.warnHome(inst.ID, fmt.Errorf("sharing %s into %s: %w", shared, account, err))
	}
}

// warnHome logs a problem preparing an instance's home once, not on every
// session start.
func (m *Manager) warnHome(id string, err error) {
	if _, seen := m.homeWarned.LoadOrStore(id+"\x00"+err.Error(), true); !seen {
		m.logf("provider instance %s: %v", id, err)
	}
}

// absolutePaths makes every path field of spec absolute, "~" and relative
// values against the home folder, so the harness and Omniplex agree on which
// folder it is: a harness resolves a relative path against each session's
// working directory.
func absolutePaths(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	cfg, ok := ad.(adapter.Configurer)
	if !ok {
		return spec
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return spec
	}
	path := map[string]bool{}
	for _, field := range cfg.ConfigFields() {
		if field.Kind == adapter.FieldPath {
			path[field.Env] = true
		}
	}
	for i, v := range spec.Env {
		if path[v.Name] && !v.Sensitive {
			spec.Env[i].Value = accounthome.Resolve(home, v.Value)
		}
	}
	return spec
}

// isolatingEnv is the variable that gives an instance of ad its own config
// home, or "" when the driver has none.
func isolatingEnv(ad adapter.Adapter) string {
	cfg, ok := ad.(adapter.Configurer)
	if !ok {
		return ""
	}
	for _, field := range cfg.ConfigFields() {
		if field.Isolates {
			return field.Env
		}
	}
	return ""
}
