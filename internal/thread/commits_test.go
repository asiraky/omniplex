package thread

import (
	"context"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// commitsFixture is a thread on a branch with three commits of its own, cut
// from main after main's root commit, with main moving on afterwards. It
// returns the root commit and the branch's commits oldest first.
func commitsFixture(t *testing.T) (mgr *Manager, id, rootSHA string, own []string) {
	t.Helper()
	root, worktree, branch := gitRepo(t)
	sha := func(dir string) string {
		raw, err := runGit(context.Background(), dir, "rev-parse", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(raw))
	}
	rootSHA = sha(root)
	write(t, root, "main-only.txt", "later\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "main moves on")

	steps := []map[string]string{
		{"a.txt": "a\n"},
		{"a.txt": "a\nmore\n", "b.txt": "b\nb\n"},
		{"b.txt": "b\n"},
	}
	for i, files := range steps {
		for name, body := range files {
			write(t, worktree, name, body)
		}
		gitRun(t, worktree, "add", ".")
		gitRun(t, worktree, "commit", "-m", "step "+string(rune('1'+i)))
		own = append(own, sha(worktree))
	}

	st, p := testProject(t, root)
	mgr = newManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	meta := store.ThreadMeta{ID: "c1", Cwd: worktree, Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle", ProjectID: p.ID, Branch: branch, WorkspaceMode: "borrowed"}
	if err := st.CreateThread(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	return mgr, "c1", rootSHA, own
}

func summary(files []ChangedFile) string {
	var parts []string
	for _, f := range files {
		parts = append(parts, f.Path+":"+f.Status[:1]+":"+string(rune('0'+f.Additions))+"/"+string(rune('0'+f.Deletions)))
	}
	return strings.Join(parts, " ")
}

func TestCommitChangesCoverExactlyTheChosenCommits(t *testing.T) {
	mgr, id, rootSHA, own := commitsFixture(t)
	ctx := context.Background()
	cases := []struct {
		name, from, to, want string
	}{
		{"single", own[1], own[1], "a.txt:m:1/0 b.txt:a:2/0"},
		{"range", own[1], own[2], "a.txt:m:1/0 b.txt:a:1/0"},
		// A root commit has no parent; it is compared with nothing at all.
		{"from the root", rootSHA, own[0], "README:a:1/0 a.txt:a:1/0"},
	}
	for _, c := range cases {
		changes, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffCommit, From: c.from, To: c.to})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if changes.Warning != "" || changes.From != c.from || changes.To != c.to || changes.Head != c.to {
			t.Fatalf("%s: range not recorded: %+v", c.name, changes)
		}
		if got := summary(changes.Files); got != c.want {
			t.Fatalf("%s: got %s, want %s", c.name, got, c.want)
		}
	}

	// A file read checks the range the client holds, as branch mode does.
	changes, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffCommit, From: own[1], To: own[1]})
	if err != nil {
		t.Fatal(err)
	}
	req := DiffRequest{Mode: DiffCommit, From: own[1], To: own[1], Base: changes.Base, Head: changes.Head}
	diff, err := mgr.ThreadFileDiff(ctx, id, "a.txt", req, PatchOptions{})
	if err != nil || !strings.Contains(diff.Patch, "+more") {
		t.Fatalf("commit patch wrong: %q %v", diff.Patch, err)
	}
	req.Base = rootSHA
	if _, err := mgr.ThreadFileDiff(ctx, id, "a.txt", req, PatchOptions{}); err == nil {
		t.Fatal("a stale range must be refused")
	}
}

func TestCommitChangesRefuseBadRanges(t *testing.T) {
	mgr, id, _, own := commitsFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"", "HEAD", own[0][:12], strings.ToUpper(own[0]), own[0][:38] + "..", own[0] + "^"} {
		if _, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffCommit, From: bad, To: own[2]}); err == nil {
			t.Fatalf("%q must be refused as a commit id", bad)
		}
	}
	if _, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffCommit, From: own[2], To: own[0]}); err == nil {
		t.Fatal("a range whose first commit comes after its last must be refused")
	}

	// Well-formed but gone, as after a rebase: explained, not an error.
	gone := strings.Repeat("a", 40)
	changes, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffCommit, From: gone, To: own[2]})
	if err != nil {
		t.Fatalf("vanished commits must not be an error: %v", err)
	}
	if changes.Warning == "" || len(changes.Files) != 0 || changes.From != gone {
		t.Fatalf("expected an explained empty list: %+v", changes)
	}
}

