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
	for _, seconds := range []int{3, 1} {
		t.Run(strconv.Itoa(seconds), func(t *testing.T) {
			testHookWithBackgroundChild(t, seconds)
		})
	}
}

func testHookWithBackgroundChild(t *testing.T, seconds int) {
	t.Helper()
	a, _, _ := newTestActor(t)
	root := t.TempDir()
	hook := filepath.Join(root, "hook.sh")
	// With a one-second execution budget, the output drain crosses that
	// deadline even though the hook itself exits successfully after 0.1s.
	script := "#!/bin/sh\nsleep 30 &\necho $! > \"$OMNIPLEX_PROJECT_ROOT/child.pid\"\nsleep 0.1\necho final-stdout\necho final-stderr >&2\n"
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
	if err := (&Manager{}).runHook(ctx, a, meta, root, hook, nil, "", "deprovision", "", "", root, seconds); err != nil {
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
