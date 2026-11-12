package accounthome

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// InstanceHome is the config home Omniplex gives the account with this id:
// ~/.omniplex/instances/<id>. Every account of every driver lives here, never
// in the machine's own harness home, so a sign-in from a terminal on the box
// cannot change who an Omniplex account is.
func InstanceHome(userHome, id string) string {
	return filepath.Join(userHome, ".omniplex", "instances", id)
}

// Move moves src (a file or a folder) to dst and never replaces anything: a
// dst that exists, or appears during the move, is an error wrapping
// fs.ErrExist and src is left where it was.
//
// On one filesystem it is a single rename. Across filesystems src is first
// renamed aside, within its own folder, under a name no harness reads, and
// copied from there; a failed copy is removed and src put back. A whole copy
// leaves the aside in place and returns its path: Move itself deletes nothing
// the caller had, so whether the leftover goes is the caller's decision.
func Move(src, dst string) (aside string, err error) {
	if _, err := os.Lstat(src); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	err = renameNoReplace(src, dst)
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, syscall.EEXIST):
		return "", fmt.Errorf("%s already exists; not overwriting it: %w", dst, fs.ErrExist)
	case errors.Is(err, errNoReplaceUnsupported):
		// A filesystem or OS without a no-replace rename: check, then
		// rename. The window between the two is the only place an overwrite
		// could slip in, and nothing else writes these paths.
		if _, statErr := os.Lstat(dst); statErr == nil {
			return "", fmt.Errorf("%s already exists; not overwriting it: %w", dst, fs.ErrExist)
		}
		err = os.Rename(src, dst)
		if err == nil {
			return "", nil
		}
		if !errors.Is(err, syscall.EXDEV) {
			return "", err
		}
	case !errors.Is(err, syscall.EXDEV):
		return "", err
	}

	if _, statErr := os.Lstat(dst); statErr == nil {
		return "", fmt.Errorf("%s already exists; not overwriting it: %w", dst, fs.ErrExist)
	}
	aside, err = asideName(src)
	if err != nil {
		return "", err
	}
	if err := os.Rename(src, aside); err != nil {
		return "", err
	}
	if err := copyTree(aside, dst); err != nil {
		// Only what this copy made is removed: copyTree creates dst and every
		// entry under it exclusively, so a dst that appeared meanwhile failed
		// the first create and is not this copy's to remove.
		if !errors.Is(err, fs.ErrExist) || !isRootExists(err, dst) {
			_ = os.RemoveAll(dst)
		}
		if back := os.Rename(aside, src); back != nil {
			return "", fmt.Errorf("%w (and restoring %s from %s failed: %v)", err, src, aside, back)
		}
		return "", err
	}
	return aside, nil
}

// MoveAndDiscardAside is Move for a caller whose src is gone once dst is
// whole: a cross-filesystem copy's aside is deleted rather than returned, and
// failing to delete it is not an error, since the move itself is complete.
func MoveAndDiscardAside(src, dst string) error {
	aside, err := Move(src, dst)
	if aside != "" {
		_ = os.RemoveAll(aside)
	}
	return err
}

// errNoReplaceUnsupported is renameNoReplace saying this OS or filesystem
// cannot rename without replacing; Move falls back to check-then-rename.
var errNoReplaceUnsupported = errors.New("no-replace rename unsupported")

// isRootExists reports whether err is copyTree failing to create dst itself.
func isRootExists(err error, dst string) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && filepath.Clean(pe.Path) == filepath.Clean(dst)
}

// asideName is a free name next to src that no harness reads: a dot-file
// ending in .moving, numbered when an earlier move left one behind.
func asideName(src string) (string, error) {
	base := filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = base + "-" + strconv.Itoa(i+1)
		}
		if _, err := os.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no free name to set %s aside", src)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Mkdir(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
}
