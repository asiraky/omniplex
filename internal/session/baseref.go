package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// baseFetchTimeout bounds a network fetch. A slow or unreachable remote must
// never hold a provision open: the fetch is an optimisation, and failing it
// only means the base is looked for somewhere else.
const baseFetchTimeout = 60 * time.Second

// baseResolution is the answer to "where does this branch begin". Ref is what
// goes to `git worktree add -b <branch> <ref>` and to hooks, and is always
// something Git can resolve locally by the time it is returned. Note carries a
// sentence for the user when the answer was not the one they asked for.
type baseResolution struct {
	Ref       string
	Requested string
	Note      string
	Fetched   bool
	Created   bool
	FellBack  bool
}

// gitEnv keeps Git non-interactive. A credential or host-key prompt inside a
// server process has nobody to answer it, so it would hang until the context
// expired; refusing to prompt turns that into a fast, ordinary failure.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"SSH_ASKPASS_REQUIRE=never",
		"GCM_INTERACTIVE=never",
	)
}

func gitCmd(ctx context.Context, root string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = gitEnv()
	return cmd
}

func gitOK(ctx context.Context, root string, args ...string) bool {
	return gitCmd(ctx, root, args...).Run() == nil
}

func gitCapture(ctx context.Context, root string, args ...string) (string, bool) {
	out, err := gitCmd(ctx, root, args...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func hasLocalBranch(ctx context.Context, root, name string) bool {
	return gitOK(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+name)
}

func hasRemoteRef(ctx context.Context, root, name string) bool {
	return gitOK(ctx, root, "show-ref", "--verify", "--quiet", "refs/remotes/"+name)
}

// remotes lists the configured remotes, longest name first, so that a base of
// "origin/upstream/foo" is matched against the longest remote that can claim it
// rather than the first one that happens to be a prefix.
func remotes(ctx context.Context, root string) []string {
	out, ok := gitCapture(ctx, root, "remote")
	if !ok || out == "" {
		return nil
	}
	names := strings.Fields(out)
	sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	return names
}

// remoteHolding picks the remote whose tracking ref already carries the base.
// origin wins when several do, because origin is what "the" remote means to
// nearly every repository.
func remoteHolding(ctx context.Context, root, base string, names []string) string {
	var found []string
	for _, r := range names {
		if hasRemoteRef(ctx, root, r+"/"+base) {
			found = append(found, r)
		}
	}
	for _, r := range found {
		if r == "origin" {
			return "origin"
		}
	}
	if len(found) > 0 {
		return found[0]
	}
	return ""
}

// originFirst orders remotes for fetching: origin is tried before the rest.
func originFirst(names []string) []string {
	ordered := make([]string, 0, len(names))
	for _, r := range names {
		if r == "origin" {
			ordered = append(ordered, r)
		}
	}
	for _, r := range names {
		if r != "origin" {
			ordered = append(ordered, r)
		}
	}
	return ordered
}

// resolveBaseRef answers where a new branch should begin, and never fails
// because of a missing base. A branch that lives only on a remote is turned
// into a local tracking branch — fetching it first if the clone has never seen
// it — and a base that exists nowhere at all falls back to the repository's
// default branch with a note, because a session the user cannot start is worse
// than a session that started from the wrong place and says so.
func resolveBaseRef(ctx context.Context, root, base string) (baseResolution, error) {
	base = strings.TrimSpace(base)
	res := baseResolution{Requested: base}
	if base == "" {
		res.Ref = "HEAD"
		return res, nil
	}
	// A ref that looks like a flag would be read as one by `git worktree add`,
	// so it never reaches git. It is still not worth failing a session over:
	// the default branch and a note say more than a refusal does.
	if strings.HasPrefix(base, "-") {
		res.Ref, res.FellBack = defaultBranch(ctx, root), true
		res.Note = fmt.Sprintf("base %q cannot be a ref — it reads as a git option; branched from %q instead", base, res.Ref)
		return res, nil
	}

	if hasLocalBranch(ctx, root, base) {
		res.Ref = base
		return res, nil
	}

	names := remotes(ctx, root)
	for _, r := range names {
		if strings.HasPrefix(base, r+"/") && hasRemoteRef(ctx, root, base) {
			res.Ref = base
			return res, nil
		}
	}

	if remote := remoteHolding(ctx, root, base, names); remote != "" {
		return trackRemote(ctx, root, base, remote, res)
	}

	for _, remote := range originFirst(names) {
		if !fetchBase(ctx, root, remote, base) {
			continue
		}
		if hasRemoteRef(ctx, root, remote+"/"+base) {
			res.Fetched = true
			return trackRemote(ctx, root, base, remote, res)
		}
	}

	return fallbackBase(ctx, root, res)
}

// trackRemote gives the remote branch a local name, so the worktree that starts
// from it is set up to push and pull against the branch it came from.
func trackRemote(ctx context.Context, root, base, remote string, res baseResolution) (baseResolution, error) {
	start := remote + "/" + base
	if gitOK(ctx, root, "branch", "--track", base, start) {
		res.Ref, res.Created = base, true
	} else {
		// Losing the tracking branch is survivable; branching from the remote
		// ref directly still lands on the right commit.
		res.Ref = start
	}
	verb := "created local branch"
	if !res.Created {
		verb = "branched directly from"
	}
	if res.Fetched {
		res.Note = fmt.Sprintf("base branch %q was fetched from %q; %s %q", base, remote, verb, res.Ref)
	} else {
		res.Note = fmt.Sprintf("base branch %q was found on %q; %s %q", base, remote, verb, res.Ref)
	}
	return res, nil
}

// fetchBase asks one remote for one branch, and treats every failure as "no".
func fetchBase(ctx context.Context, root, remote, base string) bool {
	fetchCtx, cancel := context.WithTimeout(ctx, baseFetchTimeout)
	defer cancel()
	spec := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", base, remote, base)
	return gitCmd(fetchCtx, root, "fetch", "--quiet", "--no-tags", remote, spec).Run() == nil
}

// fallbackBase is the last resort: the repository's own default branch. It is
// not an error, it is a note — provisioning goes ahead.
func fallbackBase(ctx context.Context, root string, res baseResolution) (baseResolution, error) {
	ref := defaultBranch(ctx, root)
	res.Ref, res.FellBack = ref, true
	res.Note = fmt.Sprintf("base branch %q was not found locally or on any remote; branched from %q instead", res.Requested, ref)
	return res, nil
}

func defaultBranch(ctx context.Context, root string) string {
	// Every remote is asked, origin first, because a checkout whose only remote
	// is called "upstream" still has a default branch and it is still a better
	// answer than main-or-whatever-is-checked-out.
	for _, remote := range originFirst(remotes(ctx, root)) {
		head, ok := gitCapture(ctx, root, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
		if !ok || head == "" {
			continue
		}
		short := strings.TrimPrefix(head, "refs/remotes/")
		if name := strings.TrimPrefix(short, remote+"/"); name != "" && hasLocalBranch(ctx, root, name) {
			return name
		}
		if short != "" {
			return short
		}
	}
	for _, name := range []string{"main", "master"} {
		if hasLocalBranch(ctx, root, name) {
			return name
		}
	}
	return "HEAD"
}
