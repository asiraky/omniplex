package skills

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Staging is where a source is fetched to before anything is installed: a
// throwaway folder the install dialog previews from and picks out of. It is
// named by a random id the client hands back, so everything that takes an id
// treats it, and every path under it, as untrusted.
const (
	stagePrefix   = "omniplex-skills-"
	stageManifest = ".omniplex-stage.json"
	stageMaxAge   = time.Hour
	// maxScanDirs bounds the search for SKILL.md files: a source can be any
	// folder on the machine, and "/" is a folder.
	maxScanDirs = 20000
)

// Staged is a fetched source, waiting for the user to pick from it.
type Staged struct {
	ID     string        `json:"id"`
	Method string        `json:"method"`
	Repo   string        `json:"repo"`
	Ref    string        `json:"ref,omitempty"`
	Skills []StagedSkill `json:"skills"`
	Note   string        `json:"note,omitempty"`
}

type StagedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path,omitempty"` // in-repo folder
	Files       []File `json:"files"`          // includes SKILL.md
	Problem     string `json:"problem,omitempty"`
	Picked      bool   `json:"picked"`
	// InstalledIn is each destination folder ("" for personal) whose
	// library already has a skill of this name, which installing this one
	// there replaces.
	InstalledIn []string `json:"installedIn"`
}

// manifest is what a staging dir knows about itself, so the commands that
// follow a fetch need not scan again or know which fetcher ran.
type manifest struct {
	Method string         `json:"method"`
	Repo   string         `json:"repo"`
	Ref    string         `json:"ref,omitempty"`
	Skills []stagedFolder `json:"skills"`
	// Update is set when the fetch was for stage_update.
	Update *updatePlan `json:"update,omitempty"`
}

type stagedFolder struct {
	Name string `json:"name"`
	// Folder is relative to the staging dir, slash-separated. Empty when the
	// skill could not be staged and so cannot be installed.
	Folder string `json:"folder,omitempty"`
	Path   string `json:"path,omitempty"`
	Ref    string `json:"ref,omitempty"`
}

type stage struct {
	id  string
	dir string // symlink-resolved
	m   manifest
}

var stageIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

func stagePath(id string) (string, error) {
	if !stageIDRe.MatchString(id) {
		return "", fmt.Errorf("%w: not a staging id", ErrInvalid)
	}
	return filepath.Join(os.TempDir(), stagePrefix+id), nil
}

func newStage() (*stage, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(raw)
	dir, err := stagePath(id)
	if err != nil {
		return nil, err
	}
	// Mkdir, not MkdirAll: a dir somebody else already made under the shared
	// temp folder is not ours to fetch into.
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	return &stage{id: id, dir: real}, nil
}

// openStage finds the staging dir a client named.
func openStage(id string) (*stage, error) {
	dir, err := stagePath(id)
	if err != nil {
		return nil, err
	}
	gone := fmt.Errorf("%w: the fetched copy is gone; fetch again", ErrNotFound)
	// Lstat: a symlink planted under the temp folder is not a staging dir.
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil, gone
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, gone
	}
	st := &stage{id: id, dir: real}
	data, err := os.ReadFile(filepath.Join(real, stageManifest))
	if err != nil || json.Unmarshal(data, &st.m) != nil {
		return nil, gone
	}
	return st, nil
}

func (st *stage) save() error {
	data, err := json.Marshal(st.m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(st.dir, stageManifest), data, 0o600)
}

func (st *stage) skill(name string) (stagedFolder, bool) {
	for _, sf := range st.m.Skills {
		if sf.Name == name {
			return sf, true
		}
	}
	return stagedFolder{}, false
}

// folder resolves a staged skill's directory and refuses one that is not
// inside the staging dir, whatever the manifest or a symlink says.
func (st *stage) folder(sf stagedFolder) (string, error) {
	rel := filepath.FromSlash(sf.Folder)
	if rel == "" || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %s cannot be installed", ErrInvalid, sf.Name)
	}
	real, ok := realDir(filepath.Join(st.dir, rel))
	if !ok || !within(st.dir, real) {
		return "", fmt.Errorf("%w: %s is not in the fetched copy", ErrInvalid, sf.Name)
	}
	return real, nil
}

// within reports whether path is strictly below parent. Both must already be
// symlink-resolved: this is the check that keeps a staged path in staging.
func within(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}

// held is the staging dirs something still answers for, however old: a card
// can wait on a phone for hours.
var held = struct {
	sync.Mutex
	ids map[string]bool
}{ids: map[string]bool{}}

// HoldStaged keeps a staging dir from the sweep until ReleaseStaged.
func HoldStaged(id string) {
	held.Lock()
	held.ids[id] = true
	held.Unlock()
}

// ReleaseStaged hands a staging dir back to the sweep.
func ReleaseStaged(id string) {
	held.Lock()
	delete(held.ids, id)
	held.Unlock()
}

func isHeld(id string) bool {
	held.Lock()
	defer held.Unlock()
	return held.ids[id]
}

// sweepStages removes staging dirs nobody came back for: a dialog closed by
// killing the tab never sends its discard. A held one stays.
func sweepStages(now time.Time) {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		id, ok := strings.CutPrefix(e.Name(), stagePrefix)
		if !ok || !stageIDRe.MatchString(id) || isHeld(id) {
			continue
		}
		// Info is an lstat, so a symlink of that name is left alone.
		info, err := e.Info()
		if err != nil || !info.IsDir() || now.Sub(info.ModTime()) <= stageMaxAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(tmp, e.Name()))
	}
}

