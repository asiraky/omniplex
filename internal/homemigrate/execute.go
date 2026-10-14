package homemigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/convmove"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// Report is what a run did.
type Report struct {
	// Manifest is the run's manifest; empty when there was nothing to do.
	Manifest string          `json:"manifest,omitempty"`
	Accounts []AccountResult `json:"accounts"`
	Notes    []string        `json:"notes,omitempty"`
}

// AccountResult is one account's outcome.
type AccountResult struct {
	Instance    string `json:"instance"`
	DisplayName string `json:"displayName"`
	Driver      string `json:"driver"`
	Target      string `json:"target"`
	Ambient     string `json:"ambient"`
	Leftover    bool   `json:"leftover,omitempty"`
	// Migrated: the account's config now names Target (for a leftover run,
	// its conversations moved).
	Migrated  bool `json:"migrated"`
	Moved     int  `json:"moved"`
	Recent    int  `json:"recent"`
	Conflicts int  `json:"conflicts"`
	// Aborted is why the account was not attempted; Stopped is why an
	// attempt stopped part way. Either way its config is unchanged.
	Aborted string        `json:"aborted,omitempty"`
	Stopped string        `json:"stopped,omitempty"`
	Missing []ThreadCheck `json:"missing,omitempty"`
	Sharing string        `json:"sharing,omitempty"`
}

