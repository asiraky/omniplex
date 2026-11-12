package homemigrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// ReverseResult is what Reverse put back and what it could not.
type ReverseResult struct {
	Restored int
	// Skipped are entries left at the destination, with why: the original
	// place is taken, or the move failed.
	Skipped []string
	// Accounts are the ids whose config entry went back to its before state;
	// Kept are those left as they are because a conversation of theirs could
	// not go back.
	Accounts []string
	Kept     []string
}

// Reverse undoes the run recorded in the manifest at path: every moved entry
// goes back to where it was, never over anything, then each account whose
// conversations are all back gets its config entry as it was before. An
// implicit account, which had none, gets a bare entry with no home variable
// rather than none: with no synthesised default, an account with no entry is
// unknown and its threads could not start. Copies the run left aside are
// left too. Reversing twice does nothing the second time.
func Reverse(path string, now time.Time, logf func(string, ...any)) (*ReverseResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &Manifest{path: path}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest %s is version %d; this build reads version 1", path, m.Version)
	}

	for _, a := range m.Accounts {
		a.Before = nullable(a.Before)
		a.After = nullable(a.After)
	}

	res := &ReverseResult{}
	incomplete := map[string]bool{}
	for i := len(m.Conversations) - 1; i >= 0; i-- {
		c := m.Conversations[i]
		if c.Reversed {
			continue
		}
		whole, touched := true, false
		for j := len(c.Entries) - 1; j >= 0; j-- {
			e := c.Entries[j]
			// A conversation the run was in the middle of may have moved an
			// entry it had not recorded yet: one whose destination is there
			// and whose source is not was moved by it, since the run only
			// moved to destinations that did not exist.
			if !e.Moved && !(c.Status == statusMoving && missing(e.Src) && !missing(e.Dst)) {
				continue
			}
			touched = true
			if _, err := accounthome.Move(e.Dst, e.Src); err != nil {
				whole = false
				reason := fmt.Sprintf("%s → %s: %v", e.Dst, e.Src, err)
				if errors.Is(err, os.ErrExist) {
					reason = fmt.Sprintf("%s stays at %s: %s exists", e.Dst, e.Dst, e.Src)
				}
				res.Skipped = append(res.Skipped, reason)
				logf("reverse home migration: %s", reason)
				continue
			}
			e.Moved = false
			res.Restored++
			if e.Aside != "" {
				logf("reverse home migration: %s is back; the copy run's original is still beside it at %s", e.Src, e.Aside)
			}
		}
		if !touched {
			continue
		}
		if whole {
			c.Reversed = true
		} else {
			incomplete[c.Account] = true
		}
		if err := m.save(); err != nil {
			return res, fmt.Errorf("update manifest: %w", err)
		}
	}

	for _, a := range m.Accounts {
		if !a.ConfigWritten || a.Reversed {
			continue
		}
		if incomplete[a.ID] {
			res.Kept = append(res.Kept, a.ID)
			logf("reverse home migration: account %s keeps its own home in its config: not all of its conversations could go back", a.ID)
			continue
		}
		if err := restoreEntry(a); err != nil {
			res.Kept = append(res.Kept, a.ID)
			logf("reverse home migration: account %s: %v", a.ID, err)
			continue
		}
		a.Reversed = true
		res.Accounts = append(res.Accounts, a.ID)
	}
	when := now.UTC()
	m.ReversedAt = &when
	if err := m.save(); err != nil {
		return res, fmt.Errorf("update manifest: %w", err)
	}
	return res, nil
}

// nullable is raw, or nil for a JSON null.
func nullable(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func missing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

// restoreEntry puts account a's config entry back to before, or to a bare
// entry when it had none.
func restoreEntry(a *Account) error {
	before := a.Before
	if before == nil {
		before = bareEntry(a)
	}
	_, err := userconfig.Update(func(cfg *userconfig.Config) error {
		if at := entryIndex(cfg.Providers, a.ID); at >= 0 {
			cfg.Providers[at] = before
		} else {
			cfg.Providers = append(cfg.Providers, before)
		}
		return nil
	})
	return err
}
