package thread

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/store"
)

// repoWithWorktree builds the shape omniplex manages: a project root with a
// worktree underneath it in .worktrees.
func repoWithWorktree(t *testing.T) (root, worktree string) {
	t.Helper()
	root = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-m", "init")
	worktree = filepath.Join(root, ".worktrees", "wt")
	run("worktree", "add", worktree, "-b", "feature/wt")
	return root, worktree
}

// forgetWorktree reproduces what Git does when it fails to delete a checkout:
// the administrative entry goes even though the files stay, leaving a
// directory no Git command will act on again.
func forgetWorktree(t *testing.T, root, worktree string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, ".git", "worktrees", filepath.Base(worktree))); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", worktree, "status").CombinedOutput(); err == nil {
		t.Fatalf("fixture is still a working tree: %s", out)
	}
}

func deleteFixture(t *testing.T, root, worktree string) (*Manager, store.ThreadMeta, project.Project) {
	t.Helper()
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	return mgr, store.ThreadMeta{ID: "s1", Cwd: worktree, WorkspaceMode: "managed", ProjectID: p.ID}, p
}

func TestRemoveGitWorktreeRemovesARegisteredWorktree(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)

	if err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("worktree directory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "worktrees", "wt")); !os.IsNotExist(err) {
		t.Error("administrative entry survived")
	}
}

// The bug this fixes: one failed delete unregistered the worktree, and every
// attempt afterwards — including force delete, the feature that exists for
// this exact situation — refused it forever.
func TestForceDeleteRecoversAWorktreeGitHasForgotten(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)
	forgetWorktree(t, root, worktree)

	if err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, true); err != nil {
		t.Fatalf("force delete of an orphaned worktree failed: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("orphaned directory survived force delete: %v", err)
	}
}

func TestOrdinaryCleanupStillRefusesAWorktreeGitHasForgotten(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)
	forgetWorktree(t, root, worktree)

	err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, false)
	if err == nil {
		t.Fatal("an unregistered directory was removed without force")
	}
	if !strings.Contains(err.Error(), "not a registered worktree") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Errorf("directory was removed despite the refusal: %v", statErr)
	}
}

// Force delete may remove a directory Git no longer tracks, so the .git
// pointer is the only thing proving whose it was. Without that check it would
// happily delete any directory a thread's cwd happened to name.
func TestForceDeleteWillNotRemoveADirectoryThatWasNeverOurWorktree(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, _, p := deleteFixture(t, root, worktree)

	stranger := filepath.Join(t.TempDir(), "not-ours")
	if err := os.MkdirAll(stranger, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stranger, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := store.ThreadMeta{ID: "s2", Cwd: stranger, WorkspaceMode: "managed", ProjectID: p.ID}

	if err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, true); err == nil {
		t.Fatal("force delete removed a directory that was not a worktree of this repository")
	}
	if _, err := os.Stat(filepath.Join(stranger, "keep")); err != nil {
		t.Errorf("unrelated files were removed: %v", err)
	}
}

// Refusing up front is what stops the wedge: a live process races the removal,
// Git loses on the final rmdir and unregisters the worktree anyway.
func TestCleanupRefusesWhileSomethingIsStillRunningInTheWorktree(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)
	startIn(t, worktree)

	err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, false)
	if err == nil {
		t.Fatal("cleanup proceeded with a process still inside the worktree")
	}
	if !strings.Contains(err.Error(), "still in use") || !strings.Contains(err.Error(), "sleep") {
		t.Errorf("error should name the offending process, got: %v", err)
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Errorf("worktree was removed despite the refusal: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".git", "worktrees", "wt")); statErr != nil {
		t.Errorf("administrative entry was removed despite the refusal: %v", statErr)
	}
}

// Force delete is the escape hatch for a workspace that will not go quietly,
// so it must not inherit the in-use refusal.
func TestForceDeleteProceedsDespiteARunningProcess(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)
	startIn(t, worktree)

	if err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, true); err != nil {
		t.Fatalf("force delete refused a busy worktree: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("busy worktree survived force delete: %v", err)
	}
}

// Force delete runs with a writer still active, so the tree can come back the
// instant it is emptied. Reporting success would let the caller purge the
// thread, leaving a directory nothing names any more and no route in the UI
// to clean it up — strictly worse than the wedge this change removes.
func TestRemovalDoesNotReportSuccessWhenTheDirectoryComesBack(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, _, _ := deleteFixture(t, root, worktree)

	victim := filepath.Join(t.TempDir(), "recreated")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stand in for a dev server that rewrites its cache the moment the tree is
	// emptied: the removal succeeds and the directory is there again straight
	// afterwards.
	original := removeTree
	removeTree = func(path string) error {
		if err := original(path); err != nil {
			return err
		}
		return os.MkdirAll(filepath.Join(path, "cache"), 0o755)
	}
	t.Cleanup(func() { removeTree = original })

	err := mgr.deleteWorktreeDir(context.Background(), root, victim)
	if err == nil {
		t.Fatal("removal reported success for a directory that was recreated")
	}
	if !strings.Contains(err.Error(), "recreated") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoveGitWorktreePrunesWhenTheDirectoryIsAlreadyGone(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}

	if err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "worktrees", "wt")); !os.IsNotExist(err) {
		t.Error("stale administrative entry was not pruned")
	}
}

