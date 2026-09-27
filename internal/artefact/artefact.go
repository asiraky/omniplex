// Package artefact deals with the things a thread shows you: a report the
// agent wrote, a prototype it built, a PDF someone dropped into the composer.
// Any file type, a single file or a folder.
//
// An artefact is a real file in the project. The agent writes it where it
// works, revises it in place, and the app reads it live, so there is one copy
// and no version history. Which files a thread has shown is in the event log
// (artefact.shown).
//
// The only bytes this package keeps are share snapshots. Sharing copies the
// artefact as it is right now into <dir>/<thread>/<artefact>/, so the person
// holding the link never sees a half-edited file and the agent can keep
// working. Updating the share takes a fresh copy behind the same link.
package artefact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	"strings"
	"sync"
	"time"
)

const (
	// MaxBytes caps what one share copies and what one upload writes.
	MaxBytes = 200 << 20
	// MaxFiles caps the files in a folder artefact. A prototype with
	// node_modules in it is a mistake, not an artefact.
	MaxFiles = 2000
	// ShareTTL is how long a share link lasts after it was last updated.
	ShareTTL = 7 * 24 * time.Hour
)

var (
	ErrTooLarge = errors.New("artefact is too large")
	ErrNotFound = errors.New("artefact not found")
	ErrBadPath  = errors.New("bad artefact path")
	ErrEmpty    = errors.New("artefact is empty")

	safeID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// Info describes an artefact as it is on disk now.
type Info struct {
	Dir       bool   `json:"dir"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	// Entry is the file a viewer opens, relative to the artefact: the file's
	// own name, or a folder's index.html.
	Entry string `json:"entry"`
	Files int    `json:"files"`
	// ModifiedAt is the newest file's modification time, in millis. The app
	// uses it to know a revision is not the copy it already has.
	ModifiedAt int64 `json:"modifiedAt"`
}

// skipped is what is never part of an artefact: hidden files (.git, .env,
// .DS_Store) and installed dependencies.
func skipped(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Describe reads the artefact at p, a file or a folder.
func Describe(p string) (Info, error) {
	info, err := os.Stat(p)
	if err != nil {
		return Info{}, ErrNotFound
	}
	if info.Mode().IsRegular() {
		if info.Size() == 0 {
			return Info{}, ErrEmpty
		}
		return Info{
			MediaType: MediaType(p), Size: info.Size(), Entry: filepath.Base(p), Files: 1,
			ModifiedAt: info.ModTime().UnixMilli(),
		}, nil
	}
	if !info.IsDir() {
		return Info{}, ErrBadPath
	}
	files, size, modified, err := walk(p)
	if err != nil {
		return Info{}, err
	}
	entry := pickEntry(files)
	return Info{
		Dir: true, MediaType: MediaType(filepath.Join(p, filepath.FromSlash(entry))), Size: size,
		Entry: entry, Files: len(files), ModifiedAt: modified,
	}, nil
}

// walk lists a folder artefact's files, slash-separated and sorted. Symlinks
// are left out: following one could put anything on disk into a share.
func walk(root string) (files []string, size, modified int64, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if skipped(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if len(files) == MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrTooLarge, MaxFiles)
		}
		size += info.Size()
		modified = max(modified, info.ModTime().UnixMilli())
		rel, _ := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, 0, 0, err
	}
	if len(files) == 0 {
		return nil, 0, 0, ErrEmpty
	}
	sort.Strings(files)
	return files, size, modified, nil
}

// pickEntry chooses what a folder opens on: the shallowest index.html, else
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

// Resolve finds the file rel names inside the artefact at root. A single-file
// artefact serves only itself. A folder serves what is inside it and nothing
// through a symlink or a "..", and never a hidden file.
func Resolve(root string, dir bool, rel string) (string, error) {
	clean, ok := safeRel(rel)
	if !ok {
		return "", ErrBadPath
	}
	if !dir {
		if clean != filepath.Base(root) {
			return "", ErrNotFound
		}
		return regular(root)
	}
	for _, seg := range strings.Split(clean, "/") {
		if skipped(seg) {
			return "", ErrNotFound
		}
	}
	p, err := regular(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrNotFound
	}
	// Nothing inside the folder may be a symlink: one could reach a hidden
	// file or leave the folder, and a share, which skips symlinks, would not
	// have it. Resolved, the file sits exactly where its name says.
	if rel, err := filepath.Rel(realRoot, p); err != nil || filepath.ToSlash(rel) != clean {
		return "", ErrNotFound
	}
	return p, nil
}

// regular resolves p's symlinks and insists on a regular file.
func regular(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", ErrNotFound
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrNotFound
	}
	return real, nil
}

// Within reports whether p is root or inside it. Both should be clean and
// absolute.
func Within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ---- shares ----

// Share is a snapshot someone can open with a link.
type Share struct {
	// Nonce is in the link. Stopping a share forgets it, so a link that was
	// stopped stays dead even if the artefact is shared again.
	Nonce     string `json:"nonce"`
	Entry     string `json:"entry"`
	SharedAt  int64  `json:"sharedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Expired reports whether the share has run out.
func (s Share) Expired(now time.Time) bool { return now.UnixMilli() > s.ExpiresAt }

// Store keeps share snapshots.
type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) *Store {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Store{dir: dir}
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) shareDir(thread, id string) (string, error) {
	if !safeID.MatchString(thread) || !safeID.MatchString(id) {
		return "", ErrBadPath
	}
	return filepath.Join(s.dir, thread, id), nil
}

// Snapshot copies the artefact at src into the share for (thread, id),
// replacing any earlier copy. The link stays the same when there already is a
// share; the week it lasts starts again.
func (s *Store) Snapshot(thread, id, src string, now time.Time) (Share, error) {
	dir, err := s.shareDir(thread, id)
	if err != nil {
		return Share{}, err
	}
	info, err := Describe(src)
	if err != nil {
		return Share{}, err
	}
	if info.Size > MaxBytes {
		return Share{}, ErrTooLarge
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Share{}, err
	}
	tmp, err := os.MkdirTemp(dir, ".staging-")
	if err != nil {
		return Share{}, err
	}
	defer os.RemoveAll(tmp)
	if err := copyArtefact(src, info, tmp); err != nil {
		return Share{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	prev, err := s.readShare(dir)
	nonce := prev.Nonce
	if err != nil || nonce == "" {
		nonce = newNonce()
	}
	sh := Share{Nonce: nonce, Entry: info.Entry, SharedAt: now.UnixMilli(), ExpiresAt: now.Add(ShareTTL).UnixMilli()}

	// Swap the new copy in with renames, so a reader mid-page sees the old
	// files or the new ones and never a mix.
	files := filepath.Join(dir, "files")
	old := filepath.Join(tmp, "old")
	if err := os.Rename(files, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Share{}, err
	}
	if err := os.Rename(filepath.Join(tmp, "files"), files); err != nil {
		return Share{}, err
	}
	b, _ := json.Marshal(sh)
	if err := writeAtomic(filepath.Join(dir, "share.json"), b); err != nil {
		return Share{}, err
	}
	return sh, nil
}

func copyArtefact(src string, info Info, tmp string) error {
	dst := filepath.Join(tmp, "files")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if !info.Dir {
		return copyFile(src, filepath.Join(dst, info.Entry))
	}
	files, _, _, err := walk(src)
	if err != nil {
		return err
	}
	for _, rel := range files {
		to := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := copyFile(filepath.Join(src, filepath.FromSlash(rel)), to); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = writeFile(to, in, MaxBytes)
	return err
}

// Share reads the share for (thread, id), expired or not.
func (s *Store) Share(thread, id string) (Share, error) {
	dir, err := s.shareDir(thread, id)
	if err != nil {
		return Share{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readShare(dir)
}

func (s *Store) readShare(dir string) (Share, error) {
	b, err := os.ReadFile(filepath.Join(dir, "share.json"))
	if err != nil {
		return Share{}, ErrNotFound
	}
	var sh Share
	if err := json.Unmarshal(b, &sh); err != nil || sh.Nonce == "" {
		return Share{}, ErrNotFound
	}
	return sh, nil
}

// Unshare deletes the share. Its link stops working at once.
func (s *Store) Unshare(thread, id string) error {
	dir, err := s.shareDir(thread, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(dir)
}

// OpenShared resolves a file inside a share's snapshot.
func (s *Store) OpenShared(thread, id, rel string) (string, error) {
	dir, err := s.shareDir(thread, id)
	if err != nil {
		return "", err
	}
	return Resolve(filepath.Join(dir, "files"), true, rel)
}

// PurgeThread deletes every share a thread made. The artefacts themselves
// are the project's files and are left alone.
func (s *Store) PurgeThread(thread string) error {
	if !safeID.MatchString(thread) {
		return ErrBadPath
	}
	return os.RemoveAll(filepath.Join(s.dir, thread))
}

func newNonce() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeAtomic(p string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// ---- uploads ----

// SaveUpload writes a file a human uploaded into dir under its own name,
// numbered the way a download folder would when the name is taken. It returns
// the path written.
func SaveUpload(dir, name string, r io.Reader) (string, error) {
	name = cleanName(name)
	if name == "" {
		return "", ErrBadPath
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, candidate)
		n, err := writeFile(p, r, MaxBytes)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err == nil && n == 0 {
			err = ErrEmpty
		}
		if err != nil {
			os.Remove(p)
			return "", err
		}
		return p, nil
	}
}

// ---- helpers ----

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

// cleanName reduces an uploaded file name to a single safe path segment. A
// leading dot is dropped so an upload is never a hidden file.
func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
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
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
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
