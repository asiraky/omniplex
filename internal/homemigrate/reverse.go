package homemigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/convmove"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// ReverseReport is what Reverse did.
type ReverseReport struct {
	Manifest string `json:"manifest"`
	// Restored conversations are back where the run found them.
	Restored int `json:"restored"`
	// Changed is how many of them were written to after they moved (a
	// thread carried on in the new home); they went back as they are now.
	Changed int `json:"changed"`
	// Conflicts were not moved back because something is at the original
	// path again; Missing are at neither path.
	Conflicts []string `json:"conflicts,omitempty"`
	Missing   []string `json:"missing,omitempty"`
	// Asides are sources a cross-filesystem copy left renamed aside, which
	// Reverse does not delete either.
	Asides []string `json:"asides,omitempty"`
	Config []string `json:"config,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

// Reverse undoes the run recorded in a manifest: every conversation it moved
// goes back to the path it came from, under the same never-overwrite rule,
// and each account's provider entry is put back as it was before the run
// (an entry the run created stays, without the home variable, so the
// threads on it keep an account; one edited since keeps the edits and only
// loses the home variable the run set). An account with a conversation that
// cannot go back keeps its config and its place in the manifest, so running
// Reverse again finishes it. Afterwards the migration is on hold: the server
// will not run it again until the command line does.
func Reverse(ctx context.Context, manifestPath string, opts Options) (*ReverseReport, error) {
	m, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	userHome, err := opts.home()
	if err != nil {
		return nil, err
	}
	if filepath.Clean(m.UserHome) != userHome {
		return nil, fmt.Errorf("%s was written for the home folder %s, not %s", manifestPath, m.UserHome, userHome)
	}
	rep := &ReverseReport{Manifest: m.path}

	// failed are accounts with a conversation that could not go back; their
	// config stays as the run left it, so a rerun can finish them.
	failed := map[string]int{}
	for i := len(m.Conversations) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		mc := m.Conversations[i]
		switch mc.Status {
		case StatusMoved, StatusPending, StatusFailed:
		default:
			continue
		}
		restored, problem := restoreConversation(mc, rep)
		if problem != "" {
			failed[mc.Instance]++
			if problem != inNeitherPlace { // already listed as missing
				rep.Conflicts = append(rep.Conflicts, mc.ID+": "+problem)
			}
			_ = m.save()
			continue
		}
		if !restored {
			continue // never moved (a pending entry from a run that stopped)
		}
		mc.Status = StatusReversed
		rep.Restored++
		if err := m.save(); err != nil {
			return rep, fmt.Errorf("update the manifest: %w", err)
		}
	}

	reversed := map[string]bool{}
	for _, ma := range m.Accounts {
		if n := failed[ma.Instance]; n > 0 {
			rep.Config = append(rep.Config, fmt.Sprintf("%s: NOT restored: %d of its conversations could not be moved back, so it stays on %s; sort them out, then run omniplex migrate-homes --reverse %s again", ma.Instance, n, ma.Target, m.path))
			continue
		}
		reversed[ma.Instance] = true
		if ma.Status != StatusMigrated && ma.Status != StatusStopped {
			continue
		}
		if len(ma.ConfigAfter) > 0 {
			note, err := restoreConfig(ma)
			if err != nil {
				return rep, fmt.Errorf("restore the config of %s: %w", ma.Instance, err)
			}
			rep.Config = append(rep.Config, ma.Instance+": "+note)
		}
		for _, path := range ma.CreatedShared {
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			if !info.IsDir() || os.Remove(path) != nil {
				rep.Notes = append(rep.Notes, path+" was made by the migration's sharing step and is no longer empty; left as it is")
			}
		}
		ma.Status = StatusReversed
	}
	if len(failed) == 0 {
		now := opts.now()
		m.Reversed = &now
	}
	if err := m.save(); err != nil {
		return rep, fmt.Errorf("update the manifest: %w", err)
	}

	st, err := loadState(userHome)
	if err != nil {
		return rep, err
	}
	var keep []pending
	for _, lp := range st.Pending {
		if !reversed[lp.Instance] {
			keep = append(keep, lp)
		}
	}
	st.Pending = keep
	for id := range reversed {
		delete(st.Stopped, id)
	}
	st.Hold = m.path
	if err := saveState(userHome, st); err != nil {
		return rep, err
	}
	rep.Notes = append(rep.Notes, laterRuns(m, reversed)...)
	return rep, nil
}

// restoreConfig puts an account's provider entry back as the manifest
// recorded it before the run.
func restoreConfig(ma *ManifestAccount) (string, error) {
	var note string
	before := ma.ConfigBefore
	if string(before) == "null" {
		before = nil
	}
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		idx := -1
		for i, raw := range cfg.Providers {
			var e struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &e) == nil && e.ID == ma.Instance {
				idx = i
				break
			}
		}
		switch {
		case idx < 0:
			note = "no provider entry any more; nothing to restore"
		case sameJSON(cfg.Providers[idx], ma.ConfigAfter) && before == nil:
			// The migration added it for the implicit default account its
			// threads run on. Removing it would leave them no account at
			// all, so it stays, without the home variable: on the
			// machine's home, as before.
			raw, err := restoreHome(cfg.Providers[idx], nil, ma.HomeEnv)
			if err != nil {
				return err
			}
			cfg.Providers[idx] = raw
			note = "kept the entry the migration added for the account's threads, without " + ma.HomeEnv + ", so it runs on the machine's home again"
		case sameJSON(cfg.Providers[idx], ma.ConfigAfter):
			cfg.Providers[idx] = before
			note = "restored the entry as it was before the migration"
		default:
			raw, err := restoreHome(cfg.Providers[idx], before, ma.HomeEnv)
			if err != nil {
				return err
			}
			cfg.Providers[idx] = raw
			note = "the entry was edited since the migration; kept the edits and put " + ma.HomeEnv + " back as it was"
		}
		return nil
	})
	return note, err
}

// restoreHome gives entry the home variable before had: its env item as it
// was, or none.
func restoreHome(entry, before json.RawMessage, name string) (json.RawMessage, error) {
	envOf := func(raw json.RawMessage) ([]map[string]json.RawMessage, map[string]json.RawMessage, error) {
		var generic map[string]json.RawMessage
		if len(raw) == 0 {
			return nil, map[string]json.RawMessage{}, nil
		}
		if err := json.Unmarshal(raw, &generic); err != nil {
			return nil, nil, err
		}
		var env []map[string]json.RawMessage
		if b, ok := generic["env"]; ok && string(b) != "null" {
			if err := json.Unmarshal(b, &env); err != nil {
				return nil, nil, err
			}
		}
		return env, generic, nil
	}
	isHome := func(e map[string]json.RawMessage) bool {
		var n string
		return json.Unmarshal(e["name"], &n) == nil && n == name
	}
	env, generic, err := envOf(entry)
	if err != nil {
		return nil, err
	}
	prev, _, err := envOf(before)
	if err != nil {
		return nil, err
	}
	var old map[string]json.RawMessage
	for _, e := range prev {
		if isHome(e) {
			old = e
		}
	}
	var out []map[string]json.RawMessage
	for _, e := range env {
		switch {
		case !isHome(e):
			out = append(out, e)
		case old != nil:
			out = append(out, old)
			old = nil
		}
	}
	if len(out) == 0 {
		delete(generic, "env")
		return json.Marshal(generic)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	generic["env"] = b
	return json.Marshal(generic)
}

// laterRuns names manifests written after m that moved conversations of the
// same accounts (leftovers taken up later); reversing m does not undo them.
func laterRuns(m *Manifest, accounts map[string]bool) []string {
	paths, _ := filepath.Glob(filepath.Join(filepath.Dir(m.path), "homes-*.json"))
	var out []string
	for _, path := range paths {
		if path == m.path || strings.HasSuffix(path, "homes-state.json") {
			continue
		}
		other, err := ReadManifest(path)
		if err != nil || other.Reversed != nil || !other.Created.After(m.Created) {
			continue
		}
		for _, ma := range other.Accounts {
			if accounts[ma.Instance] {
				out = append(out, fmt.Sprintf("%s moved more of %s's conversations later; reverse it too: omniplex migrate-homes --reverse %s", path, ma.Instance, path))
				break
			}
		}
	}
	return out
}

const inNeitherPlace = "in neither place"

// restoreConversation puts one conversation of a run back at its source. Each
// unit comes back from whichever copy matches the manifest: the destination
// (written to since is fine: transcripts only grow, so it matches when it
// starts with the bytes the manifest hashed), or else the original a
// cross-filesystem copy left aside, when that copy was cut short. A partial
// destination is renamed aside, never deleted. restored is false when
// nothing of it had moved; problem is why it was left as it is.
func restoreConversation(mc *ManifestConversation, rep *ReverseReport) (restored bool, problem string) {
	type action struct {
		u         ManifestUnit
		fromAside bool
	}
	var todo []action
	missing := 0
	for _, u := range mc.Units {
		src, dst, aside := there(u.Src), there(u.Dst), there(convmove.Aside(u.Src))
		files := unitFiles(mc, u)
		switch {
		case src && dst:
			return false, fmt.Sprintf("both %s and %s exist; left as they are", u.Src, u.Dst)
		case src:
			// Never moved, or already back.
		case dst && matches(u.Dst, u, files) == nil:
			todo = append(todo, action{u: u})
		case aside && matches(convmove.Aside(u.Src), u, files) == nil:
			todo = append(todo, action{u: u, fromAside: true})
		case dst:
			why := matches(u.Dst, u, files)
			return false, fmt.Sprintf("%s does not match the manifest (%v) and no intact original is left aside; left as it is", u.Dst, why)
		case aside:
			return false, fmt.Sprintf("%s is gone and the original left aside at %s does not match the manifest (%v); left as it is", u.Dst, convmove.Aside(u.Src), matches(convmove.Aside(u.Src), u, files))
		default:
			missing++
		}
	}
	if missing == len(mc.Units) && mc.Status == StatusMoved {
		rep.Missing = append(rep.Missing, fmt.Sprintf("%s: in neither %s nor %s", mc.ID, mc.Units[0].Src, mc.Units[0].Dst))
		return false, inNeitherPlace
	}
	if len(todo) == 0 {
		return false, ""
	}

	var undo []func() error
	rollback := func(err error) string {
		for i := len(undo) - 1; i >= 0; i-- {
			if back := undo[i](); back != nil {
				err = fmt.Errorf("%w (and undoing failed: %v)", err, back)
			}
		}
		return err.Error()
	}
	var partials []string
	for i := len(todo) - 1; i >= 0; i-- {
		a := todo[i]
		u := a.u
		if !a.fromAside {
			if _, err := convmove.Move(u.Dst, u.Src, true); err != nil {
				return false, rollback(err)
			}
			undo = append(undo, func() error { _, err := convmove.Move(u.Src, u.Dst, true); return err })
			continue
		}
		if there(u.Dst) {
			partial, err := setPartialAside(u.Dst)
			if err != nil {
				return false, rollback(err)
			}
			partials = append(partials, partial)
			undo = append(undo, func() error { return convmove.RenameNoReplace(partial, u.Dst) })
		}
		aside := convmove.Aside(u.Src)
		if err := convmove.RenameNoReplace(aside, u.Src); err != nil {
			return false, rollback(err)
		}
		undo = append(undo, func() error { return convmove.RenameNoReplace(u.Src, aside) })
	}

	// What is back at the source is what the manifest recorded, or the
	// conversation is not called restored.
	changed := false
	for _, a := range todo {
		files := unitFiles(mc, a.u)
		if err := matches(a.u.Src, a.u, files); err != nil {
			return false, fmt.Sprintf("put back at %s but it does not match the manifest (%v); left there", a.u.Src, err)
		}
		if !exact(a.u.Src, a.u, files) {
			changed = true
		}
	}
	if changed {
		rep.Changed++
	}
	for _, p := range partials {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%s: the copy cut short in the new home is kept at %s", mc.ID, p))
	}
	for _, u := range mc.Units {
		if aside := convmove.Aside(u.Src); there(aside) {
			rep.Asides = append(rep.Asides, aside)
		}
	}
	return true, ""
}

func there(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// unitFiles are the manifest's files of one unit.
func unitFiles(mc *ManifestConversation, u ManifestUnit) []ManifestFile {
	var out []ManifestFile
	for _, f := range mc.Files {
		if f.Src == u.Src || strings.HasPrefix(f.Src, u.Src+string(filepath.Separator)) {
			out = append(out, f)
		}
	}
	return out
}

// under is where the file the manifest recorded at f.Src is in a copy of
// unit u rooted at root.
func under(root string, u ManifestUnit, f ManifestFile) string {
	return root + strings.TrimPrefix(f.Src, u.Src)
}

// matches checks a copy of a unit, rooted at root, against the manifest:
// every file is there and starts with exactly the bytes that were hashed,
// every link points where it did. A transcript written to since still
// matches; a copy cut short does not.
func matches(root string, u ManifestUnit, files []ManifestFile) error {
	for _, f := range files {
		path := under(root, u, f)
		if f.Link != "" {
			if got, err := os.Readlink(path); err != nil || got != f.Link {
				return fmt.Errorf("%s is not the link it was", path)
			}
			continue
		}
		if err := startsWith(path, f.Size, f.SHA256); err != nil {
			return err
		}
	}
	return nil
}

// exact reports a copy whose files are exactly as recorded, not grown.
func exact(root string, u ManifestUnit, files []ManifestFile) bool {
	for _, f := range files {
		if f.Link != "" {
			continue
		}
		if info, err := os.Lstat(under(root, u, f)); err != nil || info.Size() != f.Size {
			return false
		}
	}
	return true
}

func startsWith(path string, size int64, sum string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.CopyN(h, file, size)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sum {
		return fmt.Errorf("%s does not start with the %d bytes the manifest hashed", path, size)
	}
	return nil
}

// setPartialAside renames a destination that is not the whole conversation
// to a hidden name beside it, which no harness reads.
func setPartialAside(path string) (string, error) {
	base := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".partial")
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		err := convmove.RenameNoReplace(path, name)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, convmove.ErrExists) {
			return "", err
		}
	}
}
