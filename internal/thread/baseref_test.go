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

// A local branch holding work the remote does not have — ahead of it, or
// diverged from it — is the one the user has been working on. It wins, quietly,
// even though the remote was asked, and it is not moved.
func TestResolveBaseRefPrefersALocalBranchOverTheRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent func(t *testing.T, root, remoteTip string) string
	}{
		{"ahead", func(t *testing.T, root, remoteTip string) string { return remoteTip }},
		{"diverged", func(t *testing.T, root, remoteTip string) string { return git(t, root, "rev-parse", "main") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := gitRepo(t)
			_, remoteTip := remoteOnlyBase(t, root)
			parent := tc.parent(t, root, remoteTip)
			local := git(t, root, "commit-tree", "-p", parent, "-m", "local work", parent+"^{tree}")
			git(t, root, "branch", "staging", local)

			res, err := resolveBaseRef(context.Background(), root, "staging")
			if err != nil {
				t.Fatal(err)
			}
			if res.Ref != "staging" || res.Created || res.Fetched || res.FellBack || res.Note != "" {
				t.Fatalf("resolution = %+v, want the local branch used as-is and quietly", res)
			}
			if got := git(t, root, "rev-parse", "staging"); got != local {
				t.Fatalf("the local branch moved: %s, want %s", got, local)
			}
			if got := git(t, root, "rev-parse", "origin/staging"); got != remoteTip {
				t.Fatalf("origin/staging is %s, want the remote's %s: the remote was not asked", got, remoteTip)
			}
		})
	}
}

// localBehindRemote is the issue's shape: a local staging made a while ago and
// never pulled, while origin/staging moved on. The clone's remote-tracking ref
// is stale — it still points where the local branch does.
func localBehindRemote(t *testing.T, root string) (remoteTip, local string) {
	t.Helper()
	_, remoteTip = remoteOnlyBase(t, root)
	local = git(t, root, "rev-parse", "main")
	git(t, root, "branch", "staging", local)
	git(t, root, "update-ref", "refs/remotes/origin/staging", local)
	return remoteTip, local
}

// A local branch that is merely stale starts the worktree from the remote, so
// a new thread does not begin behind origin — but the local branch, which may
// be checked out in the user's own checkout, is left exactly where it was.
func TestResolveBaseRefBranchesFromARemoteThatIsAhead(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteTip, local := localBehindRemote(t, root)

	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "origin/staging" || res.FellBack || res.Note == "" {
		t.Fatalf("resolution = %+v, want origin/staging with a note", res)
	}
	if got := git(t, root, "rev-parse", res.Ref); got != remoteTip {
		t.Fatalf("%s resolves to %s, want the remote's new commit %s", res.Ref, got, remoteTip)
	}
	if got := git(t, root, "rev-parse", "refs/heads/staging"); got != local {
		t.Fatalf("the local branch moved: %s, want %s", got, local)
	}
}

// The remote compared with is the branch's configured upstream, not whatever
// shares its name on origin.
func TestResolveBaseRefFollowsTheConfiguredUpstream(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, remoteTip := remoteOnlyBase(t, root)
	local := git(t, root, "rev-parse", "main")
	git(t, root, "branch", "stage", local)
	git(t, root, "config", "branch.stage.remote", "origin")
	git(t, root, "config", "branch.stage.merge", "refs/heads/staging")

	res, err := resolveBaseRef(context.Background(), root, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "origin/staging" {
		t.Fatalf("resolution = %+v, want the upstream origin/staging", res)
	}
	if got := git(t, root, "rev-parse", res.Ref); got != remoteTip {
		t.Fatalf("%s resolves to %s, want %s", res.Ref, got, remoteTip)
	}
	if got := git(t, root, "rev-parse", "refs/heads/stage"); got != local {
		t.Fatalf("the local branch moved: %s, want %s", got, local)
	}
}

// Offline, the local branch is used: the refresh is best-effort, and an
// unreachable remote is neither an error nor a wait.
func TestResolveBaseRefUsesTheLocalBranchWhenTheRemoteIsUnreachable(t *testing.T) {
	root, _, _ := gitRepo(t)
	git(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "definitely-not-here.git"))
	git(t, root, "branch", "staging", "main")

	start := time.Now()
	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "staging" || res.FellBack {
		t.Fatalf("resolution = %+v, want the local branch", res)
	}
	if took := time.Since(start); took >= baseRefreshTimeout {
		t.Fatalf("resolving took %s, want a fast failure, not a wait on the timeout", took)
	}
}

