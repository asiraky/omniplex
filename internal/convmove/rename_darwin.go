package convmove

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace renames src to dst and fails with ErrExists when dst is
// there, atomically: renamex_np with RENAME_EXCL. A filesystem without the
// flag (ENOTSUP, EINVAL) gets the check-then-rename fallback; EXDEV comes
// back as it is.
func RenameNoReplace(src, dst string) error {
	err := unix.RenamexNp(src, dst, unix.RENAME_EXCL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%s: %w", dst, ErrExists)
	case errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		return checkedRename(src, dst)
	}
	return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
}
