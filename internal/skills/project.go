package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Destination is somewhere a new or installed skill can go: the project's
// home folder, one of its repos, or the personal library.
type Destination struct {
	Kind   string `json:"kind"`   // project | repo | personal
	Folder string `json:"folder"` // the project home or repo checkout; "" for personal
	Label  string `json:"label"`
	// Main is a repo's main checkout: what lands there is not committed, and
	// a thread in a new worktree will not see it until it is.
	Main bool `json:"main,omitempty"`
}

// Phrase is the destination as it reads inside a sentence: "this project",
// "the omniplex repo", "your personal skills".
func (d Destination) Phrase() string {
	switch d.Kind {
	case DestProject:
		return "this project"
	case DestRepo:
		return "the " + d.Label
	}
	return "your personal skills"
}

const (
	DestProject  = "project"
	DestRepo     = "repo"
	DestPersonal = "personal"
)

// Destinations lists where a skill can go from here: the project's own
// first, then each repo, the personal library last.
func (r Roots) Destinations() []Destination {
	var out []Destination
	seen := map[string]bool{}
	if r.ProjectHome != "" {
		home := filepath.Clean(r.ProjectHome)
		seen[resolve(home)] = true
		out = append(out, Destination{Kind: DestProject, Folder: home, Label: "This project"})
	}
	// A plain-folder project's home is its folder: one entry, not two.
	for _, repo := range r.Repos {
		dir := filepath.Clean(repo.Dir)
		if repo.Dir == "" || seen[resolve(dir)] {
			continue
		}
		seen[resolve(dir)] = true
		out = append(out, Destination{Kind: DestRepo, Folder: dir, Label: repo.Name + " repo", Main: repo.Main})
	}
	return append(out, Destination{Kind: DestPersonal, Label: "Personal"})
}

// DefaultDestination is the project's own when there is a project in view,
// else the personal library ("").
func (r Roots) DefaultDestination() string {
	if r.ProjectHome == "" {
		return ""
	}
	return filepath.Clean(r.ProjectHome)
}

// destination finds the offered destination a client named. Only these are
// ever written to: a folder is a path from the client.
func (r Roots) destination(folder string) (Destination, error) {
	if folder != "" {
		folder = filepath.Clean(folder)
	}
	for _, d := range r.Destinations() {
		if d.Folder == folder {
			return d, nil
		}
	}
	return Destination{}, fmt.Errorf("%w: a skill cannot go in %s from here", ErrInvalid, abbreviate(folder, r.Home))
}

// projectLibrary is a project folder's own library, where the skills CLI's
// project install puts skills and Codex and pi read them.
func projectLibrary(folder string) string {
	return filepath.Join(folder, filepath.FromSlash(libraryDir))
}

// projectPaths are what a project install writes, under the folder.
var projectPaths = []string{libraryDir, ".claude/skills", ".pi/skills", LockFile}

// checkProjectPaths refuses a folder where one of projectPaths leads outside
// it through a symlink. The folder is often somebody else's repo, and a
// committed skills-lock.json or .agents/skills link would otherwise send our
// writes anywhere on the machine.
func checkProjectPaths(folder string) error {
	for _, rel := range projectPaths {
		if !landsInside(folder, filepath.FromSlash(rel)) {
			return fmt.Errorf("%w: %s in %s leads outside it", ErrInvalid, rel, folder)
		}
	}
	return nil
}

// landsInside reports whether rel under folder, followed through whatever
// part of it exists, stays inside folder.
func landsInside(folder, rel string) bool {
	top, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return false
	}
	want := filepath.Join(folder, rel)
	for at := want; ; at = filepath.Dir(at) {
		if real, err := filepath.EvalSymlinks(at); err == nil {
			rest, err := filepath.Rel(at, want)
			return err == nil && within(top, filepath.Join(real, rest))
		}
		// A dangling link is a link all the same: where it would lead is
		// not ours to guess.
		if info, err := os.Lstat(at); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if at == folder || at == filepath.Dir(at) {
			return false
		}
	}
}

// linkIntoProject gives Claude, and pi where the folder already has a .pi,
// a link to a skill in the folder's library, as the skills CLI's project
// install does. The link is written relative, so it survives a clone, a
// worktree or a move. A link already there is replaced; a real folder is the
// project's own skill and stays. Nothing here fails the install.
func linkIntoProject(folder, name string) {
	target := filepath.FromSlash("../../" + libraryDir + "/" + name)
	dirs := []string{filepath.Join(folder, ".claude", "skills")}
	if info, err := os.Stat(filepath.Join(folder, ".pi")); err == nil && info.IsDir() {
		dirs = append(dirs, filepath.Join(folder, ".pi", "skills"))
	}
	for _, dir := range dirs {
		at := filepath.Join(dir, name)
		if info, err := os.Lstat(at); err == nil {
			if info.Mode()&os.ModeSymlink == 0 || os.Remove(at) != nil {
				continue
			}
		}
		if os.MkdirAll(dir, 0o755) == nil {
			_ = os.Symlink(target, at)
		}
	}
}

