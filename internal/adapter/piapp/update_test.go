package piapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRunsPiSelfUpdateWithItsOwnNode(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "version")
	if err := os.WriteFile(state, []byte("0.84.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "pi")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then cat '" + state + "'; exit 0; fi\n" +
		"if [ \"$1\" = update ]; then printf '%s\\n%s' \"$PATH\" \"$PI_CODING_AGENT_DIR\" > '" + dir + "/seen'; printf 0.85.0 > '" + state + "'; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := New(bin).Update(context.Background(), map[string]string{"PI_CODING_AGENT_DIR": "/accounts/two"})
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "0.84.4" || got.To != "0.85.0" {
		t.Errorf("result = %+v; want 0.84.4 -> 0.85.0", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "seen"))
	seen := strings.Split(string(raw), "\n")
	if len(seen) != 2 {
		t.Fatalf("update did not record what it saw: %q", raw)
	}
	if !strings.HasPrefix(seen[0], dir+string(os.PathListSeparator)) {
		t.Errorf("PATH = %q; want pi's own directory first, for its node and npm", seen[0])
	}
	if seen[1] != "/accounts/two" {
		t.Errorf("PI_CODING_AGENT_DIR = %q; want the instance's", seen[1])
	}
}

func TestUpdateWithoutPi(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "pi")).Update(context.Background(), nil); err == nil {
		t.Error("updating a missing CLI reported success")
	}
}
