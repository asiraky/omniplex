package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SecretStore holds credential bytes on disk, one file per secret, keyed by
// instance id plus variable name. It is deliberately a directory rather than
// the OS keychain: omniplex runs headless over SSH, where a keychain is awkward or
// absent. The directory is mode 0700 and every file 0600.
//
// The config file records that a sensitive variable exists; this store holds
// its value. Nothing here is ever sent to a client.
type SecretStore struct {
	root string
}

// OpenSecretStore opens (creating if needed) ~/.omniplex/secrets.
// OMNIPLEX_SECRETS overrides the root, pairing with OMNIPLEX_CONFIG so an
// isolated dev server keeps its credentials beside its config.
func OpenSecretStore() (*SecretStore, error) {
	if root := os.Getenv("OMNIPLEX_SECRETS"); root != "" {
		return OpenSecretStoreAt(root)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return OpenSecretStoreAt(filepath.Join(home, ".omniplex", "secrets"))
}

// OpenSecretStoreAt opens a store rooted at an explicit directory; tests use it.
func OpenSecretStoreAt(root string) (*SecretStore, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	// An existing directory keeps whatever mode it was created with; tighten it.
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, err
	}
	return &SecretStore{root: root}, nil
}

// path validates both keys before letting them near the filesystem. Instance
// ids are already slugs; variable names are checked here because they come
// straight from config.
func (s *SecretStore) path(instanceID, name string) (string, error) {
	if !validID.MatchString(instanceID) {
		return "", fmt.Errorf("invalid instance id %q", instanceID)
	}
	if name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return "", fmt.Errorf("invalid secret name %q", name)
	}
	return filepath.Join(s.root, instanceID, name), nil
}

// Put stores one secret, replacing any previous value.
func (s *SecretStore) Put(instanceID, name, value string) error {
	path, err := s.path(instanceID, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Write-then-rename, so a crash cannot leave a truncated credential.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(value); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Get returns the stored value, and whether one exists.
func (s *SecretStore) Get(instanceID, name string) (string, bool) {
	path, err := s.path(instanceID, name)
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Delete removes one secret. Missing is not an error.
func (s *SecretStore) Delete(instanceID, name string) error {
	path, err := s.path(instanceID, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Purge removes every secret an instance holds. It is the delete-instance
// gesture: removing an instance without its secrets would leave credentials
// on disk with nothing referencing them.
func (s *SecretStore) Purge(instanceID string) error {
	if !validID.MatchString(instanceID) {
		return fmt.Errorf("invalid instance id %q", instanceID)
	}
	if err := os.RemoveAll(filepath.Join(s.root, instanceID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Sync removes secrets that the given instances no longer flag as sensitive —
// clearing the Sensitive flag is how a secret is deleted. Secrets belonging to
// instance ids not in the list are left alone: they may belong to a config
// another branch wrote, and destroying them would violate the round-trip rule.
func (s *SecretStore) Sync(instances []Instance) error {
	for _, inst := range instances {
		dir := filepath.Join(s.root, inst.ID)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		sensitive := map[string]bool{}
		for _, v := range inst.Env {
			if v.Sensitive {
				sensitive[v.Name] = true
			}
		}
		for _, e := range entries {
			if e.IsDir() || sensitive[e.Name()] {
				continue
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
