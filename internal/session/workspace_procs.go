package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procRef is a process still sitting inside a directory omniplex is about to
// remove.
type procRef struct {
	PID  int
	Name string
}

func (p procRef) String() string {
	if p.Name == "" {
		return strconv.Itoa(p.PID)
	}
	return fmt.Sprintf("%s (pid %d)", p.Name, p.PID)
}

// under reports whether path is dir itself or something inside it. Both are
// expected to be absolute and symlink-resolved already.
func under(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// processesIn lists processes whose working directory is inside target.
//
// This is the check that stops a delete from wedging the workspace. A dev
// server left running in a worktree keeps writing into it — vite rewrites
// node_modules/.vite/deps as fast as anything can delete it — and "git
// worktree remove" loses that race on the final rmdir. Git then unregisters
// the worktree anyway, so the directory survives with no administrative entry
// and every later attempt is refused for a completely different reason. It is
// far cheaper to notice the process first and say so.
//
// includeDeleted decides whether processes sitting in an already-unlinked
// directory count. They are the ones that wedged a half-finished removal, so
// they belong in a diagnosis of why files keep coming back — but they are not
// grounds for refusing to delete a directory that exists now. The kernel
// renders an unlinked cwd as its former pathname, and a worktree recreated at
// that same path (the same branch provisioned again, say) is a different
// directory the old process cannot touch. Refusing that would block a
// perfectly safe delete until an unrelated process happened to exit.
//
// Best effort by nature: the process table is a moving target, a process may
// exit or chdir a microsecond later, and a working directory is unreadable for
// processes belonging to another user. An empty result is "nothing seen", not
// a guarantee, so callers must still cope with removal failing. How the
// working directories are read is per platform — see scanCWDs.
func processesIn(target string, includeDeleted bool) []procRef {
	var found []procRef
	for _, p := range scanCWDs() {
		if p.Deleted && !includeDeleted {
			continue
		}
		if !filepath.IsAbs(p.CWD) || !under(p.CWD, target) {
			continue
		}
		found = append(found, procRef{PID: p.PID, Name: p.Name})
	}
	return found
}

// procCWD is one process as a platform scan saw it: pid, name, its working
// directory, and whether that directory has already been unlinked.
type procCWD struct {
	PID     int
	Name    string
	CWD     string
	Deleted bool
}

// describeProcs renders at most three processes for an error message, so a
// worktree full of workers does not produce an unreadable wall of pids.
func describeProcs(procs []procRef) string {
	const max = 3
	parts := make([]string, 0, max)
	for i, p := range procs {
		if i == max {
			parts = append(parts, fmt.Sprintf("and %d more", len(procs)-max))
			break
		}
		parts = append(parts, p.String())
	}
	return strings.Join(parts, ", ")
}

// orphanedWorktreeOf reports whether target is the leftover of a worktree that
// belonged to the repository whose common dir is rootCommon.
//
// Git deletes .git/worktrees/<id> even when it failed to delete the checkout,
// so the directory left behind is no longer a Git worktree by any test Git
// offers — "git -C target rev-parse" fails outright. What it does still have
// is the .git file pointing at the administrative directory that used to
// exist. That pointer is enough to prove whose worktree this was, which is the
// only question that matters before removing it.
func orphanedWorktreeOf(target, rootCommon string) bool {
	pointer := filepath.Join(target, ".git")
	// Lstat, not Stat: a symlink at .git could aim the check at a legitimate
	// pointer file anywhere on disk while the directory being removed is
	// something else entirely. Git writes a regular file here and nothing
	// else is trusted.
	info, err := os.Lstat(pointer)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(pointer)
	if err != nil {
		return false
	}
	line := strings.TrimSpace(string(b))
	gitDir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return false
	}
	gitDir = strings.TrimSpace(gitDir)
	if gitDir == "" {
		return false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(target, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	// A worktree's administrative directory is always an immediate child of
	// this exact repository's worktrees directory — rootCommon/worktrees/<id>.
	// Requiring an immediate child rather than merely something underneath
	// keeps a deeper path from being read as one, and rules out the worktrees
	// directory itself.
	dir, id := filepath.Split(gitDir)
	return filepath.Clean(dir) == filepath.Join(rootCommon, "worktrees") && id != ""
}