// LockFile is the skills CLI's project lock, in the folder it installed into.
// A project folder's provenance lives only there, so `npx skills` run in the
// folder agrees with Omniplex about what came from where.
const LockFile = "skills-lock.json"

// ProjectCLILock is where `npx skills` run inside a folder keeps its lock.
func ProjectCLILock(folder string) string {
	return filepath.Join(folder, LockFile)
}

// projectEntry is one skill in a project lock, in the order the CLI writes
// its fields.
type projectEntry struct {
	Source       string `json:"source"`
	SourceURL    string `json:"sourceUrl,omitempty"`
	Ref          string `json:"ref,omitempty"`
	SourceType   string `json:"sourceType"`
	SkillPath    string `json:"skillPath,omitempty"`
	ComputedHash string `json:"computedHash"`
}

// lockEntryFor says where a skill came from the way the skills CLI 1.7.0
// would have: GitHub as owner/repo, any other git host by URL, a folder on
// this machine relative to the lock.
func lockEntryFor(folder, method, repo, ref, inRepo, hash string) projectEntry {
	e := projectEntry{Source: repo, Ref: ref, SkillPath: path.Join(inRepo, "SKILL.md"), ComputedHash: hash}
	switch {
	case method == MethodLocal:
		e.SourceType = "local"
		e.Source = portableSource(folder, repo)
	case shorthandRe.MatchString(repo):
		e.SourceType = "github"
	default:
		e.SourceType = "git"
		if u, err := url.Parse(repo); err == nil && strings.EqualFold(strings.TrimPrefix(u.Hostname(), "www."), "gitlab.com") {
			e.SourceType = "gitlab"
		}
		e.SourceURL = repo
	}
	return e
}

// portableSource is a local source as the CLI stores it: relative to the
// lock's folder, ./ or ../ first, slash-separated.
func portableSource(folder, source string) string {
	rel, err := filepath.Rel(resolve(folder), resolve(source))
	if err != nil {
		return filepath.ToSlash(source)
	}
	rel = filepath.ToSlash(rel)
	switch {
	case rel == ".":
		return "."
	case rel == ".." || strings.HasPrefix(rel, "../"):
		return rel
	}
	return "./" + rel
}

// readProjectLock reads a folder's lock for provenance, a local source
// resolved against the folder again. Missing or unreadable is no provenance.
func readProjectLock(folder string) map[string]LockEntry {
	lock := ReadCLILock(ProjectCLILock(folder))
	for name, e := range lock {
		if e.SourceType == "local" && e.Source != "" && !filepath.IsAbs(filepath.FromSlash(e.Source)) {
			e.Source = filepath.Join(resolve(folder), filepath.FromSlash(e.Source))
			lock[name] = e
		}
	}
	return lock
}

// loadProjectLock reads a folder's lock for writing: the top-level fields
// and the skills, each kept as written. A missing lock is empty; one this
// cannot read is an error, so a write never replaces it.
func loadProjectLock(folder string) (top, skills map[string]json.RawMessage, err error) {
	file := ProjectCLILock(folder)
	top, skills = map[string]json.RawMessage{}, map[string]json.RawMessage{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return top, skills, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", file, err)
	}
	var version int
	if json.Unmarshal(top["version"], &version) != nil || version != 1 {
		return nil, nil, fmt.Errorf("%s: unsupported version %s", file, top["version"])
	}
	if raw, ok := top["skills"]; ok && json.Unmarshal(raw, &skills) != nil {
		return nil, nil, fmt.Errorf("parse %s: skills is not an object", file)
	}
	if skills == nil {
		skills = map[string]json.RawMessage{}
	}
	return top, skills, nil
}

// errUnchanged abandons a lock write that would change nothing.
var errUnchanged = errors.New("unchanged")