// Offline with a tracking ref that is already ahead: the stale remote ref is
// still fresher than the local branch, so it is used.
func TestResolveBaseRefUsesAStaleRemoteRefThatIsAheadWhenOffline(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteTip, local := localBehindRemote(t, root)
	git(t, root, "update-ref", "refs/remotes/origin/staging", remoteTip)
	git(t, root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "definitely-not-here.git"))

	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "origin/staging" || git(t, root, "rev-parse", res.Ref) != remoteTip {
		t.Fatalf("resolution = %+v, want origin/staging at %s", res, remoteTip)
	}
	if got := git(t, root, "rev-parse", "refs/heads/staging"); got != local {
		t.Fatalf("the local branch moved: %s, want %s", got, local)
	}
}

// An explicit remote ref the clone has never fetched is fetched, not dropped
// for the default branch.
func TestResolveBaseRefFetchesAnUnseenExplicitRemoteRef(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, remoteTip := remoteOnlyBase(t, root)

	res, err := resolveBaseRef(context.Background(), root, "origin/staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "origin/staging" || res.FellBack || res.Created {
		t.Fatalf("resolution = %+v, want origin/staging used verbatim", res)
	}
	if got := git(t, root, "rev-parse", res.Ref); got != remoteTip {
		t.Fatalf("%s resolves to %s, want %s", res.Ref, got, remoteTip)
	}
}

