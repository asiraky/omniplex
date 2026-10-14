// Package convmove moves a harness conversation's files between two config
// homes without ever replacing anything at the destination. Account
// switching (a thread moving to another login) and the move of Omniplex's
// conversations out of the machine's own harness homes both go through it,
// so a transcript is handled one way everywhere.
package convmove

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ErrExists is a destination that is already there. Nothing is replaced.
var ErrExists = errors.New("the destination already exists")

// rename is os.Rename; tests swap it to force the cross-filesystem path.
var rename = os.Rename

// Aside is where Move parks src while it copies across filesystems: the same
// folder, under a name no harness reads (a leading dot, a .moving suffix).
func Aside(src string) string {
	return filepath.Join(filepath.Dir(src), "."+filepath.Base(src)+".moving")
}

// Move moves src, a file or a whole folder, to dst, making dst's parent if
// needed. dst must not exist (ErrExists).
//
// On one filesystem it is a rename. Across filesystems src is first renamed
// aside (Aside, atomic), then copied to dst: a failed copy is removed and src
// put straight back, so the conversation is always whole in one place. Once
// the copy is whole the conversation has moved; the aside is under a name the
// harness never reads, so it cannot split the history. keepAside leaves it
// there (nothing is deleted); otherwise it is removed. copied reports that
// the copy path ran.
func Move(src, dst string, keepAside bool) (copied bool, err error) {
	if _, err := os.Lstat(dst); err == nil {
		return false, fmt.Errorf("%s: %w", dst, ErrExists)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	err = rename(src, dst)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return false, err
	}
	aside := Aside(src)
	// A leftover from an earlier move may be the only copy of something;
	// it is never removed or replaced here.
	if _, err := os.Lstat(aside); err == nil {
		return false, fmt.Errorf("an earlier move left %s; not replacing it", aside)
	}
	if err := os.Rename(src, aside); err != nil {
		return false, err
	}
	if err := copyTree(aside, dst); err != nil {
		_ = os.RemoveAll(dst) // only ever the partial copy: dst did not exist
		if back := os.Rename(aside, src); back != nil {
			return true, fmt.Errorf("%w (and restoring %s failed: %v)", err, src, back)
		}
		return true, err
	}
	if !keepAside {
		_ = os.RemoveAll(aside)
	}
	return true, nil
}

// copyTree copies src to dst, keeping modes and file times, refusing to
// write over anything.
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
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
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
