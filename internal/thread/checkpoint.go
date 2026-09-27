package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// What one turn changed is a different question from what the session changed,
// and Git cannot answer it after the fact: by the time a turn ends, the only
// record of where it started is gone. So each turn is bracketed by a snapshot.
//
// A snapshot is a real commit written to a hidden ref, built through a
// throwaway index so the person's own staged work is never touched. Two of them
// bound a turn, and the diff between them is what that turn did — formatters,
// codemods and stray `sed` included, because it asks the worktree rather than
// the event log.

// checkpointRefPrefix keeps snapshots out of `git branch`, `git log` and every
// other place a person looks, while still being ordinary refs that `git diff`
// and `git restore` understand.
const checkpointRefPrefix = "refs/omniplex/checkpoints"

// A turn diff is bounded like any other: a diff nobody can read is not worth
// the memory to hold it.
const maxTurnDiffFiles = 500

// TurnChanges is what one turn did, as the file list a card can render.
type TurnChanges struct {
	Files     []ChangedFile `json:"files"`
	Additions int           `json:"additions"`
	Deletions int           `json:"deletions"`
	Truncated bool          `json:"truncated,omitempty"`
}

// checkpointRef names the snapshot bounding one end of a turn. The turn id is
// already unique, so no counter has to be kept in step with the log.
func checkpointRef(sessionID, turnID, edge string) string {
	return fmt.Sprintf("%s/%s/%s/%s", checkpointRefPrefix, sessionID, turnID, edge)
}

// captureCheckpoint writes the worktree — tracked, staged, unstaged and
// untracked alike — to a commit under ref, and answers with its object id.
//
// It runs through GIT_INDEX_FILE so that `git add -A` stages into a scratch
// index instead of the person's own. Staging into the real index would make a
// snapshot visible as staged work, which is a side effect no one asked for.
func captureCheckpoint(ctx context.Context, root, ref string) (string, error) {
	index, err := os.CreateTemp("", "omniplex-checkpoint-index-*")
	if err != nil {
		return "", err
	}
	// git wants to create the index itself; an existing empty file is not a
	// valid index, so only the name is borrowed.
	indexPath := index.Name()
	_ = index.Close()
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)

	env := append(os.Environ(),
		"GIT_INDEX_FILE="+indexPath,
		// A snapshot is the server's own bookkeeping. Borrowing the person's
		// identity for it would put their name on commits they never made,
		// and a repository with no configured user would fail outright.
		"GIT_AUTHOR_NAME=omniplex",
		"GIT_AUTHOR_EMAIL=omniplex@localhost",
		"GIT_COMMITTER_NAME=omniplex",
		"GIT_COMMITTER_EMAIL=omniplex@localhost",
	)

	// A repository with no commits yet has nothing to seed the index from, and
	// that is not an error: everything in it is simply new.
	if _, err := runGit(ctx, root, "rev-parse", "--verify", "HEAD"); err == nil {
		if _, err := runGitEnv(ctx, root, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	if _, err := runGitEnv(ctx, root, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := runGitEnv(ctx, root, env, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := runGitEnv(ctx, root, env, "commit-tree", strings.TrimSpace(string(tree)),
		"-m", "omniplex checkpoint "+ref)
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(commit))
	if _, err := runGitEnv(ctx, root, env, "update-ref", ref, oid); err != nil {
		return "", err
	}
	return oid, nil
}

// diffCheckpoints is the file list between two snapshots. Both sides are real
// commits, so a rename is a rename and an untracked file that the turn created
// is simply an addition — none of the special cases the working tree needs.
func diffCheckpoints(ctx context.Context, root, from, to string) (TurnChanges, error) {
	out := TurnChanges{Files: []ChangedFile{}}

	nameStatus, err := runGit(ctx, root, "diff", "-M", "--name-status", "-z", from, to)
	if err != nil {
		return TurnChanges{}, err
	}
	numstat, err := runGit(ctx, root, "diff", "-M", "--numstat", "-z", from, to)
	if err != nil {
		return TurnChanges{}, err
	}

	files := parseNameStatus(string(nameStatus))
	counts := parseNumstat(string(numstat))
	for i := range files {
		if c, ok := counts[files[i].Path]; ok {
			files[i].Additions, files[i].Deletions, files[i].Binary = c.additions, c.deletions, c.binary
		}
	}

	// Totals are summed over everything before the list is cut, so a truncated
	// card still reports honest numbers.
	for _, f := range files {
		out.Additions += f.Additions
		out.Deletions += f.Deletions
	}
	if len(files) > maxTurnDiffFiles {
		files = files[:maxTurnDiffFiles]
		out.Truncated = true
	}
	if files != nil {
		out.Files = files
	}
	return out, nil
}

// dropCheckpoints deletes every snapshot belonging to a session. Refs are cheap
// but they are not free, and a session that has been closed will never be
// diffed again.
func dropCheckpoints(ctx context.Context, root, sessionID string) error {
	prefix := fmt.Sprintf("%s/%s/", checkpointRefPrefix, sessionID)
	listed, err := runGit(ctx, root, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return err
	}
	var failed error
	for _, ref := range strings.Fields(string(listed)) {
		if _, err := runGit(ctx, root, "update-ref", "-d", ref); err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

// purgeCheckpoints removes a session's snapshots when there is no checkpointer
// to do it — a workspace being cleaned up, or a session closed while nothing
// was running. It has to happen while the checkout still exists: the refs live
// in the repository the worktree belongs to, and they outlive the worktree.
func purgeCheckpoints(ctx context.Context, cwd, sessionID string, logf func(string, ...any)) {
	root := repoRoot(ctx, cwd)
	if root == "" {
		return
	}
	if err := dropCheckpoints(ctx, root, sessionID); err != nil {
		logf("dropping checkpoints for %s: %v", sessionID, err)
	}
}

// repoRoot is the top of the checkout a session works in, or "" when it is not
// a repository at all — which is not a failure, only a session that will have
// no turn cards.
func repoRoot(ctx context.Context, cwd string) string {
	if cwd == "" {
		return ""
	}
	top, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(top))
}

func runGitEnv(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}
