package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// UpdateStage is a source fetched again, set against what is installed.
type UpdateStage struct {
	ID     string        `json:"id"`
	Repo   string        `json:"repo"`
	Skills []UpdateSkill `json:"skills"`
}

type UpdateSkill struct {
	Name    string       `json:"name"`
	Dir     string       `json:"dir"`
	Changed bool         `json:"changed"`
	Gone    bool         `json:"gone,omitempty"` // no longer in the source
	Files   []FileChange `json:"files"`
}

type FileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | modified | removed
}

const (
	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeRemoved  = "removed"
)

// updatePlan is the part of a staging manifest that ties a fetch to the
// installed skills it would replace.
type updatePlan struct {
	Library string         `json:"library"` // symlink-resolved
	Targets []updateTarget `json:"targets"`
}

type updateTarget struct {
	Dir   string `json:"dir"`   // the installed skill's folder name in the library
	Skill string `json:"skill"` // the staged skill that replaces it
	// Hash is the staged skill as upstream has it, taken before the installed
	// copy's manual-only choice was written over it.
	Hash string `json:"hash"`
}

// libraryOf is the library a skill is installed in, or "" when it sits
// somewhere Omniplex does not manage.
func libraryOf(r Roots, s Skill) string {
	parent := filepath.Dir(s.Dir)
	for _, lib := range []string{r.ProjectLibrary, r.Library} {
		if real, ok := realDir(lib); ok && real == parent {
			return real
		}
	}
	return ""
}

// sourceOf turns a recorded source back into something to fetch. The whole
// repo is fetched, not one skill's folder: its siblings update together.
func sourceOf(s *Source) (ParsedSource, error) {
	cannot := fmt.Errorf("%w: cannot fetch from %q again", ErrInvalid, s.Repo)
	if s.Method == MethodLocal {
		if !filepath.IsAbs(s.Repo) {
			return ParsedSource{}, cannot
		}
		return ParsedSource{Local: true, Repo: filepath.Clean(s.Repo)}, nil
	}
	// Parsed only to check it: the repo is fetched as it was recorded.
	if _, err := parseRemote(s.Repo); err != nil || (s.Ref != "" && !refRe.MatchString(s.Ref)) {
		return ParsedSource{}, cannot
	}
	return ParsedSource{Repo: s.Repo, Ref: s.Ref}, nil
}

// StageUpdate fetches a skill's source again and compares every skill
// installed from it, in the same library, with what came back.
func (f Fetcher) StageUpdate(ctx context.Context, r Roots, dir string) (UpdateStage, error) {
	s, err := find(r, dir)
	if err != nil {
		return UpdateStage{}, err
	}
	if !s.Editable {
		return UpdateStage{}, ErrNotEditable
	}
	if s.Source == nil {
		return UpdateStage{}, fmt.Errorf("%w: %s was not installed from anywhere", ErrInvalid, s.Name)
	}
	library := libraryOf(r, s)
	if library == "" {
		return UpdateStage{}, fmt.Errorf("%w: %s is not in a skills library", ErrInvalid, s.Name)
	}
	src, err := sourceOf(s.Source)
	if err != nil {
		return UpdateStage{}, err
	}
	all, err := Discover(r)
	if err != nil {
		return UpdateStage{}, err
	}
	rec, err := LoadRecord(library)
	if err != nil {
		return UpdateStage{}, err
	}

	st, _, err := f.stage(ctx, r, src)
	if err != nil {
		return UpdateStage{}, err
	}
	out := UpdateStage{ID: st.id, Repo: s.Source.Repo, Skills: []UpdateSkill{}}
	st.m.Update = &updatePlan{Library: library}
	for _, inst := range all {
		if !inst.Editable || inst.Source == nil || filepath.Dir(inst.Dir) != library ||
			inst.Source.Repo != s.Source.Repo || inst.Source.Ref != s.Source.Ref {
			continue
		}
		u := UpdateSkill{Name: inst.Name, Dir: inst.Dir, Files: []FileChange{}}
		sf, ok := st.match(inst)
		folder, err := st.folder(sf)
		if !ok || err != nil {
			u.Gone = true
			out.Skills = append(out.Skills, u)
			continue
		}
		upstream, err := HashDir(folder)
		if err != nil {
			_ = os.RemoveAll(st.dir)
			return UpdateStage{}, err
		}
		// The staged copy takes on the installed one's manual-only choice now,
		// so the diff shows what applying will really write. A copy that
		// cannot take it is shown as upstream has it; applying says why.
		frontmatter, openai := FileManual(inst.Dir)
		_ = keepManual(folder, frontmatter, openai)
		files, err := diffDirs(inst.Dir, folder)
		if err != nil {
			_ = os.RemoveAll(st.dir)
			return UpdateStage{}, err
		}
		// With a record, changed means upstream moved since the install: the
		// user's own edits are not an update. Without one there is only the
		// installed copy to compare with.
		if e, ok := rec.Get(filepath.Base(inst.Dir)); ok && e.Hash != "" {
			u.Changed = e.Hash != upstream
		} else {
			u.Changed = len(files) > 0
		}
		if u.Changed {
			u.Files = files
		}
		st.m.Update.Targets = append(st.m.Update.Targets, updateTarget{Dir: filepath.Base(inst.Dir), Skill: sf.Name, Hash: upstream})
		out.Skills = append(out.Skills, u)
	}
	if err := st.save(); err != nil {
		_ = os.RemoveAll(st.dir)
		return UpdateStage{}, err
	}
	return out, nil
}

