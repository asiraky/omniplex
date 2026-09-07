package session

import (
	"context"
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

func deleteFixture(t *testing.T, root, worktree string) (*Manager, store.SessionMeta, project.Project) {
	t.Helper()
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	return mgr, store.SessionMeta{ID: "s1", Cwd: worktree, WorkspaceMode: "managed", ProjectID: p.ID}, p
}

func TestRemoveGitWorktreeRemovesARegisteredWorktree(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)

	if err := mgr.removeGitWorktree(context.Background(), meta, p, nil, false); err != nil {
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

	if err := mgr.removeGitWorktree(context.Background(), meta, p, nil, true); err != nil {
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

	err := mgr.removeGitWorktree(context.Background(), meta, p, nil, false)
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
// happily delete any directory a session's cwd happened to name.
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
	meta := store.SessionMeta{ID: "s2", Cwd: stranger, WorkspaceMode: "managed", ProjectID: p.ID}

	if err := mgr.removeGitWorktree(context.Background(), meta, p, nil, true); err == nil {
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

	err := mgr.removeGitWorktree(context.Background(), meta, p, nil, false)
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

	if err := mgr.removeGitWorktree(context.Background(), meta, p, nil, true); err != nil {
		t.Fatalf("force delete refused a busy worktree: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("busy worktree survived force delete: %v", err)
	}
}

func TestRemoveGitWorktreePrunesWhenTheDirectoryIsAlreadyGone(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, meta, p := deleteFixture(t, root, worktree)
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}

	if err := mgr.removeGitWorktree(context.Background(), meta, p, nil, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "worktrees", "wt")); !os.IsNotExist(err) {
		t.Error("stale administrative entry was not pruned")
	}
}

func TestRemoveGitWorktreeRefusesTheProjectRoot(t *testing.T) {
	root, worktree := repoWithWorktree(t)
	mgr, _, p := deleteFixture(t, root, worktree)
	meta := store.SessionMeta{ID: "s3", Cwd: root, WorkspaceMode: "managed", ProjectID: p.ID}

	if err := mgr.removeGitWorktree(context.Background(), meta, p, nil, false); err == nil {
		t.Fatal("the project root was accepted for removal")
	}
	if _, err := os.Stat(filepath.Join(root, "README")); err != nil {
		t.Errorf("project root files were removed: %v", err)
	}
}
