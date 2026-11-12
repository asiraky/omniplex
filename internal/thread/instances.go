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

// applyInstances rebuilds the registry from a full configured-instance list.
// It is ConfigureInstances for a running server.
func (m *Manager) applyInstances(instances []provider.Instance) {
	m.instMu.Lock()
	m.instances = map[string]registered{}
	m.instanceOrder = nil
	m.configureLocked(instances)
	m.instMu.Unlock()
	m.notifyHarnesses()
}

// AddProviderInstance creates a configured instance and installs it live. The
// driver must exist in this build — the UI offers only real drivers — and
// every new account, the first included, gets its own config home under
// ~/.omniplex/instances, so no account's login is the machine's own.
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
	if err := refuseMachineHome(spec, ad); err != nil {
		return err
	}
	instances, err := provider.AddInstance(spec, m.secretStore(), m.logf)
	if err != nil {
		return err
	}
	m.applyInstances(instances)
	m.prepareHome(spec.ID)
	return nil
}

// SaveProviderInstance updates a configured instance in place; the entry must
// exist.
func (m *Manager) SaveProviderInstance(spec provider.Spec) error {
	if ad, ok := m.drivers[spec.Driver]; ok {
		spec = enforceSensitive(spec, ad)
		spec = absolutePaths(spec, ad)
		if err := refuseMachineHome(spec, ad); err != nil {
			return err
		}
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

// CheckAccountHomes runs the check every use of an account runs (envFor) on
// every configured account: missing links restored, links of the wrong shape
// re-pointed, an account's own copies recorded as conflicts for the listing.
// The server calls it on startup, so a home broken while it was down is fixed
// before anyone opens it. Problems are logged; none stops the rest.
func (m *Manager) CheckAccountHomes() {
	for _, reg := range m.orderedInstances() {
		if reg.ad == nil {
			continue
		}
		if _, err := m.envFor(reg.inst); err != nil {
			m.logf("provider instance %s: checking its home: %v", reg.inst.ID, err)
		}
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
	m.homeConflicts.Delete(id)
	return nil
}

// defaultIsolation fills the adapter's isolating config field for a new
// instance that did not set it: each account, the first as much as any, gets
// its own directory under ~/.omniplex/instances, so no two accounts share a
// login and none shares the terminal's. The user's configuration in it is
// linked from the machine's own home (shareHome).
func defaultIsolation(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" {
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
		Value: accounthome.InstanceHome(home, spec.ID),
	})
	return spec
}

// refuseMachineHome refuses a spec whose config home is the machine's own
// harness home or inside it. That home is where a terminal on the box signs
// in, and it is the one copy of the shared configuration every account links
// to: an account living there would have its login changed from outside
// Omniplex, and its home linked into itself.
func refuseMachineHome(spec provider.Spec, ad adapter.Adapter) error {
	homeEnv := isolatingEnv(ad)
	sh, ok := ad.(adapter.SharedHome)
	if homeEnv == "" || !ok {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	machine := sh.DefaultHome(home)
	for _, v := range spec.Env {
		if v.Name != homeEnv || v.Sensitive {
			continue
		}
		if p := accounthome.Resolve(home, v.Value); p != "" && (p == machine || within(p, machine)) {
			return fmt.Errorf("%s=%s is the machine's own %s home, which a terminal signs in to; give the account a folder of its own", homeEnv, v.Value, ad.Meta().Name)
		}
	}
	return nil
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

// pinHome makes an isolating driver's env always name its config home, so the
// harness never inherits the server process's value. An instance with none is
// only a hand edit or a config the startup migration has not fixed yet; it
// runs on the machine's own home, said once, until it is given its own.
func (m *Manager) pinHome(inst provider.Instance, env map[string]string) map[string]string {
	ad := m.drivers[inst.Driver]
	sh, ok := ad.(adapter.SharedHome)
	homeEnv := isolatingEnv(ad)
	if !ok || homeEnv == "" || strings.TrimSpace(env[homeEnv]) != "" {
		return env
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return env
	}
	// The adapters' rule for an account's home folder: any non-blank HOME.
	if h := strings.TrimSpace(env["HOME"]); h != "" {
		userHome = h
	}
	machine := sh.DefaultHome(userHome)
	if env == nil {
		env = map[string]string{}
	}
	env[homeEnv] = machine
	m.warnHome(inst.ID, fmt.Errorf("has no home of its own; running on %s until migrated", machine))
	return env
}

// homes is where inst's shared configuration comes from and the config home
// its harness runs in, given the env envFor built. The shared source is always
// the machine's own harness home, never the server process's variable: a
// server started from inside a harness session must not re-link every account
// to that session's home. ok is false for a driver with no shared home.
func (m *Manager) homes(inst provider.Instance, env map[string]string) (sh adapter.SharedHome, shared, account string, ok bool, err error) {
	ad := m.drivers[inst.Driver]
	sh, ok = ad.(adapter.SharedHome)
	homeEnv := isolatingEnv(ad)
	if !ok || homeEnv == "" {
		return nil, "", "", false, nil
	}
	serverHome, err := os.UserHomeDir()
	if err != nil {
		return nil, "", "", false, nil
	}
	shared = sh.DefaultHome(serverHome)
	raw := strings.TrimSpace(env[homeEnv])
	// A relative value is resolved by the harness against the session's
	// working directory, so there is no one home to prepare. Saved instances
	// are made absolute (absolutePaths); this is a hand edit.
	if !filepath.IsAbs(raw) {
		return sh, shared, "", true, fmt.Errorf("%s=%q is not an absolute path; not sharing configuration into it", homeEnv, raw)
	}
	return sh, shared, filepath.Clean(raw), true, nil
}

// shareHome links the user's configuration from the machine's own config
// home into the instance's. An instance pinned to the machine's home is the
// shared home and has nothing to do. Problems are logged, once each, and
// never stop the account from running: an account whose home could not be
// linked still has its login. The entries the account keeps its own copy of
// are remembered for the listing.
func (m *Manager) shareHome(inst provider.Instance, env map[string]string) {
	sh, shared, account, ok, err := m.homes(inst, env)
	if !ok {
		return
	}
	if err != nil {
		m.warnHome(inst.ID, err)
		return
	}
	conflicts, err := accounthome.Link(shared, account, sh.SharedEntries())
	if err != nil {
		m.warnHome(inst.ID, fmt.Errorf("sharing %s into %s: %w", shared, account, err))
	}
	if len(conflicts) == 0 {
		m.homeConflicts.Delete(inst.ID)
	} else {
		m.homeConflicts.Store(inst.ID, conflicts)
	}
}

// configConflicts is what the last shareHome found instance id holding its
// own copy of.
func (m *Manager) configConflicts(id string) []string {
	if v, ok := m.homeConflicts.Load(id); ok {
		return v.([]string)
	}
	return nil
}

// UseSharedConfig gives up an account's own copy of one shared entry: the
// copy is moved aside (backup is where to) and the entry linked to the
// user's. Nothing is deleted. Clients are told so the conflict clears.
func (m *Manager) UseSharedConfig(instanceID, entry string) (backup string, err error) {
	reg, ok := m.lookup(instanceID)
	if !ok {
		return "", fmt.Errorf("unknown provider instance %q", instanceID)
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return "", err
	}
	sh, shared, account, ok, err := m.homes(reg.inst, env)
	if !ok {
		return "", fmt.Errorf("%s shares no configuration", reg.inst.DisplayName)
	}
	if err != nil {
		return "", err
	}
	var e accounthome.Entry
	found := false
	for _, se := range sh.SharedEntries() {
		if se.Name == entry {
			e, found = se, true
		}
	}
	if !found {
		return "", fmt.Errorf("%q is not shared configuration for %s", entry, reg.inst.DisplayName)
	}
	backup, err = accounthome.UseShared(shared, account, e)
	m.prepareHome(instanceID)
	m.notifyHarnesses()
	return backup, err
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
