// Package homemigrate moves every provider account off the machine's own
// harness homes (~/.claude, ~/.codex, ~/.pi/agent) and onto a home of its own
// under ~/.omniplex/instances, taking the conversations Omniplex started with
// it (issue #234).
//
// An account is migrated when it is ambient: a config entry with no home
// variable (or one naming the machine's home), or the implicit default
// account (id = driver) that threads still run on without any config entry.
// The conversations that move are the ones Omniplex started, told apart by
// what the harness records: Claude's entrypoint "sdk-ts", Codex's originator
// "omniplex". Everything else stays exactly where it is.
//
// Transcripts are the point of the exercise, so the rules are strict:
// nothing is overwritten or deleted, credentials are never moved, a manifest
// with every file's size and sha256 is written before anything moves and kept
// current as each conversation completes, every destination is re-hashed
// before the account's config changes, and Reverse puts everything back from
// the manifest. A conversation modified in the last ten minutes may still be
// being written and is left for a later run.
package homemigrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// DefaultRecent is how recently modified a conversation may be and still be
// left alone as possibly in use.
const DefaultRecent = 10 * time.Minute

// Options say whose homes to migrate and how.
type Options struct {
	// UserHome is the user's home folder; empty means os.UserHomeDir.
	UserHome string
	// Adapters are the drivers to consider. One takes part when it declares
	// an isolating config field and implements adapter.SharedHome.
	Adapters []adapter.Adapter
	// Threads is the event log; nil means there are no threads.
	Threads ThreadSource
	// ExemptReferenced lets a conversation that one of this server's threads
	// resumes move even if it was modified recently. That is safe only when
	// no harness of this server can be running: at server start, before any
	// thread resumes. Elsewhere every conversation is subject to Recent.
	ExemptReferenced bool
	// Recent is the recency window; zero means DefaultRecent.
	Recent time.Duration
	// ServerEnv is the environment the server's harnesses inherit, for
	// startup: an ambient account there uses the server's own value of the
	// home variable when it has one, which is not the machine's home, so that
	// driver is left alone. Nil (the command line) ignores the process env.
	ServerEnv func(string) string
	// Retry takes up accounts an earlier run stopped on, and a migration put
	// on hold by Reverse. Only an explicit run from the command line does.
	Retry bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) recent() time.Duration {
	if o.Recent > 0 {
		return o.Recent
	}
	return DefaultRecent
}

func (o Options) home() (string, error) {
	if o.UserHome != "" {
		return filepath.Clean(o.UserHome), nil
	}
	return os.UserHomeDir()
}

// driver is one harness that has config homes.
type driver struct {
	id, name, homeEnv string
	ambient           string // the machine's home for this harness
	shared            []accounthome.Entry
	scan              scanner // nil: conversations cannot be told apart, none move
}

func drivers(userHome string, ads []adapter.Adapter) []*driver {
	var out []*driver
	for _, ad := range ads {
		sh, ok := ad.(adapter.SharedHome)
		if !ok {
			continue
		}
		cfg, ok := ad.(adapter.Configurer)
		if !ok {
			continue
		}
		homeEnv := ""
		for _, f := range cfg.ConfigFields() {
			if f.Isolates {
				homeEnv = f.Env
				break
			}
		}
		if homeEnv == "" {
			continue
		}
		d := &driver{id: ad.ID(), name: ad.Meta().Name, homeEnv: homeEnv, ambient: filepath.Clean(sh.DefaultHome(userHome)), shared: sh.SharedEntries()}
		switch d.id {
		case "claude":
			d.scan = scanClaude
		case "codex":
			d.scan = scanCodex
		}
		out = append(out, d)
	}
	return out
}