// Execute carries out a plan made by NewPlan, account by account. Nothing an
// account does is visible in its config until every conversation it moved is
// verified, so an account that stops keeps running on the machine's home.
func Execute(ctx context.Context, p *Plan) (*Report, error) {
	rep := &Report{Notes: append([]string(nil), p.Notes...)}
	if p.held {
		return rep, nil
	}
	st := p.state
	byDriver := map[string]*driver{}
	for _, d := range p.drivers {
		byDriver[d.id] = d
	}

	var work []*AccountPlan
	for _, a := range p.Accounts {
		switch {
		case a.Abort != "":
			rep.Accounts = append(rep.Accounts, result(a, func(r *AccountResult) { r.Aborted = a.Abort }))
		case a.Leftover && len(a.Moves) == 0:
			if len(a.Recent) > 0 {
				rep.Accounts = append(rep.Accounts, result(a, nil))
			}
		default:
			work = append(work, a)
		}
	}

	nextPending := append([]pending(nil), st.keep...)
	// Leftovers not taken up this run stay pending: still in use, or their
	// account was not worked on.
	consumed := map[string]bool{}
	for _, lp := range p.dropped {
		consumed[pendingKey(lp)] = true
	}

	if len(work) == 0 {
		for _, lp := range st.Pending {
			if !consumed[pendingKey(lp)] && !containsPending(nextPending, lp) {
				nextPending = append(nextPending, lp)
			}
		}
		if len(p.dropped) > 0 {
			st.Pending = nextPending
			if err := saveState(p.UserHome, st); err != nil {
				return rep, err
			}
		}
		return rep, nil
	}

	m := &Manifest{Version: 1, Created: p.Created, UserHome: p.UserHome}
	convsOf := map[*AccountPlan][]*ManifestConversation{}
	accOf := map[*AccountPlan]*ManifestAccount{}
	hashErr := map[*AccountPlan]error{}
	for _, a := range work {
		ma := &ManifestAccount{Instance: a.Instance, Driver: a.Driver, HomeEnv: a.HomeEnv, Ambient: a.Ambient, Target: a.Target,
			Leftover: a.Leftover, ConfigBefore: a.Entry, Status: StatusPending}
		if ma.ConfigBefore == nil {
			ma.ConfigBefore = json.RawMessage("null")
		}
		m.Accounts = append(m.Accounts, ma)
		accOf[a] = ma
		for _, c := range a.Moves {
			mc := manifestConversation(a, c, StatusPending)
			files, err := hashUnits(mc.Units)
			if err != nil {
				hashErr[a] = fmt.Errorf("hash %s: %w", c.ID, err)
				break
			}
			mc.Files = files
			m.Conversations = append(m.Conversations, mc)
			convsOf[a] = append(convsOf[a], mc)
		}
		for _, list := range [][]*Conversation{a.Recent, a.Conflicts} {
			for _, c := range list {
				mc := manifestConversation(a, c, StatusSkipped)
				mc.Note = c.Reason
				m.Conversations = append(m.Conversations, mc)
			}
		}
	}
	if err := m.create(p.UserHome, p.Created); err != nil {
		return rep, fmt.Errorf("write the manifest before moving anything: %w", err)
	}
	rep.Manifest = m.path
	if p.opts.Retry {
		st.Hold = ""
	}

	for _, a := range work {
		ma := accOf[a]
		r := result(a, nil)
		stop := func(why string) {
			ma.Status, ma.Note = StatusStopped, why
			r.Stopped = why
			st.Stopped[a.Instance] = m.path
		}
		if err := ctx.Err(); err != nil {
			stop(err.Error())
		} else if err := hashErr[a]; err != nil {
			stop(err.Error() + "; nothing of this account was moved")
			for _, mc := range convsOf[a] {
				mc.Status, mc.Note = StatusSkipped, "not moved: the account stopped before any move"
			}
		} else {
			delete(st.Stopped, a.Instance)
			r.Moved, r.Conflicts = 0, len(a.Conflicts)
			if why := p.moveAll(a, convsOf[a], m, &r); why != "" {
				stop(why)
			} else if why := p.verify(byDriver[a.Driver], a, convsOf[a]); why != "" {
				stop("verify: " + why)
			} else if !a.Leftover {
				after, err := writeConfig(a)
				if err != nil {
					stop("write the config: " + err.Error())
				} else {
					ma.ConfigAfter = after
					ma.Status = StatusMigrated
					r.Migrated = true
					d := byDriver[a.Driver]
					created, err := share(d, a.Target)
					ma.CreatedShared = created
					if err != nil {
						r.Sharing = err.Error()
					}
				}
			} else {
				ma.Status = StatusMigrated
				r.Migrated = true
			}
		}
		if r.Stopped != "" {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s: to undo what moved, run: omniplex migrate-homes --reverse %s", a.Instance, m.path))
		}
		_ = m.save()

		// Leftovers: what this account could not take now is tried again
		// later, unless the account stopped, in which case a person decides.
		if r.Migrated {
			for _, c := range a.Recent {
				nextPending = append(nextPending, pending{Instance: a.Instance, Driver: a.Driver, ID: c.ID, Units: c.Units, Ambient: a.Ambient, Target: a.Target, Manifest: m.path})
			}
		} else if a.Leftover {
			for _, mc := range convsOf[a] {
				if mc.Status != StatusMoved {
					nextPending = append(nextPending, pendingFrom(a, mc, m.path))
				}
			}
			for _, c := range a.Recent {
				nextPending = append(nextPending, pending{Instance: a.Instance, Driver: a.Driver, ID: c.ID, Units: c.Units, Ambient: a.Ambient, Target: a.Target, Manifest: m.path})
			}
		}
		if a.Leftover {
			for _, lp := range st.Pending {
				if lp.Instance == a.Instance {
					consumed[pendingKey(lp)] = true
				}
			}
		}
		rep.Accounts = append(rep.Accounts, r)
	}
	for _, lp := range st.Pending {
		if !consumed[pendingKey(lp)] && !containsPending(nextPending, lp) {
			nextPending = append(nextPending, lp)
		}
	}
	st.Pending = nextPending
	now := p.opts.now()
	m.Finished = &now
	if err := m.save(); err != nil {
		return rep, fmt.Errorf("update the manifest: %w", err)
	}
	if err := saveState(p.UserHome, st); err != nil {
		return rep, fmt.Errorf("save the migration state: %w", err)
	}
	return rep, nil
}

func pendingKey(lp pending) string {
	return lp.Instance + "\x00" + lp.Ambient + "\x00" + lp.Units[0].Rel
}

func containsPending(list []pending, lp pending) bool {
	for _, x := range list {
		if pendingKey(x) == pendingKey(lp) {
			return true
		}
	}
	return false
}

func pendingFrom(a *AccountPlan, mc *ManifestConversation, manifest string) pending {
	lp := pending{Instance: a.Instance, Driver: a.Driver, ID: mc.ID, Ambient: a.Ambient, Target: a.Target, Manifest: manifest}
	for _, u := range mc.Units {
		rel, _ := filepath.Rel(a.Ambient, u.Src)
		lp.Units = append(lp.Units, Unit{Rel: rel, Dir: u.Dir})
	}
	return lp
}

func result(a *AccountPlan, f func(*AccountResult)) AccountResult {
	r := AccountResult{Instance: a.Instance, DisplayName: a.DisplayName, Driver: a.Driver, Target: a.Target, Ambient: a.Ambient,
		Leftover: a.Leftover, Recent: len(a.Recent), Conflicts: len(a.Conflicts)}
	for _, t := range a.Threads {
		if t.Status == ThreadMissing {
			r.Missing = append(r.Missing, t)
		}
	}
	if f != nil {
		f(&r)
	}
	return r
}

