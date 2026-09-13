package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/asiraky/omniplex/internal/userconfig"
)

// This file is the write side of provider instances: the operations behind
// the provider-management UI. Instances persist in the user config's
// "providers" array; secrets go to the secret store at write time so a
// credential never rests in the config file, mirroring what LoadInstances
// does for hand-authored entries at startup.
//
// Every operation is a userconfig.Update — an atomic read-modify-write —
// and returns the full re-parsed instance list, which the caller installs
// into the running manager.

// Spec is a client-authored instance definition. A sensitive EnvVar with an
// empty Value means "keep the stored secret"; with a value it replaces it.
// The value of a sensitive variable never comes back out.
type Spec struct {
	ID          string   `json:"id"`
	Driver      string   `json:"driver"`
	DisplayName string   `json:"displayName"`
	Env         []EnvVar `json:"env"`
	Enabled     bool     `json:"enabled"`
}

func (s Spec) validate() error {
	if !validID.MatchString(s.ID) {
		return fmt.Errorf("instance id %q is not a slug", s.ID)
	}
	if strings.TrimSpace(s.Driver) == "" {
		return fmt.Errorf("instance %q has no driver", s.ID)
	}
	for _, v := range s.Env {
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("instance %q has an environment variable with no name", s.ID)
		}
	}
	return nil
}

// entryFor builds the config entry for a spec, merging over the previous raw
// entry when one exists so driver-specific keys this build does not know
// survive an edit. Sensitive values are stored as secrets and blanked.
func entryFor(spec Spec, prev json.RawMessage, secrets *SecretStore) (json.RawMessage, error) {
	generic := map[string]json.RawMessage{}
	if len(prev) > 0 {
		if err := json.Unmarshal(prev, &generic); err != nil {
			return nil, err
		}
	}
	env := make([]EnvVar, 0, len(spec.Env))
	for _, v := range spec.Env {
		if v.Sensitive && v.Value != "" {
			if secrets == nil {
				return nil, fmt.Errorf("instance %q: %s is sensitive but no secret store is available", spec.ID, v.Name)
			}
			if err := secrets.Put(spec.ID, v.Name, v.Value); err != nil {
				return nil, fmt.Errorf("store secret %s/%s: %w", spec.ID, v.Name, err)
			}
			v.Value = ""
		}
		env = append(env, v)
	}
	set := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		generic[key] = b
		return nil
	}
	if err := set("id", spec.ID); err != nil {
		return nil, err
	}
	if err := set("driver", spec.Driver); err != nil {
		return nil, err
	}
	if err := set("displayName", spec.DisplayName); err != nil {
		return nil, err
	}
	if err := set("env", env); err != nil {
		return nil, err
	}
	if err := set("enabled", spec.Enabled); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

// indexOf finds the config entry with the given instance id, or -1. Entries
// that do not parse are skipped, matching LoadInstances' tolerance.
func indexOf(entries []json.RawMessage, id string) int {
	for i, entry := range entries {
		if inst, err := Parse(entry); err == nil && inst.ID == id {
			return i
		}
	}
	return -1
}

// AddInstance appends a new instance to the config. The id must not already
// be configured; overriding a driver's implicit default instance is done by
// adding an entry whose id equals the driver id.
func AddInstance(spec Spec, secrets *SecretStore, logf func(string, ...any)) ([]Instance, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	var instances []Instance
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		if indexOf(cfg.Providers, spec.ID) >= 0 {
			return fmt.Errorf("provider instance %q already exists", spec.ID)
		}
		entry, err := entryFor(spec, nil, secrets)
		if err != nil {
			return err
		}
		cfg.Providers = append(cfg.Providers, entry)
		instances, _, _, err = LoadInstances(cfg.Providers, secrets, logf)
		return err
	})
	if err != nil {
		return nil, err
	}
	return instances, nil
}

// SaveInstance replaces an existing instance's entry. Driver and id are
// immutable — sessions route on both — so a save that changes the driver is
// refused rather than silently rebinding old sessions.
func SaveInstance(spec Spec, secrets *SecretStore, logf func(string, ...any)) ([]Instance, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	var instances []Instance
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		i := indexOf(cfg.Providers, spec.ID)
		if i < 0 {
			return fmt.Errorf("unknown provider instance %q", spec.ID)
		}
		prev, err := Parse(cfg.Providers[i])
		if err != nil {
			return err
		}
		if prev.Driver != spec.Driver {
			return fmt.Errorf("provider instance %q runs driver %q; it cannot become %q", spec.ID, prev.Driver, spec.Driver)
		}
		entry, err := entryFor(spec, cfg.Providers[i], secrets)
		if err != nil {
			return err
		}
		cfg.Providers[i] = entry
		instances, _, _, err = LoadInstances(cfg.Providers, secrets, logf)
		if err != nil {
			return err
		}
		// A variable no longer marked sensitive gives up its stored secret.
		if secrets != nil {
			if err := secrets.Sync(instances); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return instances, nil
}

// DeleteInstance removes an instance and purges its secrets. Deleting the
// entry that overrides a driver's default instance reverts that driver to
// ambient credentials; it does not remove the driver.
func DeleteInstance(id string, secrets *SecretStore, logf func(string, ...any)) ([]Instance, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("instance id %q is not a slug", id)
	}
	var instances []Instance
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		i := indexOf(cfg.Providers, id)
		if i < 0 {
			return fmt.Errorf("unknown provider instance %q", id)
		}
		cfg.Providers = append(cfg.Providers[:i], cfg.Providers[i+1:]...)
		var err error
		instances, _, _, err = LoadInstances(cfg.Providers, secrets, logf)
		return err
	})
	if err != nil {
		return nil, err
	}
	if secrets != nil {
		if err := secrets.Purge(id); err != nil {
			return nil, err
		}
	}
	return instances, nil
}
