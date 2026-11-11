package store

import (
	"context"
	"log"
	"time"
)

// dsnPragmas go in the DSN rather than through db.Exec because a pragma is
// per connection: one Exec reaches whichever pooled connection ran it, and
// every other connection keeps the defaults — a full fsync on each commit,
// in synchronous's case. auto_vacuum comes first so a new database takes it
// before anything writes its first page; an existing one only takes it at a
// VACUUM (see convertToIncrementalVacuum). journal_size_limit is what lets
// the WAL shrink back after a checkpoint instead of keeping its high-water
// mark on disk.
const dsnPragmas = "?_pragma=busy_timeout(5000)" +
	"&_pragma=auto_vacuum(INCREMENTAL)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=journal_size_limit(67108864)"

// commandRetention is how long a command id stays replayable. A client only
// retries a command it is still holding in memory, across a reconnect, so a
// week is far past any retry that can actually arrive.
const commandRetention = 7 * 24 * time.Hour

// convertToIncrementalVacuum moves a database made before auto_vacuum was set
// onto incremental vacuuming. The mode only changes at a VACUUM, which also
// hands back every free page snapshot churn has left behind, so this is the
// one-off compaction too; after it, Maintain keeps the freelist down.
//
// A failure is logged, not returned: the database works the same either way,
// and a full disk refusing the copy must not stop the server starting.
func (s *Store) convertToIncrementalVacuum() {
	var mode, free, pages int64
	if err := s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode == 2 {
		return
	}
	s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free)
	s.db.QueryRow(`PRAGMA page_count`).Scan(&pages)
	log.Printf("store: compacting the database once for incremental vacuum (%d of %d pages free)", free, pages)
	start := time.Now()
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		log.Printf("store: compact database: %v", err)
		return
	}
	// VACUUM under WAL writes the whole database through the log; truncate it
	// now rather than leave a file the size of the database beside it.
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		log.Printf("store: checkpoint after compacting: %v", err)
	}
	log.Printf("store: compacted in %s", time.Since(start).Round(time.Millisecond))
}

// Maintain drops command ids past commandRetention and returns the free
// pages to the filesystem. It is cheap when there is nothing to do, so it
// runs on a timer rather than on a measure of how much is free.
func (s *Store) Maintain(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-commandRetention).UnixMilli()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM commands WHERE created_at < ?`, cutoff); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`)
	return err
}

// MaintainEvery runs Maintain now and then every interval until ctx is done.
func (s *Store) MaintainEvery(ctx context.Context, every time.Duration, logf func(string, ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Maintain(ctx); err != nil && ctx.Err() == nil {
			logf("store maintenance: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
