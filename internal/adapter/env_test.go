package adapter

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// A server started inside a harness session, or from a service environment
// that exports a credential, inherits a home and a login that are nobody's
// account; no harness process may get them, and an account's own values
// always reach it.
func TestHarnessEnvNeverPassesTheServersHomeOrCredentials(t *testing.T) {
	t.Setenv("CODEX_HOME", "/terminal/.codex")
	t.Setenv("OPENAI_API_KEY", "sk-server")
	t.Setenv("OMNIPLEX_TEST_KEPT", "yes")

	got := envMap(HarnessEnv(nil, "CODEX_HOME", "OPENAI_API_KEY"))
	for _, k := range []string{"CODEX_HOME", "OPENAI_API_KEY"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s=%q reached the harness from the server", k, v)
		}
	}
	if got["OMNIPLEX_TEST_KEPT"] != "yes" {
		t.Error("the rest of the server's environment was dropped")
	}

	got = envMap(HarnessEnv(map[string]string{"CODEX_HOME": "/accounts/work", "OPENAI_API_KEY": "sk-account"}, "CODEX_HOME", "OPENAI_API_KEY"))
	if got["CODEX_HOME"] != "/accounts/work" || got["OPENAI_API_KEY"] != "sk-account" {
		t.Errorf("CODEX_HOME = %q, OPENAI_API_KEY = %q, want the account's", got["CODEX_HOME"], got["OPENAI_API_KEY"])
	}
}
