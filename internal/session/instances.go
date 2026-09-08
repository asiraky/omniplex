package session

import (
	"fmt"
	"os"
	"path/filepath"

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
	instances, err := provider.AddInstance(spec, m.secretStore(), m.logf)
	if err != nil {
		return err
	}
	m.applyInstances(instances)
	return nil
}

// SaveProviderInstance updates a configured instance in place. Editing the
// implicit default instance works by creating a config entry with the
// driver's id, which Add covers; Save requires the entry to exist.
func (m *Manager) SaveProviderInstance(spec provider.Spec) error {
	if ad, ok := m.drivers[spec.Driver]; ok {
		spec = enforceSensitive(spec, ad)
	}
	instances, err := provider.SaveInstance(spec, m.secretStore(), m.logf)
	if err != nil {
		return err
	}
	m.applyInstances(instances)
	m.forgetInstance(spec.ID)
	return nil
}

// DeleteProviderInstance removes a configured instance and its secrets.
// Sessions that ran on it keep their history; resuming one reports the
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
// overwrite each other's credentials. The default instance never passes
// through here and keeps ambient behaviour.
func defaultIsolation(spec provider.Spec, ad adapter.Adapter) provider.Spec {
	cfg, ok := ad.(adapter.Configurer)
	if !ok || spec.ID == ad.ID() {
		return spec
	}
	for _, field := range cfg.ConfigFields() {
		if !field.Isolates {
			continue
		}
		for _, v := range spec.Env {
			if v.Name == field.Env {
				return spec // explicitly set; respect it
			}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return spec
		}
		spec.Env = append(spec.Env, provider.EnvVar{
			Name:  field.Env,
			Value: filepath.Join(home, ".omniplex", "instances", spec.ID),
		})
		return spec
	}
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
