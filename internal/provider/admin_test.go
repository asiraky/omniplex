package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/userconfig"
)

func adminEnv(t *testing.T) *SecretStore {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(dir, "config.json"))
	secrets, err := OpenSecretStoreAt(filepath.Join(dir, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	return secrets
}

func discard(string, ...any) {}

func TestAddSaveDeleteRoundTrip(t *testing.T) {
	secrets := adminEnv(t)

	spec := Spec{
		ID: "codex-work", Driver: "codex", DisplayName: "Work", Enabled: true,
		Env: []EnvVar{{Name: "CODEX_HOME", Value: "/home/x/codex-work"}},
	}
	instances, err := AddInstance(spec, secrets, discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != "codex-work" || instances[0].Env[0].Value != "/home/x/codex-work" {
		t.Fatalf("add round-trip = %+v", instances)
	}

	// Adding the same id again must refuse, not duplicate.
	if _, err := AddInstance(spec, secrets, discard); err == nil {
		t.Fatal("duplicate add must fail")
	}

	spec.DisplayName = "Work account"
	instances, err = SaveInstance(spec, secrets, discard)
	if err != nil {
		t.Fatal(err)
	}
	if instances[0].DisplayName != "Work account" {
		t.Fatalf("save did not stick: %+v", instances[0])
	}

	instances, err = DeleteInstance("codex-work", secrets, discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Fatalf("delete left %+v", instances)
	}
}

func TestSaveRefusesDriverChange(t *testing.T) {
	secrets := adminEnv(t)
	spec := Spec{ID: "acct", Driver: "codex", DisplayName: "A", Enabled: true}
	if _, err := AddInstance(spec, secrets, discard); err != nil {
		t.Fatal(err)
	}
	spec.Driver = "claude"
	if _, err := SaveInstance(spec, secrets, discard); err == nil {
		t.Fatal("driver is immutable; save must refuse")
	}
}

func TestSaveUnknownInstanceFails(t *testing.T) {
	secrets := adminEnv(t)
	if _, err := SaveInstance(Spec{ID: "ghost", Driver: "codex", Enabled: true}, secrets, discard); err == nil {
		t.Fatal("saving a non-existent instance must fail")
	}
}

func TestSensitiveValueSweptToSecretStoreAndNeverInConfig(t *testing.T) {
	secrets := adminEnv(t)
	spec := Spec{
		ID: "pi-main", Driver: "pi", DisplayName: "Pi", Enabled: true,
		Env: []EnvVar{{Name: "OPENROUTER_API_KEY", Value: "sk-or-secret", Sensitive: true}},
	}
	instances, err := AddInstance(spec, secrets, discard)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := secrets.Get("pi-main", "OPENROUTER_API_KEY"); !ok || v != "sk-or-secret" {
		t.Fatalf("secret not stored: %q %v", v, ok)
	}
	// The config file on disk must not contain the value.
	path, _ := userconfig.Path()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sk-or-secret") {
		t.Fatalf("secret rested in the config file: %s", b)
	}
	// The overlay materialises it back at spawn time.
	env, err := instances[0].EnvOverlay(secrets)
	if err != nil {
		t.Fatal(err)
	}
	if env["OPENROUTER_API_KEY"] != "sk-or-secret" {
		t.Fatalf("overlay = %v", env)
	}
}

func TestEmptySensitiveValueKeepsStoredSecret(t *testing.T) {
	secrets := adminEnv(t)
	spec := Spec{
		ID: "pi-main", Driver: "pi", DisplayName: "Pi", Enabled: true,
		Env: []EnvVar{{Name: "KEY", Value: "v1", Sensitive: true}},
	}
	if _, err := AddInstance(spec, secrets, discard); err != nil {
		t.Fatal(err)
	}
	// A save with the value blank — how a client says "unchanged" — must keep v1.
	spec.Env = []EnvVar{{Name: "KEY", Sensitive: true}}
	if _, err := SaveInstance(spec, secrets, discard); err != nil {
		t.Fatal(err)
	}
	if v, _ := secrets.Get("pi-main", "KEY"); v != "v1" {
		t.Fatalf("blank sensitive value must keep the stored secret, got %q", v)
	}
	// Clearing the Sensitive flag is the delete gesture; Sync removes it.
	spec.Env = nil
	if _, err := SaveInstance(spec, secrets, discard); err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets.Get("pi-main", "KEY"); ok {
		t.Fatal("dropping the sensitive var must remove the stored secret")
	}
}

func TestDeletePurgesSecrets(t *testing.T) {
	secrets := adminEnv(t)
	spec := Spec{
		ID: "pi-main", Driver: "pi", DisplayName: "Pi", Enabled: true,
		Env: []EnvVar{{Name: "KEY", Value: "v1", Sensitive: true}},
	}
	if _, err := AddInstance(spec, secrets, discard); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteInstance("pi-main", secrets, discard); err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets.Get("pi-main", "KEY"); ok {
		t.Fatal("deleting the instance must purge its secrets")
	}
}

func TestUnknownConfigKeysSurviveEdit(t *testing.T) {
	secrets := adminEnv(t)
	// A hand-authored (or future-build) entry with a key this build has never
	// heard of. Editing through the UI must not destroy it.
	if _, err := userconfig.Update(func(cfg *userconfig.Config) error {
		cfg.Providers = append(cfg.Providers, json.RawMessage(
			`{"id":"pi-main","driver":"pi","displayName":"Pi","enabled":true,"env":[],"futureKnob":42}`))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveInstance(Spec{ID: "pi-main", Driver: "pi", DisplayName: "Renamed", Enabled: true}, secrets, discard); err != nil {
		t.Fatal(err)
	}
	cfg, err := userconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(cfg.Providers[0], &entry); err != nil {
		t.Fatal(err)
	}
	if string(entry["futureKnob"]) != "42" {
		t.Fatalf("unknown key destroyed by save: %v", entry)
	}
	var name string
	_ = json.Unmarshal(entry["displayName"], &name)
	if name != "Renamed" {
		t.Fatalf("save did not apply: %v", entry)
	}
}

func TestInvalidSpecRejected(t *testing.T) {
	secrets := adminEnv(t)
	for _, spec := range []Spec{
		{ID: "Bad ID!", Driver: "codex"},
		{ID: "ok", Driver: "  "},
		{ID: "ok", Driver: "codex", Env: []EnvVar{{Name: " "}}},
	} {
		if _, err := AddInstance(spec, secrets, discard); err == nil {
			t.Errorf("spec %+v must be rejected", spec)
		}
	}
	if _, err := DeleteInstance("../etc", secrets, discard); err == nil {
		t.Error("path-shaped id must be rejected")
	}
}
