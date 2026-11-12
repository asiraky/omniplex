package homemigrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// Manifest is the record a run keeps at
// ~/.omniplex/migrations/homes-<UTC timestamp>.json: every planned move with
// the size and sha256 of every file, each account's config entry before and
// after, and the outcome of each conversation as it happens. Reverse reads
// it.
type Manifest struct {
	Version       int             `json:"version"`
	CreatedAt     time.Time       `json:"createdAt"`
	Home          string          `json:"home"`
	Accounts      []*Account      `json:"accounts"`
	Conversations []*Conversation `json:"conversations"`
	SameFS        []FSCheck       `json:"sameFilesystem,omitempty"`
	ReversedAt    *time.Time      `json:"reversedAt,omitempty"`

	path string
}

// Result is what a run did.
type Result struct {
	Plan     *Plan
	Manifest string
}

// errVerify stops a run: something on disk is not what the manifest says, and
// nothing more is moved until a person has looked.
var errVerify = errors.New("verify failed")

// Run migrates every account the plan finds. An account that cannot be
// migrated whole is left as it was; the rest go ahead. The error is for a
// run that could not plan, could not write its manifest, or stopped on a
// failed verify.
func Run(ctx context.Context, opts Options) (*Result, error) {
	p, err := PlanMigration(ctx, opts)
	if err != nil {
		return nil, err
	}
	res := &Result{Plan: p}
	for _, n := range p.Notes {
		opts.logf("home migration: %s", n)
	}
	for _, a := range p.Accounts {
		if a.Status == statusAborted {
			opts.logf("home migration: NOT MIGRATING account %s (%s): %s; its config is unchanged and it keeps running on %s", a.ID, a.Name, a.Reason, a.Shared)
		}
	}
	for _, c := range p.Stay {
		if len(c.Referenced) > 0 || strings.HasPrefix(c.Reason, "no account") {
			opts.logf("home migration: %s conversation %s stays in %s: %s", c.Driver, c.ID, c.Entries[len(c.Entries)-1].Src, c.Reason)
		}
	}
	if !p.HasWork() {
		for _, a := range p.Accounts {
			logSkipped(opts, a)
		}
		return res, nil
	}

	for _, a := range p.Accounts {
		if !a.work() {
			continue
		}
		for _, c := range a.Moves {
			if err := hashFiles(c.Files); err != nil {
				a.Status = statusAborted
				a.Reason = fmt.Sprintf("cannot read %s conversation %s: %v", c.Driver, c.ID, err)
				opts.logf("home migration: NOT MIGRATING account %s (%s): %s; its config is unchanged", a.ID, a.Name, a.Reason)
				break
			}
			c.Bytes = 0
			for _, f := range c.Files {
				c.Bytes += f.Size
			}
		}
	}

	m := &Manifest{Version: 1, CreatedAt: opts.Now.UTC(), Home: p.Home, Accounts: p.Accounts, SameFS: p.SameFS}
	for _, a := range p.Accounts {
		m.Conversations = append(m.Conversations, a.Moves...)
		m.Conversations = append(m.Conversations, a.Skipped...)
	}
	m.Conversations = append(m.Conversations, p.Stay...)
	path, err := createManifest(filepath.Join(p.Home, ".omniplex", "migrations"), m)
	if err != nil {
		return res, fmt.Errorf("write the migration manifest: %w", err)
	}
	res.Manifest = path
	for _, s := range p.SameFS {
		if !s.Same {
			opts.logf("home migration: %s and %s are not on one filesystem (%s); conversations are copied, and each original is left beside itself as .<name>.moving", s.Ambient, filepath.Join(p.Home, ".omniplex", "instances"), s.Err)
		}
	}

	for _, a := range p.Accounts {
		if !a.work() {
			continue
		}
		err := migrateAccount(m, a, opts)
		if saveErr := m.save(); saveErr != nil && err == nil {
			err = fmt.Errorf("update the manifest: %w", saveErr)
		}
		if err != nil {
			opts.logf("home migration: account %s (%s) NOT MIGRATED: %v; its config is unchanged (manifest %s)", a.ID, a.Name, err, path)
			if errors.Is(err, errVerify) {
				return res, err
			}
			continue
		}
		moved := 0
		var size int64
		for _, c := range a.Moves {
			if c.Status == statusMoved {
				moved++
				size += c.Bytes
			}
		}
		line := fmt.Sprintf("home migration: account %s (%s) now has its own home %s: moved %d conversations (%s), skipped %d, manifest %s",
			a.ID, a.Name, a.Home, moved, humanBytes(size), countSkipped(a), path)
		if len(a.Gone) > 0 {
			line += fmt.Sprintf("; %d threads' conversations exist nowhere on disk, nothing to move (%s)", len(a.Gone), goneList(a.Gone))
		}
		if a.After != nil {
			line += "; it needs signing in"
		}
		opts.logf("%s", line)
		logSkipped(opts, a)
	}
	return res, nil
}

