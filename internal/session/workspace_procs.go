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
// Linux only, and best effort by nature: /proc is a moving target and a
// process may exit or chdir a microsecond later. An empty result is "nothing
// seen", not a guarantee, so callers must still cope with removal failing.
func processesIn(target string) []procRef {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var found []procRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil || pid == self {
			continue
		}
		// Reading another user's cwd fails with EACCES; those processes are
		// not ours to report on anyway.
		cwd, linkErr := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if linkErr != nil {
			continue
		}
		// A deleted cwd reads back as "/path/to/dir (deleted)". That process
		// is exactly the one that survived a half-finished removal, so strip
		// the marker rather than skipping it.
		cwd = strings.TrimSuffix(cwd, " (deleted)")
		if !filepath.IsAbs(cwd) || !under(cwd, target) {
			continue
		}
		found = append(found, procRef{PID: pid, Name: procName(pid)})
	}
	return found
}

func procName(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
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
	b, err := os.ReadFile(filepath.Join(target, ".git"))
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
	// Only ever a worktree admin directory of this exact repository, never the
	// repository itself: rootCommon/worktrees/<id>.
	return under(gitDir, filepath.Join(rootCommon, "worktrees")) && gitDir != filepath.Join(rootCommon, "worktrees")
}
