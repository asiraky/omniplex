package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// The project home reaches Claude as an additional directory, which is also
// how its private skills do: Claude loads <dir>/.claude/skills from each.
func TestExtraDirsBecomeAdditionalDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in bridge is a shell script")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("HOME", dir)
	claude := filepath.Join(dir, "claude")
	bridge := filepath.Join(dir, "bridge")
	for path, body := range map[string]string{
		claude: "#!/bin/sh\necho 2.0.0\n",
		// The config blob is the bridge's last argument.
		bridge: "#!/bin/sh\nfor a; do :; done\nprintf '%s' \"$a\" > \"" + dir + "/config\"\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	home := filepath.Join(dir, "Omniplex", "bowerbird")
	a := &Adapter{ClaudePath: claude, bundledSidecar: bridge}
	s, err := a.CreateSession(context.Background(), &fakeHost{}, adapter.CreateOptions{
		ThreadID: "t", Cwd: dir, ExtraDirs: []string{home},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The stand-in exits once it has written the config; wait for that
	// rather than kill it first.
	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-s.Events():
		case <-deadline:
			t.Fatal("stand-in bridge never exited")
		}
	}
	_ = s.Close()

	raw, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg sidecarConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.AdditionalDirectories, []string{home}) {
		t.Fatalf("additionalDirectories = %v, want [%s]", cfg.AdditionalDirectories, home)
	}
}
