package thread

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// remoteOnlyBase builds the shape this whole file exists for: a fresh clone
// where "staging" is a real branch on origin and nothing at all locally. It
// returns the repository and the commit the remote branch points at.
func remoteOnlyBase(t *testing.T, root string) (remote, want string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "clone", "--bare", root, remote)
	git(t, root, "checkout", "-b", "staging")
	if err := os.WriteFile(filepath.Join(root, "STAGING"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "STAGING")
	git(t, root, "commit", "-m", "staging")
	want = git(t, root, "rev-parse", "staging")
	git(t, root, "checkout", "main")
	git(t, root, "remote", "add", "origin", remote)
	git(t, root, "push", "origin", "staging")
	// Leave the clone in the state a fresh one is in: the branch lives on the
	// remote and this repository has never heard of it, locally or as a
	// remote-tracking ref.
	git(t, root, "branch", "-D", "staging")
	git(t, root, "update-ref", "-d", "refs/remotes/origin/staging")
	return remote, want
}

// The bug this replaced: a project default of baseBranch "staging" in a fresh
// clone could not be seen at all, because it exists only as origin/staging.
func TestResolveBaseRefFetchesABranchThatOnlyExistsOnARemote(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)

	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "staging" || !res.Fetched || !res.Created {
		t.Fatalf("resolution = %+v, want a fetched, created local branch", res)
	}
	if got := git(t, root, "rev-parse", "refs/heads/staging"); got != want {
		t.Fatalf("local staging is %s, want the remote's commit %s", got, want)
	}
	if upstream := git(t, root, "rev-parse", "--abbrev-ref", "staging@{upstream}"); upstream != "origin/staging" {
		t.Fatalf("tracking upstream = %q, want origin/staging", upstream)
	}
}

// Already fetched is the common case on a warm clone: no network, just a name.
func TestResolveBaseRefTracksAnAlreadyFetchedRemoteBranch(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)
	git(t, root, "fetch", "origin")

	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "staging" || res.Fetched || !res.Created {
		t.Fatalf("resolution = %+v, want a created branch with no fetch", res)
	}
	if got := git(t, root, "rev-parse", "refs/heads/staging"); got != want {
		t.Fatalf("local staging is %s, want %s", got, want)
	}
}

// A user who names a remote ref meant that ref. It is used as written, and no
// local branch is invented behind their back.
func TestResolveBaseRefUsesAnExplicitRemoteRefAsIs(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteOnlyBase(t, root)
	git(t, root, "fetch", "origin")

	res, err := resolveBaseRef(context.Background(), root, "origin/staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "origin/staging" || res.Created || res.Fetched || res.FellBack {
		t.Fatalf("resolution = %+v, want origin/staging used verbatim", res)
	}
	if hasLocalBranch(context.Background(), root, "origin/staging") {
		t.Fatal("a local branch was created for an explicit remote ref")
	}
}

// The local branch is the one the user has been working on. It wins, and
// nothing goes to the network to second-guess it.
func TestResolveBaseRefPrefersALocalBranchOverTheRemote(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteOnlyBase(t, root)
	git(t, root, "branch", "staging", "main")
	want := git(t, root, "rev-parse", "staging")

	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "staging" || res.Created || res.Fetched || res.FellBack {
		t.Fatalf("resolution = %+v, want the local branch used as-is", res)
	}
	if got := git(t, root, "rev-parse", "staging"); got != want {
		t.Fatalf("the local branch moved: %s, want %s", got, want)
	}
	if hasRemoteRef(context.Background(), root, "origin/staging") {
		t.Fatal("a fetch happened even though the branch was already local")
	}
}

// Nowhere at all is a note, not a failure: the session still starts.
func TestResolveBaseRefFallsBackToTheDefaultBranch(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteOnlyBase(t, root)

	res, err := resolveBaseRef(context.Background(), root, "no/such/ref")
	if err != nil {
		t.Fatalf("a missing base must not fail provisioning: %v", err)
	}
	if res.Ref != "main" || !res.FellBack {
		t.Fatalf("resolution = %+v, want a fallback to main", res)
	}
	if !strings.Contains(res.Note, "no/such/ref") || !strings.Contains(res.Note, "main") {
		t.Fatalf("note %q names neither what was asked for nor what was used", res.Note)
	}
}

