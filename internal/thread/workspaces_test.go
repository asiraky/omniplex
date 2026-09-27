package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// gitRepo builds a repository with one extra worktree, which is the shape the
// picker exists to offer: a checkout omniplex did not create but can still attach to.
func gitRepo(t *testing.T) (root, worktree, branch string) {
	t.Helper()
	root = t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(root, "add", "README")
	run(root, "commit", "-m", "init")
	worktree, branch = filepath.Join(t.TempDir(), "side"), "issue/1-side"
	run(root, "worktree", "add", worktree, "-b", branch)
	return root, worktree, branch
}

func testProject(t *testing.T, root string) (*store.Store, project.Project) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "workspaces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := proto.NowMillis()
	p := project.Project{ID: "p1", Root: root, CreatedAt: now, UpdatedAt: now, Config: project.DefaultConfig(root)}
	p.Config.Defaults.Harness = "fake"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return st, p
}

func find(spaces []Workspace, path string) (Workspace, bool) {
	for _, w := range spaces {
		if resolve(w.Path) == resolve(path) {
			return w, true
		}
	}
	return Workspace{}, false
}

func resolve(path string) string {
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		return canonical
	}
	return path
}

func TestListWorkspacesReportsRootAndWorktrees(t *testing.T) {
	root, worktree, branch := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	spaces, err := mgr.ListWorkspaces(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := find(spaces, root)
	if !ok || !got.IsRoot || got.Branch != "main" {
		t.Fatalf("project root missing or mislabelled: %+v", spaces)
	}
	side, ok := find(spaces, worktree)
	if !ok || side.IsRoot || side.Branch != branch {
		t.Fatalf("existing worktree missing or mislabelled: %+v", spaces)
	}
	if side.Busy {
		t.Fatal("an unused worktree must not be reported busy")
	}
}

func TestListWorkspacesMarksCheckoutsHeldByLiveSessions(t *testing.T) {
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	now := proto.NowMillis()
	meta := store.SessionMeta{ID: "s1", Cwd: worktree, Harness: "fake", Title: "already here", CreatedAt: now, UpdatedAt: now, Phase: "idle", ProjectID: p.ID}
	if err := st.CreateSession(context.Background(), meta); err != nil {
		t.Fatal(err)
	}

	spaces, err := mgr.ListWorkspaces(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	side, ok := find(spaces, worktree)
	if !ok || !side.Busy || side.BusySessionID != "s1" || side.BusyTitle != "already here" {
		t.Fatalf("held worktree not reported busy: %+v", side)
	}
	// Busy is advice, not a lock: nothing about Git stops two sessions sharing
	// a checkout, so attaching still succeeds and the presenter warns.
	if _, err := mgr.ResolveWorkspace(context.Background(), p.ID, worktree); err != nil {
		t.Fatalf("attaching to a busy workspace should be allowed: %v", err)
	}
}

func TestResolveWorkspaceRejectsDirectoriesOutsideTheProject(t *testing.T) {
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	if _, err := mgr.ResolveWorkspace(context.Background(), p.ID, t.TempDir()); err == nil {
		t.Fatal("a directory that is not a worktree of this project must be refused")
	}
	if _, err := mgr.ResolveWorkspace(context.Background(), p.ID, worktree); err != nil {
		t.Fatalf("a real worktree should resolve: %v", err)
	}
}

func TestCreateProjectAttachesToExistingWorktreeWithoutProvisioning(t *testing.T) {
	root, worktree, branch := gitRepo(t)
	st, p := testProject(t, root)
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		meta, err := st.Session(context.Background(), a.ID)
		return err == nil && meta.Phase == "ready"
	})
	meta, err := st.Session(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolve(meta.Cwd) != resolve(worktree) {
		t.Fatalf("session ran in %s, want the borrowed worktree %s", meta.Cwd, worktree)
	}
	if meta.WorkspaceMode != "borrowed" {
		t.Fatalf("workspace mode is %q, want borrowed", meta.WorkspaceMode)
	}
	if meta.Branch != branch {
		t.Fatalf("branch is %q, want %q", meta.Branch, branch)
	}

	// The whole point of borrowing: omniplex did not make this checkout, so closing
	// the session must leave it exactly where it was.
	if err := mgr.Cleanup(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, err := st.Session(context.Background(), a.ID)
		return err == nil && m.Phase == "closed"
	})
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("cleanup removed a borrowed worktree: %v", err)
	}
}

func TestParseWorktreeListReadsDetachedHeads(t *testing.T) {
	porcelain := "worktree /repo\nHEAD 1234567890abcdef\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/loose\nHEAD fedcba0987654321\ndetached\nlocked\n"
	got := parseWorktreeList(porcelain, "/repo")
	if len(got) != 2 {
		t.Fatalf("parsed %d worktrees, want 2", len(got))
	}
	if !got[0].IsRoot || got[0].Branch != "main" || got[0].Head != "12345678" {
		t.Fatalf("root parsed wrong: %+v", got[0])
	}
	if got[1].IsRoot || got[1].Branch != "" || got[1].Head != "fedcba09" || !got[1].Locked {
		t.Fatalf("detached worktree parsed wrong: %+v", got[1])
	}
}