func manifestConversation(a *AccountPlan, c *Conversation, status string) *ManifestConversation {
	mc := &ManifestConversation{Instance: a.Instance, Driver: a.Driver, ID: c.ID, Status: status}
	for _, u := range c.Units {
		mc.Units = append(mc.Units, ManifestUnit{Src: filepath.Join(a.Ambient, u.Rel), Dst: filepath.Join(a.Target, u.Rel), Dir: u.Dir})
	}
	return mc
}

// moveAll moves an account's conversations one at a time, recording each in
// the manifest as it completes. It returns why it stopped, or "".
func (p *Plan) moveAll(a *AccountPlan, convs []*ManifestConversation, m *Manifest, r *AccountResult) string {
	referenced := map[string]bool{}
	for _, t := range a.Threads {
		referenced[t.SessionID] = true
	}
	for _, mc := range convs {
		var gone, there []string
		for _, u := range mc.Units {
			if _, err := os.Lstat(u.Src); err != nil {
				gone = append(gone, u.Src)
			}
			if _, err := os.Lstat(u.Dst); err == nil {
				there = append(there, u.Dst)
			}
		}
		switch {
		case len(gone) > 0:
			mc.Status, mc.Note = StatusFailed, "gone from the machine's home since the plan: "+strings.Join(gone, ", ")
			_ = m.save()
			return fmt.Sprintf("%s changed while the migration ran (%s); nothing further moved", mc.ID, mc.Note)
		case len(there) > 0:
			mc.Status, mc.Note = StatusConflict, "appeared at the destination since the plan: "+strings.Join(there, ", ")
			r.Conflicts++
			if err := m.save(); err != nil {
				return "update the manifest: " + err.Error()
			}
			if referenced[mc.ID] {
				return fmt.Sprintf("a thread's conversation %s could not move: %s", mc.ID, mc.Note)
			}
			continue
		}
		var done []ManifestUnit
		for _, u := range mc.Units {
			copied, err := convmove.Move(u.Src, u.Dst, true)
			if copied {
				mc.Copied = true
			}
			if err != nil {
				// The conversation stays whole in one place: what moved goes
				// back, under the same never-overwrite rule.
				for i := len(done) - 1; i >= 0; i-- {
					if _, back := convmove.Move(done[i].Dst, done[i].Src, true); back != nil {
						err = fmt.Errorf("%w (and putting %s back failed: %v)", err, done[i].Src, back)
					}
				}
				mc.Status, mc.Note = StatusFailed, err.Error()
				_ = m.save()
				return fmt.Sprintf("move %s: %v", mc.ID, err)
			}
			done = append(done, u)
		}
		mc.Status = StatusMoved
		r.Moved++
		if err := m.save(); err != nil {
			return "update the manifest: " + err.Error()
		}
	}
	return ""
}

// verify re-hashes every file the account moved against the manifest and
// recounts the machine's home: every conversation that moved is gone from
// it, and the ones of the account's that remain are exactly the ones it
// skipped. It returns what does not match, or "".
func (p *Plan) verify(d *driver, a *AccountPlan, convs []*ManifestConversation) string {
	var problems []string
	moved, conflicts := 0, 0
	movedRel := map[string]bool{}
	for _, mc := range convs {
		switch mc.Status {
		case StatusConflict:
			conflicts++
			continue
		case StatusMoved:
		default:
			problems = append(problems, fmt.Sprintf("%s is %s", mc.ID, mc.Status))
			continue
		}
		moved++
		for _, u := range mc.Units {
			if _, err := os.Lstat(u.Src); !errors.Is(err, fs.ErrNotExist) {
				problems = append(problems, u.Src+" is still in the machine's home")
			}
			if _, err := os.Lstat(u.Dst); err != nil {
				problems = append(problems, u.Dst+" is missing")
			}
			rel, _ := filepath.Rel(a.Ambient, u.Src)
			movedRel[rel] = true
		}
		for _, f := range mc.Files {
			if f.Link != "" {
				if got, err := os.Readlink(f.Dst); err != nil || got != f.Link {
					problems = append(problems, f.Dst+" is not the link it was")
				}
				continue
			}
			size, sum, err := hashFile(f.Dst)
			switch {
			case err != nil:
				problems = append(problems, err.Error())
			case size != f.Size || sum != f.SHA256:
				problems = append(problems, fmt.Sprintf("%s does not match the manifest (%d bytes, sha256 %s; recorded %d, %s)", f.Dst, size, sum, f.Size, f.SHA256))
			}
		}
	}
	if moved+conflicts != len(a.Moves) {
		problems = append(problems, fmt.Sprintf("%d moved and %d conflicts, but the plan moves %d", moved, conflicts, len(a.Moves)))
	}
	if d != nil && d.scan != nil {
		left, err := d.scan(a.Ambient)
		if err != nil {
			problems = append(problems, "recount "+a.Ambient+": "+err.Error())
		} else {
			skipped := map[string]bool{}
			for _, list := range [][]*Conversation{a.Recent, a.Conflicts} {
				for _, c := range list {
					skipped[c.Units[0].Rel] = true
				}
			}
			for _, mc := range convs {
				if mc.Status == StatusConflict {
					rel, _ := filepath.Rel(a.Ambient, mc.Units[0].Src)
					skipped[rel] = true
				}
			}
			remaining := 0
			for _, c := range left {
				rel := c.Units[0].Rel
				if movedRel[rel] {
					problems = append(problems, rel+" was moved but is still in "+a.Ambient)
				}
				if c.Owned && skipped[rel] {
					remaining++
				}
			}
			if want := len(a.Recent) + len(a.Conflicts) + conflicts; remaining != want {
				problems = append(problems, fmt.Sprintf("%d of the account's Omniplex conversations are still in %s; %d were skipped", remaining, a.Ambient, want))
			}
		}
	}
	return strings.Join(problems, "; ")
}