func logSkipped(opts Options, a *Account) {
	for _, c := range a.Skipped {
		opts.logf("home migration: skipped %s conversation %s (%s): %s", c.Driver, c.ID, c.Entries[len(c.Entries)-1].Src, c.Reason)
	}
}

func countSkipped(a *Account) int {
	n := len(a.Skipped)
	for _, c := range a.Moves {
		if c.Status == statusSkipped {
			n++
		}
	}
	return n
}

// migrateAccount is one account, in the order that never leaves it half
// done: its home and shared links, its conversations, a verify of what
// moved, and only then its config entry.
func migrateAccount(m *Manifest, a *Account, opts Options) error {
	fail := func(status string, err error) error {
		a.Status, a.Reason = status, err.Error()
		return err
	}
	if err := os.MkdirAll(a.Home, 0o700); err != nil {
		return fail(statusFailed, fmt.Errorf("create %s: %w", a.Home, err))
	}
	var entries []accounthome.Entry
	for _, d := range opts.Drivers {
		if d.ID == a.Driver {
			entries = d.SharedEntries
		}
	}
	conflicts, err := accounthome.Link(a.Shared, a.Home, entries)
	if err != nil {
		// The links are remade before every session; a missing one costs
		// configuration, not history, so the move goes on.
		opts.logf("home migration: sharing %s into %s: %v", a.Shared, a.Home, err)
	}
	if len(conflicts) > 0 {
		opts.logf("home migration: account %s keeps its own %s rather than the shared ones", a.ID, strings.Join(conflicts, ", "))
	}

	for i, c := range a.Moves {
		c.Status = statusMoving
		if err := m.save(); err != nil {
			c.Status = statusPlanned
			rollbackAccount(a.Moves[:i], opts)
			return fail(statusFailed, fmt.Errorf("update the manifest: %w", err))
		}
		err := moveConversation(c)
		switch {
		case err == nil:
			c.Status = statusMoved
		case errors.Is(err, fs.ErrExist) && len(c.Referenced) == 0:
			c.Status, c.Reason = statusSkipped, "destination appeared: "+err.Error()
		default:
			c.Status, c.Reason = statusFailed, err.Error()
			_ = m.save()
			rollbackAccount(a.Moves[:i], opts)
			return fail(statusFailed, fmt.Errorf("move %s conversation %s: %w; this account's moves are undone", c.Driver, c.ID, err))
		}
		if err := m.save(); err != nil {
			rollbackAccount(a.Moves[:i+1], opts)
			return fail(statusFailed, fmt.Errorf("update the manifest: %w", err))
		}
	}

	if err := verify(a, opts); err != nil {
		return fail(statusVerifyFailed, fmt.Errorf("%w: %v", errVerify, err))
	}
	if a.After != nil {
		if err := writeEntry(a.ID, a.Before, a.After); err != nil {
			return fail(statusFailed, fmt.Errorf("write config: %w", err))
		}
		a.ConfigWritten = true
	}
	a.Status = statusMigrated
	return nil
}

// moveConversation renames each entry in order; a failure puts back what
// had moved, so a conversation is never split across homes.
func moveConversation(c *Conversation) error {
	for i, e := range c.Entries {
		aside, err := accounthome.Move(e.Src, e.Dst)
		if err != nil {
			for j := i - 1; j >= 0; j-- {
				undoEntry(c.Entries[j])
			}
			return err
		}
		e.Moved, e.Aside = true, aside
	}
	return nil
}

// undoEntry moves one entry back, never over anything.
func undoEntry(e *Entry) {
	if !e.Moved {
		return
	}
	if _, err := accounthome.Move(e.Dst, e.Src); err == nil {
		e.Moved = false
	}
}

// rollbackAccount moves an account's moved conversations back, newest first.
func rollbackAccount(done []*Conversation, opts Options) {
	for i := len(done) - 1; i >= 0; i-- {
		c := done[i]
		if c.Status != statusMoved {
			continue
		}
		for j := len(c.Entries) - 1; j >= 0; j-- {
			undoEntry(c.Entries[j])
		}
		back := true
		for _, e := range c.Entries {
			if e.Moved {
				back = false
				opts.logf("home migration: could not move %s back to %s; it is still at the destination", e.Dst, e.Src)
			}
		}
		if back {
			c.Status, c.Reason = statusRolledBack, "its account's migration failed"
		}
	}
}

