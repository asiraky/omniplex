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

// A server started inside a harness session inherits that session's home;
// no harness process may get it, and an account's own home always reaches it.
func TestHarnessEnvNeverPassesTheServersHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/terminal/.codex")
	t.Setenv("OMNIPLEX_TEST_KEPT", "yes")

	got := envMap(HarnessEnv("CODEX_HOME", nil))
	if v, ok := got["CODEX_HOME"]; ok {
		t.Errorf("CODEX_HOME=%q reached the harness from the server", v)
	}
	if got["OMNIPLEX_TEST_KEPT"] != "yes" {
		t.Error("the rest of the server's environment was dropped")
	}

	got = envMap(HarnessEnv("CODEX_HOME", map[string]string{"CODEX_HOME": "/accounts/work"}))
	if got["CODEX_HOME"] != "/accounts/work" {
		t.Errorf("CODEX_HOME = %q, want the account's", got["CODEX_HOME"])
	}
}
