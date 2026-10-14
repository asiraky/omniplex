package homemigrate

import (
	"context"
	"encoding/json"
	"fmt"
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
// (an entry the run created is removed; one edited since keeps the edits and
// only loses the home variable the run set). Afterwards the migration is on
// hold: the server will not run it again until the command line does.
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
		back, conflict, missing := []ManifestUnit{}, false, 0
		for _, u := range mc.Units {
			_, srcErr := os.Lstat(u.Src)
			_, dstErr := os.Lstat(u.Dst)
			switch {
			case dstErr == nil && srcErr == nil:
				conflict = true
				rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("%s: both %s and %s exist; left as they are", mc.ID, u.Src, u.Dst))
			case dstErr == nil:
				back = append(back, u)
			case srcErr != nil:
				missing++
			}
		}
		if conflict {
			continue
		}
		if missing == len(mc.Units) && mc.Status == StatusMoved {
			rep.Missing = append(rep.Missing, fmt.Sprintf("%s: in neither %s nor %s", mc.ID, mc.Units[0].Src, mc.Units[0].Dst))
			continue
		}
		var done []ManifestUnit
		var moveErr error
		for j := len(back) - 1; j >= 0; j-- {
			u := back[j]
			if _, err := convmove.Move(u.Dst, u.Src, true); err != nil {
				moveErr = err
				break
			}
			done = append(done, u)
		}
		if moveErr != nil {
			for _, u := range done {
				if _, err := convmove.Move(u.Src, u.Dst, true); err != nil {
					moveErr = fmt.Errorf("%w (and putting %s back failed: %v)", moveErr, u.Dst, err)
				}
			}
			rep.Conflicts = append(rep.Conflicts, fmt.Sprintf("%s: %v", mc.ID, moveErr))
			_ = m.save()
			continue
		}
		if len(done) == 0 {
			continue // never moved (a pending entry from a run that stopped)
		}
		for _, f := range mc.Files {
			if f.Link != "" {
				continue
			}
			if size, sum, err := hashFile(f.Src); err != nil || size != f.Size || sum != f.SHA256 {
				rep.Changed++
				break
			}
		}
		if mc.Copied {
			for _, u := range mc.Units {
				if _, err := os.Lstat(convmove.Aside(u.Src)); err == nil {
					rep.Asides = append(rep.Asides, convmove.Aside(u.Src))
				}
			}
		}
		mc.Status = StatusReversed
		rep.Restored++
		if err := m.save(); err != nil {
			return rep, fmt.Errorf("update the manifest: %w", err)
		}
	}

	reversed := map[string]bool{}
	for _, ma := range m.Accounts {
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
	now := opts.now()
	m.Reversed = &now
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
			cfg.Providers = append(cfg.Providers[:idx], cfg.Providers[idx+1:]...)
			note = "removed the entry the migration added"
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
