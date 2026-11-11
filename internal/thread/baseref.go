package thread

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"
)

// baseFetchTimeout bounds a network fetch. A slow or unreachable remote must
// never hold a provision open: the fetch is an optimisation, and failing it
// only means the base is looked for somewhere else.
const baseFetchTimeout = 60 * time.Second

// baseRefreshTimeout bounds the fetch that freshens a base the clone already
// has. It is much shorter than baseFetchTimeout because it runs on nearly every
// create and its failure costs nothing — the ref that is already here is used —
// so an offline laptop must not pay a minute per thread to learn it is offline.
const baseRefreshTimeout = 15 * time.Second

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

// gitEnv keeps Git non-interactive and predictable. A credential or host-key
// prompt inside a server process has nobody to answer it, so it would hang
// until the context expired; refusing to prompt turns that into a fast,
// ordinary failure. Optional locks are off so a read such as `git status`
// never takes the index lock a concurrent write needs, and the C locale keeps
// the output this package parses in the one language it was written against.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
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
// because of a missing base or an unreachable remote. Whatever the base names
// is fetched first, best-effort and briefly, because a worktree that starts
// days behind origin is a merge conflict waiting to happen. A local branch is
// used unless its remote has moved strictly ahead of it, in which case the
// worktree starts from the remote ref and says so; the local branch itself is
// never moved, because it may be checked out in the user's own checkout. A
// branch that lives only on a remote is turned into a local tracking branch —
// fetching it first if the clone has never seen it — and a base that exists
// nowhere at all falls back to the repository's default branch with a note,
// because a thread the user cannot start is worse than a thread that started
// from the wrong place and says so.
func resolveBaseRef(ctx context.Context, root, base string) (baseResolution, error) {
	base = strings.TrimSpace(base)
	res := baseResolution{Requested: base}
	if base == "" {
		res.Ref = "HEAD"
		return res, nil
	}
	// A ref that looks like a flag would be read as one by `git worktree add`,
	// so it never reaches git. It is still not worth failing a thread over:
	// the default branch and a note say more than a refusal does.
	if strings.HasPrefix(base, "-") {
		res.Ref, res.FellBack = defaultBranch(ctx, root), true
		res.Note = fmt.Sprintf("base %q cannot be a ref — it reads as a git option; branched from %q instead", base, res.Ref)
		return res, nil
	}

	names := remotes(ctx, root)

	if hasLocalBranch(ctx, root, base) {
		return preferFresherRemote(ctx, root, base, names, res), nil
	}

	// An explicit remote ref is fetched before it is looked for, so it is as
	// fresh as the network allows and one this clone has never seen still
	// resolves. Only a ref that was absent before the fetch counts as fetched.
	for _, r := range names {
		branch, ok := strings.CutPrefix(base, r+"/")
		if !ok || branch == "" {
			continue
		}
		seen := hasRemoteRef(ctx, root, base)
		fetchBranch(ctx, root, r, branch, refreshTimeout(seen))
		if hasRemoteRef(ctx, root, base) {
			res.Ref, res.Fetched = base, !seen
			return res, nil
		}
	}

	if remote := remoteHolding(ctx, root, base, names); remote != "" {
		// The tracking ref is here but may be old; refreshing it first means the
		// local branch about to be created starts at the remote's current tip.
		// This is not a discovery, so Fetched stays false.
		fetchBranch(ctx, root, remote, base, baseRefreshTimeout)
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

// refreshTimeout picks the fetch bound for a ref: short when a copy is already
// here to fall back on, the full discovery bound when the fetch is the only way
// the ref will exist at all.
func refreshTimeout(seen bool) time.Duration {
	if seen {
		return baseRefreshTimeout
	}
	return baseFetchTimeout
}

// preferFresherRemote decides between a local branch and its remote. The local
// branch wins unless the remote-tracking ref is strictly ahead of it — local is
// an ancestor and they differ — because then the local branch is simply stale
// and nothing on it would be lost by starting from the remote. A local branch
// that is ahead or has diverged holds work the remote does not, so it is used
// as-is and quietly. A failed fetch still leaves whatever tracking ref was
// already here, and if that is ahead it is used: offline, a stale remote ref is
// still fresher than a staler local branch.
func preferFresherRemote(ctx context.Context, root, base string, names []string, res baseResolution) baseResolution {
	res.Ref = base
	remote, branch := upstreamOf(ctx, root, base, names)
	if remote == "" {
		return res
	}
	fetchBranch(ctx, root, remote, branch, baseRefreshTimeout)
	tracking := remote + "/" + branch
	local, ok := gitCapture(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+base+"^{commit}")
	if !ok {
		return res
	}
	theirs, ok := gitCapture(ctx, root, "rev-parse", "--verify", "--quiet", "refs/remotes/"+tracking+"^{commit}")
	if !ok || theirs == local {
		return res
	}
	if !gitOK(ctx, root, "merge-base", "--is-ancestor", local, theirs) {
		return res
	}
	res.Ref = tracking
	res.Note = fmt.Sprintf("local branch %q is behind %q; branched from %q and left the local branch where it was", base, tracking, tracking)
	return res
}

// upstreamOf names the remote branch a local branch should be compared with:
// its configured upstream when that is a real remote, else the same name on
// origin, else nothing. An upstream of "." is another local branch, which has
// no network to be refreshed from, so it is treated as no upstream at all.
func upstreamOf(ctx context.Context, root, base string, names []string) (remote, branch string) {
	if r, ok := gitCapture(ctx, root, "config", "--get", "branch."+base+".remote"); ok && r != "." && slices.Contains(names, r) {
		if merge, ok := gitCapture(ctx, root, "config", "--get", "branch."+base+".merge"); ok {
			if b, ok := strings.CutPrefix(merge, "refs/heads/"); ok && b != "" {
				return r, b
			}
		}
	}
	if slices.Contains(names, "origin") {
		return "origin", base
	}
	return "", ""
}

// trackRemote gives the remote branch a local name, so the worktree that starts
// from it is set up to push and pull against the branch it came from.
func trackRemote(ctx context.Context, root, base, remote string, res baseResolution) (baseResolution, error) {
	start := remote + "/" + base
	unlock := lockRepo(ctx, root)
	created := gitOK(ctx, root, "branch", "--track", base, start)
	unlock()
	if created {
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
	return fetchBranch(ctx, root, remote, base, baseFetchTimeout)
}

// fetchBranch updates one remote-tracking ref from its remote, within timeout.
// The repository lock is held for the fetch alone: it writes refs another
// provision may be writing too, and nothing else may run under the lock.
func fetchBranch(ctx context.Context, root, remote, branch string, timeout time.Duration) bool {
	spec := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", branch, remote, branch)
	unlock := lockRepo(ctx, root)
	defer unlock()
	// The clock starts once the lock is held, so a queue of provisions bounds
	// each one's network time rather than eating it while waiting in line.
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
