// Package homemigrate gives every account that still runs on the machine's
// own harness home (~/.claude, ~/.codex, ~/.pi/agent) a home of its own under
// ~/.omniplex/instances/<id>, and takes the conversations Omniplex started
// there with it.
//
// Accounts used to come in two kinds: the first one per driver ran on the
// machine's home, so a terminal sign-in on the box changed who it was, and
// every later one had its own. Every account now has its own home, and this
// is the one-time move that gets the old kind there. It runs at server start
// before any thread does, and from `omniplex migrate-homes`.
//
// The transcripts are the user's history, so nothing is ever overwritten or
// deleted: every move is a never-replace rename (accounthome.Move), a
// manifest records every file with its hash before the first move and after
// each one, and Reverse puts everything back from it. A conversation is only
// moved when the harness recorded Omniplex as what started it; the
// terminal's own stay where they are, untouched. An account whose threads'
// conversations cannot all go with it is left exactly as it was.
package homemigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// Driver ids whose conversations live in the config home and move with an
// account. Pi keeps its sessions there too, but none of its accounts was ever
// ambient with threads, so nothing of Pi's moves.
const (
	DriverClaude = "claude"
	DriverCodex  = "codex"
)

// DefaultMinAge is how long a conversation must have been left alone before
// it moves. Dev servers in worktrees and probe runs write Omniplex-started
// transcripts into the machine's home too, and may be mid-write.
const DefaultMinAge = 10 * time.Minute

// Driver is one harness whose accounts keep a config home: the adapter's
// isolating variable, its default home and its shared entries, as the
// adapters declare them.
type Driver struct {
	ID string
	// Name is the display name an implicit default account is given when it
	// gets a config entry.
	Name string
	// HomeEnv is the variable naming an account's config home.
	HomeEnv string
	// DefaultHome is where the harness looks with HomeEnv unset, for this
	// user's home: the machine's own home, and the shared configuration
	// source.
	DefaultHome   string
	SharedEntries []accounthome.Entry
}

// Store is the part of the event log the migration reads: which threads
// exist, on which account, and which conversation each resumes.
type Store interface {
	ListThreads(ctx context.Context) ([]store.ThreadMeta, error)
	LatestSnapshot(ctx context.Context, threadID string) (int64, json.RawMessage, error)
	ReadEvents(ctx context.Context, threadID string, afterSeq int64, limit int) ([]proto.Event, error)
}

// Options are the migration's inputs. The user config is read and written
// through package userconfig, so $OMNIPLEX_CONFIG applies.
type Options struct {
	// Home is the user's home folder.
	Home    string
	Store   Store
	Drivers []Driver
	Now     time.Time
	// MinAge skips a conversation modified more recently than this. A
	// conversation a thread in Store resumes is exempt: a real run holds the
	// database's lock, so no Omniplex session of that store is running, and
	// skipping it would leave its account unmigrated on every start that
	// follows a busy one.
	MinAge time.Duration
	Logf   func(string, ...any)
}

