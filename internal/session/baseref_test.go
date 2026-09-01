package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
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

// A compatibility hook predates every flag omniplex passes. It gets the branch
// and only the branch — passing --base into its argv broke real scripts — and
// reads the base from the environment if it wants it.
func TestCompatibilityHookGetsOneArgAndTheBaseInTheEnvironment(t *testing.T) {
	root, _, _ := gitRepo(t)
	hook := filepath.Join(root, "worktree-setup.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho argc:$#\necho arg1:$1\necho base:$OMNIPLEX_BASE_REF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "compat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := proto.NowMillis()
	p := project.Project{ID: "compat", Root: root, CreatedAt: now, UpdatedAt: now, Config: project.DefaultConfig(root)}
	p.Config.Defaults.Harness = "fake"
	p.Config.Defaults.Workspace = "managed"
	p.Config.Defaults.BaseBranch = "main"
	p.Config.Workspace.Provision = "worktree-setup.sh"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, a.ID)
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Workspace.Output, "argc:1") {
		t.Fatalf("compatibility hook argv was not just the branch: %q", s.Workspace.Output)
	}
	if !strings.Contains(s.Workspace.Output, "base:main") {
		t.Fatalf("OMNIPLEX_BASE_REF did not reach the hook: %q", s.Workspace.Output)
	}
}
