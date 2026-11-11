package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateRunsClaudeUpdateUnderTheInstanceEnv(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "version")
	if err := os.WriteFile(state, []byte("2.1.300"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then echo \"$(cat '" + state + "') (Claude Code)\"; exit 0; fi\n" +
		"if [ \"$1\" = update ]; then printf '%s' \"$CLAUDE_CONFIG_DIR\" > '" + dir + "/config'; printf 2.1.301 > '" + state + "'; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := (&Adapter{ClaudePath: bin}).Update(context.Background(), map[string]string{"CLAUDE_CONFIG_DIR": "/accounts/two"})
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "2.1.300" || got.To != "2.1.301" {
		t.Errorf("result = %+v; want 2.1.300 -> 2.1.301", got)
	}
	if seen, _ := os.ReadFile(filepath.Join(dir, "config")); string(seen) != "/accounts/two" {
		t.Errorf("update ran with CLAUDE_CONFIG_DIR=%q; want the instance's", seen)
	}
}

func TestUpdateWithoutClaude(t *testing.T) {
	a := &Adapter{ClaudePath: filepath.Join(t.TempDir(), "missing")}
	if _, err := a.Update(context.Background(), nil); err == nil {
		t.Error("updating a missing install reported success")
	}
}