// Plan is what a run would do. It is computed without changing anything.
type Plan struct {
	Created   time.Time      `json:"created"`
	UserHome  string         `json:"userHome"`
	Instances string         `json:"instances"`
	Harnesses []*HarnessPlan `json:"harnesses"`
	Accounts  []*AccountPlan `json:"accounts"`
	// Notes are things a reader should know that change nothing.
	Notes []string `json:"notes,omitempty"`

	held    bool      // put on hold by Reverse; nothing runs
	state   *state    // the persisted leftovers as loaded
	dropped []pending // leftovers that no longer apply
	threads []thread  // every thread, for the verify step
	drivers []*driver // by id, for execution
	opts    Options
}

// HarnessPlan is one harness's machine home as found.
type HarnessPlan struct {
	Driver  string `json:"driver"`
	Ambient string `json:"ambient"`
	// Owned, Others and Unknown count the conversations in the machine's
	// home started by Omniplex, by something else, and with no origin.
	Owned   int   `json:"owned"`
	Others  int   `json:"others"`
	Unknown int   `json:"unknown"`
	Bytes   int64 `json:"bytes"` // of the Owned ones
	// SameFilesystem: moves to the instances folder are renames. Otherwise
	// each is a copy, with the source left renamed aside.
	SameFilesystem bool   `json:"sameFilesystem"`
	FilesystemNote string `json:"filesystemNote,omitempty"`
}

// AccountPlan is one account: where its home goes and what moves with it.
type AccountPlan struct {
	Instance    string `json:"instance"`
	Driver      string `json:"driver"`
	DisplayName string `json:"displayName"`
	HomeEnv     string `json:"homeEnv"`
	Ambient     string `json:"ambient"`
	Target      string `json:"target"`
	// Why the account is in the plan.
	Why string `json:"why"`
	// Entry is the config entry before the run; nil when there is none.
	Entry json.RawMessage `json:"entry,omitempty"`
	// Leftover: the account was migrated by an earlier run; this one only
	// moves conversations that run had to leave.
	Leftover bool `json:"leftover,omitempty"`

	Moves []*Conversation `json:"moves,omitempty"`
	// Recent were modified inside the recency window; a later run takes them.
	Recent []*Conversation `json:"recent,omitempty"`
	// Conflicts have something at the destination already, or hold a
	// credentials file. They are left where they are.
	Conflicts []*Conversation `json:"conflicts,omitempty"`
	// Left are Omniplex's but resumed by a thread of an account that is not
	// being migrated; they stay with it.
	Left    []*Conversation `json:"left,omitempty"`
	Threads []ThreadCheck   `json:"threads,omitempty"`
	// Abort, when set, is why this account is not migrated: nothing moves
	// and its config is not touched.
	Abort string `json:"abort,omitempty"`
}

// Bytes is the size of everything the account would move.
func (a *AccountPlan) Bytes() (n int64, files int) {
	for _, c := range a.Moves {
		n += c.Bytes
		files += c.Files
	}
	return n, files
}

