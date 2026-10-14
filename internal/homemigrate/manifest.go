package homemigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Manifest is the record of one run: every account it touched with its
// config before and after, and every conversation with each file's source,
// destination, size and sha256. It is written before anything moves and
// rewritten (temp file and rename) as each conversation completes, so a crash
// leaves an exact record, and Reverse can put everything back from it.
type Manifest struct {
	Version       int                     `json:"version"`
	Created       time.Time               `json:"created"`
	Finished      *time.Time              `json:"finished,omitempty"`
	Reversed      *time.Time              `json:"reversed,omitempty"`
	UserHome      string                  `json:"userHome"`
	Accounts      []*ManifestAccount      `json:"accounts"`
	Conversations []*ManifestConversation `json:"conversations"`

	path string
}

// ManifestAccount is one account of a run.
type ManifestAccount struct {
	Instance string `json:"instance"`
	Driver   string `json:"driver"`
	HomeEnv  string `json:"homeEnv"`
	Ambient  string `json:"ambient"`
	Target   string `json:"target"`
	Leftover bool   `json:"leftover,omitempty"`
	// ConfigBefore is the provider entry before the run, null when there was
	// none. ConfigAfter is what the run wrote, absent until it has.
	ConfigBefore json.RawMessage `json:"configBefore"`
	ConfigAfter  json.RawMessage `json:"configAfter,omitempty"`
	// CreatedShared are entries the sharing step made in the machine's home
	// because they were missing (empty folders). Reverse removes them while
	// they are still empty.
	CreatedShared []string `json:"createdShared,omitempty"`
	// Status is pending | migrated | stopped | reversed.
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// ManifestConversation is one conversation of a run.
type ManifestConversation struct {
	Instance string         `json:"instance"`
	Driver   string         `json:"driver"`
	ID       string         `json:"id"`
	Units    []ManifestUnit `json:"units"`
	Files    []ManifestFile `json:"files,omitempty"`
	// Status is pending | moved | skipped | conflict | failed | reversed.
	Status string `json:"status"`
	// Copied: the move crossed filesystems; each source is left renamed
	// aside (convmove.Aside) and was not deleted.
	Copied bool   `json:"copied,omitempty"`
	Note   string `json:"note,omitempty"`
}

// ManifestUnit is one path of a conversation, absolute on both sides.
type ManifestUnit struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Dir bool   `json:"dir,omitempty"`
}

// ManifestFile is one file as it was before it moved.
type ManifestFile struct {
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	// Link is a symlink's target; a link has no hash.
	Link string `json:"link,omitempty"`
}

// Conversation statuses in a manifest.
const (
	StatusPending  = "pending"
	StatusMoved    = "moved"
	StatusSkipped  = "skipped"
	StatusConflict = "conflict"
	StatusFailed   = "failed"
	StatusReversed = "reversed"
	StatusMigrated = "migrated"
	StatusStopped  = "stopped"
)

func migrationsDir(userHome string) string {
	return filepath.Join(userHome, ".omniplex", "migrations")
}

// create writes a new manifest under a name no earlier run has.
func (m *Manifest) create(userHome string, now time.Time) error {
	dir := migrationsDir(userHome)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := "homes-" + now.UTC().Format("20060102T150405Z")
	for i := 0; ; i++ {
		name := base + ".json"
		if i > 0 {
			name = fmt.Sprintf("%s-%d.json", base, i)
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		f.Close()
		m.path = path
		return m.save()
	}
}

// Path is where the manifest is.
func (m *Manifest) Path() string { return m.path }

func (m *Manifest) save() error {
	return writeJSON(m.path, m)
}

// ReadManifest loads a manifest written by a run.
func ReadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("%s: unknown manifest version %d", path, m.Version)
	}
	m.path = path
	return &m, nil
}

// writeJSON replaces path atomically: a temp file beside it, synced, renamed.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// hashUnits lists every file of the units with its size and sha256.
func hashUnits(units []ManifestUnit) ([]ManifestFile, error) {
	var out []ManifestFile
	for _, u := range units {
		err := filepath.WalkDir(u.Src, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(u.Src, path)
			if err != nil {
				return err
			}
			f := ManifestFile{Src: path, Dst: filepath.Join(u.Dst, rel)}
			switch {
			case d.Type()&fs.ModeSymlink != 0:
				if f.Link, err = os.Readlink(path); err != nil {
					return err
				}
			case d.Type().IsRegular():
				if f.Size, f.SHA256, err = hashFile(path); err != nil {
					return err
				}
			default:
				return nil
			}
			out = append(out, f)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func hashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// state is what runs leave for later runs, in
// ~/.omniplex/migrations/homes-state.json.
type state struct {
	// Pending are conversations a run had to leave because they were in use;
	// later runs move them once they are quiet.
	Pending []pending `json:"pending,omitempty"`
	// Stopped are accounts a run stopped on (a failed move, a failed check,
	// a config that would not save), by instance id, with its manifest. The
	// server leaves them for a person to look at.
	Stopped map[string]string `json:"stopped,omitempty"`
	// Hold is the manifest Reverse undid. While set, the server does not
	// migrate again.
	Hold string `json:"hold,omitempty"`

	// keep are pending entries this build cannot act on; they stay.
	keep []pending
}

type pending struct {
	Instance string `json:"instance"`
	Driver   string `json:"driver"`
	ID       string `json:"id"`
	Units    []Unit `json:"units"`
	Ambient  string `json:"ambient"`
	Target   string `json:"target"`
	Manifest string `json:"manifest"`
}

func statePath(userHome string) string {
	return filepath.Join(migrationsDir(userHome), "homes-state.json")
}

func loadState(userHome string) (*state, error) {
	st := &state{Stopped: map[string]string{}}
	b, err := os.ReadFile(statePath(userHome))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", statePath(userHome), err)
	}
	if st.Stopped == nil {
		st.Stopped = map[string]string{}
	}
	return st, nil
}

func saveState(userHome string, st *state) error {
	if err := os.MkdirAll(migrationsDir(userHome), 0o700); err != nil {
		return err
	}
	return writeJSON(statePath(userHome), st)
}
