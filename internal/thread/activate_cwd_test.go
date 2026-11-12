package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// restoredThread persists an idle thread whose workspace is cwd, as the
// server would find it after a restart.
func restoredThread(t *testing.T, cwd string) (*store.Store, store.ThreadMeta) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	meta := store.ThreadMeta{ID: "restored", Cwd: cwd, Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "idle"}
	if err := st.CreateThread(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, meta.ID, proto.Emit(proto.ThreadCreated, proto.ThreadCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness})); err != nil {
		t.Fatal(err)
	}
	return st, meta
}

func TestActivateResumeRefusesDeletedWorkspace(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	st, meta := restoredThread(t, cwd)
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()
	ctx := context.Background()

	actor, err := mgr.View(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = actor.ActivateResume(ctx)
	if err == nil || !strings.Contains(err.Error(), cwd) {
		t.Fatalf("ActivateResume err = %v, want an error naming %s", err, cwd)
	}
	if _, err := mgr.Get(ctx, meta.ID); err == nil || !strings.Contains(err.Error(), cwd) {
		t.Fatalf("Get err = %v, want an error naming %s", err, cwd)
	}
	if fa.thread() != nil {
		t.Fatal("harness was started in a missing workspace")
	}
}

func TestActivateResumeRefusesWorkspaceReplacedByFile(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "worktree")
	if err := os.WriteFile(cwd, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, meta := restoredThread(t, cwd)
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	if _, err := mgr.Get(context.Background(), meta.ID); err == nil || !strings.Contains(err.Error(), cwd) {
		t.Fatalf("Get err = %v, want an error naming %s", err, cwd)
	}
	if fa.thread() != nil {
		t.Fatal("harness was started in a workspace that is not a directory")
	}
}

func TestActivateResumeStartsInExistingWorkspace(t *testing.T) {
	cwd := t.TempDir()
	st, meta := restoredThread(t, cwd)
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	if _, err := mgr.Get(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if fa.thread() == nil {
		t.Fatal("harness did not start in an existing workspace")
	}
}
