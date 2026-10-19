package convmove

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace renames src to dst and fails with ErrExists when dst is
// there, atomically: renameat2 with RENAME_NOREPLACE, so nothing created at
// dst a moment earlier is replaced. A filesystem without the flag (EINVAL,
// ENOSYS) gets the check-then-rename fallback; EXDEV comes back as it is.
func RenameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("%s: %w", dst, ErrExists)
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		return checkedRename(src, dst)
	}
	return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
}
