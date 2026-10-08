package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
)

// RecordFile sits in a library and says where each installed skill came from.
// It lives there, not in ~/.omniplex, so it travels with the library: a
// library kept in a dotfiles repo carries its provenance to the next machine.
const RecordFile = ".omniplex-skills.json"

const (
	MethodNpx   = "npx"
	MethodGit   = "git"
	MethodLocal = "local"
)

// RecordEntry is one installed skill, keyed by its directory name.
type RecordEntry struct {
	Method string `json:"method"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
	// Hash is HashDir of the skill as fetched, before any local edit, so an
	// update can tell upstream changes from the user's own.
	Hash        string `json:"hash"`
	InstalledAt string `json:"installedAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type Record struct {
	Version int                    `json:"version"`
	Skills  map[string]RecordEntry `json:"skills"`
}

func (r Record) Get(name string) (RecordEntry, bool) {
	e, ok := r.Skills[name]
	return e, ok
}

func (r *Record) Set(name string, e RecordEntry) {
	if r.Skills == nil {
		r.Skills = map[string]RecordEntry{}
	}
	r.Skills[name] = e
}

// Delete reports whether there was an entry to remove.
func (r *Record) Delete(name string) bool {
	_, ok := r.Skills[name]
	delete(r.Skills, name)
	return ok
}

func (e RecordEntry) source() *Source {
	return &Source{Method: e.Method, Repo: e.Repo, Ref: e.Ref, Path: e.Path, Managed: true, InstalledAt: e.InstalledAt, UpdatedAt: e.UpdatedAt}
}

// LoadRecord reads a library's record. A library without one has an empty
// record; one that does not parse is an error, so a save never replaces a
// file this build could not read.
func LoadRecord(library string) (Record, error) {
	rec := Record{Version: 1, Skills: map[string]RecordEntry{}}
	file := filepath.Join(library, RecordFile)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("parse %s: %w", file, err)
	}
	if rec.Version != 1 {
		return rec, fmt.Errorf("%s: unsupported version %d", file, rec.Version)
	}
	if rec.Skills == nil {
		rec.Skills = map[string]RecordEntry{}
	}
	return rec, nil
}

// SaveRecord writes the record through a temp file and a rename. Keys are
// written sorted and indented so the file diffs cleanly in the library's repo.
func SaveRecord(library string, rec Record) error {
	rec.Version = 1
	if rec.Skills == nil {
		rec.Skills = map[string]RecordEntry{}
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(library, 0o755); err != nil {
		return err
	}
	// Write to the real file behind a symlinked library or record.
	return writeAtomic(resolve(filepath.Join(library, RecordFile)), append(data, '\n'))
}

// recordMu serialises read-modify-write cycles on record files: an install
// and a remove landing together must not drop each other's entry.
var recordMu sync.Mutex

// UpdateRecord applies fn to the library's record and saves it. fn returning
// an error abandons the write.
func UpdateRecord(library string, fn func(*Record) error) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	rec, err := LoadRecord(library)
	if err != nil {
		return err
	}
	if err := fn(&rec); err != nil {
		return err
	}
	return SaveRecord(library, rec)
}

// forgetRecord drops a skill's entry, leaving a library with no record of it
// untouched.
func forgetRecord(library, name string) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	rec, err := LoadRecord(library)
	if err != nil {
		return err
	}
	if !rec.Delete(name) {
		return nil
	}
	return SaveRecord(library, rec)
}

// HashDir is the content hash of a skill folder: sha256 over its files in
// slash-relative path order, each contributing its path, a NUL and its bytes.
// .git and node_modules are skipped, so a checkout and a plain copy of the
// same skill hash alike.
func HashDir(dir string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	var files []string
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
		// A symlink to a folder has no bytes of its own to hash.
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(real, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		io.WriteString(h, rel)
		h.Write([]byte{0})
		f, err := os.Open(filepath.Join(real, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LockEntry is one skill in the skills CLI's own lock file. The global (v3)
// and project (v1) locks share these fields; the project lock has no dates.
type LockEntry struct {
	Source      string `json:"source"`
	SourceType  string `json:"sourceType"`
	SourceURL   string `json:"sourceUrl"`
	Ref         string `json:"ref"`
	SkillPath   string `json:"skillPath"`
	InstalledAt string `json:"installedAt"`
	UpdatedAt   string `json:"updatedAt"`
	// ComputedHash is the project lock's cliHash of the skill as installed.
	ComputedHash string `json:"computedHash"`
}

// ReadCLILock reads a skills CLI lock, global or project. It is someone
// else's file: a missing or unreadable one is simply no provenance, and it is
// never written.
func ReadCLILock(file string) map[string]LockEntry {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var lock struct {
		Skills map[string]LockEntry `json:"skills"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil
	}
	return lock.Skills
}

func (e LockEntry) source() *Source {
	s := &Source{Method: MethodNpx, Repo: e.Source, Ref: e.Ref, InstalledAt: e.InstalledAt, UpdatedAt: e.UpdatedAt}
	if s.Repo == "" {
		s.Repo = e.SourceURL
	}
	if e.SourceType == "local" {
		s.Method = MethodLocal
	}
	// skillPath names the SKILL.md; the folder is what gets fetched again.
	if dir := path.Dir(filepath.ToSlash(e.SkillPath)); e.SkillPath != "" && dir != "." {
		s.Path = dir
	}
	return s
}

// sourceIndex answers where a skill came from, reading each record and lock
// once per discovery.
type sourceIndex struct {
	r       Roots
	record  Record // the personal library's
	library string // symlink-resolved
	global  map[string]LockEntry
	project map[string]map[string]LockEntry // by project folder
}

func newSourceIndex(r Roots) *sourceIndex {
	x := &sourceIndex{r: r, project: map[string]map[string]LockEntry{}}
	if real, ok := realDir(r.Library); ok {
		if rec, err := LoadRecord(real); err == nil {
			x.library, x.record = real, rec
		}
	}
	if r.CLILock != "" {
		x.global = ReadCLILock(r.CLILock)
	}
	return x
}

func (x *sourceIndex) projectLock(folder string) map[string]LockEntry {
	lock, ok := x.project[folder]
	if !ok {
		lock = readProjectLock(folder)
		x.project[folder] = lock
	}
	return lock
}

// lookup reads a personal skill's source from our own record, with the
// skills CLI's global lock as the fallback for one installed from a terminal
// before Omniplex had a hand in it. A project skill's is only ever in its
// folder's skills-lock.json, which Omniplex and the CLI both keep.
func (x *sourceIndex) lookup(s *Skill) *Source {
	if x.library != "" && filepath.Dir(s.Dir) == x.library {
		if e, ok := x.record.Get(filepath.Base(s.Dir)); ok {
			return e.source()
		}
	}
	if !s.Editable {
		return nil
	}
	var lock map[string]LockEntry
	switch s.Scope {
	case ScopeUser:
		lock = x.global
	case ScopeProject:
		if folder := s.lockFolder(x.r); folder != "" {
			lock = x.projectLock(folder)
		}
	}
	if key := lockKey(lock, *s); key != "" {
		return lock[key].source()
	}
	return nil
}