func TestRemoveGitWorktreeRefusesTheProjectRoot(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, _, p := deleteFixture(t, root, worktree)
	meta := store.ThreadMeta{ID: "s3", Cwd: root, WorkspaceMode: "managed", ProjectID: p.ID}

	if err := mgr.removeGitWorktree(context.Background(), meta, p.Folders[0], nil, false); err == nil {
		t.Fatal("the project root was accepted for removal")
	}
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Errorf("project root files were removed: %v", err)
	}
}

// deprovisionProject is a git project whose folder tears its threads down
// with a hook that records each run in a log outside the repository, and exits
// with the given status. It stands in for a hook that owns more than the
// checkout — a database, a bucket, a port lease — so a run that never happens
// is a leak.
func deprovisionProject(t *testing.T, exit int) (root string, st *store.Store, p project.Project, runs string) {
	t.Helper()
	root, _, _ = gitRepo(t)
	st, p = testProject(t, root)
	runs = filepath.Join(t.TempDir(), "deprovision-runs")
	script := fmt.Sprintf("#!/bin/sh\necho \"$OMNIPLEX_THREAD_ID\" >> %q\nexit %d\n", runs, exit)
	if err := os.WriteFile(filepath.Join(root, "deprovision"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p.Defaults.Workspace = "managed"
	p.Folders[0].Deprovision = "deprovision"
	if err := putProject(context.Background(), st, p); err != nil {
		t.Fatal(err)
	}
	return root, st, p, runs
}

func deprovisionRuns(t *testing.T, runs string) []string {
	t.Helper()
	b, err := os.ReadFile(runs)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func waitGone(t *testing.T, st *store.Store, id string) {
	t.Helper()
	waitFor(t, func() bool {
		_, e := st.Thread(context.Background(), id)
		return errors.Is(e, store.ErrNotFound)
	})
}

// Closing stops the harness but keeps the checkout, so a closed thread's
// resources are still out there. Deleting it with the box ticked used to remove
// only the directory and leak everything else the hook would have released.
func TestDeletingAClosedThreadRunsItsDeprovisionHook(t *testing.T) {
	_, st, p, runs := deprovisionProject(t, 0)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Branch: "issue/5-closed"})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, a.ID)
	if err := mgr.Close(context.Background(), a.ID, "done for now"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Thread(context.Background(), a.ID)
		return e == nil && m.Phase == "closed"
	})

	if err := mgr.Delete(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	waitGone(t, st, a.ID)
	if got := deprovisionRuns(t, runs); len(got) != 1 || got[0] != a.ID {
		t.Fatalf("deprovision runs = %v, want one for %s", got, a.ID)
	}
}

// A closed thread's teardown can fail like any other, and has to leave the
// thread behind to retry or force rather than vanish with its resources.
func TestAFailedTeardownOfAClosedThreadKeepsIt(t *testing.T) {
	_, st, p, runs := deprovisionProject(t, 1)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Branch: "issue/6-closed-fails"})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if err := mgr.Close(context.Background(), a.ID, "done for now"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Delete(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Thread(context.Background(), a.ID)
		return e == nil && m.Phase == "cleanup_failed"
	})
	if got := deprovisionRuns(t, runs); len(got) != 1 {
		t.Fatalf("deprovision runs = %v, want one", got)
	}
	if _, err := os.Stat(meta.Cwd); err != nil {
		t.Fatalf("a failed teardown removed the worktree: %v", err)
	}
}

// Force delete is offered because the hook failed, but skipping it outright
// leaked everything it owns for good. It runs once more, and its failing again
// does not stop the delete.
func TestForceDeleteRunsTheDeprovisionHookAndDeletesWhenItFails(t *testing.T) {
	_, st, p, runs := deprovisionProject(t, 1)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Branch: "issue/7-force"})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if err := mgr.Delete(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Thread(context.Background(), a.ID)
		return e == nil && m.Phase == "cleanup_failed"
	})

	if err := mgr.ForceDelete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	waitGone(t, st, a.ID)
	if got := deprovisionRuns(t, runs); len(got) != 2 {
		t.Fatalf("deprovision runs = %v, want the delete's and the force delete's", got)
	}
	if _, err := os.Stat(meta.Cwd); !os.IsNotExist(err) {
		t.Fatalf("force delete left the worktree at %s: %v", meta.Cwd, err)
	}
}
