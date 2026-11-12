//go:build !linux && !darwin

package accounthome

// renameNoReplace has no atomic form here; Move checks, then renames.
func renameNoReplace(src, dst string) error {
	return errNoReplaceUnsupported
}
