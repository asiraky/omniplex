package thread

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	m.homeMu.Lock()
	for id := range m.homeConflicts {
		if _, ok := m.lookup(id); !ok {
			delete(m.homeConflicts, id)
		}
	}
	m.homeMu.Unlock()
	m.notifyHarnesses()
}

// AddProviderInstance creates a configured instance and installs it live. The
// driver must exist in this build — the UI offers only real drivers — and
// every instance gets an isolated config home by default, so no account is
// the machine's own and no two share a login.
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
	if err := notMachineHome(spec, ad); err != nil {
		return err
	}
	if err := m.homeNotTaken(spec, ad); err != nil {
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

// SaveProviderInstance updates a configured instance in place; the entry
// must exist. A home field left blank is filled in as Add fills it, except
// on an account that has no home of its own yet (one a migration left on
// the machine's home): repointing it would strand its conversations there,
// so it stays as it is until it is given a home explicitly.
func (m *Manager) SaveProviderInstance(spec provider.Spec) error {
	if ad, ok := m.drivers[spec.Driver]; ok {
		if m.staysAmbient(spec, ad) {
			spec = withoutBlankHome(spec, ad)
		} else {
			spec = defaultIsolation(spec, ad)
		}
		spec = enforceSensitive(spec, ad)
		spec = absolutePaths(spec, ad)
		if err := notMachineHome(spec, ad); err != nil {
			return err
		}
		if err := m.homeNotTaken(spec, ad); err != nil {
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
		m.linkHome(reg.inst)
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

// defaultIsolation fills the adapter's isolating config field when the spec
// leaves it unset or blank: each account gets its own directory under
// ~/.omniplex/instances, so two Pi (or Codex, or Claude) accounts never
// overwrite each other's credentials and none is the machine's own. The
// user's configuration in it is linked from the machine's home (shareHome).
func defaultIsolation(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" {
		return spec
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return spec
	}
	value := filepath.Join(home, ".omniplex", "instances", spec.ID)
	for i, v := range spec.Env {
		if v.Name != homeEnv {
			continue
		}
		if strings.TrimSpace(v.Value) == "" && !v.Sensitive {
			spec.Env[i].Value = value
		}
		return spec // explicitly set; respect it
	}
	spec.Env = append(spec.Env, provider.EnvVar{Name: homeEnv, Value: value})
	return spec
}

// notMachineHome refuses an account whose config home is the machine's own
// (~/.claude, ~/.codex, ~/.pi/agent): that home's login is whatever a
// terminal on the box last signed in as, and it is the shared configuration
// every account links to.
func notMachineHome(spec provider.Spec, ad adapter.Adapter) error {
	sh, ok := ad.(adapter.SharedHome)
	homeEnv := isolatingEnv(ad)
	if !ok || homeEnv == "" {
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
		if p := accounthome.Resolve(home, v.Value); p != "" && (p == machine || samePath(p, machine)) {
			return fmt.Errorf("%s is this machine's own %s home, which a terminal sign-in changes; give the account a folder of its own", machine, ad.Meta().Name)
		}
	}
	return nil
}

// onMachineHome reports an instance of a driver that isolates its home
// running on the machine's own one anyway: no home variable, or one naming
// the machine's home. A secret value is not read, so it is not reported.
func onMachineHome(inst provider.Instance, ad adapter.Adapter) bool {
	if ad == nil {
		return false
	}
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" {
		return false
	}
	value, sensitive := homeValue(inst.Env, homeEnv)
	switch {
	case sensitive:
		return false
	case value == "":
		return true
	}
	sh, ok := ad.(adapter.SharedHome)
	if !ok {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	machine := sh.DefaultHome(home)
	p := accounthome.Resolve(home, value)
	return p == machine || samePath(p, machine)
}

// homeValue is spec's plain value for the home variable, trimmed, and
// whether it is a secret (whose value Omniplex does not read here).
func homeValue(env []provider.EnvVar, homeEnv string) (value string, sensitive bool) {
	for _, v := range env {
		if v.Name == homeEnv {
			return strings.TrimSpace(v.Value), v.Sensitive
		}
	}
	return "", false
}

// staysAmbient reports a save that leaves the home blank on an instance
// whose stored entry has none.
func (m *Manager) staysAmbient(spec provider.Spec, ad adapter.Adapter) bool {
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" {
		return false
	}
	reg, ok := m.lookup(spec.ID)
	if !ok {
		return false
	}
	if stored, sensitive := homeValue(reg.inst.Env, homeEnv); stored != "" || sensitive {
		return false
	}
	incoming, sensitive := homeValue(spec.Env, homeEnv)
	return incoming == "" && !sensitive
}

// withoutBlankHome drops a blank home variable, so the entry stays as it was
// stored: without one.
func withoutBlankHome(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	homeEnv := isolatingEnv(ad)
	env := make([]provider.EnvVar, 0, len(spec.Env))
	for _, v := range spec.Env {
		if v.Name == homeEnv && strings.TrimSpace(v.Value) == "" && !v.Sensitive {
			continue
		}
		env = append(env, v)
	}
	spec.Env = env
	return spec
}

// homeNotTaken refuses a config home another instance already uses: two
// accounts on one home share one login, whatever their names say.
func (m *Manager) homeNotTaken(spec provider.Spec, ad adapter.Adapter) error {
	homeEnv := isolatingEnv(ad)
	if homeEnv == "" {
		return nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	value, sensitive := homeValue(spec.Env, homeEnv)
	if value == "" || sensitive {
		return nil
	}
	mine := accounthome.Resolve(userHome, value)
	for _, reg := range m.orderedInstances() {
		if reg.inst.ID == spec.ID || reg.ad == nil {
			continue
		}
		otherEnv := isolatingEnv(reg.ad)
		if otherEnv == "" {
			continue
		}
		other, sensitive := homeValue(reg.inst.Env, otherEnv)
		if other == "" || sensitive {
			continue
		}
		if theirs := accounthome.Resolve(userHome, other); theirs == mine || samePath(theirs, mine) {
			return fmt.Errorf("%s is already the config home of %s (%s); two accounts on one home share one login, so give this one a folder of its own", mine, reg.inst.DisplayName, reg.inst.ID)
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

// HomeConflict is a shared entry an account's home holds a real copy of
// instead of a link to the shared one. Nothing is overwritten: the copy
// stays until the user picks the shared one (UseSharedHomeEntry).
type HomeConflict struct {
	Entry  string `json:"entry"`
	Path   string `json:"path"`
	Shared string `json:"shared"`
}

// accountHome works out where an instance's configuration is shared from
// and into. The shared home is always the machine's own (~/.claude and so
// on), never the server process's environment: a server started from inside
// a Claude session must not re-link every account to that session's home.
// The account's home is its own home variable; with none set it runs in the
// ambient home and there is nothing to link (ok is false).
func (m *Manager) accountHome(inst provider.Instance, env map[string]string) (sh adapter.SharedHome, shared, account string, ok bool, err error) {
	ad := m.drivers[inst.Driver]
	sh, isShared := ad.(adapter.SharedHome)
	homeEnv := isolatingEnv(ad)
	if !isShared || homeEnv == "" {
		return nil, "", "", false, nil
	}
	serverHome, err := os.UserHomeDir()
	if err != nil {
		return nil, "", "", false, nil
	}
	raw := strings.TrimSpace(env[homeEnv])
	if raw == "" {
		return nil, "", "", false, nil
	}
	// A relative value is resolved by the harness against the session's
	// working directory, so there is no one home to prepare. Saved instances
	// are made absolute (absolutePaths); this is a hand edit.
	if !filepath.IsAbs(raw) {
		return nil, "", "", false, fmt.Errorf("%s=%q is not an absolute path; not sharing configuration into it", homeEnv, raw)
	}
	return sh, sh.DefaultHome(serverHome), filepath.Clean(raw), true, nil
}

// shareHome links the user's configuration from the machine's own config
// home into the instance's, and records what it could not link. Problems are
// logged, once each, and never stop the account from running: an account
// whose home could not be linked still has its login.
func (m *Manager) shareHome(inst provider.Instance, env map[string]string) {
	sh, shared, account, ok, err := m.accountHome(inst, env)
	if err != nil {
		m.warnHome(inst.ID, err)
	}
	if !ok {
		m.setHomeConflicts(inst.ID, nil)
		return
	}
	err = accounthome.Link(shared, account, sh.SharedEntries())
	var conflicts []HomeConflict
	for _, c := range accounthome.Conflicts(err) {
		conflicts = append(conflicts, HomeConflict{Entry: c.Entry, Path: c.Path, Shared: c.Shared})
	}
	m.setHomeConflicts(inst.ID, conflicts)
	if err != nil {
		m.warnHome(inst.ID, fmt.Errorf("sharing %s into %s: %w", shared, account, err))
	}
}

// setHomeConflicts records an instance's conflicts, telling clients when the
// picture changed.
func (m *Manager) setHomeConflicts(id string, conflicts []HomeConflict) {
	m.homeMu.Lock()
	prev := m.homeConflicts[id]
	if len(conflicts) == 0 {
		delete(m.homeConflicts, id)
	} else {
		m.homeConflicts[id] = conflicts
	}
	m.homeMu.Unlock()
	if !slices.Equal(prev, conflicts) {
		m.notifyHarnesses()
	}
}

func (m *Manager) homeConflictsOf(id string) []HomeConflict {
	m.homeMu.Lock()
	defer m.homeMu.Unlock()
	return slices.Clone(m.homeConflicts[id])
}

// UseSharedHomeEntry resolves one conflict: the account's own copy of entry
// is moved aside (never deleted) and the shared one linked in its place.
func (m *Manager) UseSharedHomeEntry(instanceID, entry string) error {
	reg, ok := m.lookup(instanceID)
	if !ok || reg.ad == nil {
		return fmt.Errorf("unknown provider instance %q", instanceID)
	}
	env, err := reg.inst.EnvOverlay(m.secretStore())
	if err != nil {
		return err
	}
	sh, shared, account, ok, err := m.accountHome(reg.inst, env)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s has no config home of its own to share into", reg.inst.DisplayName)
	}
	var found *accounthome.Entry
	for _, e := range sh.SharedEntries() {
		if e.Name == entry {
			found = &e
			break
		}
	}
	if found == nil {
		return fmt.Errorf("%q is not shared configuration for %s", entry, reg.inst.DisplayName)
	}
	if err := accounthome.UseShared(shared, account, *found); err != nil {
		return err
	}
	m.shareHome(reg.inst, env)
	m.notifyHarnesses()
	return nil
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
