package skills

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type GitInfo struct {
	Root   string `json:"root"`
	Branch string `json:"branch"`
}

func abbreviate(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}

// realDir resolves path when it is, through any symlinks, a directory.
func realDir(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(real)
	return real, err == nil && info.IsDir()
}

// GitRepo reports the work tree dir sits in, or nil: no git on this machine
// and a dir outside any repository are both just "not versioned".
func GitRepo(dir string) *GitInfo {
	root, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return nil
	}
	branch, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		// A repository with no commit yet has a branch but no HEAD to resolve.
		branch, _ = gitOut(dir, "symbolic-ref", "--short", "HEAD")
	}
	return &GitInfo{Root: root, Branch: branch}
}

func gitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// symlinkTo makes path a symlink to real, relative where that is possible so
// a home folder that moves keeps its links.
func symlinkTo(real, path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	target, err := filepath.Rel(realParent, real)
	if err != nil {
		target = real
	}
	return os.Symlink(target, path)
}

// agentDirs is each harness's personal skills dir: the one Omniplex gives a
// link to the library, or links skills into one by one.
func (r Roots) agentDirs() map[Harness]string {
	out := map[Harness]string{}
	if r.ClaudeConfigDir != "" {
		out[Claude] = filepath.Join(r.ClaudeConfigDir, "skills")
	}
	if r.CodexHome != "" {
		out[Codex] = filepath.Join(r.CodexHome, "skills")
	}
	if r.PiAgentDir != "" {
		out[Pi] = filepath.Join(r.PiAgentDir, "skills")
	}
	return out
}

// readsLibrary reports whether one of h's personal roots is the library
// itself, so whatever lands there reaches h with nothing more to do. Codex
// and pi read <home>/.agents/skills as standard.
func readsLibrary(r Roots, h Harness, lib string) bool {
	for _, rt := range r.ownRoots() {
		if rt.scope != ScopeUser || !containsHarness(rt.harnesses, h) {
			continue
		}
		if dir, ok := realDir(rt.path); ok && dir == lib {
			return true
		}
	}
	return false
}

// reachEveryAgent makes a skill in the library visible to every harness. A
// harness with no skills dir yet is given the whole library as its skills dir;
// one with a dir of its own gets a link to this skill in it. A link already
// there under the name is pointed at this skill; a real folder is the
// harness's own and is left alone. Nothing here fails the install: the skill
// is in the library either way.
func reachEveryAgent(r Roots, dir string) {
	lib, ok := realDir(r.Library)
	if !ok {
		return
	}
	name := filepath.Base(dir)
	for _, h := range []Harness{Claude, Codex, Pi} {
		into, ok := r.agentDirs()[h]
		if !ok || readsLibrary(r, h, lib) {
			continue
		}
		if _, err := os.Lstat(into); err != nil {
			_ = symlinkTo(lib, into)
			continue
		}
		if real, ok := realDir(into); !ok || real == lib {
			continue
		}
		at := filepath.Join(into, name)
		info, err := os.Lstat(at)
		switch {
		case err != nil:
		case info.Mode()&os.ModeSymlink == 0:
			continue // the harness's own skill of the same name
		case resolve(at) == dir:
			continue
		default:
			if os.Remove(at) != nil {
				continue
			}
		}
		_ = symlinkTo(dir, at)
	}
}
