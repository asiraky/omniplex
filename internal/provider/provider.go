// Package provider models one configured account for one adapter. An adapter
// is a singleton in code; a provider instance pairs it with credentials, so
// the same harness can run under several accounts. The default instance for an
// adapter uses the ambient environment, which is the single-account case and
// needs no configuration.
//
// Instances are declared in the user config file (~/.omniplex/config.json) under
// "providers". Each entry is held raw and written back verbatim, so a config
// authored on a branch that knows a driver this build does not still loads,
// presents as unavailable, and loses nothing on rewrite.
package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// EnvVar is one variable in an instance's credential overlay. A variable
// marked Sensitive never carries its value in the config file: the value lives
// in the secret store and is materialised at spawn time.
type EnvVar struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

// Instance is one configured account for one adapter.
//
// ID is the routing key: threads, the wire protocol, and the secret store
// reference instance ids, never driver kinds. Driver selects which adapter
// implementation serves the instance; it is an open slug, not a closed enum,
// because a persisted config may name a driver this build has never heard of.
type Instance struct {
	ID          string
	Driver      string
	DisplayName string
	Env         []EnvVar
	Enabled     bool

	// Raw is the config entry exactly as it appeared on disk, including any
	// driver-specific settings this build does not understand. It is what gets
	// written back, so rolling between branches never destroys configuration.
	Raw json.RawMessage
}

// Default synthesises the implicit instance for a registered adapter: same id
// as the driver, ambient environment, enabled. This is why today's behaviour
// is a special case of instances rather than a separate mode.
func Default(driver, displayName string) Instance {
	return Instance{ID: driver, Driver: driver, DisplayName: displayName, Enabled: true}
}

// validID keeps instance ids usable as secret-store directory names and wire
// keys. It deliberately matches the shape of adapter ids.
var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Parse reads one raw config entry. An unknown driver is not an error — the
// caller cannot know which drivers a build has — but a missing or unusable id
// is, because nothing could ever route to the instance.
func Parse(raw json.RawMessage) (Instance, error) {
	var aux struct {
		ID          string   `json:"id"`
		Driver      string   `json:"driver"`
		DisplayName string   `json:"displayName"`
		Env         []EnvVar `json:"env"`
		Enabled     *bool    `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return Instance{}, fmt.Errorf("parse provider instance: %w", err)
	}
	if !validID.MatchString(aux.ID) {
		return Instance{}, fmt.Errorf("provider instance id %q is missing or not a slug", aux.ID)
	}
	if strings.TrimSpace(aux.Driver) == "" {
		return Instance{}, fmt.Errorf("provider instance %q has no driver", aux.ID)
	}
	inst := Instance{
		ID:          aux.ID,
		Driver:      aux.Driver,
		DisplayName: aux.DisplayName,
		Env:         aux.Env,
		Enabled:     aux.Enabled == nil || *aux.Enabled,
		Raw:         raw,
	}
	if inst.DisplayName == "" {
		inst.DisplayName = inst.ID
	}
	return inst, nil
}

// LoadInstances parses every raw provider entry from the user config.
//
// A sensitive variable that still carries a literal value — the way an
// operator provisions an instance by hand, since there is no management UI —
// is swept into the secret store and blanked in the returned raw entries.
// `changed` reports whether the caller must persist the rewritten entries.
// An entry that does not parse is reported through logf and kept verbatim, so
// a malformed instance never fails startup or loses its configuration.
func LoadInstances(raw []json.RawMessage, secrets *SecretStore, logf func(string, ...any)) (instances []Instance, rewritten []json.RawMessage, changed bool, err error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	rewritten = make([]json.RawMessage, 0, len(raw))
	for _, entry := range raw {
		inst, parseErr := Parse(entry)
		if parseErr != nil {
			logf("provider config: %v (entry kept as written)", parseErr)
			rewritten = append(rewritten, entry)
			continue
		}
		swept, entryChanged, sweepErr := sweepSecrets(&inst, secrets)
		if sweepErr != nil {
			return nil, nil, false, sweepErr
		}
		if entryChanged {
			changed = true
			inst.Raw = swept
		}
		rewritten = append(rewritten, inst.Raw)
		instances = append(instances, inst)
	}
	return instances, rewritten, changed, nil
}

// sweepSecrets moves literal values of sensitive variables into the secret
// store and blanks them in the raw entry. The rewrite goes through a generic
// map so keys this build does not know survive verbatim.
func sweepSecrets(inst *Instance, secrets *SecretStore) (json.RawMessage, bool, error) {
	needs := false
	for _, v := range inst.Env {
		if v.Sensitive && v.Value != "" {
			needs = true
		}
	}
	if !needs || secrets == nil {
		return inst.Raw, false, nil
	}

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(inst.Raw, &generic); err != nil {
		return nil, false, err
	}
	var env []map[string]json.RawMessage
	if err := json.Unmarshal(generic["env"], &env); err != nil {
		return nil, false, err
	}
	for i := range inst.Env {
		v := &inst.Env[i]
		if !v.Sensitive || v.Value == "" {
			continue
		}
		if err := secrets.Put(inst.ID, v.Name, v.Value); err != nil {
			return nil, false, fmt.Errorf("store secret %s/%s: %w", inst.ID, v.Name, err)
		}
		v.Value = ""
		if i < len(env) {
			delete(env[i], "value")
		}
	}
	envRaw, err := json.Marshal(env)
	if err != nil {
		return nil, false, err
	}
	generic["env"] = envRaw
	raw, err := json.Marshal(generic)
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// EnvOverlay materialises the instance's environment: plain values from the
// config, sensitive values from the secret store. A sensitive variable whose
// secret is missing is an error, and the caller must fail closed: omitting it
// would let the spawn fall through to the ambient credential, silently running
// this instance's thread under a different account.
func (i Instance) EnvOverlay(secrets *SecretStore) (map[string]string, error) {
	if len(i.Env) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(i.Env))
	for _, v := range i.Env {
		if v.Name == "" {
			continue
		}
		if v.Sensitive {
			if secrets == nil {
				return nil, fmt.Errorf("instance %q: %s is sensitive but no secret store is available", i.ID, v.Name)
			}
			value, ok := secrets.Get(i.ID, v.Name)
			if !ok {
				return nil, fmt.Errorf("instance %q: no stored secret for %s", i.ID, v.Name)
			}
			out[v.Name] = value
			continue
		}
		out[v.Name] = v.Value
	}
	return out, nil
}
