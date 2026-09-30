package skills

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AllHarnesses is the order harnesses are listed in wherever each gets a line.
var AllHarnesses = []Harness{Claude, Codex, Pi}

const (
	LinkDirect   = "direct"    // one of the harness's roots is the library
	LinkPerSkill = "per-skill" // the harness has a skills dir of its own; skills are linked one by one
	LinkNone     = "none"      // the harness has no skills dir yet
)

// Link is how one harness reaches a library. It is detected from the disk on
// every read and never stored: the user's symlinks are the configuration.
type Link struct {
	Harness Harness `json:"harness"`
	Dir     string  `json:"dir"` // the harness skills dir that reaches, or would reach, the library
	State   string  `json:"state"`
}

type GitInfo struct {
	Root   string `json:"root"`
	Branch string `json:"branch"`
}

type Setup struct {
	Library        string   `json:"library"`    // as configured, "~"-abbreviated for display
	LibraryDir     string   `json:"libraryDir"` // absolute; symlink-resolved when it exists
	Exists         bool     `json:"exists"`
	ProjectLibrary string   `json:"projectLibrary"` // relative to the project root
	CLIVersion     string   `json:"cliVersion"`
	Npx            bool     `json:"npx"`
	Git            *GitInfo `json:"git,omitempty"` // set when the library is inside a git work tree
	Links          []Link   `json:"links"`
}

// DetectSetup describes the personal library and how each harness reaches it.
func DetectSetup(r Roots) Setup {
	s := Setup{
		Library:        abbreviate(r.Library, r.Home),
		LibraryDir:     r.Library,
		ProjectLibrary: DefaultProjectLibrary,
		CLIVersion:     r.CLIVersion,
		Links:          make([]Link, 0, len(AllHarnesses)),
	}
	if real, ok := realDir(r.Library); ok {
		s.LibraryDir, s.Exists = real, true
		s.Git = GitRepo(real)
	}
	if r.ProjectRoot != "" && r.ProjectLibrary != "" {
		if rel, err := filepath.Rel(r.ProjectRoot, r.ProjectLibrary); err == nil {
			s.ProjectLibrary = filepath.ToSlash(rel)
		}
	}
	_, err := exec.LookPath("npx")
	s.Npx = err == nil
	for _, h := range AllHarnesses {
		s.Links = append(s.Links, LinkState(r, h, ScopeUser))
	}
	return s
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

// LibraryDir is the library a scope's skills are written into.
func LibraryDir(r Roots, scope string) (string, error) {
	switch scope {
	case ScopeUser:
		if r.Library == "" {
			return "", fmt.Errorf("%w: no skills library", ErrInvalid)
		}
		return r.Library, nil
	case ScopeProject:
		if r.ProjectRoot == "" || r.ProjectLibrary == "" {
			return "", fmt.Errorf("%w: no project to keep the skill in", ErrInvalid)
		}
		return r.ProjectLibrary, nil
	}
	return "", fmt.Errorf("%w: scope must be user or project", ErrInvalid)
}

// linkDir is the skills dir Omniplex links into for a harness: the one root
// of each that the others' tools also know about.
func linkDir(r Roots, h Harness, scope string) string {
	switch {
	case scope == ScopeUser && h == Claude && r.ClaudeConfigDir != "":
		return filepath.Join(r.ClaudeConfigDir, "skills")
	case scope == ScopeUser && (h == Codex || h == Pi) && r.Home != "":
		return filepath.Join(r.Home, ".agents", "skills")
	case scope == ScopeProject && h == Claude && r.ProjectRoot != "":
		return filepath.Join(r.ProjectRoot, ".claude", "skills")
	case scope == ScopeProject && (h == Codex || h == Pi) && r.ProjectRoot != "":
		return filepath.Join(r.ProjectRoot, ".agents", "skills")
	}
	return ""
}

// LinkState detects how a harness reaches the library of a scope (user or
// project).
func LinkState(r Roots, h Harness, scope string) Link {
	out := Link{Harness: h, Dir: linkDir(r, h, scope), State: LinkNone}
	lib, _ := LibraryDir(r, scope)
	if real, ok := realDir(lib); ok {
		for _, rt := range r.ownRoots() {
			if rt.scope != scope || !containsHarness(rt.harnesses, h) {
				continue
			}
			if dir, ok := realDir(rt.path); ok && dir == real {
				out.Dir, out.State = rt.path, LinkDirect
				return out
			}
		}
	}
	if _, ok := realDir(out.Dir); ok {
		out.State = LinkPerSkill
	}
	return out
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

// LinkSkill lets one more harness see an editable skill, by a symlink in the
// harness's link dir for the skill's scope.
func LinkSkill(r Roots, dir string, h Harness) (Skill, error) {
	s, err := find(r, dir)
	if err != nil {
		return Skill{}, err
	}
	if !s.Editable {
		return Skill{}, ErrNotEditable
	}
	if containsHarness(s.Harnesses, h) {
		return Skill{}, fmt.Errorf("%w: %s already reads %s", ErrInvalid, h, s.Name)
	}
	into := linkDir(r, h, s.Scope)
	if into == "" {
		return Skill{}, fmt.Errorf("%w: no skills folder for %s", ErrInvalid, h)
	}
	name := filepath.Base(s.Dir)
	if _, err := os.Lstat(filepath.Join(into, name)); err == nil {
		return Skill{}, fmt.Errorf("%w: %s already exists in %s", ErrInvalid, name, into)
	}
	if err := symlinkTo(s.Dir, filepath.Join(into, name)); err != nil {
		return Skill{}, err
	}
	return find(r, s.Dir)
}

// LinkLibrary points a harness's personal skills dir at the library, making
// the library if it is not there yet. It never replaces a dir the harness
// already has: that one holds skills of its own.
func LinkLibrary(r Roots, h Harness) error {
	into := linkDir(r, h, ScopeUser)
	if into == "" || r.Library == "" {
		return fmt.Errorf("%w: no skills folder for %s", ErrInvalid, h)
	}
	if LinkState(r, h, ScopeUser).State == LinkDirect {
		return fmt.Errorf("%w: %s already reads the library", ErrInvalid, h)
	}
	// Codex and pi's link dir is the default library itself, so checking for
	// it comes before making the library.
	if _, err := os.Lstat(into); err == nil {
		return fmt.Errorf("%w: %s already exists", ErrInvalid, into)
	}
	if err := os.MkdirAll(r.Library, 0o755); err != nil {
		return err
	}
	lib, err := filepath.EvalSymlinks(r.Library)
	if err != nil {
		return err
	}
	if real, ok := realDir(into); ok && real == lib {
		return nil // making the library made the link dir
	}
	return symlinkTo(lib, into)
}
