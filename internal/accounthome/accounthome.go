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

// Link makes account present every entry of shared. It is idempotent and
// cheap enough to run before every session.
//
// For each entry:
//   - shared missing, account has a real one: the account's is moved into
//     shared (the first account to create a shared file makes it shared);
//   - shared missing, account a link that resolves: shared becomes a link to
//     the same place, so the account loses nothing when it is re-pointed;
//   - shared missing otherwise: a folder is made in shared, a file is skipped;
//   - account missing: linked to shared;
//   - account a link whose literal target is not shared's path — elsewhere,
//     dangling, or straight at whatever shared itself links to: re-pointed at
//     shared, so every account has the same shape;
//   - account a real entry while shared has one too: left as it is, and its
//     name returned in conflicts. Nothing an account owns is ever replaced;
//     UseShared is the explicit way to give it up.
//
// When account is shared itself there is nothing to do. Errors are joined;
// one bad entry does not stop the rest.
func Link(shared, account string, entries []Entry) (conflicts []string, err error) {
	if shared == "" || account == "" || samePath(shared, account) {
		return nil, nil
	}
	linking.Lock()
	defer linking.Unlock()
	if err := os.MkdirAll(account, 0o700); err != nil {
		return nil, err
	}
	var errs []error
	for _, e := range entries {
		conflict, err := link(filepath.Join(shared, e.Name), filepath.Join(account, e.Name), e.Dir)
		if conflict {
			conflicts = append(conflicts, e.Name)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return conflicts, errors.Join(errs...)
}

// BackupSuffix is appended to an account's own copy of a shared entry when
// UseShared moves it aside.
const BackupSuffix = ".account-backup"

// UseShared gives up the account's own copy of one shared entry: the copy is
// moved aside to <name>.account-backup (numbered if that is taken; nothing is
// overwritten or deleted) and the entry linked to shared. backup is where the
// copy went, "" when there was none to move.
func UseShared(shared, account string, e Entry) (backup string, err error) {
	if shared == "" || account == "" || samePath(shared, account) {
		return "", nil
	}
	linking.Lock()
	defer linking.Unlock()
	dst := filepath.Join(account, e.Name)
	if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink == 0 {
		for i := 1; ; i++ {
			backup = dst + BackupSuffix
			if i > 1 {
				backup = fmt.Sprintf("%s%s-%d", dst, BackupSuffix, i)
			}
			if _, err := Move(dst, backup); err == nil {
				break
			} else if !errors.Is(err, os.ErrExist) || i >= 100 {
				return "", fmt.Errorf("move %s aside: %w", dst, err)
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := link(filepath.Join(shared, e.Name), dst, e.Dir); err != nil {
		return backup, err
	}
	return backup, nil
}

// link presents src at dst; conflict reports dst being the account's own
// real entry while src exists.
func link(src, dst string, dir bool) (conflict bool, err error) {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink == 0 && info.IsDir() == dir {
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				return false, err
			}
			if err := adopt(dst, src, dir); err != nil {
				return false, fmt.Errorf("share %s: %w", dst, err)
			}
		} else if target, ok := liveLink(dst, dir); ok {
			// The account links somewhere real (straight at dotfiles, say)
			// and shared has nothing: shared takes the account's link, so
			// re-pointing the account below keeps what it saw.
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				return false, err
			}
			if err := os.Symlink(target, src); err != nil && !errors.Is(err, os.ErrExist) {
				return false, fmt.Errorf("share %s: %w", dst, err)
			}
		} else if dir {
			if err := os.MkdirAll(src, 0o755); err != nil {
				return false, err
			}
		} else {
			return false, nil
		}
	} else if err != nil {
		return false, err
	}

	info, err := os.Lstat(dst)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return false, err
	case info.Mode()&os.ModeSymlink == 0:
		return true, nil
	case linksTo(dst, src):
		return false, nil
	default:
		if err := os.Remove(dst); err != nil {
			return false, err
		}
	}
	if err := os.Symlink(src, dst); err != nil {
		// Another session preparing the same account got there first.
		if errors.Is(err, os.ErrExist) && linksTo(dst, src) {
			return false, nil
		}
		return false, err
	}
	return false, nil
}

// linksTo reports whether dst is a link whose literal target is src. Where it
// resolves to does not matter: a link straight at the final file has a
// different shape from every other account's, and is re-pointed.
func linksTo(dst, src string) bool {
	target, err := os.Readlink(dst)
	return err == nil && target == src
}

// liveLink reports dst's target when dst is a link that resolves to an entry
// of the expected kind, made absolute so it means the same from shared's
// folder as from the account's.
func liveLink(dst string, dir bool) (string, bool) {
	target, err := os.Readlink(dst)
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(dst); err != nil || info.IsDir() != dir {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dst), target)
	}
	return target, true
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
