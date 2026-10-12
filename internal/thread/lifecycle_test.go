package thread

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

func TestHookDrainsOutputWithoutWaitingForBackgroundChildren(t *testing.T) {
	a, _, _ := newTestActor(t)
	root := t.TempDir()
	hook := filepath.Join(root, "hook.sh")
	script := "#!/bin/sh\nsleep 30 &\necho $! > \"$OMNIPLEX_PROJECT_ROOT/child.pid\"\necho final-stdout\necho final-stderr >&2\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		b, err := os.ReadFile(filepath.Join(root, "child.pid"))
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				if child, err := os.FindProcess(pid); err == nil {
					_ = child.Kill()
				}
			}
		}
	})
	ctx := context.Background()
	meta := store.ThreadMeta{ID: a.ID}
	if err := (&Manager{}).runHook(ctx, a, meta, root, hook, nil, "", "deprovision", "", "", root, 3); err != nil {
		t.Fatalf("successful hook waited for its background child: %v", err)
	}
	state, err := a.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"final-stdout\n", "final-stderr\n"} {
		if !strings.Contains(state.Workspace.Output, output) {
			t.Fatalf("missing %q in hook output %q", output, state.Workspace.Output)
		}
	}
}
