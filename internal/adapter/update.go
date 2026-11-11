package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// FactVersion is the Availability fact every adapter reports its harness
// CLI's version under: the bare version number, so a UI can show it without
// knowing which harness printed it.
const FactVersion = "version"

// Executable is implemented by an adapter whose harness is a program on disk.
// It names the binary a session would run right now, found the same way a
// session finds it. The core watches it: a binary that changed under a
// running server has a different catalogue than the one cached.
type Executable interface {
	Executable() (string, bool)
}

// Updater is implemented by an adapter whose harness CLI can update itself in
// place. Nobody logs in to a server that only runs harnesses through omniplex,
// and at least Claude Code's own updater only runs from its interactive UI, so
// without this the CLI stays at whatever version was installed by hand.
type Updater interface {
	// Update runs the harness's own updater under an instance's environment
	// overlay (nil means ambient) and reports the version before and after.
	// It must leave running sessions alone: a process already started keeps
	// the binary it started with. An updater that fails returns an error
	// that says why in terms a person can act on.
	Update(ctx context.Context, env map[string]string) (UpdateResult, error)
}

// UpdateResult is one update run. From and To are equal when there was
// nothing newer to install.
type UpdateResult struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// Changed says whether the run installed a different version.
func (r UpdateResult) Changed() bool { return r.From != r.To }

// FileIdentity is a binary's identity on disk: the path after symlinks, its
// size, and its modification time. One stat, cheap enough to check on every
// listing, and it changes whenever an install replaces the file or repoints a
// link at a new version. Empty when the file is not there.
func FileIdentity(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
}