// ThreadCheck is the per-thread assertion: the conversation a thread on the
// account resumes must go with it.
type ThreadCheck struct {
	Thread    string `json:"thread"`
	Title     string `json:"title,omitempty"`
	SessionID string `json:"sessionId"`
	// Status is moves | in-new-home | missing | blocks.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Thread check statuses.
const (
	ThreadMoves     = "moves"
	ThreadInNewHome = "in-new-home"
	ThreadMissing   = "missing"
	ThreadBlocks    = "blocks"
)

// configEntry is the part of a provider entry the plan reads.
type configEntry struct {
	ID          string `json:"id"`
	Driver      string `json:"driver"`
	DisplayName string `json:"displayName"`
	Env         []struct {
		Name      string `json:"name"`
		Value     string `json:"value"`
		Sensitive bool   `json:"sensitive"`
	} `json:"env"`
	raw json.RawMessage
}

func readEntries(cfg userconfig.Config) []configEntry {
	var out []configEntry
	for _, raw := range cfg.Providers {
		var e configEntry
		if json.Unmarshal(raw, &e) != nil || e.ID == "" {
			continue
		}
		e.raw = raw
		out = append(out, e)
	}
	return out
}

// homeValue is the entry's value for the home variable: set reports whether
// the variable is there and not blank.
func (e configEntry) homeValue(name string) (value string, set, sensitive bool) {
	for _, v := range e.Env {
		if v.Name == name {
			if v.Sensitive {
				return "", true, true
			}
			return v.Value, strings.TrimSpace(v.Value) != "", false
		}
	}
	return "", false, false
}

// NewPlan works out what a run would do. It reads the config, the event log
// and the homes, and changes nothing.
func NewPlan(ctx context.Context, opts Options) (*Plan, error) {
	userHome, err := opts.home()
	if err != nil {
		return nil, fmt.Errorf("find the home folder: %w", err)
	}
	p := &Plan{Created: opts.now(), UserHome: userHome, Instances: filepath.Join(userHome, ".omniplex", "instances"), opts: opts}
	st, err := loadState(userHome)
	if err != nil {
		return nil, err
	}
	p.state = st
	if st.Hold != "" && !opts.Retry {
		p.held = true
		p.Notes = append(p.Notes, fmt.Sprintf("on hold since %s was reversed; run omniplex migrate-homes to migrate again", st.Hold))
		return p, nil
	}
	if st.Hold != "" {
		p.Notes = append(p.Notes, fmt.Sprintf("the hold left by reversing %s is lifted by this run", st.Hold))
	}
	cfg, err := userconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("load the user config: %w", err)
	}
	entries := readEntries(cfg)
	threads, err := loadThreads(ctx, opts.Threads)
	if err != nil {
		return nil, err
	}
	p.threads = threads
	p.drivers = drivers(userHome, opts.Adapters)
	for _, d := range p.drivers {
		if err := p.planDriver(d, entries, threads); err != nil {
			return nil, fmt.Errorf("%s: %w", d.id, err)
		}
	}
	p.planLeftovers(entries)
	return p, nil
}