// updateProjectLock applies fn to the skills in a folder's lock and writes
// it back. The lock is the skills CLI's file as much as ours, so every entry
// and field Omniplex did not write survives.
func updateProjectLock(folder string, fn func(skills map[string]json.RawMessage) error) error {
	if err := checkProjectPaths(folder); err != nil {
		return err
	}
	recordMu.Lock()
	defer recordMu.Unlock()
	top, skills, err := loadProjectLock(folder)
	if err != nil {
		return err
	}
	if err := fn(skills); err != nil {
		if errors.Is(err, errUnchanged) {
			return nil
		}
		return err
	}
	// Laid out as the CLI writes it, version then skills sorted by name, so
	// the two can take turns on the file without a diff for nothing.
	var b bytes.Buffer
	b.WriteString(`{"version":1,"skills":`)
	b.Write(plainJSON(skills)) // map keys come out sorted
	others := make([]string, 0, len(top))
	for k := range top {
		if k != "version" && k != "skills" {
			others = append(others, k)
		}
	}
	sort.Strings(others)
	for _, k := range others {
		b.WriteByte(',')
		b.Write(plainJSON(k))
		b.WriteByte(':')
		b.Write(top[k])
	}
	b.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	// Write to the real file behind a symlinked lock.
	return writeAtomic(resolve(ProjectCLILock(folder)), out.Bytes())
}

// plainJSON marshals the way JSON.stringify does, < > & left as they are.
// Only maps, strings and raw JSON come here, none of which can fail.
func plainJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// lockFields is the fields of an entry Omniplex writes, in the CLI's order.
var lockFields = []string{"source", "sourceUrl", "ref", "sourceType", "skillPath", "computedHash"}

// entryJSON writes an entry's fields in the CLI's order, any it does not
// know sorted after.
func entryJSON(fields map[string]json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	put := func(k string) {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(plainJSON(k))
		b.WriteByte(':')
		b.Write(fields[k])
	}
	known := map[string]bool{}
	for _, k := range lockFields {
		known[k] = true
		if _, ok := fields[k]; ok {
			put(k)
		}
	}
	rest := make([]string, 0, len(fields))
	for k := range fields {
		if !known[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		put(k)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// setLockEntry puts a fresh install's entry over the old one, keeping only
// the old fields Omniplex does not write.
func setLockEntry(skills map[string]json.RawMessage, name string, e projectEntry) {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(skills[name], &fields)
	for _, k := range lockFields {
		delete(fields, k)
	}
	_ = json.Unmarshal(plainJSON(e), &fields)
	skills[name] = entryJSON(fields)
}

// patchLockEntry changes some fields of an entry and leaves the rest as the
// CLI or the user wrote them.
func patchLockEntry(skills map[string]json.RawMessage, name string, set map[string]string) {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(skills[name], &fields)
	for k, v := range set {
		fields[k] = plainJSON(v)
	}
	skills[name] = entryJSON(fields)
}

// lockKey is the name a lock knows a skill by: its folder's, or its own
// where the CLI used that. "" when the lock has neither.
func lockKey[V any](lock map[string]V, s Skill) string {
	for _, name := range []string{filepath.Base(s.Dir), s.Name} {
		if _, ok := lock[name]; ok {
			return name
		}
	}
	return ""
}

// forgetLockEntry drops a removed skill from its folder's lock. A lock with
// no entry for it is not written.
func forgetLockEntry(folder string, s Skill) error {
	return updateProjectLock(folder, func(skills map[string]json.RawMessage) error {
		key := lockKey(skills, s)
		if key == "" {
			return errUnchanged
		}
		delete(skills, key)
		return nil
	})
}

// projectFolderOf is the project folder whose library dir is directly in,
// "" when it is in none.
func (r Roots) projectFolderOf(dir string) string {
	parent := filepath.Dir(dir)
	for _, d := range r.projectDirs() {
		if real, ok := realDir(projectLibrary(d)); ok && real == parent {
			return d
		}
	}
	return ""
}

// cliHash is the skills CLI's computeSkillFolderHash, so a lock entry written
// here means what one written by `npx skills` does: sha256 over the folder's
// regular files, each its slash path then its bytes, in the order JavaScript's
// localeCompare puts the paths, which is the Unicode root collation. Symlinks
// count for nothing, and .git and node_modules folders are skipped.
func cliHash(dir string) (string, error) {
	top, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	var files []string
	err = filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != top && (d.Name() == ".git" || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(top, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	c := collate.New(language.Und)
	sort.Slice(files, func(i, j int) bool {
		if n := c.CompareString(files[i], files[j]); n != 0 {
			return n < 0
		}
		return files[i] < files[j]
	})
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(top, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// lockFolder is the folder whose skills-lock.json speaks for a project skill:
// the one beside the library it is in, else the folder it was found in, since
// the CLI installing for Claude alone puts a skill in .claude/skills.
func (s Skill) lockFolder(r Roots) string {
	if s.Scope != ScopeProject {
		return ""
	}
	if folder := r.projectFolderOf(s.Dir); folder != "" {
		return folder
	}
	return s.Folder
}
