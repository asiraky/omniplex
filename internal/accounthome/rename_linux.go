package accounthome

import (
	"errors"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames src to dst, failing with EEXIST if dst exists.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return errNoReplaceUnsupported
	}
	return err
}
