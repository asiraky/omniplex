// Package artefact stores the outputs a session produces: a report the agent
// wrote, a prototype it built, a PDF someone dropped into the composer. Any
// file type, a single file or a directory bundle.
//
// The bytes live here, beside the database and outside every worktree, because
// a worktree is deleted with its session's workspace while what the session
// produced is worth keeping. Which artefacts a session has, and their versions,
// is in the event log (artefact.published). This package only keeps the bytes,
// laid out as <dir>/<session>/<artefact>/<version>/<files...>.
//
// A version is written once and never changed: it is staged in a temporary
// directory and renamed into place, so a reader never sees half a bundle.
package artefact

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// MaxBytes caps one version, across every file in a bundle.
	MaxBytes = 200 << 20
	// MaxFiles caps the files in one bundle. A prototype with node_modules in
	// it is a mistake, not an artefact.
	MaxFiles = 2000
)

var (
	ErrTooLarge = errors.New("artefact is too large")
	ErrNotFound = errors.New("artefact not found")
	ErrBadPath  = errors.New("bad artefact path")
	ErrEmpty    = errors.New("artefact is empty")

	safeID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// Meta describes one stored version.
type Meta struct {
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	// Entry is the file a viewer opens: the single file, or a bundle's
	// index.html.
	Entry string `json:"entry"`
	Files int    `json:"files"`
}

type Store struct{ dir string }

func New(dir string) *Store {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Store{dir: dir}
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) versionDir(session, id string, version int) (string, error) {
	if !safeID.MatchString(session) || !safeID.MatchString(id) || version < 1 {
		return "", ErrBadPath
	}
	return filepath.Join(s.dir, session, id, strconv.Itoa(version)), nil
}

// Staged is a version whose bytes are written but which has no identity yet.
// Staging is the slow part (an upload over 4G, a bundle off disk) and happens
// outside the session actor; Commit is one rename, cheap enough to run inside
// it, which is where the version number is decided.
type Staged struct {
	Meta
	session string
	tmp     string
	store   *Store
}

// Commit moves the staged bytes into place as id's version.
func (st *Staged) Commit(id string, version int) error {
	dst, err := st.store.versionDir(st.session, id, version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(st.tmp, dst); err != nil {
		return err
	}
	st.tmp = ""
	return nil
}

// Discard drops staged bytes that were never committed. Safe after Commit.
func (st *Staged) Discard() {
	if st.tmp != "" {
		os.RemoveAll(st.tmp)
		st.tmp = ""
	}
}

// StageFile stages a single file.
func (s *Store) StageFile(session, name string, r io.Reader) (*Staged, error) {
	name = cleanName(name)
	if name == "" {
		return nil, ErrBadPath
	}
	return s.stage(session, func(tmp string) error {
		n, err := writeFile(filepath.Join(tmp, name), r, MaxBytes)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrEmpty
		}
		return nil
	})
}

// StageTar stages the contents of a tar stream. A tar holding one regular file
// is a single-file artefact; anything more is a bundle. Links and anything
// that is not a regular file are skipped rather than trusted.
func (s *Store) StageTar(session string, r io.Reader) (*Staged, error) {
	return s.stage(session, func(tmp string) error {
		tr := tar.NewReader(r)
		var total int64
		files := 0
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			rel, ok := safeRel(h.Name)
			if !ok {
				return fmt.Errorf("%w: %q", ErrBadPath, h.Name)
			}
			files++
			if files > MaxFiles {
				return fmt.Errorf("%w: more than %d files", ErrTooLarge, MaxFiles)
			}
			dst := filepath.Join(tmp, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			n, err := writeFile(dst, tr, MaxBytes-total)
			if err != nil {
				return err
			}
			total += n
		}
		if files == 0 {
			return ErrEmpty
		}
		return nil
	})
}

func (s *Store) stage(session string, fill func(tmp string) error) (*Staged, error) {
	if !safeID.MatchString(session) {
		return nil, ErrBadPath
	}
	root := filepath.Join(s.dir, session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return nil, err
	}
	if err := fill(tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	meta, err := describe(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return &Staged{Meta: meta, session: session, tmp: tmp, store: s}, nil
}

// describe works out the entry, media type, size and file count of a staged
// version.
func describe(root string) (Meta, error) {
	var files []string
	var size int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		rel, _ := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return Meta{}, err
	}
	if len(files) == 0 {
		return Meta{}, ErrEmpty
	}
	sort.Strings(files)
	entry := pickEntry(files)
	return Meta{
		MediaType: MediaType(filepath.Join(root, filepath.FromSlash(entry))),
		Size:      size,
		Entry:     entry,
		Files:     len(files),
	}, nil
}

// pickEntry chooses what a bundle opens on: the shallowest index.html, else
// the shallowest HTML file, else the shallowest README or markdown file, else
// the first file.
func pickEntry(files []string) string {
	if len(files) == 1 {
		return files[0]
	}
	best, bestScore := files[0], 1<<30
	for _, f := range files {
		base := strings.ToLower(path.Base(f))
		ext := path.Ext(base)
		rank := 4
		switch {
		case base == "index.html" || base == "index.htm":
			rank = 0
		case ext == ".html" || ext == ".htm":
			rank = 1
		case strings.HasPrefix(base, "readme"):
			rank = 2
		case ext == ".md":
			rank = 3
		}
		score := rank*1000 + strings.Count(f, "/")
		if score < bestScore {
			best, bestScore = f, score
		}
	}
	return best
}

// Open resolves a file inside a stored version. rel is slash-separated and
// relative to the version; anything that climbs out is refused.
func (s *Store) Open(session, id string, version int, rel string) (string, error) {
	dir, err := s.versionDir(session, id, version)
	if err != nil {
		return "", err
	}
	clean, ok := safeRel(rel)
	if !ok {
		return "", ErrBadPath
	}
	p := filepath.Join(dir, filepath.FromSlash(clean))
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrNotFound
	}
	return p, nil
}

// Latest is the highest version stored for an artefact, read off disk so a
// share link that follows the latest version needs nothing but the store.
func (s *Store) Latest(session, id string) (int, error) {
	if !safeID.MatchString(session) || !safeID.MatchString(id) {
		return 0, ErrBadPath
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, session, id))
	if err != nil {
		return 0, ErrNotFound
	}
	latest := 0
	for _, e := range entries {
		if v, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() && v > latest {
			latest = v
		}
	}
	if latest == 0 {
		return 0, ErrNotFound
	}
	return latest, nil
}