// verify checks what moved against the manifest: every destination file
// hashes as its source did and no moved source is left behind, every
// skipped conversation is still where it was, nothing planned is missing
// from both, and the totals are the plan's.
func verify(a *Account, opts Options) error {
	var problems []string
	moved, skipped := 0, 0
	var movedBytes, plannedBytes int64
	for _, c := range a.Moves {
		plannedBytes += c.Bytes
		switch c.Status {
		case statusMoved:
			moved++
			movedBytes += c.Bytes
			for _, f := range c.Files {
				sum, size, err := hashFile(f.Dst)
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v", f.Dst, err))
				} else if sum != f.SHA256 || size != f.Size {
					problems = append(problems, fmt.Sprintf("%s does not match the manifest (%d bytes, sha256 %s; want %d, %s)", f.Dst, size, sum, f.Size, f.SHA256))
				}
			}
			for _, e := range c.Entries {
				if _, err := os.Lstat(e.Src); err == nil {
					problems = append(problems, e.Src+" is still in the machine home after moving")
				}
			}
		case statusSkipped:
			skipped++
		default:
			problems = append(problems, fmt.Sprintf("%s conversation %s is %s", c.Driver, c.ID, c.Status))
		}
	}
	for _, c := range a.Skipped {
		skipped++
		if _, err := os.Lstat(c.Entries[len(c.Entries)-1].Src); err != nil {
			problems = append(problems, fmt.Sprintf("skipped %s is gone: %v", c.Entries[len(c.Entries)-1].Src, err))
		}
	}
	if moved+skipped != len(a.Moves)+len(a.Skipped) {
		problems = append(problems, fmt.Sprintf("%d moved and %d skipped, but %d planned", moved, skipped, len(a.Moves)+len(a.Skipped)))
	}
	if moved == len(a.Moves) && movedBytes != plannedBytes {
		problems = append(problems, fmt.Sprintf("moved %d bytes, planned %d", movedBytes, plannedBytes))
	}

	// What the machine home still holds of this account's conversations is
	// exactly what was skipped.
	if movesConversations(a.Driver) && len(a.Moves)+len(a.Skipped) > 0 {
		mine := map[string]bool{}
		for _, c := range append(append([]*Conversation(nil), a.Moves...), a.Skipped...) {
			mine[c.Entries[len(c.Entries)-1].Src] = true
		}
		left, _, err := scan(a.Driver, a.Shared)
		if err != nil {
			problems = append(problems, fmt.Sprintf("rescan %s: %v", a.Shared, err))
		}
		remaining := 0
		for _, c := range left {
			if mine[c.Entries[len(c.Entries)-1].Src] {
				remaining++
			}
		}
		if remaining != skipped {
			problems = append(problems, fmt.Sprintf("%d of this account's Omniplex conversations are still in %s, but %d were skipped", remaining, a.Shared, skipped))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// writeEntry puts after in place of the entry with id, or adds it when the
// account had none. An entry that changed since the plan read it is left
// alone: someone edited it meanwhile, and their edit wins.
func writeEntry(id string, before, after json.RawMessage) error {
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		at := entryIndex(cfg.Providers, id)
		switch {
		case before == nil && at >= 0:
			return fmt.Errorf("an entry for %s appeared since the plan", id)
		case before != nil && at < 0:
			return fmt.Errorf("the entry for %s went away since the plan", id)
		case before != nil && !bytes.Equal(compact(cfg.Providers[at]), compact(before)):
			return fmt.Errorf("the entry for %s changed since the plan", id)
		case at >= 0:
			cfg.Providers[at] = after
		default:
			cfg.Providers = append(cfg.Providers, after)
		}
		return nil
	})
	return err
}

func entryIndex(entries []json.RawMessage, id string) int {
	for i, raw := range entries {
		var bare struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &bare) == nil && bare.ID == id {
			return i
		}
	}
	return -1
}

// hashFiles records each file's sha256 and size.
func hashFiles(files []*File) error {
	for _, f := range files {
		sum, size, err := hashFile(f.Src)
		if err != nil {
			return err
		}
		f.SHA256, f.Size = sum, size
	}
	return nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// createManifest writes m under a new name in dir, never over an earlier
// run's.
func createManifest(dir string, m *Manifest) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	base := "homes-" + m.CreatedAt.UTC().Format("20060102T150405Z")
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		path := filepath.Join(dir, name+".json")
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			m.path = path
			return path, m.save()
		}
	}
	return "", fmt.Errorf("no free manifest name in %s", dir)
}

// save writes the manifest atomically: a crash leaves the last whole one.
func (m *Manifest) save() error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".homes-*.tmp")
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
	return os.Rename(name, m.path)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func goneList(gone []GoneThread) string {
	parts := make([]string, len(gone))
	for i, g := range gone {
		parts[i] = g.Thread + " " + g.Session
	}
	return strings.Join(parts, ", ")
}
