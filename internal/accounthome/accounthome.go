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
// the one copy of that configuration, and no Omniplex account uses it as its
// home: its login is whatever a terminal last signed in as. Every account's
// home holds its own login and conversation state as real files, and a link
// to the shared copy (literally <shared>/<entry>) for each entry the harness
// declares as configuration. One account or three,
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
	Entry  string // the entry's name, as SharedEntries lists it
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
//   - account a link whose literal target is anything but the shared path,
//     or dangling: re-pointed at shared. A link that reaches the same file by
//     another route (straight at the dotfiles the shared entry links to) is
//     re-pointed too, so every account's home has the same shape;
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
		if err := link(filepath.Join(shared, e.Name), filepath.Join(account, e.Name), e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// UseShared gives up an account's own copy of one entry in favour of the
// shared one: the copy is moved aside to <entry>.account-backup (or
// .account-backup-2, -3, ... when that is taken), never overwritten and never
// deleted, and the entry is then linked. When there is no shared entry at
// that moment the account's copy becomes the shared one instead, as Link
// would make it, and nothing needs backing up.
func UseShared(shared, account string, entry Entry) error {
	if shared == "" || account == "" || samePath(shared, account) {
		return fmt.Errorf("%s is the shared home; there is nothing to share into it", account)
	}
	linking.Lock()
	defer linking.Unlock()
	src, dst := filepath.Join(shared, entry.Name), filepath.Join(account, entry.Name)
	if _, err := os.Stat(src); err == nil {
		info, err := os.Lstat(dst)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return err
		case info.Mode()&os.ModeSymlink == 0:
			if err := moveAside(dst, info.IsDir()); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return link(src, dst, entry)
}

// moveAside renames path to the first free <path>.account-backup[-n]. Like
// adopt, it never replaces anything already there.
func moveAside(path string, dir bool) error {
	for n := 1; ; n++ {
		backup := path + ".account-backup"
		if n > 1 {
			backup = fmt.Sprintf("%s-%d", backup, n)
		}
		if _, err := os.Lstat(backup); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		err := adopt(path, backup, dir)
		if err == nil {
			return nil
		}
		// Something took the name between the check and the move.
		if _, statErr := os.Lstat(backup); statErr == nil {
			continue
		}
		return fmt.Errorf("move %s aside: %w", path, err)
	}
}

// Conflicts lists every *ConflictError in an error Link returned.
func Conflicts(err error) []*ConflictError {
	if err == nil {
		return nil
	}
	if c, ok := err.(*ConflictError); ok {
		return []*ConflictError{c}
	}
	var out []*ConflictError
	switch u := err.(type) {
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			out = append(out, Conflicts(e)...)
		}
	case interface{ Unwrap() error }:
		out = Conflicts(u.Unwrap())
	}
	return out
}

func link(src, dst string, e Entry) error {
	dir := e.Dir
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() == dir {
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				return err
			}
			if err := adopt(dst, src, dir); err != nil {
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
		return &ConflictError{Entry: e.Name, Path: dst, Shared: src}
	case linksTo(dst, src):
		return nil
	default:
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.Symlink(src, dst); err != nil {
		// Another session preparing the same account got there first.
		if errors.Is(err, os.ErrExist) && linksTo(dst, src) {
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

// linksTo reports whether dst is a link whose literal target is src. Where
// it leads in the end does not count: the shape of every account's home is
// the same, a link to the shared entry.
func linksTo(dst, src string) bool {
	target, err := os.Readlink(dst)
	return err == nil && target == src
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
