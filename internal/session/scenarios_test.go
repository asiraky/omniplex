package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ready waits for a session to finish provisioning and returns its row.
func ready(t *testing.T, st *store.Store, id string) store.SessionMeta {
	t.Helper()
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), id)
		return e == nil && m.Phase == "ready"
	})
	m, err := st.Session(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A session may stack itself on any ref, not only the project's default base
// branch: that is the whole point of being able to build on another worktree's
// work before it has merged.
func TestManagedWorktreeBranchesFromTheSessionsOwnBase(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	// A second commit that only "stacked" carries, so branching from it is
	// distinguishable from branching from the default base.
	git(t, root, "checkout", "-b", "stacked")
	if err := os.WriteFile(filepath.Join(root, "STACKED"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "STACKED")
	git(t, root, "commit", "-m", "stacked")
	want := git(t, root, "rev-parse", "stacked")
	git(t, root, "checkout", "main")

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/7-on-top", BaseRef: "stacked",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got := git(t, meta.Cwd, "rev-parse", "HEAD"); got != want {
		t.Fatalf("worktree HEAD %s, want the base ref %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(meta.Cwd, "STACKED")); err != nil {
		t.Fatalf("the base ref's content is missing from the worktree: %v", err)
	}
}

// A base nobody can find is no longer fatal. Refusing to start the session
// punishes the user for a stale project default they may not have written, so
// the worktree branches from the repository's default branch and says so.
func TestUnknownBaseRefFallsBackToTheDefaultBranch(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	wantMain := git(t, root, "rev-parse", "main")

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/8-nowhere", BaseRef: "no/such/ref",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got := git(t, meta.Cwd, "rev-parse", "HEAD"); got != wantMain {
		t.Fatalf("worktree HEAD %s, want the default branch %s", got, wantMain)
	}
	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Workspace.Output, "no/such/ref") || !strings.Contains(state.Workspace.Output, "note:") {
		t.Fatalf("the fallback was swallowed instead of shown: %q", state.Workspace.Output)
	}
}

// The bug in the field: a project default of baseBranch "staging" in a clone
// that only has origin/staging used to hard-fail every session it touched.
func TestManagedWorktreeBranchesFromARemoteOnlyBaseBranch(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/9-on-staging", BaseRef: "staging",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got := git(t, meta.Cwd, "merge-base", "HEAD", want); got != want {
		t.Fatalf("merge-base with the remote branch is %s, want %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(meta.Cwd, "STAGING")); err != nil {
		t.Fatalf("the remote base's content is missing from the worktree: %v", err)
	}
	if git(t, root, "rev-parse", "refs/heads/staging") != want {
		t.Fatal("no local tracking branch was created for the remote base")
	}
}

// Deleting a session is deleting a session. Removing the checkout it ran in is
// a separate, explicit answer, and its absence means no.
func TestDeleteLeavesTheWorktreeUnlessAsked(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/2-keep",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)

	if err := mgr.Delete(context.Background(), a.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, e := st.Session(context.Background(), a.ID)
		return errors.Is(e, store.ErrNotFound)
	})
	if _, err := os.Stat(meta.Cwd); err != nil {
		t.Fatalf("deleting the session removed the worktree nobody asked to remove: %v", err)
	}
	if !strings.Contains(git(t, root, "worktree", "list", "--porcelain"), resolve(meta.Cwd)) {
		t.Fatal("the worktree was unregistered from Git")
	}
}

func TestDeleteRemovesTheWorktreeWhenAsked(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/3-go",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)

	if err := mgr.Delete(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, statErr := os.Stat(meta.Cwd)
		return os.IsNotExist(statErr)
	})
	// The branch outlives the checkout: omniplex never deletes branches.
	if out := git(t, root, "branch", "--list", "issue/3-go"); !strings.Contains(out, "issue/3-go") {
		t.Fatalf("the branch was deleted with the worktree: %q", out)
	}
}

// Now that two sessions may share one checkout, the last one out is the only
// one allowed to take it with them.
func TestDeleteRefusesToRemoveASharedWorktree(t *testing.T) {
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	first, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, first.ID)
	second, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree})
	if err != nil {
		t.Fatalf("a second session should be able to share a worktree: %v", err)
	}
	ready(t, st, second.ID)

	if err := mgr.Delete(context.Background(), first.ID, true); err == nil {
		t.Fatal("removing a worktree another session is still in should be refused")
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("the shared worktree was removed anyway: %v", err)
	}
	// Without the request it is an ordinary delete, and it goes through.
	if err := mgr.Delete(context.Background(), first.ID, false); err != nil {
		t.Fatal(err)
	}
}

// The main checkout is the user's own working directory, and no answer to any
// dialog makes it omniplex's to delete.
func TestDeleteNeverRemovesTheMainCheckout(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, a.ID)

	if err := mgr.Delete(context.Background(), a.ID, true); err == nil {
		t.Fatal("the main checkout should never be removable")
	}
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Fatalf("the user's checkout was touched: %v", err)
	}
}

// A closed session still has a checkout on disk, and the checkbox has to mean
// the same thing whatever phase the row is in.
func TestDeletingAClosedSessionStillHonoursTheCheckbox(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/4-closed",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	// Stop the harness without releasing the checkout.
	if err := mgr.Close(context.Background(), a.ID, "done for now"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), a.ID)
		return e == nil && m.Phase == "closed"
	})

	if err := mgr.Delete(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(meta.Cwd); !os.IsNotExist(statErr) {
		t.Fatalf("a closed session's worktree survived a ticked delete: %v", statErr)
	}
}

// A closed sibling is still a session omniplex knows of, and it still names the
// directory somebody is about to delete.
func TestAClosedSiblingStillProtectsTheWorktree(t *testing.T) {
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	first, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, first.ID)
	second, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, second.ID)
	if err := mgr.Close(context.Background(), second.ID, "done for now"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		m, e := st.Session(context.Background(), second.ID)
		return e == nil && m.Phase == "closed"
	})

	if err := mgr.Delete(context.Background(), first.ID, true); err == nil {
		t.Fatal("a worktree a closed session still names should not be removable")
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("the worktree was removed anyway: %v", err)
	}
}

// Attaching is allowed while somebody else is working; it is not allowed while
// somebody else is tearing the directory down.
func TestAttachingToACheckoutBeingCleanedUpIsRefused(t *testing.T) {
	root, worktree, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if err := st.SetPhase(context.Background(), meta.ID, "cleaning"); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, WorkspacePath: worktree}); err == nil {
		t.Fatal("attaching to a checkout being cleaned up should be refused")
	}
}