func (o Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Plan is what a run would do, worked out without changing anything. Dry
// run prints it; Run carries it out.
type Plan struct {
	Home     string     `json:"home"`
	Accounts []*Account `json:"accounts"`
	// Stay are Omniplex-started conversations no account being migrated
	// takes. They are left where they are and reported.
	Stay []*Conversation `json:"-"`
	// Kept counts, per driver, the conversations Omniplex did not start,
	// which always stay.
	Kept    map[string]int `json:"-"`
	Threads []ThreadCheck  `json:"-"`
	SameFS  []FSCheck      `json:"-"`
	// Notes are things worth saying that change nothing: an entry left alone,
	// a thread on an account that no longer exists.
	Notes []string `json:"-"`
}

// Account is one account the migration touches.
type Account struct {
	ID     string `json:"id"`
	Driver string `json:"driver"`
	Name   string `json:"name"`
	// Home is the account's own home, InstanceHome(home, id).
	Home string `json:"home"`
	// Shared is the driver's default home: where conversations come from and
	// what the account's shared entries link to.
	Shared string `json:"shared"`
	// Implicit is the old synthesised default: threads use the id, no config
	// entry exists.
	Implicit bool `json:"implicit,omitempty"`
	// CatchAll is an account already given its home by an earlier run: only
	// conversations that were skipped then still move.
	CatchAll bool `json:"catchAll,omitempty"`
	// Before and After are the config entry as it was and as the run writes
	// it. Before is null for an implicit account; After is null when the
	// config does not change.
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
	// Status is planned, aborted, failed, verify-failed or migrated.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// ConfigWritten records that After is in the config, which is what
	// Reverse undoes.
	ConfigWritten bool `json:"configWritten,omitempty"`
	Reversed      bool `json:"reversed,omitempty"`
	// Gone are the account's threads whose conversation exists nowhere on
	// disk (Claude's own cleanup deletes old transcripts). They do not hold
	// the account back: there is nothing to move and nothing to lose.
	Gone []GoneThread `json:"gone,omitempty"`

	Moves   []*Conversation `json:"-"`
	Skipped []*Conversation `json:"-"`
}

// work reports whether the account would change anything.
func (a *Account) work() bool {
	return a.Status != statusAborted && (a.After != nil || len(a.Moves) > 0)
}

// Conversation is one harness conversation: everything that moves together.
type Conversation struct {
	Driver  string `json:"driver"`
	ID      string `json:"id"`
	Account string `json:"account,omitempty"`
	// Origin is what the harness recorded as having started it: Claude's
	// entrypoint, Codex's originator.
	Origin string `json:"origin,omitempty"`
	// Entries are what is renamed, in order. A Claude conversation's sidecar
	// folder goes before its transcript, so a crash between the two leaves
	// the transcript, which is what resumes, where the unmigrated account
	// still reads it.
	Entries []*Entry `json:"entries"`
	// Files is every regular file under Entries, with the hash taken before
	// the move.
	Files      []*File  `json:"files"`
	Bytes      int64    `json:"bytes"`
	Referenced []string `json:"threads,omitempty"`
	// Status is planned, moving, moved, skipped, failed or rolled-back.
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Reversed bool   `json:"reversed,omitempty"`

	modified time.Time
}

// Entry is one rename.
type Entry struct {
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	Dir   bool   `json:"dir,omitempty"`
	Moved bool   `json:"moved,omitempty"`
	// Aside is where the source was set aside when the move had to copy
	// across filesystems. It is left there.
	Aside string `json:"aside,omitempty"`
}

// File is one file of a conversation.
type File struct {
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// ThreadCheck is the per-thread assertion: the conversation a thread on a
// migrated account resumes goes with it, or is already there.
type ThreadCheck struct {
	Thread    string `json:"thread"`
	Account   string `json:"account"`
	Session   string `json:"session"`
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	Where     string `json:"where,omitempty"`
	Conflicts string `json:"conflicts,omitempty"`
	// Gone is a conversation in no home at all: OK, with nothing to move.
	Gone bool `json:"gone,omitempty"`
}

// statusGone is a thread check's status when its conversation is nowhere.
const statusGone = "transcript exists nowhere on disk; nothing to move"

// GoneThread is a thread whose conversation exists nowhere on disk.
type GoneThread struct {
	Thread  string `json:"thread"`
	Session string `json:"session"`
}

// FSCheck says whether a machine home and the instances folder share a
// device, which is what makes every move a rename rather than a copy.
type FSCheck struct {
	Ambient string `json:"ambient"`
	Same    bool   `json:"same"`
	Err     string `json:"err,omitempty"`
}

const (
	statusPlanned      = "planned"
	statusAborted      = "aborted"
	statusFailed       = "failed"
	statusVerifyFailed = "verify-failed"
	statusMigrated     = "migrated"
	statusMoving       = "moving"
	statusMoved        = "moved"
	statusSkipped      = "skipped"
	statusRolledBack   = "rolled-back"
)

// credentialNames are never moved or copied: an account comes up signed out
// rather than sharing a login with the terminal.
var credentialNames = map[string]bool{".credentials.json": true, ".claude.json": true, "auth.json": true}

// threadRef is one thread and the conversation it resumes.
type threadRef struct {
	thread, account, driver, session string
}

// PlanMigration works out what a run would do. It reads the user config, the
// store and the machine homes, and writes nothing.
func PlanMigration(ctx context.Context, opts Options) (*Plan, error) {
	if opts.Home == "" {
		return nil, errors.New("home folder is required")
	}
	if opts.Store == nil {
		return nil, errors.New("store is required")
	}
	cfg, err := userconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("load user config: %w", err)
	}
	refs, err := loadThreads(ctx, opts.Store)
	if err != nil {
		return nil, err
	}
	drivers := map[string]Driver{}
	for _, d := range opts.Drivers {
		drivers[d.ID] = d
	}
	p := &Plan{Home: opts.Home, Kept: map[string]int{}}
	p.Accounts, p.Notes = accountsToMigrate(opts.Home, cfg, drivers, refs)
	if len(p.Accounts) == 0 {
		return p, nil
	}

	byID := map[string]*Account{}
	for _, a := range p.Accounts {
		byID[a.ID] = a
	}
	type key struct{ driver, session string }
	refsBySession := map[key][]threadRef{}
	for _, r := range refs {
		if r.session != "" {
			k := key{r.driver, r.session}
			refsBySession[k] = append(refsBySession[k], r)
		}
	}

	// Conversations in each machine home that has an account to give them to.
	scanned := map[string][]*Conversation{}
	var scanOrder []string
	for _, a := range p.Accounts {
		if _, done := scanned[a.Driver]; done || !movesConversations(a.Driver) {
			continue
		}
		d := drivers[a.Driver]
		found, kept, err := scan(d.ID, d.DefaultHome)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", d.DefaultHome, err)
		}
		scanned[d.ID] = found
		scanOrder = append(scanOrder, d.ID)
		p.Kept[d.ID] = kept
	}

	for _, driver := range scanOrder {
		for _, c := range scanned[driver] {
			for _, r := range refsBySession[key{driver, c.ID}] {
				c.Referenced = append(c.Referenced, r.thread)
			}
			owner, why := assign(c, driver, refsBySession[key{driver, c.ID}], byID)
			if owner == nil {
				c.Status, c.Reason = statusSkipped, why
				p.Stay = append(p.Stay, c)
				continue
			}
			c.Account = owner.ID
			c.rebase(owner.Shared, owner.Home)
			if reason := skipReason(c, opts); reason != "" {
				c.Status, c.Reason = statusSkipped, reason
				owner.Skipped = append(owner.Skipped, c)
				continue
			}
			c.Status = statusPlanned
			owner.Moves = append(owner.Moves, c)
		}
	}

	// The assertion: every thread on an account that moves resumes a
	// conversation that goes with it or is already there.
	locations := homesByDriver(opts.Home, cfg, drivers)
	for _, a := range p.Accounts {
		for _, r := range refs {
			if r.account != a.ID || r.session == "" {
				continue
			}
			check := checkThread(a, r, scanned[a.Driver], locations[a.Driver])
			p.Threads = append(p.Threads, check)
			if check.Gone {
				a.Gone = append(a.Gone, GoneThread{Thread: r.thread, Session: r.session})
			}
			if check.OK || a.Status == statusAborted {
				continue
			}
			if a.CatchAll {
				// Already on its own home: nothing here can make the thread
				// whole, and holding back the rest would not either.
				continue
			}
			a.Status = statusAborted
			a.Reason = fmt.Sprintf("thread %s resumes %s conversation %s, which is %s", r.thread, a.Driver, r.session, check.Status)
		}
		if a.Status == statusAborted {
			for _, c := range append(a.Moves, a.Skipped...) {
				c.Status, c.Reason = statusSkipped, "account "+a.ID+" is not being migrated: "+a.Reason
				p.Stay = append(p.Stay, c)
			}
			a.Moves, a.Skipped = nil, nil
		}
	}

	ambient := map[string]bool{}
	for _, a := range p.Accounts {
		if !ambient[a.Shared] {
			ambient[a.Shared] = true
			p.SameFS = append(p.SameFS, sameFS(a.Shared, filepath.Join(opts.Home, ".omniplex", "instances")))
		}
	}
	return p, nil
}