// ambient reports whether an entry of d uses the machine's home.
func (p *Plan) ambient(d *driver, e configEntry) (bool, string) {
	value, set, sensitive := e.homeValue(d.homeEnv)
	switch {
	case sensitive:
		return false, fmt.Sprintf("account %s keeps %s as a secret; left as it is", e.ID, d.homeEnv)
	case !set:
		return true, "configured without " + d.homeEnv
	}
	v := strings.TrimSpace(value)
	if !filepath.IsAbs(v) && v != "~" && !strings.HasPrefix(v, "~/") {
		return false, ""
	}
	home := accounthome.Resolve(p.UserHome, v)
	if home == d.ambient || sameFile(home, d.ambient) {
		return true, fmt.Sprintf("%s is the machine's own %s", d.homeEnv, d.ambient)
	}
	return false, ""
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

func (p *Plan) target(id string) string { return filepath.Join(p.Instances, id) }

func (p *Plan) planDriver(d *driver, entries []configEntry, threads []thread) error {
	var accounts []*AccountPlan
	byID := map[string]configEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	for _, e := range entries {
		if e.Driver != d.id {
			continue
		}
		ok, why := p.ambient(d, e)
		if !ok {
			if why != "" {
				p.Notes = append(p.Notes, why)
			}
			continue
		}
		name := e.DisplayName
		if name == "" {
			name = e.ID
		}
		accounts = append(accounts, &AccountPlan{Instance: e.ID, Driver: d.id, DisplayName: name, Why: why, Entry: e.raw})
	}
	if _, configured := byID[d.id]; !configured {
		n := 0
		for _, t := range threads {
			if t.Instance == d.id && t.Harness == d.id {
				n++
			}
		}
		if n > 0 {
			accounts = append(accounts, &AccountPlan{Instance: d.id, Driver: d.id, DisplayName: d.name,
				Why: fmt.Sprintf("the implicit default account, with %d threads and no config entry", n)})
		}
	}
	if len(accounts) == 0 {
		return nil
	}
	if p.opts.ServerEnv != nil {
		if v := strings.TrimSpace(p.opts.ServerEnv(d.homeEnv)); v != "" {
			p.Notes = append(p.Notes, fmt.Sprintf("%s: the server's environment sets %s=%s, so its accounts without one use that rather than %s; not migrating them", d.id, d.homeEnv, v, d.ambient))
			// The implicit default still needs an entry, or its threads
			// have no account at all.
			for _, a := range accounts {
				if a.Entry == nil {
					a.HomeEnv, a.Ambient, a.Target = d.homeEnv, v, p.target(a.Instance)
					a.Abort = fmt.Sprintf("the server's environment sets %s", d.homeEnv)
					p.Accounts = append(p.Accounts, a)
				}
			}
			return nil
		}
	}
	for _, a := range accounts {
		a.HomeEnv, a.Ambient, a.Target = d.homeEnv, d.ambient, p.target(a.Instance)
		if m, stopped := p.state.Stopped[a.Instance]; stopped && !p.opts.Retry {
			a.Abort = fmt.Sprintf("an earlier run stopped on this account (manifest %s); check it, then run omniplex migrate-homes, or reverse it with omniplex migrate-homes --reverse %s", m, m)
		}
	}
	p.Accounts = append(p.Accounts, accounts...)

	hp := &HarnessPlan{Driver: d.id, Ambient: d.ambient}
	hp.SameFilesystem, hp.FilesystemNote = sameFilesystem(d.ambient, p.Instances)
	p.Harnesses = append(p.Harnesses, hp)

	candidate := map[string]*AccountPlan{}
	for _, a := range accounts {
		candidate[a.Instance] = a
	}
	primary := accounts[0]
	for _, a := range accounts {
		if a.Instance == d.id {
			primary = a
			break
		}
	}

	// Who resumes what, across every account of this harness.
	refs := map[string][]thread{}
	for _, t := range threads {
		if t.Harness == d.id && t.SessionID != "" {
			refs[t.SessionID] = append(refs[t.SessionID], t)
		}
	}

	if d.scan == nil {
		for _, a := range accounts {
			for _, t := range threads {
				if t.Instance == a.Instance && t.Harness == d.id && t.SessionID != "" {
					a.Threads = append(a.Threads, ThreadCheck{Thread: t.ID, Title: t.Title, SessionID: t.SessionID, Status: ThreadBlocks,
						Detail: d.name + " records nothing that tells Omniplex's conversations from the terminal's, so none can move"})
				}
			}
			if len(a.Threads) > 0 && a.Abort == "" {
				a.Abort = fmt.Sprintf("%d threads resume conversations this migration cannot move", len(a.Threads))
			}
		}
		return nil
	}

	ambient, err := d.scan(d.ambient)
	if err != nil {
		return fmt.Errorf("read %s: %w", d.ambient, err)
	}
	inAmbient := map[string][]*Conversation{}
	for _, c := range ambient {
		switch {
		case c.Owned:
			hp.Owned++
			hp.Bytes += c.Bytes
		case c.Origin != "":
			hp.Others++
		default:
			hp.Unknown++
		}
		if c.ID != "" {
			inAmbient[c.ID] = append(inAmbient[c.ID], c)
		}
	}

	assigned := map[*Conversation]*AccountPlan{}
	for _, c := range ambient {
		if !c.Owned {
			continue
		}
		owner := primary
		var other string
		for _, t := range refs[c.ID] {
			c.Threads = append(c.Threads, t.ID)
			if a, ok := candidate[t.Instance]; ok {
				if owner == primary && a != primary && !referencedBy(c, primary, refs) {
					owner = a
				}
			} else if other == "" {
				other = t.Instance
			}
		}
		if other != "" && !referencedByAny(c, candidate, refs) {
			c.Reason = fmt.Sprintf("resumed by a thread on %s, which is not being migrated", other)
			primary.Left = append(primary.Left, c)
			continue
		}
		assigned[c] = owner
		p.place(owner, d.ambient, c)
	}

	for _, a := range accounts {
		inTarget := map[string]bool{}
		if found, err := d.scan(a.Target); err == nil {
			for _, c := range found {
				inTarget[c.ID] = true
			}
		}
		moving := map[string]bool{}
		for _, c := range a.Moves {
			moving[c.ID] = true
		}
		blocks := 0
		for _, t := range threads {
			if t.Instance != a.Instance || t.Harness != d.id || t.SessionID == "" {
				continue
			}
			check := ThreadCheck{Thread: t.ID, Title: t.Title, SessionID: t.SessionID}
			switch convs := inAmbient[t.SessionID]; {
			case moving[t.SessionID] && len(convs) == 1:
				check.Status = ThreadMoves
			case len(convs) > 0:
				check.Status = ThreadBlocks
				check.Detail = whyNotMoving(convs, a, assigned)
				blocks++
			case inTarget[t.SessionID]:
				check.Status = ThreadInNewHome
			default:
				check.Status = ThreadMissing
				check.Detail = "in neither " + a.Ambient + " nor " + a.Target + "; left as it is"
			}
			a.Threads = append(a.Threads, check)
		}
		if blocks > 0 && a.Abort == "" {
			a.Abort = fmt.Sprintf("%d of its threads resume a conversation in %s that is not in the move set", blocks, a.Ambient)
		}
	}
	return nil
}

func referencedBy(c *Conversation, a *AccountPlan, refs map[string][]thread) bool {
	for _, t := range refs[c.ID] {
		if t.Instance == a.Instance {
			return true
		}
	}
	return false
}

func referencedByAny(c *Conversation, candidate map[string]*AccountPlan, refs map[string][]thread) bool {
	for _, t := range refs[c.ID] {
		if _, ok := candidate[t.Instance]; ok {
			return true
		}
	}
	return false
}

func whyNotMoving(convs []*Conversation, a *AccountPlan, assigned map[*Conversation]*AccountPlan) string {
	if len(convs) > 1 {
		return fmt.Sprintf("%d conversations in %s carry this id", len(convs), a.Ambient)
	}
	c := convs[0]
	switch owner := assigned[c]; {
	case !c.Owned && c.Origin == "":
		return c.Units[0].Rel + " records no origin, so it is not in the move set"
	case !c.Owned:
		return fmt.Sprintf("%s was started by %q, not Omniplex, so it is not in the move set", c.Units[0].Rel, c.Origin)
	case owner == nil:
		return c.Units[0].Rel + ": " + c.Reason
	case owner != a:
		return fmt.Sprintf("%s goes to %s, whose thread also resumes it", c.Units[0].Rel, owner.Instance)
	default:
		return c.Units[0].Rel + ": " + c.Reason
	}
}

// sameFilesystem compares the device of the machine's home with that of the
// instances folder, or the nearest folder above it that exists.
func sameFilesystem(a, b string) (bool, string) {
	dev := func(path string) (uint64, string, error) {
		for p := path; ; p = filepath.Dir(p) {
			info, err := os.Stat(p)
			if err == nil {
				st, ok := info.Sys().(*syscall.Stat_t)
				if !ok {
					return 0, p, fmt.Errorf("no device for %s", p)
				}
				return uint64(st.Dev), p, nil
			}
			if p == filepath.Dir(p) {
				return 0, p, err
			}
		}
	}
	da, pa, err := dev(a)
	if err != nil {
		return false, err.Error()
	}
	db, pb, err := dev(b)
	if err != nil {
		return false, err.Error()
	}
	if da != db {
		return false, fmt.Sprintf("%s and %s are on different filesystems: every move is a copy, with the source left renamed aside", pa, pb)
	}
	return true, ""
}

// Nothing reports a plan with nothing to do.
func (p *Plan) Nothing() bool {
	for _, a := range p.Accounts {
		if a.Abort == "" && (!a.Leftover || len(a.Moves) > 0) {
			return false
		}
	}
	return true
}

// planLeftovers takes up the conversations earlier runs had to leave. Each
// still belongs to its account only while that account's home is the one the
// run moved it to.
func (p *Plan) planLeftovers(entries []configEntry) {
	byDriver := map[string]*driver{}
	for _, d := range p.drivers {
		byDriver[d.id] = d
	}
	byID := map[string]configEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	accounts := map[string]*AccountPlan{}
	for _, lp := range p.state.Pending {
		d, ok := byDriver[lp.Driver]
		if !ok {
			p.state.keep = append(p.state.keep, lp)
			continue
		}
		e, ok := byID[lp.Instance]
		value, set, sensitive := e.homeValue(d.homeEnv)
		if !ok || e.Driver != lp.Driver || !set || sensitive || accounthome.Resolve(p.UserHome, value) != lp.Target {
			p.dropped = append(p.dropped, lp)
			p.Notes = append(p.Notes, fmt.Sprintf("%s: left %s in %s: the account no longer uses %s", lp.Instance, lp.ID, lp.Ambient, lp.Target))
			continue
		}
		c := &Conversation{ID: lp.ID, Units: lp.Units, Owned: true}
		if !present(lp.Ambient, c) {
			p.dropped = append(p.dropped, lp)
			p.Notes = append(p.Notes, fmt.Sprintf("%s: %s is no longer in %s; nothing to do", lp.Instance, lp.ID, lp.Ambient))
			continue
		}
		if err := measure(lp.Ambient, c); err != nil {
			p.state.keep = append(p.state.keep, lp)
			p.Notes = append(p.Notes, fmt.Sprintf("%s: %s: %v; tried again next run", lp.Instance, lp.ID, err))
			continue
		}
		a := accounts[lp.Instance]
		if a == nil {
			name := e.DisplayName
			if name == "" {
				name = e.ID
			}
			a = &AccountPlan{Instance: lp.Instance, Driver: lp.Driver, DisplayName: name, HomeEnv: d.homeEnv,
				Ambient: lp.Ambient, Target: lp.Target, Why: "conversations an earlier run left because they were in use", Entry: e.raw, Leftover: true}
			accounts[lp.Instance] = a
			p.Accounts = append(p.Accounts, a)
		}
		for _, t := range p.threads {
			if t.Harness == lp.Driver && t.SessionID == lp.ID {
				c.Threads = append(c.Threads, t.ID)
			}
		}
		if p.place(a, lp.Ambient, c) {
			p.dropped = append(p.dropped, lp)
		}
	}
}

// place files an Omniplex conversation in ambient under what the account
// does with it: a conflict when something is at the destination already or
// it holds a login file, recent when it may still be in use, otherwise a
// move. It reports a conflict.
func (p *Plan) place(a *AccountPlan, ambient string, c *Conversation) (conflict bool) {
	switch {
	case present(a.Target, c):
		c.Reason = "already exists in " + a.Target
		a.Conflicts = append(a.Conflicts, c)
		return true
	case holdsCredentials(ambient, c):
		c.Reason = "holds a file named like a login; credentials are never moved"
		a.Conflicts = append(a.Conflicts, c)
		return true
	case c.Modified.After(p.Created.Add(-p.opts.recent())) && !(p.opts.ExemptReferenced && len(c.Threads) > 0):
		c.Reason = "modified " + c.Modified.Format(time.RFC3339) + ", inside the last " + p.opts.recent().String()
		a.Recent = append(a.Recent, c)
	default:
		a.Moves = append(a.Moves, c)
	}
	return false
}