// A remote-tracking ref that is here but stale is refreshed before the local
// branch is made from it, so the new branch starts at the remote's tip.
func TestResolveBaseRefRefreshesAStaleRemoteRefBeforeTrackingIt(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, remoteTip := remoteOnlyBase(t, root)
	git(t, root, "update-ref", "refs/remotes/origin/staging", git(t, root, "rev-parse", "main"))

	res, err := resolveBaseRef(context.Background(), root, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "staging" || !res.Created || res.Fetched {
		t.Fatalf("resolution = %+v, want a created branch that was found, not discovered", res)
	}
	if got := git(t, root, "rev-parse", "refs/heads/staging"); got != remoteTip {
		t.Fatalf("local staging is %s, want the remote's tip %s", got, remoteTip)
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

// onAFeatureBranch moves the main checkout onto a branch of its own with a
// commit main does not have, the way a user's checkout usually is. A base that
// came from HEAD would then land on this commit, not on any version of main.
func onAFeatureBranch(t *testing.T, root string) (head string) {
	t.Helper()
	git(t, root, "checkout", "-b", "feature/mine")
	if err := os.WriteFile(filepath.Join(root, "MINE"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "MINE")
	git(t, root, "commit", "-m", "mine")
	return git(t, root, "rev-parse", "HEAD")
}

// withOrigin gives the repository an origin that is a copy of it and fetches
// it, so origin/main is here and level with main. withHead also records
// origin's default branch, as a clone does; without it the default branch is
// found by name.
func withOrigin(t *testing.T, root string, withHead bool) (remote string) {
	t.Helper()
	remote = filepath.Join(t.TempDir(), "origin.git")
	git(t, root, "clone", "--bare", root, remote)
	git(t, root, "remote", "add", "origin", remote)
	git(t, root, "fetch", "origin")
	if withHead {
		git(t, root, "remote", "set-head", "origin", "main")
	}
	return remote
}

// defaultBehindRemote is the issue's shape for an empty base: someone pushed
// to origin's main since this clone last fetched, and local main was never
// pulled. Pushing updates the clone's origin/main too, so it is put back where
// a clone that never fetched would have it: neither it nor local main has seen
// the new commit.
func defaultBehindRemote(t *testing.T, root string, withHead bool) (remoteTip, local string) {
	t.Helper()
	withOrigin(t, root, withHead)
	local = git(t, root, "rev-parse", "main")
	remoteTip = git(t, root, "commit-tree", "-p", local, "-m", "pushed by someone else", local+"^{tree}")
	git(t, root, "push", "origin", remoteTip+":refs/heads/main")
	git(t, root, "update-ref", "refs/remotes/origin/main", local)
	return remoteTip, local
}

// No base means the default branch, freshened like a named one: a thread does
// not start from whatever the checkout is on, nor from a main that is behind
// origin. Local main is not moved. With origin/HEAD absent, main is still found
// by name and still compared with origin/main.
func TestResolveBaseRefWithNoBaseBranchesFromARemoteDefaultThatIsAhead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		base     string
		withHead bool
		feature  bool
	}{
		{"origin HEAD", "", true, false},
		{"origin HEAD, checkout on a feature branch", "  ", true, true},
		{"no origin HEAD, checkout on a feature branch", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := gitRepo(t)
			remoteTip, local := defaultBehindRemote(t, root, tc.withHead)
			head := git(t, root, "rev-parse", "HEAD")
			if tc.feature {
				head = onAFeatureBranch(t, root)
			}

			res, err := resolveBaseRef(context.Background(), root, tc.base)
			if err != nil {
				t.Fatal(err)
			}
			if res.FellBack || res.Note == "" || !strings.Contains(res.Note, "main") {
				t.Fatalf("resolution = %+v, want the remote's main with a note naming it", res)
			}
			if got := git(t, root, "rev-parse", res.Ref); got != remoteTip {
				t.Fatalf("%s resolves to %s, want origin's new commit %s", res.Ref, got, remoteTip)
			}
			if got := git(t, root, "rev-parse", "refs/heads/main"); got != local {
				t.Fatalf("local main moved: %s, want %s", got, local)
			}
			if got := git(t, root, "rev-parse", "HEAD"); got != head {
				t.Fatalf("the checkout moved: %s, want %s", got, head)
			}
		})
	}
}

// A local main level with origin, or ahead of it, is the one to use, and
// quietly — even while the checkout is on another branch.
func TestResolveBaseRefWithNoBaseUsesALocalMainThatIsNotBehind(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ahead bool
	}{
		{"level", false},
		{"ahead", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := gitRepo(t)
			withOrigin(t, root, true)
			if tc.ahead {
				parent := git(t, root, "rev-parse", "main")
				git(t, root, "update-ref", "refs/heads/main", git(t, root, "commit-tree", "-p", parent, "-m", "local work", parent+"^{tree}"))
			}
			want := git(t, root, "rev-parse", "main")
			remoteMain := git(t, root, "rev-parse", "origin/main")
			onAFeatureBranch(t, root)

			res, err := resolveBaseRef(context.Background(), root, "")
			if err != nil {
				t.Fatal(err)
			}
			if res.Ref != "main" || res.FellBack || res.Note != "" {
				t.Fatalf("resolution = %+v, want local main, quietly", res)
			}
			if got := git(t, root, "rev-parse", "main"); got != want {
				t.Fatalf("local main moved: %s, want %s", got, want)
			}
			if got := git(t, root, "rev-parse", "origin/main"); got != remoteMain {
				t.Fatalf("origin/main is %s, want %s: the remote did not move", got, remoteMain)
			}
		})
	}
}

