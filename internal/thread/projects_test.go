package thread

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

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type harnessNamedAdapter struct {
	fakeAdapter
	id string
}

func (a *harnessNamedAdapter) ID() string { return a.id }

func TestProjectHarnessDefaultsDoNotCrossToAnotherHarness(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	p.Defaults.Harness = "claude"
	p.Defaults.Harnesses = map[string]project.HarnessDefaults{
		"claude": {Model: "opus", Mode: "bypassPermissions", Effort: "high"},
		"codex":  {Model: "gpt-5.6-sol", Mode: "full-access", Effort: "xhigh"},
	}
	p.Defaults.Workspace = "local"
	if err := putProject(context.Background(), st, p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {},
		&harnessNamedAdapter{id: "claude"},
		&harnessNamedAdapter{id: "codex"},
	)
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID,
		Harness:   "codex",
		Workspace: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, err := st.Thread(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Model != "gpt-5.6-sol" || meta.Mode != "full-access" || meta.Effort != "xhigh" {
		t.Fatalf("Codex defaults not restored: model=%q mode=%q effort=%q", meta.Model, meta.Mode, meta.Effort)
	}
}

func TestExplicitHarnessDefaultsAreNotReplacedByProjectProfile(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	p.Defaults.Harness = "codex"
	p.Defaults.Harnesses = map[string]project.HarnessDefaults{
		"codex": {Model: "stale-model", Mode: "stale-mode", Effort: "stale-effort"},
	}
	p.Defaults.Workspace = "local"
	if err := putProject(context.Background(), st, p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &harnessNamedAdapter{id: "codex"})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Harness: "codex", Workspace: "local", AgentSettingsExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, err := st.Thread(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Model != "" || meta.Mode != "" || meta.Effort != "" {
		t.Fatalf("explicit harness defaults were replaced: model=%q mode=%q effort=%q", meta.Model, meta.Mode, meta.Effort)
	}
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
// user's own directory, and not omniplex's to remove.
func TestDeleteProjectLeavesTheCheckoutAlone(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Fatalf("the checkout is gone: %v", err)
	}
}

// Threads have transcripts and worktrees behind them. Tidying the project
// list must not take them with it, so a project that still owns one is
// refused and says why.
func TestDeleteProjectRefusesWhileThreadsRemain(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	meta := store.ThreadMeta{ID: "s1", Cwd: root, Harness: "fake", ProjectID: p.ID, Phase: "idle", CreatedAt: proto.NowMillis(), UpdatedAt: proto.NowMillis()}
	if err := st.CreateThread(context.Background(), meta); err != nil {
		t.Fatal(err)
	}

	err := mgr.DeleteProject(context.Background(), p.ID)
	if !errors.Is(err, store.ErrProjectInUse) {
		t.Fatalf("delete gave %v, want ErrProjectInUse", err)
	}
	if !strings.Contains(err.Error(), "1 thread") {
		t.Fatalf("the refusal does not say how many threads are in the way: %v", err)
	}
	if _, err := st.Project(context.Background(), p.ID); err != nil {
		t.Fatalf("the refused project was removed anyway: %v", err)
	}

	// Once the thread goes, the project can too.
	if err := st.DeleteThread(context.Background(), meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatalf("delete still refused after the thread went: %v", err)
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

// The refusal is only as good as the window it covers. Creating a thread
// reads its project long before it writes the row — probing the harness and
// resolving a workspace happen in between — so the check that matters is the
// one in the insert's own transaction.
func TestCreateThreadRefusesADeletedProject(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)

	// Stands in for the gap: the caller read the project, then it went.
	if err := st.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}

	meta := store.ThreadMeta{ID: "s1", Cwd: root, Harness: "fake", ProjectID: p.ID, Phase: "creating", CreatedAt: proto.NowMillis(), UpdatedAt: proto.NowMillis()}
	if err := st.CreateThread(context.Background(), meta); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("creating a thread in a deleted project gave %v, want ErrNotFound", err)
	}
	threads, err := st.ListThreads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 0 {
		t.Fatalf("the orphaned thread was written anyway: %d rows", len(threads))
	}
}

// A thread with no project at all is the pre-project shape and still legal.
// The new check must not turn it into an error.
func TestCreateThreadStillAllowsNoProject(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, _ := testProject(t, root)

	meta := store.ThreadMeta{ID: "s1", Cwd: root, Harness: "fake", Phase: "idle", CreatedAt: proto.NowMillis(), UpdatedAt: proto.NowMillis()}
	if err := st.CreateThread(context.Background(), meta); err != nil {
		t.Fatalf("a thread with no project was refused: %v", err)
	}
}

// Saving is an update, not an upsert. A save that read the project before a
// delete and wrote after it would otherwise put the row straight back.
func TestSaveProjectCannotResurrectADeletedOne(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.SaveProject(context.Background(), p.ID, p.Name, p.Defaults); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("saving a deleted project gave %v, want ErrNotFound", err)
	}
	projects, err := mgr.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("the deleted project came back: %d rows", len(projects))
	}
}

// The registry is shared across paired devices, so a project removed on one
// has to leave the others' lists without a reconnect.
func TestProjectChangesReachEveryConnection(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	id, ch := mgr.SubscribeProjects()
	defer mgr.UnsubscribeProjects(id)

	if err := mgr.DeleteProject(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("deleting a project woke no project subscriber, so other devices keep showing it")
	}
}

// Copies and attaching are git's. A plain folder has neither, so a thread
// there works in the folder whatever the project defaults to.
func TestAPlainFolderThreadWorksInTheFolder(t *testing.T) {
	root := t.TempDir()
	st, p := testProject(t, root)
	p.Defaults.Workspace = "managed"
	if err := putProject(context.Background(), st, p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, _ := st.Thread(context.Background(), a.ID)
	if meta.WorkspaceMode != "local" || meta.Cwd != root || meta.FolderID != p.Folders[0].ID {
		t.Fatalf("thread = mode %q cwd %q folder %q", meta.WorkspaceMode, meta.Cwd, meta.FolderID)
	}
	if _, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: root}); err == nil {
		t.Fatal("attached to a copy of a plain folder")
	}
}

// With several folders and none chosen, the thread is the whole project's:
// it starts in the home folder and never works on a copy.
func TestAThreadAcrossTheProjectStartsInTheHomeFolder(t *testing.T) {
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	repo, _, _ := gitRepo(t)
	st, p := testProject(t, repo)
	home := t.TempDir()
	if err := st.SetProjectHome(context.Background(), p.ID, home); err != nil {
		t.Fatal(err)
	}
	if err := st.AddFolder(context.Background(), p.ID, project.NewFolder("f2", t.TempDir())); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if _, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "managed"}); err == nil {
		t.Fatal("made a copy for a thread across several folders")
	}
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	meta, _ := st.Thread(context.Background(), a.ID)
	if meta.Cwd != home || meta.FolderID != "" {
		t.Fatalf("thread cwd %q folder %q, want the home folder and no folder", meta.Cwd, meta.FolderID)
	}
	b, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, FolderID: p.Folders[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Dispose("test done")
	if meta, _ := st.Thread(context.Background(), b.ID); meta.Cwd != canonicalPath(repo) && meta.Cwd != repo {
		t.Fatalf("a thread in one folder started in %q", meta.Cwd)
	}
}
