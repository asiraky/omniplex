package skills

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GitStatus is what the personal library has uncommitted, when it is kept in
// a git repository.
type GitStatus struct {
	Root    string      `json:"root"`
	Branch  string      `json:"branch"`
	Changes []GitChange `json:"changes"`
}

// GitChange is one top-level entry of the library: a skill's folder, or a
// file such as the source record.
type GitChange struct {
	Name   string `json:"name"`
	Status string `json:"status"` // added | modified | removed
	Files  int    `json:"files"`
}

// libraryGit runs git in the library. Pathspecs are taken literally: a skill
// folder is named by whoever wrote the skill, and ":(top)" is a legal name.
func libraryGit(ctx context.Context, library string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--literal-pathspecs", "-C", library}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// A hook that refuses says why on either stream.
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = strings.TrimSpace(stdout.String())
		}
		if reason == "" {
			reason = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], reason)
	}
	return stdout.String(), nil
}

// LibraryStatus lists the personal library's uncommitted changes, grouped by
// top-level entry. It is nil when the library is not in a git work tree.
func LibraryStatus(ctx context.Context, r Roots) (*GitStatus, error) {
	library, ok := realDir(r.Library)
	if !ok {
		return nil, nil
	}
	info := GitRepo(library)
	if info == nil {
		return nil, nil
	}
	prefix, err := filepath.Rel(resolve(info.Root), library)
	if err != nil || !filepath.IsLocal(prefix) {
		return nil, nil
	}
	prefix = filepath.ToSlash(prefix)

	// Every file is listed, untracked ones too, so an entry's count is real;
	// renames are reported as a removal and an addition, which is what they
	// are to a list grouped by folder.
	out, err := libraryGit(ctx, library, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all", "--", ".")
	if err != nil {
		return nil, err
	}
	// What the last commit holds tells a new skill from a changed one. A
	// repository with no commit yet holds nothing. The tree named is already
	// the library's, so git must not narrow it to this folder a second time.
	tree := "HEAD"
	if prefix != "." {
		tree += ":" + prefix
	}
	committed := map[string]bool{}
	if names, err := libraryGit(ctx, library, "ls-tree", "--full-tree", "--name-only", "-z", tree); err == nil {
		for _, name := range strings.Split(names, "\x00") {
			committed[name] = true
		}
	}

	files := map[string]int{}
	for _, entry := range strings.Split(out, "\x00") {
		// "XY path", the path relative to the repository's root.
		if len(entry) < 4 {
			continue
		}
		path := entry[3:]
		if prefix != "." {
			rest, ok := strings.CutPrefix(path, prefix+"/")
			if !ok {
				continue
			}
			path = rest
		}
		name, _, _ := strings.Cut(path, "/")
		if name != "" {
			files[name]++
		}
	}
	status := &GitStatus{Root: info.Root, Branch: info.Branch, Changes: make([]GitChange, 0, len(files))}
	for name, n := range files {
		change := GitChange{Name: name, Status: ChangeModified, Files: n}
		if !committed[name] {
			change.Status = ChangeAdded
		} else if _, err := os.Lstat(filepath.Join(library, name)); err != nil {
			change.Status = ChangeRemoved
		}
		status.Changes = append(status.Changes, change)
	}
	sort.Slice(status.Changes, func(i, j int) bool { return status.Changes[i].Name < status.Changes[j].Name })
	return status, nil
}

// CommitSkills commits the named top-level entries of the personal library
// and nothing else: whatever else is changed or staged in the repository
// stays as it was, and nothing is pushed. The user's hooks run.
func CommitSkills(ctx context.Context, r Roots, names []string, message string) (commit string, after *GitStatus, err error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return "", nil, fmt.Errorf("%w: a commit needs a message", ErrInvalid)
	}
	if len(names) == 0 {
		return "", nil, fmt.Errorf("%w: pick something to commit", ErrInvalid)
	}
	status, err := LibraryStatus(ctx, r)
	if err != nil {
		return "", nil, err
	}
	if status == nil {
		return "", nil, fmt.Errorf("%w: the skills library is not in a git repository", ErrInvalid)
	}
	// Only a name git itself reports as changed becomes a path, so a name
	// cannot reach outside the library or stand in for a flag.
	changed := map[string]bool{}
	for _, c := range status.Changes {
		changed[c.Name] = true
	}
	var paths []string
	seen := map[string]bool{}
	for _, name := range names {
		if !changed[name] {
			return "", nil, fmt.Errorf("%w: %s has nothing to commit", ErrInvalid, name)
		}
		if !seen[name] {
			seen[name] = true
			paths = append(paths, name)
		}
	}
	library := resolve(r.Library)
	if _, err := libraryGit(ctx, library, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", nil, err
	}
	if _, err := libraryGit(ctx, library, append([]string{"commit", "-q", "-m", message, "--"}, paths...)...); err != nil {
		return "", nil, err
	}
	sha, err := libraryGit(ctx, library, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", nil, err
	}
	after, err = LibraryStatus(ctx, r)
	return strings.TrimSpace(sha), after, err
}

// MarkUncommitted sets Uncommitted on each skill in a repo's main checkout
// that git has not committed, its folder or its link: a thread in a new
// worktree of the repo will not have it. A repo git cannot answer for, in
// time or at all, leaves its skills unmarked.
func MarkUncommitted(ctx context.Context, r Roots, skills []Skill) {
	for _, repo := range r.Repos {
		if !repo.Main || repo.Dir == "" {
			continue
		}
		dir := resolve(repo.Dir)
		var in []int
		for i, s := range skills {
			if s.Folder != "" && resolve(s.Folder) == dir {
				in = append(in, i)
			}
		}
		if len(in) == 0 {
			continue
		}
		changed, err := uncommittedSkills(ctx, repo.Dir)
		if err != nil {
			continue
		}
		for _, i := range in {
			s := &skills[i]
			for _, p := range append([]string{s.Dir}, s.paths...) {
				if changed[filepath.Base(p)] {
					s.Uncommitted = true
				}
			}
		}
	}
}

// uncommittedSkills names every skill folder or link under a checkout's
// skills dirs that has something git has not committed.
func uncommittedSkills(ctx context.Context, dir string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := libraryGit(ctx, dir, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all",
		"--", ".agents/skills", ".claude/skills", ".pi/skills")
	if err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for _, entry := range strings.Split(out, "\x00") {
		if len(entry) < 4 {
			continue
		}
		// Paths are from the top of the repository, which the checkout
		// folder need not be: the skill is whatever follows a skills dir.
		parts := strings.Split(entry[3:], "/")
		for i := 2; i < len(parts); i++ {
			if parts[i-1] == "skills" && (parts[i-2] == ".agents" || parts[i-2] == ".claude" || parts[i-2] == ".pi") {
				changed[parts[i]] = true
				break
			}
		}
	}
	return changed, nil
}