// A repository with no remote and no main or master has no default branch to
// name, so an empty base is the checkout's HEAD, as it always was, and quiet.
func TestResolveBaseRefWithNoBaseAndNoDefaultBranchIsHEAD(t *testing.T) {
	root, _, _ := gitRepo(t)
	git(t, root, "branch", "-m", "main", "trunk")

	res, err := resolveBaseRef(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "HEAD" || res.Note != "" || res.FellBack {
		t.Fatalf("resolution = %+v, want a quiet HEAD", res)
	}
}

// Offline, an empty base is local main: the refresh is best-effort, and an
// unreachable remote is neither an error nor a wait.
func TestResolveBaseRefWithNoBaseUsesLocalMainWhenTheRemoteIsUnreachable(t *testing.T) {
	root, _, _ := gitRepo(t)
	git(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "definitely-not-here.git"))
	onAFeatureBranch(t, root)

	start := time.Now()
	res, err := resolveBaseRef(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "main" || res.FellBack || res.Note != "" {
		t.Fatalf("resolution = %+v, want local main, quietly", res)
	}
	if took := time.Since(start); took >= baseRefreshTimeout {
		t.Fatalf("resolving took %s, want a fast failure, not a wait on the timeout", took)
	}
}

// Upstream renamed its default branch and the old one was pruned, but the
// clone's origin/HEAD still names it. Neither the dead ref — the worktree add
// would fail on it — nor the leftover local copy of the old default may become
// the base, so the default is found by name instead.
func TestResolveBaseRefWithNoBaseSkipsADanglingOriginHEAD(t *testing.T) {
	for _, tc := range []struct {
		name          string
		leftoverLocal bool
	}{
		{"no local copy of the old default", false},
		{"a leftover local copy of the old default", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := gitRepo(t)
			withOrigin(t, root, false)
			git(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
			if tc.leftoverLocal {
				git(t, root, "branch", "master", "main")
			}

			res, err := resolveBaseRef(context.Background(), root, "")
			if err != nil {
				t.Fatal(err)
			}
			if res.Ref != "main" || res.FellBack {
				t.Fatalf("resolution = %+v, want main, found by name", res)
			}
		})
	}
}

// The bug in the field: a folder default of baseBranch "staging" in a clone
// that only has origin/staging used to hard-fail every thread it touched.
func TestManagedWorktreeBranchesFromARemoteOnlyBaseBranch(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)
	st, p := testProject(t, root)
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
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

// End to end: a thread based on a stale local staging starts at origin's tip.
func TestManagedWorktreeBranchesFromARemoteThatIsAhead(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteTip, local := localBehindRemote(t, root)
	st, p := testProject(t, root)
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/9-on-staging", BaseRef: "staging",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got := git(t, meta.Cwd, "rev-parse", "HEAD"); got != remoteTip {
		t.Fatalf("worktree HEAD %s, want the remote's newer commit %s", got, remoteTip)
	}
	if got := git(t, root, "rev-parse", "refs/heads/staging"); got != local {
		t.Fatalf("the local branch moved: %s, want %s", got, local)
	}
}

// End to end, the issue itself: a thread on "Default" in a folder with no base
// branch starts at origin's main, not at the checkout's feature branch and not
// at a stale local main, and the workspace card says so.
func TestManagedWorktreeWithNoBaseBranchesFromARemoteDefaultThatIsAhead(t *testing.T) {
	root, _, _ := gitRepo(t)
	remoteTip, local := defaultBehindRemote(t, root, true)
	onAFeatureBranch(t, root)
	st, p := testProject(t, root)
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()

	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/9-on-default",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got := git(t, meta.Cwd, "rev-parse", "HEAD"); got != remoteTip {
		t.Fatalf("worktree HEAD %s, want origin's newer main %s", got, remoteTip)
	}
	if got := git(t, root, "rev-parse", "refs/heads/main"); got != local {
		t.Fatalf("local main moved: %s, want %s", got, local)
	}
	// Started from origin/main is not pushing to it: a bare push from the
	// thread's branch must not be able to land on main.
	if upstream, ok := gitCapture(context.Background(), meta.Cwd, "rev-parse", "--abbrev-ref", "HEAD@{upstream}"); ok {
		t.Fatalf("thread branch tracks %q, want no upstream", upstream)
	}
	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Workspace.Output, "note: ") || !strings.Contains(state.Workspace.Output, "main") {
		t.Fatalf("branching from origin's main was not reported on the workspace card: %q", state.Workspace.Output)
	}
}

// A thread branch named after its own remote-only base: resolving the base
// creates the local branch, so the worktree must check it out rather than try
// to create it a second time.
func TestManagedWorktreeWhoseBranchIsItsRemoteOnlyBase(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, want := remoteOnlyBase(t, root)
	st, p := testProject(t, root)
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
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
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
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