// HasWork reports whether a run would change anything.
func (p *Plan) HasWork() bool {
	for _, a := range p.Accounts {
		if a.work() {
			return true
		}
	}
	return false
}

func movesConversations(driver string) bool {
	return driver == DriverClaude || driver == DriverCodex
}

// loadThreads reads every thread's account and the conversation it resumes.
func loadThreads(ctx context.Context, st Store) ([]threadRef, error) {
	metas, err := st.ListThreads(ctx)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	refs := make([]threadRef, 0, len(metas))
	for _, m := range metas {
		session, err := harnessSession(ctx, st, m.ID)
		if err != nil {
			return nil, fmt.Errorf("read thread %s: %w", m.ID, err)
		}
		account := m.ProviderInstance
		if account == "" {
			account = m.Harness
		}
		refs = append(refs, threadRef{thread: m.ID, account: account, driver: m.Harness, session: session})
	}
	return refs, nil
}

// harnessSession is the harness conversation thread id resumes: its latest
// snapshot with every later event applied, as internal/thread's loadState
// builds it.
func harnessSession(ctx context.Context, st Store, id string) (string, error) {
	state := projection.New(id)
	if seq, blob, err := st.LatestSnapshot(ctx, id); err == nil && blob != nil {
		if s, err := projection.FromSnapshot(blob); err == nil {
			state = s
			state.Seq = seq
		}
	}
	for {
		evs, err := st.ReadEvents(ctx, id, state.Seq, 1000)
		if err != nil {
			return "", err
		}
		if len(evs) == 0 {
			return state.HarnessSessionID, nil
		}
		for _, ev := range evs {
			state.Apply(ev)
		}
	}
}