// match finds the staged skill an installed one came from: by its folder in
// the repo where that was recorded, since a skill can be renamed on the way
// in, and by name otherwise.
func (st *stage) match(inst Skill) (stagedFolder, bool) {
	if inst.Source.Path != "" {
		for _, sf := range st.m.Skills {
			if sf.Path == inst.Source.Path {
				return sf, true
			}
		}
	}
	for _, name := range []string{filepath.Base(inst.Dir), inst.Name} {
		if sf, ok := st.skill(name); ok {
			return sf, true
		}
	}
	return stagedFolder{}, false
}

// keepManual makes dir say what the installed copy said about manual-only,
// each mechanism on its own: an installed copy whose harnesses disagreed
// keeps disagreeing, and that stays the user's to fix.
func keepManual(dir string, frontmatter, openai bool) error {
	nowFrontmatter, nowOpenAI := FileManual(dir)
	if nowFrontmatter == frontmatter && nowOpenAI == openai {
		return nil
	}
	if err := SetManual(dir, frontmatter); err != nil {
		return err
	}
	if openai == frontmatter {
		return nil
	}
	// SetManual wrote both files one way; Codex's goes back the other.
	path := filepath.Join(dir, filepath.FromSlash(openaiYAML))
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next, err := editOpenAIManual(string(data), openai)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if next == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(resolve(path), []byte(next))
}

// fileHashes maps each file of a skill folder to a hash of its bytes, over
// the same files HashDir counts.
func fileHashes(dir string) (map[string]string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	err = filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != real && (d.Name() == ".git" || d.Name() == "node_modules") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(real, p)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return out, err
}