// Exactly what the user will do: type a branch name into the picker on a
// project whose default workspace is "local", and expect a worktree.
func TestTypedBranchCreatesWorktreeEvenWhenProjectDefaultIsLocal(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	if p.Config.Defaults.Workspace != "local" {
		t.Fatalf("precondition: default workspace is %q", p.Config.Defaults.Workspace)
	}
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/42-typed-in",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, err := st.Session(context.Background(), a.ID)
		return err == nil && (m.Phase == "ready" || m.Phase == "provision_failed")
	})
	m, _ := st.Session(context.Background(), a.ID)
	if m.Phase != "ready" {
		t.Fatalf("phase %q, want ready", m.Phase)
	}
	want := filepath.Join(root, ".worktrees", "issue-42-typed-in")
	if resolve(m.Cwd) != resolve(want) {
		t.Fatalf("cwd %s, want %s", m.Cwd, want)
	}
	if _, err := os.Stat(filepath.Join(m.Cwd, "README")); err != nil {
		t.Fatalf("worktree has no checkout: %v", err)
	}
	if m.Branch != "issue/42-typed-in" {
		t.Fatalf("branch %q", m.Branch)
	}
	if fa.session() == nil {
		t.Fatal("harness never started in the new worktree")
	}
}

// The same, but with the worktree root pointed outside the repo.
func TestSuggestedRootMayLiveOutsideTheProject(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	p.Config.Workspace.SuggestedRoot = "../sibling-worktrees"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/7-outside",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, err := st.Session(context.Background(), a.ID)
		return err == nil && (m.Phase == "ready" || m.Phase == "provision_failed")
	})
	m, _ := st.Session(context.Background(), a.ID)
	if m.Phase != "ready" {
		t.Fatalf("phase %q, want ready", m.Phase)
	}
	want := filepath.Join(filepath.Dir(root), "sibling-worktrees", "issue-7-outside")
	if resolve(m.Cwd) != resolve(want) {
		t.Fatalf("cwd %s, want %s", m.Cwd, want)
	}
	if _, err := os.Stat(filepath.Join(m.Cwd, "README")); err != nil {
		t.Fatalf("worktree has no checkout: %v", err)
	}
}

// The "Main checkout" side of the new-session toggle: run where the user
// already works, touch nothing on the way in or out.
func TestLocalSessionRunsInTheProjectRootAndSkipsHooks(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	// A provision hook that would be destructive against a live checkout: if
	// it runs, it leaves evidence.
	for _, name := range []string{"provision", "deprovision"} {
		script := "#!/bin/sh\ntouch \"$OMNIPLEX_PROJECT_ROOT/" + name + "-ran\"\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p.Config.Defaults.Workspace = "local"
	p.Config.Workspace.Provision = "provision"
	p.Config.Workspace.Deprovision = "deprovision"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), a.ID)
		return e == nil && (m.Phase == "ready" || m.Phase == "provision_failed")
	})
	m, err := st.Session(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Phase != "ready" {
		t.Fatalf("phase %q, want ready", m.Phase)
	}
	if resolve(m.Cwd) != resolve(root) {
		t.Fatalf("cwd %s, want the project root %s", m.Cwd, root)
	}
	if m.WorkspaceMode != "local" {
		t.Fatalf("workspace mode %q, want local", m.WorkspaceMode)
	}
	// The session is on whatever the checkout was already on. No branch was
	// created, so none may be claimed.
	if m.Branch != "main" {
		t.Fatalf("branch %q, want the checkout's own branch main", m.Branch)
	}
	// Cleared at creation, not merely skipped at run time: the record itself
	// must show there is nothing to run.
	if m.ProvisionScript != "" || m.DeprovisionScript != "" {
		t.Fatalf("hooks survived onto a local session: %q / %q", m.ProvisionScript, m.DeprovisionScript)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees")); !os.IsNotExist(err) {
		t.Fatal("a local session must not create a worktree")
	}
	if _, err := os.Stat(filepath.Join(root, "provision-ran")); !os.IsNotExist(err) {
		t.Fatal("the provision hook ran against the user's own checkout")
	}

	// Closing it is the dangerous half: nothing omniplex did not create may go.
	if err := mgr.Cleanup(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		c, e := st.Session(context.Background(), a.ID)
		return e == nil && c.Phase == "closed"
	})
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Fatalf("cleanup removed files from the main checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deprovision-ran")); !os.IsNotExist(err) {
		t.Fatal("the deprovision hook ran against the user's own checkout")
	}
}

// Sharing the main checkout is the user's call: omniplex reports that somebody is
// already there and starts the session anyway.
func TestSecondLocalSessionIsAllowedWhileTheFirstIsLive(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	first, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "local"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), first.ID)
		return e == nil && m.Phase == "ready"
	})

	// The warning the presenter shows is this flag, and it has to be set
	// before the second session can be warned about anything.
	spaces, err := mgr.ListWorkspaces(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := find(spaces, root); !ok || !w.Busy {
		t.Fatalf("the held root should be reported busy: %+v", w)
	}

	second, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "local"})
	if err != nil {
		t.Fatalf("a second local session in the same checkout should be allowed: %v", err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), second.ID)
		return e == nil && m.Phase == "ready"
	})
	m, _ := st.Session(context.Background(), second.ID)
	if resolve(m.Cwd) != resolve(root) {
		t.Fatalf("second session cwd %s, want %s", m.Cwd, root)
	}
}