// accountsToMigrate finds the accounts on a machine home: config entries
// whose home variable is unset or names the machine home, the implicit
// default of a driver whose id threads use with no entry, and any default
// already moved by an earlier run (a catch-all for what it skipped).
func accountsToMigrate(home string, cfg userconfig.Config, drivers map[string]Driver, refs []threadRef) ([]*Account, []string) {
	var accounts []*Account
	var notes []string
	entries := map[string]bool{}
	for _, raw := range cfg.Providers {
		var bare struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &bare) == nil && bare.ID != "" {
			entries[bare.ID] = true
		}
		inst, err := provider.Parse(raw)
		if err != nil {
			continue
		}
		d, ok := drivers[inst.Driver]
		if !ok || d.HomeEnv == "" {
			continue
		}
		own := accounthome.InstanceHome(home, inst.ID)
		a := &Account{
			ID: inst.ID, Driver: d.ID, Name: inst.DisplayName, Home: own, Shared: d.DefaultHome,
			Before: compact(raw), Status: statusPlanned,
		}
		value, set, sensitive := homeValue(inst, d.HomeEnv)
		resolved := accounthome.Resolve(home, value)
		switch {
		case sensitive:
			notes = append(notes, fmt.Sprintf("account %s keeps %s in the secret store; left as it is", inst.ID, d.HomeEnv))
			continue
		case !set || resolved == "" || resolved == filepath.Clean(d.DefaultHome):
			after, err := withHome(raw, d.HomeEnv, own)
			if err != nil {
				notes = append(notes, fmt.Sprintf("account %s: %v; left as it is", inst.ID, err))
				continue
			}
			a.After = after
		case resolved == own && inst.ID == d.ID:
			a.CatchAll = true
		default:
			continue
		}
		accounts = append(accounts, a)
	}

	implicit := map[string]bool{}
	for _, r := range refs {
		if entries[r.account] || implicit[r.account] {
			continue
		}
		d, ok := drivers[r.driver]
		if !ok || d.HomeEnv == "" {
			continue
		}
		if r.account != d.ID {
			implicit[r.account] = true
			notes = append(notes, fmt.Sprintf("threads use account %s, which has no config entry and is no driver's default; left as it is", r.account))
			continue
		}
		implicit[r.account] = true
		own := accounthome.InstanceHome(home, d.ID)
		after, _ := json.Marshal(map[string]any{
			"id":          d.ID,
			"driver":      d.ID,
			"displayName": d.Name,
			"enabled":     true,
			"env":         []provider.EnvVar{{Name: d.HomeEnv, Value: own}},
		})
		accounts = append(accounts, &Account{
			ID: d.ID, Driver: d.ID, Name: d.Name, Home: own, Shared: d.DefaultHome,
			Implicit: true, After: after, Status: statusPlanned,
		})
	}
	return accounts, notes
}

