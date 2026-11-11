package store

import (
	"context"
	"log"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// commandRetention is how long a command id stays replayable. A client only
// retries a command it is still holding in memory, across a reconnect, so a
// week is far past any retry that can actually arrive.
const commandRetention = 7 * 24 * time.Hour

// autoVacuumIncremental is what PRAGMA auto_vacuum reads back as for
// INCREMENTAL.
const autoVacuumIncremental = 2

// convertThreshold is the share of a legacy database's pages that must be
// free before Open pays for the VACUUM that converts it. Below it the copy
// would cost a startup and a database's worth of disk to reclaim little; the
// database stays as it is until snapshot churn takes it over the line.
const convertThreshold = 0.1

// convertToIncrementalVacuum moves a database made before auto_vacuum was set
// onto incremental vacuuming. The mode only changes at a VACUUM, which also
// hands back every free page snapshot churn has left behind, so this is the
// one-off compaction too; after it, Maintain keeps the freelist down.
//
// A failure is logged, not returned: the database works the same either way,
// and a full disk refusing the copy must not stop the server starting.
func (s *Store) convertToIncrementalVacuum() {
	var mode, free, pages int64
	for _, read := range []struct {
		pragma string
		into   *int64
	}{{"auto_vacuum", &mode}, {"freelist_count", &free}, {"page_count", &pages}} {
		if err := s.db.QueryRow(`PRAGMA ` + read.pragma).Scan(read.into); err != nil {
			log.Printf("store: read %s: %v", read.pragma, err)
			return
		}
	}
	if mode == autoVacuumIncremental {
		return
	}
	if float64(free) <= convertThreshold*float64(pages) {
		return
	}
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
	cutoff := proto.NowMillis() - commandRetention.Milliseconds()
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