// An unreachable remote must not hang the server or become an error: it is one
// more place the base was not found.
func TestResolveBaseRefSurvivesAnUnreachableRemote(t *testing.T) {
	root, _, _ := gitRepo(t)
	git(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "definitely-not-here.git"))

	done := make(chan baseResolution, 1)
	go func() {
		res, err := resolveBaseRef(context.Background(), root, "staging")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		if res.Ref != "main" || !res.FellBack {
			t.Fatalf("resolution = %+v, want a fallback to main", res)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("resolving against an unreachable remote hung")
	}
}

// A ref that would be read as a flag by `git worktree add` never reaches git —
// but it does not fail the session either. It falls back and says so, like
// every other base that cannot be resolved.
func TestResolveBaseRefFallsBackFromAFlagLikeRef(t *testing.T) {
	root, _, _ := gitRepo(t)
	res, err := resolveBaseRef(context.Background(), root, "--upload-pack=touch /tmp/x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(res.Ref, "-") {
		t.Fatalf("resolution = %+v, want a ref git cannot read as an option", res)
	}
	if !res.FellBack || res.Note == "" {
		t.Fatalf("resolution = %+v, want a fallback carrying a note", res)
	}
}

func TestResolveBaseRefWithNoBaseIsHEAD(t *testing.T) {
	root, _, _ := gitRepo(t)
	res, err := resolveBaseRef(context.Background(), root, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "HEAD" || res.Note != "" {
		t.Fatalf("resolution = %+v, want a quiet HEAD", res)
	}
}

// The bug in the field: a folder default of baseBranch "staging" in a clone
// that only has origin/staging used to hard-fail every thread it touched.
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
	if got := git(t, meta.Cwd, "rev-parse", "HEAD"); got != want {
		t.Fatalf("worktree HEAD %s, want the remote branch %s", got, want)
	}
	if git(t, root, "rev-parse", "refs/heads/staging") != want {
		t.Fatal("no local tracking branch was created for the remote base")
	}
	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Workspace.Output, "note: ") || !strings.Contains(state.Workspace.Output, "staging") {
		t.Fatalf("the fetch was not reported on the workspace card: %q", state.Workspace.Output)
	}
}

// A thread branch named after its own remote-only base: resolving the base
// creates the local branch, so the worktree must check it out rather than try
// to create it a second time.
func TestManagedWorktreeWhoseBranchIsItsRemoteOnlyBase(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)
	st, p := testProject(t, root)
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "staging", BaseRef: "staging",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got := git(t, meta.Cwd, "rev-parse", "HEAD"); got != want {
		t.Fatalf("worktree HEAD %s, want the remote branch %s", got, want)
	}
	if got := git(t, meta.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); got != "staging" {
		t.Fatalf("worktree is on %q, want staging", got)
	}
}

// A compatibility hook predates every flag omniplex passes. It gets the branch
// and only the branch — passing --base into its argv broke real scripts — and
// reads the resolved base from the environment if it wants it.
func TestCompatibilityHookGetsOneArgAndTheBaseInTheEnvironment(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteOnlyBase(t, root)
	// The hook reports through a file rather than its output: what reaches
	// the workspace card is not what is under test here.
	seen := filepath.Join(t.TempDir(), "seen")
	hook := filepath.Join(root, "worktree-setup.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'argc:%s\\narg1:%s\\nbase:%s\\n' \"$#\" \"$1\" \"$OMNIPLEX_BASE_REF\" > '"+seen+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, p := testProject(t, root)
	p.Defaults.Workspace = "managed"
	p.Folders[0].BaseBranch = "staging"
	p.Folders[0].Provision = "worktree-setup.sh"
	if err := putProject(context.Background(), st, p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, a.ID)
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "argc:1\n") || !strings.Contains(got, "arg1:feature/omniplex-") {
		t.Fatalf("compatibility hook argv was not just the branch: %q", got)
	}
	// The hook sees the resolved local branch, which only exists because the
	// resolver fetched and tracked it before the hook ran.
	if !strings.Contains(got, "base:staging\n") {
		t.Fatalf("OMNIPLEX_BASE_REF did not reach the hook: %q", got)
	}
	if !hasLocalBranch(context.Background(), root, "staging") {
		t.Fatal("the base the hook was handed does not exist locally")
	}
}