// homeValue is the account's own value for its home variable.
func homeValue(inst provider.Instance, name string) (value string, set, sensitive bool) {
	for _, v := range inst.Env {
		if v.Name != name {
			continue
		}
		if v.Sensitive {
			return "", true, true
		}
		if strings.TrimSpace(v.Value) != "" {
			value, set = v.Value, true
		}
	}
	return value, set, false
}

// withHome is raw with its home variable set to dir: the entry's own value
// replaced, or one added. Every key the entry has, known or not, is kept.
func withHome(raw json.RawMessage, name, dir string) (json.RawMessage, error) {
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	var env []map[string]json.RawMessage
	if e, ok := generic["env"]; ok && string(e) != "null" {
		if err := json.Unmarshal(e, &env); err != nil {
			return nil, fmt.Errorf("env: %w", err)
		}
	}
	value, _ := json.Marshal(dir)
	replaced := false
	for _, v := range env {
		var n string
		if json.Unmarshal(v["name"], &n) == nil && n == name {
			v["value"] = value
			replaced = true
		}
	}
	if !replaced {
		nameJSON, _ := json.Marshal(name)
		env = append(env, map[string]json.RawMessage{"name": nameJSON, "value": value})
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	generic["env"] = envJSON
	return json.Marshal(generic)
}

// compact is raw without insignificant whitespace, so entries compare by
// content.
func compact(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return buf.Bytes()
}

// assign is the account a conversation goes to: the account of the threads
// that resume it, or the driver's own default for one no thread resumes. nil
// leaves it where it is, with why.
func assign(c *Conversation, driver string, refs []threadRef, accounts map[string]*Account) (*Account, string) {
	if len(refs) > 0 {
		owners := map[string]bool{}
		for _, r := range refs {
			owners[r.account] = true
		}
		if len(owners) > 1 {
			names := make([]string, 0, len(owners))
			for o := range owners {
				names = append(names, o)
			}
			sort.Strings(names)
			return nil, "resumed by threads on accounts " + strings.Join(names, ", ")
		}
		owner := accounts[refs[0].account]
		if owner == nil || owner.Driver != driver {
			return nil, "its threads' account " + refs[0].account + " keeps its own home"
		}
		return owner, ""
	}
	if owner := accounts[driver]; owner != nil && owner.Driver == driver {
		return owner, ""
	}
	return nil, "no account takes it: no " + driver + " account is on the machine's home"
}

// skipReason is why a conversation assigned to an account does not move this
// run, or "".
func skipReason(c *Conversation, opts Options) string {
	for _, f := range c.Files {
		if credentialNames[filepath.Base(f.Src)] {
			return "holds a credential file, " + f.Src
		}
	}
	for _, e := range c.Entries {
		if _, err := os.Lstat(e.Dst); err == nil {
			return "destination exists, " + e.Dst
		} else if !errors.Is(err, os.ErrNotExist) {
			return "cannot check the destination: " + err.Error()
		}
	}
	if len(c.Referenced) == 0 && opts.MinAge > 0 {
		if age := opts.Now.Sub(c.modified); age < opts.MinAge {
			return fmt.Sprintf("modified %s ago, under %s", age.Round(time.Second), opts.MinAge)
		}
	}
	return ""
}

// rebase points the conversation's destinations into home, keeping each
// path's place relative to the machine home.
func (c *Conversation) rebase(shared, home string) {
	to := func(src string) string {
		rel, err := filepath.Rel(shared, src)
		if err != nil {
			return ""
		}
		return filepath.Join(home, rel)
	}
	for _, e := range c.Entries {
		e.Dst = to(e.Src)
	}
	for _, f := range c.Files {
		f.Dst = to(f.Src)
	}
}

// homesByDriver is every home a driver's conversations could be in, for
// saying where a thread's conversation is when it is not where it should be.
func homesByDriver(home string, cfg userconfig.Config, drivers map[string]Driver) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	add := func(driver, dir string) {
		if dir != "" && !seen[driver+"\x00"+dir] {
			seen[driver+"\x00"+dir] = true
			out[driver] = append(out[driver], dir)
		}
	}
	for _, d := range drivers {
		add(d.ID, d.DefaultHome)
	}
	for _, raw := range cfg.Providers {
		inst, err := provider.Parse(raw)
		if err != nil {
			continue
		}
		d, ok := drivers[inst.Driver]
		if !ok {
			continue
		}
		if v, set, sensitive := homeValue(inst, d.HomeEnv); set && !sensitive {
			add(d.ID, accounthome.Resolve(home, v))
		}
		add(d.ID, accounthome.InstanceHome(home, inst.ID))
	}
	// Every instance home, configured or not: a removed account's home still
	// holds its conversations.
	if entries, err := os.ReadDir(filepath.Join(home, ".omniplex", "instances")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			for _, d := range drivers {
				add(d.ID, accounthome.InstanceHome(home, e.Name()))
			}
		}
	}
	return out
}

