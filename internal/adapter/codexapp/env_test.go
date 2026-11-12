package codexapp

import (
	"strings"
	"testing"
)

// An API key exported in the server's environment would sign every account in
// as the same login; only the account's own overlay may give Codex one.
func TestCodexEnvKeepsServerAPIKeysOut(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-server")
	t.Setenv("CODEX_API_KEY", "sk-server-codex")

	has := func(env []string, key string) (string, bool) {
		for _, kv := range env {
			if k, v, _ := strings.Cut(kv, "="); k == key {
				return v, true
			}
		}
		return "", false
	}
	env := codexEnv(map[string]string{codexHomeEnv: "/accounts/work"})
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_API_KEY"} {
		if v, ok := has(env, k); ok {
			t.Errorf("%s=%q reached Codex from the server", k, v)
		}
	}
	env = codexEnv(map[string]string{"OPENAI_API_KEY": "sk-account"})
	if v, _ := has(env, "OPENAI_API_KEY"); v != "sk-account" {
		t.Errorf("OPENAI_API_KEY = %q, want the account's", v)
	}
}
