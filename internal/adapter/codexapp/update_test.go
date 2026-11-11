package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateRunsCodexUpdateUnderTheInstanceEnv(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "version")
	if err := os.WriteFile(state, []byte("0.162.1"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "codex")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then echo \"codex-cli $(cat '" + state + "')\"; exit 0; fi\n" +
		"if [ \"$1\" = update ]; then printf '%s' \"$CODEX_HOME\" > '" + dir + "/home'; printf 0.163.0 > '" + state + "'; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := New(bin).Update(context.Background(), map[string]string{"CODEX_HOME": "/accounts/two"})
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "0.162.1" || got.To != "0.163.0" {
		t.Errorf("result = %+v; want 0.162.1 -> 0.163.0", got)
	}
	if seen, _ := os.ReadFile(filepath.Join(dir, "home")); string(seen) != "/accounts/two" {
		t.Errorf("update ran with CODEX_HOME=%q; want the instance's", seen)
	}
}

func TestUpdateWithoutCodex(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "codex")).Update(context.Background(), nil); err == nil {
		t.Error("updating a missing CLI reported success")
	}
}