// checkThread is the assertion for one thread of account a.
func checkThread(a *Account, r threadRef, scanned []*Conversation, homes []string) ThreadCheck {
	check := ThreadCheck{Thread: r.thread, Account: a.ID, Session: r.session}
	if a.Driver != DriverClaude && a.Driver != DriverCodex {
		if a.After == nil {
			check.OK, check.Status = true, "on its own home"
			return check
		}
		check.Status = a.Driver + " conversations do not move"
		return check
	}
	atDest := locate(a.Driver, a.Home, r.session)
	if atDest == "" {
		// A sidecar with no transcript beside it is not the conversation
		// being there, and not it being gone either.
		if side := locateSidecar(a.Driver, a.Home, r.session); side != "" {
			check.Status, check.Where = "only its sidecar folder is at the destination", side
			return check
		}
	}
	for _, c := range a.Moves {
		if c.ID == r.session {
			check.OK, check.Status, check.Where = true, "moves", c.Entries[len(c.Entries)-1].Src
			if atDest != "" {
				check.Conflicts = "also at " + atDest
			}
			return check
		}
	}
	if atDest != "" {
		check.OK, check.Status, check.Where = true, "at destination", atDest
		for _, c := range a.Skipped {
			if c.ID == r.session {
				check.Conflicts = "also in the machine home, " + c.Entries[len(c.Entries)-1].Src + ": " + c.Reason
			}
		}
		return check
	}
	for _, c := range a.Skipped {
		if c.ID == r.session {
			check.Status, check.Where = "skipped: "+c.Reason, c.Entries[len(c.Entries)-1].Src
			return check
		}
	}
	if path := locate(a.Driver, a.Shared, r.session); path != "" {
		origin := "not started by Omniplex"
		for _, c := range scanned {
			if c.ID == r.session && c.Status == statusSkipped {
				origin = "left: " + c.Reason
			}
		}
		check.Status, check.Where = origin, path
		return check
	}
	if side := locateSidecar(a.Driver, a.Shared, r.session); side != "" {
		check.Status, check.Where = "only its sidecar folder is in the machine home", side
		return check
	}
	var elsewhere []string
	for _, h := range homes {
		if h == a.Shared || h == a.Home {
			continue
		}
		if path := locate(a.Driver, h, r.session); path != "" {
			elsewhere = append(elsewhere, path)
		} else if side := locateSidecar(a.Driver, h, r.session); side != "" {
			elsewhere = append(elsewhere, side)
		}
	}
	if len(elsewhere) > 0 {
		check.Status = "in another home, not the machine home"
		check.Where = strings.Join(elsewhere, ", ")
		return check
	}
	check.OK, check.Gone, check.Status = true, true, statusGone
	return check
}

// sameFS compares the devices of ambient and dir, or of dir's nearest
// existing parent when it does not exist yet.
func sameFS(ambient, dir string) FSCheck {
	check := FSCheck{Ambient: ambient}
	a, err := device(ambient)
	if err != nil {
		check.Err = err.Error()
		return check
	}
	for {
		b, err := device(dir)
		if err == nil {
			check.Same = a == b
			return check
		}
		parent := filepath.Dir(dir)
		if !errors.Is(err, os.ErrNotExist) || parent == dir {
			check.Err = err.Error()
			return check
		}
		dir = parent
	}
}

func device(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("no device for %s", path)
	}
	return uint64(st.Dev), nil
}
