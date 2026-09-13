package session

import "fmt"

import "github.com/asiraky/omniplex/internal/adapter"

// ModelSettings returns one instance's per-model harness settings — the values
// a user would otherwise hand-edit into the harness's own config file — keyed
// by model id. An instance whose driver has no such setting answers with an
// empty map rather than an error: the settings surface is optional, and a UI
// asking about it is not doing anything wrong.
func (m *Manager) ModelSettings(instanceID string) (map[string]string, error) {
	reg, ok := m.lookup(instanceID)
	if !ok || reg.ad == nil {
		return nil, fmt.Errorf("unknown provider instance %q", instanceID)
	}
	ms, ok := reg.ad.(adapter.ModelSettings)
	if !ok {
		return map[string]string{}, nil
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return nil, err
	}
	values, err := ms.ModelSettings(env)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]string{}
	}
	return values, nil
}

// SetModelSetting stores one model's setting for an instance, or clears it
// when value is empty. The adapter owns the file and the schema; the manager
// only supplies the instance's environment, which is what decides *which*
// harness directory gets written — the whole point of per-instance isolation.
func (m *Manager) SetModelSetting(instanceID, modelID, value string) error {
	reg, ok := m.lookup(instanceID)
	if !ok || reg.ad == nil {
		return fmt.Errorf("unknown provider instance %q", instanceID)
	}
	ms, ok := reg.ad.(adapter.ModelSettings)
	if !ok {
		return fmt.Errorf("%s has no per-model settings", reg.inst.DisplayName)
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return err
	}
	return ms.SetModelSetting(env, modelID, value)
}
