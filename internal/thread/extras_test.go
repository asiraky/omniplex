package thread

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
)

// A session is asked for prompt suggestions unless the user turned them off,
// and an unreadable config is no reason to lose them.
func TestSessionsSuggestPromptsUnlessTurnedOff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string // "" writes no config at all
		want   bool
	}{
		{"no config", "", true},
		{"unset", `{"version":1}`, true},
		{"on", `{"version":1,"suggestPrompts":true}`, true},
		{"off", `{"version":1,"suggestPrompts":false}`, false},
		{"unreadable", `{not json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if tc.config != "" {
				if err := os.WriteFile(path, []byte(tc.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("OMNIPLEX_CONFIG", path)
			cwd := t.TempDir()
			meta := store.ThreadMeta{ID: "loose", Cwd: cwd, Harness: "fake"}
			o := harnessExtras(context.Background(), nil, nil, meta, cwd, t.Logf).options(adapter.CreateOptions{})
			if o.SuggestPrompts != tc.want {
				t.Fatalf("SuggestPrompts = %v, want %v", o.SuggestPrompts, tc.want)
			}
		})
	}
}