// diffDirs lists what replacing the old folder with the new one changes.
func diffDirs(oldDir, newDir string) ([]FileChange, error) {
	before, err := fileHashes(oldDir)
	if err != nil {
		return nil, err
	}
	after, err := fileHashes(newDir)
	if err != nil {
		return nil, err
	}
	out := []FileChange{}
	for path, hash := range after {
		switch was, ok := before[path]; {
		case !ok:
			out = append(out, FileChange{Path: path, Status: ChangeAdded})
		case was != hash:
			out = append(out, FileChange{Path: path, Status: ChangeModified})
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			out = append(out, FileChange{Path: path, Status: ChangeRemoved})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// openUpdate finds an update's staging dir and the installed skill one of its
// targets replaces. dir is checked against the plan: an update touches only
// the skills it was staged for.
func openUpdate(r Roots, id, dir string) (*stage, Skill, updateTarget, error) {
	st, err := openStage(id)
	if err != nil {
		return nil, Skill{}, updateTarget{}, err
	}
	if st.m.Update == nil {
		return nil, Skill{}, updateTarget{}, fmt.Errorf("%w: that fetch was not for an update", ErrInvalid)
	}
	s, err := find(r, dir)
	if err != nil {
		return nil, Skill{}, updateTarget{}, err
	}
	if !s.Editable {
		return nil, Skill{}, updateTarget{}, ErrNotEditable
	}
	if filepath.Dir(s.Dir) == st.m.Update.Library {
		for _, t := range st.m.Update.Targets {
			if t.Dir == filepath.Base(s.Dir) {
				return st, s, t, nil
			}
		}
	}
	return nil, Skill{}, updateTarget{}, fmt.Errorf("%w: %s is not part of this update", ErrInvalid, s.Name)
}

// ReadUpdateFile returns one file as installed and as the update would leave
// it. A file on one side only comes back empty on the other.
func ReadUpdateFile(r Roots, id, dir, rel string) (before, after string, binary bool, err error) {
	st, s, target, err := openUpdate(r, id, dir)
	if err != nil {
		return "", "", false, err
	}
	sf, _ := st.skill(target.Skill)
	folder, err := st.folder(sf)
	if err != nil {
		return "", "", false, err
	}
	before, wasBinary, errOld := readInside(s.Dir, rel)
	after, isBinary, errNew := readInside(folder, rel)
	missing := func(err error) bool { return errors.Is(err, ErrNotFound) }
	switch {
	case errOld != nil && !missing(errOld):
		return "", "", false, errOld
	case errNew != nil && !missing(errNew):
		return "", "", false, errNew
	case errOld != nil && errNew != nil:
		return "", "", false, ErrNotFound
	}
	if wasBinary || isBinary {
		return "", "", true, nil
	}
	return before, after, false, nil
}

// ApplyUpdate replaces installed skills with their staged copies. Each keeps
// the manual-only choice it had, whatever upstream now says, and ends up in
// our own record: a skill only the skills CLI's lock knew about is adopted.
// The staging dir stays, so the rest of an update can be applied later.
func ApplyUpdate(r Roots, id string, dirs []string) ([]Skill, error) {
	if len(dirs) == 0 {
		return nil, fmt.Errorf("%w: pick at least one skill", ErrInvalid)
	}
	type applying struct {
		st     *stage
		s      Skill
		target updateTarget
		sf     stagedFolder
		folder string
	}
	// Everything is looked up before anything is replaced.
	var plan []applying
	seen := map[string]bool{}
	for _, dir := range dirs {
		st, s, target, err := openUpdate(r, id, dir)
		if err != nil {
			return nil, err
		}
		if seen[s.Dir] {
			continue
		}
		seen[s.Dir] = true
		sf, _ := st.skill(target.Skill)
		folder, err := st.folder(sf)
		if err != nil {
			return nil, err
		}
		plan = append(plan, applying{st: st, s: s, target: target, sf: sf, folder: folder})
	}

	out := make([]Skill, 0, len(plan))
	for _, p := range plan {
		library := filepath.Dir(p.s.Dir)
		name := filepath.Base(p.s.Dir)
		frontmatter, openai := FileManual(p.s.Dir)
		tmp, err := copyBeside(p.folder, library, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.s.Name, err)
		}
		// The choice goes onto the copy, so a copy that cannot take it never
		// replaces the skill.
		if err := keepManual(tmp, frontmatter, openai); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("%s: %w", p.s.Name, err)
		}
		if err := swapIn(tmp, p.s.Dir); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
		now := stamp()
		err = UpdateRecord(library, func(rec *Record) error {
			e, ok := rec.Get(name)
			if !ok {
				e.InstalledAt = p.s.Source.installedOr(now)
			}
			// Repo and ref stay as the skill had them: that is what was fetched,
			// and what keeps it grouped with the siblings not yet updated.
			e.Method, e.Repo, e.Ref = p.st.m.Method, p.s.Source.Repo, p.s.Source.Ref
			e.Path, e.Hash, e.UpdatedAt = p.sf.Path, p.target.Hash, now
			rec.Set(name, e)
			return nil
		})
		if err != nil {
			return nil, err
		}
		s, err := find(r, p.s.Dir)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// installedOr is when the skills CLI says it installed the skill, for a skill
// being adopted, or now when it did not say.
func (s *Source) installedOr(now string) string {
	if s != nil && s.InstalledAt != "" {
		return s.InstalledAt
	}
	return now
}
