//go:build !linux && !darwin

package convmove

// RenameNoReplace renames src to dst and fails with ErrExists when dst is
// there. This platform has no atomic no-replace rename, so it checks first.
func RenameNoReplace(src, dst string) error {
	return checkedRename(src, dst)
}