// DiscardStaged removes a staging dir. One that is already gone is fine: the
// dialog discards on close whether or not an install got there first.
func DiscardStaged(id string) error {
	dir, err := stagePath(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// ReadStagedFile returns a file of a staged skill for the preview.
func ReadStagedFile(id, skill, rel string) (content string, binary bool, err error) {
	st, err := openStage(id)
	if err != nil {
		return "", false, err
	}
	sf, ok := st.skill(skill)
	if !ok {
		return "", false, ErrNotFound
	}
	dir, err := st.folder(sf)
	if err != nil {
		return "", false, err
	}
	return readInside(dir, rel)
}

var errTooBig = errors.New("too many folders to search for skills; point at the folder that holds them")

// scanSkills finds the folders under root that hold a SKILL.md. root must be
// symlink-resolved. A skill's own subfolders are not searched: an example
// SKILL.md inside a skill is part of that skill.
func scanSkills(ctx context.Context, root string) ([]string, error) {
	var out []string
	seen := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // a folder that cannot be read holds no skills for us
		}
		if !d.IsDir() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p != root && (d.Name() == ".git" || d.Name() == "node_modules") {
			return fs.SkipDir
		}
		if seen++; seen > maxScanDirs {
			return errTooBig
		}
		if hasSkillFile(p) {
			out = append(out, p)
			return fs.SkipDir
		}
		return nil
	})
	return out, err
}

// listFiles lists a skill folder's files, SKILL.md included.
func listFiles(dir string) []File {
	files := []File{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != dir && (d.Name() == ".git" || d.Name() == "node_modules") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if len(files) >= maxFiles {
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(dir, p)
		var size int64
		if info, err := os.Stat(p); err == nil {
			size = info.Size()
		}
		files = append(files, File{Path: filepath.ToSlash(rel), Size: size})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// fetched is one skill folder a fetcher found, whichever fetcher it was.
type fetched struct {
	folder string // absolute, in the staging dir; "" when it could not be staged
	base   string // the name to fall back on when the frontmatter has none
	path   string // folder inside the source, slash-separated
	ref    string
	// problem says why a skill that was found could not be staged.
	problem string
}

// describe turns what a fetcher found into what the dialog shows, and writes
// the manifest the later commands read.
func (st *stage) describe(r Roots, src ParsedSource, method, note string, found []fetched) (Staged, error) {
	staged := Staged{ID: st.id, Method: method, Repo: src.Repo, Ref: src.Ref, Note: note, Skills: []StagedSkill{}}
	st.m = manifest{Method: method, Repo: src.Repo, Ref: src.Ref}

	picked := map[string]bool{}
	for _, name := range src.Picked {
		picked[name] = true
	}
	seen := map[string]bool{}
	refs := map[string]bool{}
	for _, f := range found {
		s := StagedSkill{Name: f.base, Path: f.path, Problem: f.problem, Files: []File{}}
		sf := stagedFolder{Path: f.path, Ref: f.ref}
		if f.folder != "" {
			// A fetcher is a program somebody else wrote, run over a repo
			// somebody else wrote: where it says a skill is gets checked.
			real, ok := realDir(f.folder)
			if !ok || !within(st.dir, real) {
				continue
			}
			s = stagedMeta(real, f.base)
			s.Path = f.path
			rel, err := filepath.Rel(st.dir, real)
			if err != nil {
				continue
			}
			sf.Folder = filepath.ToSlash(rel)
		}
		// Names are how the dialog refers to a staged skill, so there is one
		// of each: the first found wins.
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		s.Picked = picked["*"] || picked[s.Name] || picked[f.base]
		s.InstalledIn = installedIn(r, s.Name)
		sf.Name = s.Name
		if f.ref != "" {
			refs[f.ref] = true
		}
		staged.Skills = append(staged.Skills, s)
		st.m.Skills = append(st.m.Skills, sf)
	}
	if len(staged.Skills) == 0 {
		return Staged{}, fmt.Errorf("%w: no skills found in %s", ErrInvalid, src.Repo)
	}
	sort.Slice(staged.Skills, func(i, j int) bool { return staged.Skills[i].Name < staged.Skills[j].Name })
	// With no ref asked for, the one the fetcher resolved is what was fetched.
	if staged.Ref == "" && len(refs) == 1 {
		for ref := range refs {
			staged.Ref, st.m.Ref = ref, ref
		}
	}
	return staged, st.save()
}

func installedIn(r Roots, name string) []string {
	out := []string{}
	for _, d := range r.Destinations() {
		library := r.Library
		if d.Kind != DestPersonal {
			library = projectLibrary(d.Folder)
		}
		if inLibrary(library, name) {
			out = append(out, d.Folder)
		}
	}
	return out
}

func inLibrary(library, name string) bool {
	if library == "" || ValidateName(name) != nil {
		return false
	}
	_, err := os.Lstat(filepath.Join(library, name))
	return err == nil
}

// stagedMeta reads a staged skill folder. The skill is named by its
// frontmatter where that is a usable name, since that is the folder it will
// be installed as; the folder it came in is only the fallback.
func stagedMeta(dir, base string) StagedSkill {
	s := Skill{Dir: dir}
	fillMeta(&s, base)
	name := s.Name
	switch {
	case ValidateName(name) != nil:
		name = base
		if err := ValidateName(base); err != nil && !strings.Contains(s.Problem, err.Error()) {
			s.Problem = strings.TrimPrefix(s.Problem+"; "+err.Error(), "; ")
		}
	case name != base:
		fillMeta(&s, name) // read again as the folder it will be, so the names agree
	}
	return StagedSkill{
		Name:        name,
		Description: s.Description,
		Files:       listFiles(dir),
		Problem:     s.Problem,
	}
}
