package thread

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

// settled waits for a thread to finish provisioning either way and returns its
// row, so a test can assert on a failure as well as a success.
func settled(t *testing.T, st *store.Store, id string) store.ThreadMeta {
	t.Helper()
	waitFor(t, func() bool {
		m, e := st.Thread(context.Background(), id)
		return e == nil && (m.Phase == "ready" || m.Phase == "provision_failed")
	})
	m, err := st.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func managedThread(t *testing.T, mgr *Manager, projectID, branch, base string) string {
	t.Helper()
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: projectID, Workspace: "managed", Branch: branch, BaseRef: base})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func registeredWorktrees(t *testing.T, root string) []Workspace {
	t.Helper()
	return parseWorktreeList(git(t, root, "worktree", "list", "--porcelain"), canonicalPath(root))
}

func registeredAt(t *testing.T, root, path string) (Workspace, bool) {
	t.Helper()
	return find(registeredWorktrees(t, root), path)
}

// "fix/x" and "fix-x" fold to the same slug. They are different branches and
// must get different checkouts, each on its own branch.
func TestManagedWorktreesForBranchesWithTheSameSlugGetDistinctDirectories(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	first := ready(t, st, managedThread(t, mgr, p.ID, "fix/x", ""))
	second := ready(t, st, managedThread(t, mgr, p.ID, "fix-x", ""))

	if resolve(first.Cwd) == resolve(second.Cwd) {
		t.Fatalf("both threads were given %s", first.Cwd)
	}
	for _, m := range []store.ThreadMeta{first, second} {
		if got := git(t, m.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); got != m.Branch {
			t.Fatalf("%s is on %q, want %q", m.Cwd, got, m.Branch)
		}
		if w, ok := registeredAt(t, root, m.Cwd); !ok || w.Branch != m.Branch {
			t.Fatalf("%s is not registered as a worktree on %s: %+v", m.Cwd, m.Branch, w)
		}
	}
}

// A retry of the thread that lost the slug must land on the worktree its first
// attempt made, not on the other branch's and not on a third directory.
func TestWorktreePathIsStableForTheBranchThatLostTheSlug(t *testing.T) {
	root, _, _ := gitRepo(t)
	ctx := context.Background()
	slug := filepath.Join(root, ".worktrees", "fix-x")
	git(t, root, "worktree", "add", slug, "-b", "fix/x")

	path, held, err := worktreePath(ctx, root, "fix-x", slug)
	if err != nil || held != claimFree || path == slug {
		t.Fatalf("first choice = %q claim=%v err=%v, want a fresh directory other than %s", path, held, err, slug)
	}
	git(t, root, "worktree", "add", path, "-b", "fix-x")

	again, held, err := worktreePath(ctx, root, "fix-x", slug)
	if err != nil || held != claimOurs || again != path {
		t.Fatalf("retry = %q claim=%v err=%v, want to reuse %s", again, held, err, path)
	}
	owner, held, err := worktreePath(ctx, root, "fix/x", slug)
	if err != nil || held != claimOurs || owner != slug {
		t.Fatalf("slug owner = %q claim=%v err=%v, want to reuse %s", owner, held, err, slug)
	}

	// Once the branch that pushed fix-x aside is gone the slug is free again,
	// but fix-x is still checked out on the hashed path, and Git would refuse
	// to check it out a second time at the slug.
	git(t, root, "worktree", "remove", slug)
	moved, held, err := worktreePath(ctx, root, "fix-x", slug)
	if err != nil || held != claimOurs || moved != path {
		t.Fatalf("after the slug freed = %q claim=%v err=%v, want to reuse %s", moved, held, err, path)
	}
}