func TestThreadCommitsListsTheBranchsOwnNewestFirst(t *testing.T) {
	mgr, id, rootSHA, own := commitsFixture(t)
	got, err := mgr.ThreadCommits(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Warning != "" || got.BaseRef != "main" || got.Truncated || len(got.Commits) != 3 {
		t.Fatalf("unexpected list: %+v", got)
	}
	for i, c := range got.Commits {
		if c.SHA != own[2-i] || c.Subject != "step "+string(rune('3'-i)) {
			t.Fatalf("commit %d wrong: %+v", i, c)
		}
		if c.SHA == rootSHA {
			t.Fatal("the base branch's commits are not the branch's own")
		}
	}
}

// A side branch merged into the thread's branch is part of the merge, not a
// run of commits of its own: listing its commits would offer ranges that are
// not one line of history, and git would refuse them.
func TestThreadCommitsFollowTheBranchsOwnLine(t *testing.T) {
	mgr, id, _, own := commitsFixture(t)
	ctx := context.Background()
	meta, err := mgr.store.Thread(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, meta.Cwd, "checkout", "-q", "-b", "side", own[0])
	write(t, meta.Cwd, "side.txt", "side\n")
	gitRun(t, meta.Cwd, "add", ".")
	gitRun(t, meta.Cwd, "commit", "-m", "side work")
	gitRun(t, meta.Cwd, "checkout", "-q", "-")
	gitRun(t, meta.Cwd, "merge", "-q", "--no-ff", "-m", "merge side", "side")

	got, err := mgr.ThreadCommits(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, c := range got.Commits {
		subjects = append(subjects, c.Subject)
	}
	if strings.Join(subjects, ",") != "merge side,step 3,step 2,step 1" {
		t.Fatalf("listed %v", subjects)
	}
	// Every pair the list offers, older first, is a range git accepts; the
	// side branch's file arrives with the merge.
	list := got.Commits
	for i := range list {
		for j := 0; j <= i; j++ {
			changes, err := mgr.ThreadChanges(ctx, id, DiffRequest{Mode: DiffCommit, From: list[i].SHA, To: list[j].SHA})
			if err != nil {
				t.Fatalf("%s..%s refused: %v", list[i].Subject, list[j].Subject, err)
			}
			hasSide := strings.Contains(summary(changes.Files), "side.txt")
			if hasSide != (j == 0) {
				t.Fatalf("%s..%s: side.txt shown=%v: %s", list[i].Subject, list[j].Subject, hasSide, summary(changes.Files))
			}
		}
	}
}

func TestThreadCommitsCutsALongBranch(t *testing.T) {
	mgr, id, _, _ := commitsFixture(t)
	meta, err := mgr.store.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxBranchCommits; i++ {
		gitRun(t, meta.Cwd, "commit", "-q", "--allow-empty", "--allow-empty-message", "-m", "")
	}
	got, err := mgr.ThreadCommits(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Commits) != maxBranchCommits || got.Commits[0].Subject != "" || len(got.Commits[0].SHA) != 40 {
		t.Fatalf("expected a cut list of %d: truncated=%v len=%d first=%+v", maxBranchCommits, got.Truncated, len(got.Commits), got.Commits[0])
	}
}

func TestThreadCommitsExplainsACheckoutThatIsNotARepository(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	now := proto.NowMillis()
	if err := st.CreateThread(context.Background(), store.ThreadMeta{ID: "s2", Cwd: t.TempDir(), Harness: "fake", CreatedAt: now, UpdatedAt: now, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.ThreadCommits(context.Background(), "s2")
	if err != nil || got.Warning == "" || got.Commits == nil || len(got.Commits) != 0 {
		t.Fatalf("expected an explained empty list: %+v %v", got, err)
	}
}