// writeConfig points the account's provider entry at its new home, keeping
// its id and display name and every key this build does not know; it adds
// the entry when the account was the implicit default. It returns the entry
// as written. A config that changed since the plan is left alone.
func writeConfig(a *AccountPlan) (json.RawMessage, error) {
	var after json.RawMessage
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		idx := -1
		for i, raw := range cfg.Providers {
			var e struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &e) == nil && e.ID == a.Instance {
				idx = i
				break
			}
		}
		switch {
		case idx < 0 && a.Entry != nil:
			return fmt.Errorf("the provider entry %s is gone from the config since the plan", a.Instance)
		case idx >= 0 && a.Entry == nil:
			return fmt.Errorf("a provider entry %s appeared in the config since the plan", a.Instance)
		case idx >= 0 && !sameJSON(cfg.Providers[idx], a.Entry):
			return fmt.Errorf("the provider entry %s changed since the plan", a.Instance)
		}
		var raw json.RawMessage
		var err error
		if idx >= 0 {
			raw, err = setHome(cfg.Providers[idx], a.HomeEnv, a.Target)
			if err != nil {
				return err
			}
			cfg.Providers[idx] = raw
		} else {
			raw, err = json.Marshal(map[string]any{
				"id": a.Instance, "driver": a.Driver, "displayName": a.DisplayName, "enabled": true,
				"env": []map[string]string{{"name": a.HomeEnv, "value": a.Target}},
			})
			if err != nil {
				return err
			}
			cfg.Providers = append(cfg.Providers, raw)
		}
		after = raw
		return nil
	})
	return after, err
}

// setHome sets one env variable of a raw provider entry, through generic
// maps so every other key survives as written.
func setHome(raw json.RawMessage, name, value string) (json.RawMessage, error) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	var env []map[string]json.RawMessage
	if b, ok := generic["env"]; ok && string(b) != "null" {
		if err := json.Unmarshal(b, &env); err != nil {
			return nil, err
		}
	}
	v, _ := json.Marshal(value)
	n, _ := json.Marshal(name)
	found := false
	for _, e := range env {
		var en string
		if json.Unmarshal(e["name"], &en) == nil && en == name {
			e["value"] = v
			delete(e, "sensitive")
			found = true
		}
	}
	if !found {
		env = append(env, map[string]json.RawMessage{"name": n, "value": v})
	}
	b, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	generic["env"] = b
	return json.Marshal(generic)
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// share makes the account's home and links the user's configuration into
// it from the machine's home, as every account's home is prepared. It
// returns the entries the sharing had to create in the machine's home.
func share(d *driver, target string) ([]string, error) {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, err
	}
	var missing []string
	for _, e := range d.shared {
		if _, err := os.Lstat(filepath.Join(d.ambient, e.Name)); errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, e.Name)
		}
	}
	err := accounthome.Link(d.ambient, target, d.shared)
	var created []string
	for _, name := range missing {
		if _, err := os.Lstat(filepath.Join(d.ambient, name)); err == nil {
			created = append(created, filepath.Join(d.ambient, name))
		}
	}
	return created, err
}