// A worktree add that dies partway leaves its worktree registered, on the
// right branch, under Git's "initializing" lock. That checkout is not finished
// and is never handed to a thread: it is removed and made again.
func TestManagedWorktreeRemakesACheckoutWhoseAddDied(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	slug := filepath.Join(root, ".worktrees", "issue-11-crashed")
	git(t, root, "worktree", "add", slug, "-b", "issue/11-crashed")
	git(t, root, "worktree", "lock", "--reason", "initializing", slug)
	leftover := filepath.Join(slug, "HALF-WRITTEN")
	if err := os.WriteFile(leftover, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := ready(t, st, managedThread(t, mgr, p.ID, "issue/11-crashed", ""))
	if m.Cwd != slug {
		t.Fatalf("worktree at %s, want it remade at %s", m.Cwd, slug)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("the unfinished checkout was reused: %s still there (%v)", leftover, err)
	}
	if w, ok := registeredAt(t, root, slug); !ok || w.Locked || w.Branch != "issue/11-crashed" {
		t.Fatalf("remade worktree = %+v registered=%v, want an unlocked worktree on issue/11-crashed", w, ok)
	}
}

// Prune skips locked worktrees, so a dead add whose directory is already gone
// stays registered. It must not wedge the path: the thread still gets its
// worktree there.
func TestManagedWorktreeRecoversFromADeadAddWhoseDirectoryIsGone(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	slug := filepath.Join(root, ".worktrees", "issue-13-vanished")
	git(t, root, "worktree", "add", slug, "-b", "issue/13-vanished")
	git(t, root, "worktree", "lock", "--reason", "initializing", slug)
	if err := os.RemoveAll(slug); err != nil {
		t.Fatal(err)
	}

	m := ready(t, st, managedThread(t, mgr, p.ID, "issue/13-vanished", ""))
	if m.Cwd != slug {
		t.Fatalf("worktree at %s, want it made at %s", m.Cwd, slug)
	}
	if w, ok := registeredAt(t, root, slug); !ok || w.Locked {
		t.Fatalf("worktree = %+v registered=%v, want an unlocked worktree", w, ok)
	}
}

// A lock the user put on a worktree is theirs. It is not mistaken for a dead
// add, and the worktree under it is reused untouched.
func TestManagedWorktreeReusesAWorktreeTheUserLocked(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	slug := filepath.Join(root, ".worktrees", "issue-12-locked")
	git(t, root, "worktree", "add", slug, "-b", "issue/12-locked")
	git(t, root, "worktree", "lock", "--reason", "on a usb drive", slug)
	work := filepath.Join(slug, "WORK")
	if err := os.WriteFile(work, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := ready(t, st, managedThread(t, mgr, p.ID, "issue/12-locked", ""))
	if m.Cwd != slug {
		t.Fatalf("worktree at %s, want the locked one at %s reused", m.Cwd, slug)
	}
	if b, err := os.ReadFile(work); err != nil || string(b) != "mine\n" {
		t.Fatalf("the user's file was touched: %q %v", b, err)
	}
}

// When both the slug and the hashed fallback are held by something else there
// is nowhere safe to go, and the answer is an error rather than either one.
func TestWorktreePathFailsWhenBothChoicesAreTaken(t *testing.T) {
	root, _, _ := gitRepo(t)
	ctx := context.Background()
	slug := filepath.Join(root, ".worktrees", "fix-x")
	git(t, root, "worktree", "add", slug, "-b", "fix/x")
	hashed, _, err := worktreePath(ctx, root, "fix-x", slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hashed, 0o755); err != nil {
		t.Fatal(err)
	}
	if path, held, err := worktreePath(ctx, root, "fix-x", slug); err == nil {
		t.Fatalf("got %q claim=%v, want an error", path, held)
	}
}

// A plain directory sitting where the worktree would go is not a worktree,
// however much it looks like one. The thread gets a real checkout elsewhere
// and the directory is left exactly as it was.
func TestManagedWorktreeRefusesToAdoptAPlainDirectory(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	squatter := filepath.Join(root, ".worktrees", "issue-7-plain")
	if err := os.MkdirAll(squatter, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(squatter, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := ready(t, st, managedThread(t, mgr, p.ID, "issue/7-plain", ""))
	if resolve(m.Cwd) == resolve(squatter) {
		t.Fatalf("the thread adopted the plain directory %s", squatter)
	}
	if w, ok := registeredAt(t, root, m.Cwd); !ok || w.Branch != "issue/7-plain" {
		t.Fatalf("%s is not a worktree on issue/7-plain: %+v", m.Cwd, w)
	}
	entries, err := os.ReadDir(squatter)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "mine\n" || len(entries) != 1 {
		t.Fatalf("the plain directory was touched: %d entries, notes=%q err=%v", len(entries), b, err)
	}
}

func failCheckouts(t *testing.T, root string) (restore func()) {
	t.Helper()
	hook := filepath.Join(root, ".git", "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(hook); err != nil {
			t.Fatal(err)
		}
	}
}

// A failing post-checkout hook makes "worktree add -b" exit non-zero after it
// has created the branch and registered the worktree. Both are undone, so the
// retry is not stopped by "branch already exists".
func TestFailedWorktreeAddRemovesTheBranchItCreated(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	ctx := context.Background()

	restore := failCheckouts(t, root)
	failed := settled(t, st, managedThread(t, mgr, p.ID, "issue/8-hooked", ""))
	if failed.Phase != "provision_failed" {
		t.Fatalf("phase %q, want provision_failed", failed.Phase)
	}
	if hasLocalBranch(ctx, root, "issue/8-hooked") {
		t.Fatal("the failed add left its branch behind")
	}
	for _, w := range registeredWorktrees(t, root) {
		if w.Branch == "issue/8-hooked" {
			t.Fatalf("the failed add left a registered worktree at %s", w.Path)
		}
	}

	restore()
	m := ready(t, st, managedThread(t, mgr, p.ID, "issue/8-hooked", ""))
	if got := git(t, m.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); got != "issue/8-hooked" {
		t.Fatalf("retry is on %q, want issue/8-hooked", got)
	}
}

// Only a branch the attempt itself created is deleted. One that was there
// before holds someone's work. The worktree the failed add registered goes
// either way, or the retry would adopt it as if it had finished.
func TestFailedWorktreeAddKeepsABranchThatAlreadyExisted(t *testing.T) {
	root, _, _ := gitRepo(t)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	git(t, root, "branch", "issue/9-existing")
	want := git(t, root, "rev-parse", "issue/9-existing")
	failCheckouts(t, root)

	failed := settled(t, st, managedThread(t, mgr, p.ID, "issue/9-existing", ""))
	if failed.Phase != "provision_failed" {
		t.Fatalf("phase %q, want provision_failed", failed.Phase)
	}
	if got := git(t, root, "rev-parse", "refs/heads/issue/9-existing"); got != want {
		t.Fatalf("existing branch is at %s, want %s", got, want)
	}
	for _, w := range registeredWorktrees(t, root) {
		if w.Branch == "issue/9-existing" {
			t.Fatalf("the failed add left a registered worktree at %s", w.Path)
		}
	}
}

// Two threads provisioning at once in one repository, both on a base that only
// exists on the remote, so both fetch, both create the tracking branch and both
// add a worktree. The repository lock is what lets both succeed.
func TestConcurrentManagedWorktreesInOneRepositoryBothSucceed(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	branches := []string{"issue/10-left", "issue/10-right"}
	ids := make([]string, len(branches))
	var wg sync.WaitGroup
	for i, branch := range branches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID, Workspace: "managed", Branch: branch, BaseRef: "staging"})
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = a.ID
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}

	seen := map[string]bool{}
	for i, id := range ids {
		m := settled(t, st, id)
		if m.Phase != "ready" {
			t.Fatalf("%s: phase %q, want ready", branches[i], m.Phase)
		}
		if seen[resolve(m.Cwd)] {
			t.Fatalf("%s shares %s with another thread", branches[i], m.Cwd)
		}
		seen[resolve(m.Cwd)] = true
		if got := git(t, m.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); got != branches[i] {
			t.Fatalf("%s is on %q", m.Cwd, got)
		}
		if got := git(t, m.Cwd, "rev-parse", "HEAD"); got != want {
			t.Fatalf("%s starts at %s, want the remote base %s", branches[i], got, want)
		}
	}
}
