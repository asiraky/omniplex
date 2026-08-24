package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// The config held against a project is a cache of the file in the repo, so a
// pull that changes .omniplex/project.json has to take effect without the operator
// removing and re-adding the project.
func TestReloadProjectsTakesTheFileOverTheCache(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if err := os.MkdirAll(filepath.Join(root, ".omniplex"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := project.DefaultConfig(root)
	cfg.Workspace.Provision = "scripts/omniplex-provision.mjs"
	cfg.Defaults.BaseBranch = "main"
	if err := os.WriteFile(filepath.Join(root, project.ConfigPath), mustJSON(t, cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.ReloadProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Project(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Workspace.Provision != "scripts/omniplex-provision.mjs" {
		t.Fatalf("provision hook is %q, want the one from the file", got.Config.Workspace.Provision)
	}
	if got.Config.Defaults.BaseBranch != "main" {
		t.Fatalf("base branch is %q, want main", got.Config.Defaults.BaseBranch)
	}
}

// A missing file means the checkout moved or is mid-checkout, not that the
// project's settings were cleared.
func TestReloadProjectsLeavesAProjectWithNoFileAlone(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	before, err := st.Project(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	before.Config.Workspace.Provision = "scripts/set-by-hand"
	if err := st.PutProject(context.Background(), before); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ReloadProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := st.Project(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Config.Workspace.Provision != "scripts/set-by-hand" {
		t.Fatalf("provision hook became %q; a missing file must not clear settings", after.Config.Workspace.Provision)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The whole point of the feature: a project added with the wrong path is a
// mistake with nothing behind it, and the user must be able to take it back.
func TestDeleteProjectRemovesItFromTheRegistry(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	projects, err := mgr.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("project list still has %d entries, want none", len(projects))
	}
	if _, err := st.Project(context.Background(), p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reading the deleted project gave %v, want ErrNotFound", err)
	}
}

// Deleting a project is a registry edit. The checkout it points at is the
// user's own directory, and its config file is what makes re-adding it
// restore everything — neither is omniplex's to remove.
func TestDeleteProjectLeavesTheCheckoutAlone(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".omniplex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, project.ConfigPath), mustJSON(t, project.DefaultConfig(root)), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Fatalf("the checkout is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, project.ConfigPath)); err != nil {
		t.Fatalf("the project config is gone: %v", err)
	}
}

// Sessions have transcripts and worktrees behind them. Tidying the project
// list must not take them with it, so a project that still owns one is
// refused and says why.
func TestDeleteProjectRefusesWhileSessionsRemain(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	meta := store.SessionMeta{ID: "s1", Cwd: root, Harness: "fake", ProjectID: p.ID, Phase: "idle", CreatedAt: proto.NowMillis(), UpdatedAt: proto.NowMillis()}
	if err := st.CreateSession(context.Background(), meta); err != nil {
		t.Fatal(err)
	}

	err := mgr.DeleteProject(context.Background(), p.ID)
	if !errors.Is(err, store.ErrProjectInUse) {
		t.Fatalf("delete gave %v, want ErrProjectInUse", err)
	}
	if !strings.Contains(err.Error(), "1 session") {
		t.Fatalf("the refusal does not say how many sessions are in the way: %v", err)
	}
	if _, err := st.Project(context.Background(), p.ID); err != nil {
		t.Fatalf("the refused project was removed anyway: %v", err)
	}

	// Once the session goes, the project can too.
	if err := st.DeleteSession(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatalf("delete still refused after the session went: %v", err)
	}
}

// Deleting the same project twice is the phone reconnecting, not a new
// intent: it must not read as success and leave the client thinking the
// second one did something.
func TestDeleteProjectReportsAnUnknownID(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, _ := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if err := mgr.DeleteProject(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting an unknown project gave %v, want ErrNotFound", err)
	}
}
