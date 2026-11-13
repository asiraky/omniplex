package thread

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A branch's commits are listed so a reviewer can step through how the work
// got where it is, one commit or a run of them at a time, rather than only
// ever seeing the branch as one lump.

// Enough history to step through by hand; a branch longer than this is read
// as a whole, or as a pull request.
const maxBranchCommits = 50

// BranchCommit is one commit on the thread's branch.
type BranchCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	// Time is the committer's time in unix seconds: when the commit took its
	// place on the branch, which is the order the list is in.
	Time int64 `json:"time"`
}

// BranchCommits is the branch's own commits, newest first.
type BranchCommits struct {
	Commits   []BranchCommit `json:"commits"`
	BaseRef   string         `json:"baseRef,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	// Warning explains an empty list that is not simply "no commits yet on
	// this branch" — a checkout that is not a repository, most often.
	Warning string `json:"warning,omitempty"`
}

// ThreadCommits lists the commits the thread's branch has that its base branch
// does not, found the same way the branch comparison finds its base. With no
// base branch at all, the whole of HEAD's history is the branch's own.
func (m *Manager) ThreadCommits(ctx context.Context, threadID string) (BranchCommits, error) {
	out := BranchCommits{Commits: []BranchCommit{}}
	meta, err := m.store.Thread(ctx, threadID)
	if err != nil {
		return BranchCommits{}, err
	}
	if meta.Cwd == "" {
		out.Warning = "this thread has no checkout"
		return out, nil
	}
	top, err := runGit(ctx, meta.Cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		out.Warning = "this thread's directory is not a Git repository"
		return out, nil
	}
	root := strings.TrimSpace(string(top))
	if _, err := runGit(ctx, root, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		out.Warning = "this checkout has no commits yet"
		return out, nil
	}

	span := "HEAD"
	if ref, base := m.branchBase(ctx, root, meta); base != "" {
		out.BaseRef, span = ref, base+"..HEAD"
	}
	// Fields and commits are both NUL-separated, so a subject or a name can
	// hold anything a commit message can and still parse. One past the cap is
	// asked for so a cut list knows it was cut.
	raw, err := runGit(ctx, root, "log", "-z", "--format=%H%x00%s%x00%an%x00%ct", "--max-count="+strconv.Itoa(maxBranchCommits+1), span, "--")
	if err != nil {
		return BranchCommits{}, err
	}
	out.Commits = parseBranchLog(string(raw))
	if len(out.Commits) > maxBranchCommits {
		out.Commits, out.Truncated = out.Commits[:maxBranchCommits], true
	}
	return out, nil
}

// parseBranchLog reads the log format above: four fields per commit. Fields
// are split positionally rather than by dropping empties, since a commit made
// with an empty message has an empty subject.
func parseBranchLog(raw string) []BranchCommit {
	fields := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	out := []BranchCommit{}
	for i := 0; i+3 < len(fields); i += 4 {
		when, _ := strconv.ParseInt(fields[i+3], 10, 64)
		out = append(out, BranchCommit{SHA: fields[i], Subject: fields[i+1], Author: fields[i+2], Time: when})
	}
	return out
}

// validCommitSHA admits a full object id and nothing else: forty hex digits
// for SHA-1, sixty-four for SHA-256.
func validCommitSHA(sha string) error {
	if len(sha) != 40 && len(sha) != 64 {
		return fmt.Errorf("%q is not a commit id", sha)
	}
	for _, r := range sha {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return fmt.Errorf("%q is not a commit id", sha)
		}
	}
	return nil
}

// commitScope compares the parent of from with to, which is every change the
// commits from..to made, from included. Commits that have gone, rebased or
// garbage-collected away, are an ordinary answer, like a turn's lost
// snapshots; a pair that is not one run of history is the client's mistake.
func commitScope(ctx context.Context, scope diffScope, from, to string) (diffScope, error) {
	scope.from, scope.to = from, to
	oids := make([]string, 0, 2)
	for _, sha := range []string{from, to} {
		raw, err := runGit(ctx, scope.root, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
		if err != nil {
			scope.warning = "these commits are no longer in this checkout"
			return scope, nil
		}
		oids = append(oids, strings.TrimSpace(string(raw)))
	}
	if _, err := runGit(ctx, scope.root, "merge-base", "--is-ancestor", oids[0], oids[1]); err != nil {
		return diffScope{}, errors.New("the first commit of a range must come before its last")
	}
	scope.head = oids[1]
	if parent, err := runGit(ctx, scope.root, "rev-parse", "--verify", "--quiet", oids[0]+"^1"); err == nil {
		scope.base = strings.TrimSpace(string(parent))
		return scope, nil
	}
	// A root commit has no parent, so it is compared with the empty tree. Its
	// id depends on the repository's hash, so git is asked for it rather than
	// it being written down here.
	empty, err := runGit(ctx, scope.root, "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return diffScope{}, err
	}
	scope.base = strings.TrimSpace(string(empty))
	return scope, nil
}
