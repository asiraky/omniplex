package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxFileBytes = 1 << 20
	maxFiles     = 500
)

// find returns the discovered skill whose real directory is dir. Only a
// discovered dir is ever read or written, so a caller cannot point these
// functions at an arbitrary path.
func find(r Roots, dir string) (Skill, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return Skill{}, ErrNotFound
	}
	dir = filepath.Clean(dir)
	all, err := Discover(r)
	if err != nil {
		return Skill{}, err
	}
	for _, s := range all {
		if s.Dir == dir {
			return s, nil
		}
	}
	return Skill{}, ErrNotFound
}

// Read returns a discovered skill's SKILL.md and the other files beside it.
func Read(r Roots, dir string) (Detail, error) {
	s, err := find(r, dir)
	if err != nil {
		return Detail{}, err
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
	if err != nil {
		return Detail{}, err
	}
	files := []File{}
	_ = filepath.WalkDir(s.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != s.Dir && (d.Name() == ".git" || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(s.Dir, path)
		if rel == "SKILL.md" {
			return nil
		}
		if len(files) >= maxFiles {
			return fs.SkipAll
		}
		var size int64
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}
		files = append(files, File{Path: filepath.ToSlash(rel), Size: size})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Detail{Skill: s, Content: string(data), Files: files}, nil
}

// Save replaces an editable skill's SKILL.md. The write goes to the real file
// behind any symlink, through a temp file and a rename, so a reader never
// sees half a skill and a symlinked SKILL.md stays a symlink.
func Save(r Roots, dir, content string) error {
	s, err := find(r, dir)
	if err != nil {
		return err
	}
	if !s.Editable {
		return ErrNotEditable
	}
	if err := validateContent(content); err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(s.Dir, "SKILL.md"))
	if err != nil {
		return err
	}
	return writeAtomic(target, []byte(content))
}

func validateContent(content string) error {
	fields, _, err := parseFrontmatter(content)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(fields["name"]) == "" {
		return fmt.Errorf("%w: frontmatter needs a name", ErrInvalid)
	}
	if err := validateDescription(fields["description"]); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ReadFile returns a file inside a discovered skill. rel may not leave the
// skill dir, directly or through a symlink. binary is true when the content
// is not UTF-8 text, in which case content is empty.
func ReadFile(r Roots, dir, rel string) (content string, binary bool, err error) {
	s, err := find(r, dir)
	if err != nil {
		return "", false, err
	}
	rel = filepath.FromSlash(rel)
	if rel == "" || filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
		return "", false, fmt.Errorf("%w: path must stay inside the skill", ErrInvalid)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(s.Dir, rel))
	if err != nil {
		return "", false, ErrNotFound
	}
	if inside, err := filepath.Rel(s.Dir, real); err != nil || !filepath.IsLocal(inside) {
		return "", false, fmt.Errorf("%w: path must stay inside the skill", ErrInvalid)
	}
	f, err := os.Open(real)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("%w: %s is a directory", ErrInvalid, rel)
	}
	if info.Size() > maxFileBytes {
		return "", false, fmt.Errorf("%w: %s is larger than 1 MiB", ErrInvalid, rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > maxFileBytes {
		return "", false, fmt.Errorf("%w: %s is larger than 1 MiB", ErrInvalid, rel)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", true, nil
	}
	return string(data), false, nil
}

// Create writes a new skill into the library for the scope, and links it into
// Claude's skills dir unless that dir already is the library. Codex and pi are
// left to reach the library their own way; a library neither reads shows the
// skill with a link action for each.
func Create(r Roots, scope, name, description string) (Skill, error) {
	if err := ValidateName(name); err != nil {
		return Skill{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	description = strings.TrimSpace(description)
	if err := validateDescription(description); err != nil {
		return Skill{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	library, err := LibraryDir(r, scope)
	if err != nil {
		return Skill{}, err
	}
	claudeDir := linkDir(r, Claude, scope)
	if claudeDir == "" {
		return Skill{}, fmt.Errorf("%w: no home directory", ErrInvalid)
	}
	for _, d := range []string{library, claudeDir} {
		if _, err := os.Lstat(filepath.Join(d, name)); err == nil {
			return Skill{}, fmt.Errorf("%w: %s already exists in %s", ErrInvalid, name, d)
		}
	}

	if err := os.MkdirAll(library, 0o755); err != nil {
		return Skill{}, err
	}
	dir := filepath.Join(library, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return Skill{}, err
	}
	content := "---\nname: " + name + "\ndescription: " + yamlScalar(description) + "\n---\n\n# " + name +
		"\n\nDescribe what this skill does and the steps to follow when it is used.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return Skill{}, err
	}

	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Skill{}, err
	}
	if LinkState(r, Claude, scope).State != LinkDirect {
		if err := symlinkTo(realDir, filepath.Join(claudeDir, name)); err != nil {
			return Skill{}, err
		}
	}
	s, err := find(r, realDir)
	if errors.Is(err, ErrNotFound) {
		return Skill{}, fmt.Errorf("created %s but it is not discoverable", realDir)
	}
	return s, err
}

// Remove deletes an editable skill: the real directory, every path it was
// discovered at that is itself a symlink to it, and its line in the library's
// source record. A path that only reaches it through a symlinked parent goes
// with the directory.
func Remove(r Roots, dir string) error {
	s, err := find(r, dir)
	if err != nil {
		return err
	}
	if !s.Editable {
		return ErrNotEditable
	}
	for _, p := range s.Paths {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(s.Dir); err != nil {
		return err
	}
	library := filepath.Dir(s.Dir)
	for _, lib := range []string{r.ProjectLibrary, r.Library} {
		if real, ok := realDir(lib); ok && real == library {
			return forgetRecord(library, filepath.Base(s.Dir))
		}
	}
	return nil
}
