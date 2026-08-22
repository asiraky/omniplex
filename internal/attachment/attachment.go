// Package attachment stores images a human attaches to a prompt.
//
// The bytes never travel on the event log. An image is uploaded once over
// HTTP, lands in a file under this store, and everything afterwards refers to
// it by id: the prompt command names ids, the turn.started event records ids,
// and any attached presenter reads the picture back through the same endpoint.
// That keeps snapshots, replay, and the sync protocol as small as they were —
// which matters most on the phone this UI is half used from, where a megabyte
// inlined into a replayed event would be paid for again on every reattach.
//
// Files are the storage because that is what the harnesses want. Codex takes a
// path (`localImage`) and reads it itself; the Claude sidecar reads the path
// and base64s it into a content block. Neither needs the bytes to pass through
// the server's memory a second time.
package attachment

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// MaxBytes bounds one image. Set by what the far end will take rather than by
// what a disk can hold: the Claude sidecar base64s the file into a content
// block, which inflates it by a third, and the API refuses an encoded image
// over 5 MB. 3.75 MB of raw bytes is exactly that limit encoded. The UI shrinks
// anything bigger before it uploads, so this is the backstop, not the rule.
const MaxBytes = 3_750_000

// MaxPerPrompt bounds how many images one message may carry. The sidecar holds
// every image of a prompt in memory, base64-expanded, at once; without a cap a
// bulk selection is a way to run Node out of heap.
const MaxPerPrompt = 12

// ErrTooLarge is returned when an upload exceeds MaxBytes.
var ErrTooLarge = errors.New("image is larger than 3.75 MB")

// ErrUnsupported is returned when the bytes are not an image this store keeps.
var ErrUnsupported = errors.New("only PNG, JPEG, GIF and WebP images are supported")

// extensions is the allowlist, keyed by the media type sniffed from the bytes.
// The client's declared type is never trusted: it decides nothing here.
var extensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// safeID matches the identifiers this store will build a path from. Session
// ids and attachment ids are both UUIDs; anything else is refused rather than
// cleaned, so no request can walk out of the store's directory.
var safeID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// Meta is what a caller learns about a stored image.
type Meta struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}

// Store keeps images under one directory, one subdirectory per session.
type Store struct{ dir string }

// New returns a store rooted at dir. The directory is created lazily, on the
// first upload: a server nobody attaches an image to leaves nothing behind.
//
// The root is made absolute here, because the paths this hands out are read by
// a harness process running in the session's checkout, not in the server's
// working directory. A relative -db path would otherwise produce paths that
// resolve for the server and for nobody else.
func New(dir string) *Store {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Store{dir: dir}
}

func (s *Store) sessionDir(sessionID string) (string, error) {
	if !safeID.MatchString(sessionID) {
		return "", errors.New("bad session id")
	}
	return filepath.Join(s.dir, sessionID), nil
}

// Put stores one image for a session and returns how to refer to it.
//
// The media type comes from sniffing the leading bytes, not from the upload's
// declared type or its file name, and an upload larger than MaxBytes is
// refused rather than truncated.
func (s *Store) Put(sessionID string, r io.Reader) (Meta, error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return Meta{}, err
	}

	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Meta{}, err
	}
	head = head[:n]
	mediaType, _, _ := strings.Cut(http.DetectContentType(head), ";")
	ext, ok := extensions[mediaType]
	if !ok {
		return Meta{}, ErrUnsupported
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Meta{}, err
	}
	id := uuid.NewString()
	path := filepath.Join(dir, id+ext)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Meta{}, err
	}
	// One byte past the limit is read deliberately: it is the difference
	// between "exactly at the limit" and "too big", and without it a file of
	// exactly MaxBytes would be indistinguishable from a truncated one.
	written, err := io.Copy(f, io.MultiReader(bytes.NewReader(head), io.LimitReader(r, MaxBytes+1-int64(n))))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && written > MaxBytes {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(path)
		return Meta{}, err
	}
	return Meta{ID: id, MediaType: mediaType, Size: written}, nil
}

// Path resolves a stored image to a file on disk, along with its media type.
// It fails for an id this store did not mint, and for one whose file has since
// been purged.
func (s *Store) Path(sessionID, id string) (path, mediaType string, err error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return "", "", err
	}
	if !safeID.MatchString(id) {
		return "", "", errors.New("bad attachment id")
	}
	for mt, ext := range extensions {
		candidate := filepath.Join(dir, id+ext)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, mt, nil
		}
	}
	return "", "", os.ErrNotExist
}

// Resolve turns the ids a prompt named into stored images, in the order they
// were given. An id that names nothing is an error: sending a prompt that
// silently lost one of its pictures is worse than not sending it.
func (s *Store) Resolve(sessionID string, ids []string) ([]Meta, []string, error) {
	metas := make([]Meta, 0, len(ids))
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		path, mediaType, err := s.Path(sessionID, id)
		if err != nil {
			return nil, nil, fmt.Errorf("attachment %s: %w", id, err)
		}
		metas = append(metas, Meta{ID: id, MediaType: mediaType})
		paths = append(paths, path)
	}
	return metas, paths, nil
}

// PurgeSession removes every image a session holds. Best effort by design: it
// is called on the delete path, where failing to remove a picture must not
// stop the session from going away.
func (s *Store) PurgeSession(sessionID string) error {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