// Entry re-derives the entry of a stored version, for a caller that only has
// the store (a share link following the latest version).
func (s *Store) Entry(session, id string, version int) (Meta, error) {
	dir, err := s.versionDir(session, id, version)
	if err != nil {
		return Meta{}, err
	}
	if _, err := os.Stat(dir); err != nil {
		return Meta{}, ErrNotFound
	}
	return describe(dir)
}

// PurgeSession deletes everything a session stored.
func (s *Store) PurgeSession(session string) error {
	if !safeID.MatchString(session) {
		return ErrBadPath
	}
	return os.RemoveAll(filepath.Join(s.dir, session))
}

// MediaType names a file's type by extension first, because sniffing cannot
// tell markdown from plain text or CSV from anything, and falls back to
// sniffing the first bytes.
func MediaType(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	if t, ok := extraTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	f, err := os.Open(p)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	return http.DetectContentType(buf[:n])
}

// extraTypes covers what the system mime table often gets wrong or lacks.
var extraTypes = map[string]string{
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
	".csv":      "text/csv; charset=utf-8",
	".tsv":      "text/tab-separated-values; charset=utf-8",
	".json":     "application/json",
	".yaml":     "application/yaml",
	".yml":      "application/yaml",
	".txt":      "text/plain; charset=utf-8",
	".log":      "text/plain; charset=utf-8",
	".html":     "text/html; charset=utf-8",
	".htm":      "text/html; charset=utf-8",
	".svg":      "image/svg+xml",
	".js":       "text/javascript; charset=utf-8",
	".mjs":      "text/javascript; charset=utf-8",
	".ts":       "text/x-typescript; charset=utf-8",
	".tsx":      "text/x-typescript; charset=utf-8",
	".go":       "text/x-go; charset=utf-8",
	".py":       "text/x-python; charset=utf-8",
	".sh":       "text/x-shellscript; charset=utf-8",
	".sql":      "text/x-sql; charset=utf-8",
	".pdf":      "application/pdf",
	".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx":     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".m4a":      "audio/mp4",
	".mp3":      "audio/mpeg",
	".wav":      "audio/wav",
	".webm":     "video/webm",
	".mp4":      "video/mp4",
	".mov":      "video/quicktime",
}

// cleanName reduces an uploaded file name to a single safe path segment.
func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "" {
		return ""
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	return name
}

// safeRel cleans a slash-separated relative path and reports whether it stays
// inside its root.
func safeRel(rel string) (string, bool) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsRune(rel, 0) {
		return "", false
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

// writeFile writes at most limit bytes from r to a new file at p.
func writeFile(p string, r io.Reader, limit int64) (int64, error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, ErrTooLarge
	}
	return n, nil
}
