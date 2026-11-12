package accounthome

import (
	"errors"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames src to dst, failing with EEXIST if dst exists.
func renameNoReplace(src, dst string) error {
	err := unix.RenamexNp(src, dst, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return errNoReplaceUnsupported
	}
	return err
}
