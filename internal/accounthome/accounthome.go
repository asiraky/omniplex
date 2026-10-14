// Package accounthome makes every account of a harness see the same user
// configuration.
//
// A harness keeps one account's login in a config home (CLAUDE_CONFIG_DIR,
// CODEX_HOME, PI_CODING_AGENT_DIR), and none of them can move the login out on
// its own: Claude Code reads the account's identity from .claude.json inside
// the home, Codex and Pi read auth.json there. So a second account needs a
// second home. But the same home also holds what the user wrote: settings,
// instructions, skills, agents, commands, plugins. A second home that starts
// empty is a second, different user.
//
// The machine's own home (where the harness looks with no variable set) is
// the one copy of that configuration. Every other account's home holds its own
// login and conversation state as real files, and a link to the shared copy
// for each entry the harness declares as configuration. One account or three,
// each sees the same skills, settings and instructions; the only thing that
// differs is who is signed in.
package accounthome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// linking serialises Link within the process: every use of an account
// prepares its home, and two sessions starting on one account at once must
// not both adopt the same file.
var linking sync.Mutex

// Entry is one top-level name in a config home that is the user's
// configuration rather than one account's state.
type Entry struct {
	Name string
	// Dir entries are always shared: the shared folder is made if it is
	// missing, so whatever any account writes there lands in the one copy.
	// A file is shared once it exists somewhere.
	Dir bool
}

// Resolve turns a config-home value the way the harnesses read it: "~" and
// "~/" are the home folder, a relative path is relative to it, and blank
// means unset.
func Resolve(home, value string) string {
	v := strings.TrimSpace(value)
	switch {
	case v == "":
		return ""
	case v == "~":
		return home
	case strings.HasPrefix(v, "~/"):
		return filepath.Join(home, v[2:])
	case !filepath.IsAbs(v):
		return filepath.Join(home, v)
	}
	return filepath.Clean(v)
}

// ConflictError is an account home holding its own copy of an entry that the
// shared home also has. Nothing is merged or removed: the account keeps its
// copy until someone moves it aside.
type ConflictError struct {
	Path   string // the account's own entry
	Shared string // what it would link to
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is this account's own copy; move it aside so the account shares %s", e.Path, e.Shared)
}

// Link makes account present every entry of shared. It is idempotent and
// cheap enough to run before every session.
//
// For each entry:
//   - shared missing, account has a real one: the account's is moved into
//     shared (the first account to create a shared file makes it shared);
//   - shared missing otherwise: a folder is made in shared, a file is skipped;
//   - account missing: linked to shared;
//   - account a link anywhere else, or dangling: re-pointed at shared;
//   - account a real entry while shared has one too: a *ConflictError, left
//     as it is.
//
// When account is shared itself (the machine's own account) there is nothing
// to do. Errors are joined; one bad entry does not stop the rest.
func Link(shared, account string, entries []Entry) error {
	if shared == "" || account == "" || samePath(shared, account) {
		return nil
	}
	linking.Lock()
	defer linking.Unlock()
	if err := os.MkdirAll(account, 0o700); err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if err := link(filepath.Join(shared, e.Name), filepath.Join(account, e.Name), e.Dir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func link(src, dst string, dir bool) error {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() == dir {
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				return err
			}
			if err := os.Rename(dst, src); err != nil {
				return fmt.Errorf("share %s: %w", dst, err)
			}
		} else if dir {
			if err := os.MkdirAll(src, 0o755); err != nil {
				return err
			}
		} else {
			return nil
		}
	} else if err != nil {
		return err
	}

	info, err := os.Lstat(dst)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink == 0:
		return &ConflictError{Path: dst, Shared: src}
	case samePath(dst, src):
		return nil
	default:
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.Symlink(src, dst); err != nil {
		// Another session preparing the same account got there first.
		if errors.Is(err, os.ErrExist) && samePath(dst, src) {
			return nil
		}
		return err
	}
	return nil
}

// adopt moves an account's own entry to the shared path without ever
// replacing something that appeared there meanwhile (another process
// adopting its own copy, or this account's link already in place). A file is
// hard-linked, which fails if src exists, and then unlinked from the account.
// A folder is renamed, which fails onto anything but an empty folder.
func adopt(dst, src string, dir bool) error {
	if dir {
		return os.Rename(dst, src)
	}
	if err := os.Link(dst, src); err != nil {
		return err
	}
	return os.Remove(dst)
}

// samePath reports whether a and b resolve, through any links, to the same
// existing entry.
func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	return ra == rb
}
