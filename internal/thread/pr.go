package session

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// PullRequest is the little omniplex knows about the pull request for a session's
// branch: enough to say "this landed" and link to it, and nothing more. It is
// never persisted — the answer is only true until someone merges something, so
// it is fetched on demand and thrown away.
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
	URL    string `json:"url,omitempty"`
	// State is gh's own word: OPEN, MERGED or CLOSED.
	State  string `json:"state,omitempty"`
	Merged bool   `json:"merged,omitempty"`
	// MergedAt is RFC 3339, and empty for a pull request that never landed.
	MergedAt string `json:"mergedAt,omitempty"`
	// HeadRefOid is the commit the pull request's branch pointed at. It is what
	// tells a finished branch from one that was merged and then worked on again.
	HeadRefOid  string `json:"headRefOid,omitempty"`
	BaseRefName string `json:"baseRefName,omitempty"`
	BaseRefOid  string `json:"baseRefOid,omitempty"`
}

const prLookupTimeout = 12 * time.Second

// prFields is what `gh pr view` is asked for. Kept next to the struct that
// receives it so the two cannot drift apart unnoticed.
const prFields = "number,title,url,state,mergedAt,headRefOid,baseRefName,baseRefOid"

// SessionPR reports the pull request for a session's branch, if there is one.
//
// Every failure is soft, and deliberately so: this exists to offer a cleanup
// affordance, so the cost of not knowing is that the affordance stays hidden.
// gh missing, gh unauthenticated, no remote, a remote that is not GitHub, or
// simply no pull request yet are all ordinary states of a perfectly healthy
// session, and none of them is worth an error in front of the user. The second
// return is a reason, for logs and for anyone debugging why no prompt appeared.
func (m *Manager) SessionPR(ctx context.Context, sessionID string) (*PullRequest, string) {
	meta, err := m.store.Session(ctx, sessionID)
	if err != nil {
		return nil, err.Error()
	}
	// A local session runs in the user's own checkout. There is no worktree to
	// reclaim, so a merged pull request tells omniplex nothing it should act on.
	if meta.WorkspaceMode != "managed" && meta.WorkspaceMode != "borrowed" {
		return nil, "not a worktree session"
	}
	if meta.Branch == "" {
		return nil, "session has no branch"
	}
	if meta.Cwd == "" {
		return nil, "session has no working directory"
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, "the GitHub CLI (gh) is not installed"
	}

	ctx, cancel := context.WithTimeout(ctx, prLookupTimeout)
	defer cancel()
	// `gh pr list --head` rather than `gh pr view <branch>`, because view's
	// positional argument is overloaded: it takes a number, a URL or a branch,
	// and a branch named "75" is read as pull request #75 — someone else's, in
	// all likelihood, and quite possibly a merged one. --head is only ever a
	// branch name, so no branch can be mistaken for a pull request.
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--head", meta.Branch, "--state", "all", "--limit", "1", "--json", prFields)
	cmd.Dir = meta.Cwd
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, strings.TrimSpace(string(ee.Stderr))
		}
		if ctx.Err() != nil {
			return nil, "gh pr list timed out"
		}
		return nil, err.Error()
	}
	pr, parseErr := parsePR(out)
	if parseErr != "" {
		return nil, parseErr
	}
	// A branch that has moved on since its pull request merged is not finished
	// work: the worktree holds commits the merge never contained. Keeping the
	// branch and carrying on is an ordinary way to work, and omniplex offering to
	// delete that worktree would be offering to delete those commits.
	if pr.Merged && pr.HeadRefOid != "" {
		if head, headErr := runGit(ctx, meta.Cwd, "rev-parse", "HEAD"); headErr == nil {
			if !strings.EqualFold(strings.TrimSpace(string(head)), pr.HeadRefOid) {
				pr.Merged = false
			}
		}
		// A HEAD that cannot be read leaves Merged as gh reported it: the
		// prompt only ever opens a confirmation, and the confirmation is where
		// anything is actually agreed to.
	}
	return pr, ""
}

// parsePR reads `gh pr list --json` output, which is an array, and takes the
// most recent entry gh offered. Merged is decided by both the state and the
// timestamp: either one alone has been enough to mislead — a closed-then-
// reopened pull request keeps a mergedAt in some views — and requiring both
// makes the false positive, the one that offers to delete a worktree still
// being worked in, the unlikely direction.
func parsePR(out []byte) (*PullRequest, string) {
	var prs []PullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, "could not parse gh output: " + err.Error()
	}
	if len(prs) == 0 || prs[0].Number == 0 {
		return nil, "gh reported no pull request"
	}
	pr := prs[0]
	pr.Merged = strings.EqualFold(pr.State, "MERGED") && pr.MergedAt != ""
	return &pr, ""
}