// Force delete is the one path that removes a worktree without a hook, and it
// must still recognise a checkout that was never omniplex's to remove.
func TestForceDeleteOfALocalSessionRemovesNothing(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "local"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), a.ID)
		return e == nil && m.Phase == "ready"
	})
	if err := st.SetPhase(context.Background(), a.ID, "cleanup_failed"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ForceDelete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Fatalf("force delete removed files from the main checkout: %v", err)
	}
}

// An upgrade must not turn a hook that was written to tear down a worktree
// into one that runs over the user's own files.
func TestCleanupIgnoresADeprovisionScriptLeftOnALocalSession(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	script := "#!/bin/sh\ntouch \"$OMNIPLEX_PROJECT_ROOT/deprovision-ran\"\n"
	if err := os.WriteFile(filepath.Join(root, "deprovision"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p.Config.Workspace.Deprovision = "deprovision"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	// The row an older build would have written: local, but carrying a
	// teardown script because that build attached one regardless of mode.
	now := proto.NowMillis()
	meta := store.SessionMeta{
		ID: "legacy", Cwd: root, Harness: "fake", CreatedAt: now, UpdatedAt: now,
		Phase: "idle", ProjectID: p.ID, WorkspaceMode: "local", DeprovisionScript: "deprovision",
	}
	if err := st.CreateSession(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Get(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Cleanup(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), "legacy")
		return e == nil && m.Phase == "closed"
	})
	if _, err := os.Stat(filepath.Join(root, "deprovision-ran")); !os.IsNotExist(err) {
		t.Fatal("a teardown hook ran against the main checkout")
	}
}

// A workspace mode omniplex does not recognise must not fall through into "start a
// harness in the project root with the hooks still attached".
func TestUnknownWorkspaceModeIsRefused(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	for _, mode := range []string{"borrowed", "local ", "garbage"} {
		if _, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: mode}); err == nil {
			t.Fatalf("workspace mode %q should be refused", mode)
		}
	}
}

// A managed session that got its worktree but failed afterwards still holds
// it: offering it again would put two harnesses in it, and cleaning the
// failure up would delete it underneath the second.
func TestAFailedManagedSessionStillHoldsTheWorktreeItCreated(t *testing.T) {
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	now := proto.NowMillis()
	failed := store.SessionMeta{
		ID: "half-made", Cwd: worktree, Harness: "fake", Title: "half made", CreatedAt: now,
		UpdatedAt: now, Phase: "provision_failed", ProjectID: p.ID, WorkspaceMode: "managed",
	}
	if err := st.CreateSession(context.Background(), failed); err != nil {
		t.Fatal(err)
	}
	spaces, err := mgr.ListWorkspaces(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := find(spaces, worktree); !ok || !w.Busy {
		t.Fatalf("a worktree held by a failed session must stay busy: %+v", w)
	}
	// The placeholder is the opposite case: it names the root only because
	// provisioning has not replaced it yet, so the root stays free.
	placeholder := store.SessionMeta{
		ID: "not-yet", Cwd: root, Harness: "fake", CreatedAt: now, UpdatedAt: now,
		Phase: "provisioning", ProjectID: p.ID, WorkspaceMode: "managed",
	}
	if err := st.CreateSession(context.Background(), placeholder); err != nil {
		t.Fatal(err)
	}
	spaces, err = mgr.ListWorkspaces(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := find(spaces, root); !ok || w.Busy {
		t.Fatalf("an unprovisioned managed session must not hold the root: %+v", w)
	}
}

// A project rooted inside a repository is a checkout Git never names, because
// `worktree list` reports the repository root instead.
func TestProjectRootInsideARepositoryIsStillAttachable(t *testing.T) {
	repo, _, _ := gitRepo(t)
	sub := filepath.Join(repo, "packages", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	st, p := testProject(t, sub)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	spaces, err := mgr.ListWorkspaces(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := find(spaces, sub); !ok || !w.IsRoot {
		t.Fatalf("the configured project root is missing from %+v", spaces)
	}
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "local"})
	if err != nil {
		t.Fatalf("a main-checkout session in a subdirectory project should work: %v", err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), a.ID)
		return e == nil && m.Phase == "ready"
	})
	m, _ := st.Session(context.Background(), a.ID)
	if resolve(m.Cwd) != resolve(sub) {
		t.Fatalf("cwd %s, want %s", m.Cwd, sub)
	}
}

// Creating a session reads the workspace list and then writes a row that
// changes it, so the pair is one critical section — but the outcome is that
// every caller gets a session, not that one of them wins a lock.
func TestConcurrentLocalSessionsAllShareTheRoot(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "local"}); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if won != racers {
		t.Fatalf("%d of %d concurrent local sessions started, want all of them", won, racers)
	}
}
