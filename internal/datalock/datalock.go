// Package datalock keeps two servers off one database.
//
// A second server on the same database would treat the first one's running
// turns as interrupted and resume them into the same worktrees. That is easy
// to do by accident once there are two ways to run Omniplex: the desktop app
// picks the next free port when a terminal-started server holds the usual one,
// and both default to ~/.omniplex.
package datalock

import (
	"errors"
	"fmt"
	"os"
)

// ErrHeld means another process holds the lock.
var ErrHeld = errors.New("another Omniplex server is using this database")

// Lock is held until Release, or until the process exits: the OS drops it with
// the file handle, so a crash never leaves a stale lock behind.
type Lock struct{ f *os.File }

// Acquire takes the lock for the database at dbPath, failing at once with
// ErrHeld if another process has it.
func Acquire(dbPath string) (*Lock, error) {
	f, err := os.OpenFile(dbPath+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lock(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlock(l.f)
	_ = l.f.Close()
	l.f = nil
}
