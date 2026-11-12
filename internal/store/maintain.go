package store

import (
	"context"
	"database/sql"
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

// convertToIncrementalVacuum puts the database on incremental vacuuming.
// The mode only changes at a VACUUM, and only on the connection that asked
// for it, so both run on one connection. It runs before Open writes
// anything, which makes a new database free to convert. One made before
// auto_vacuum was set converts once it is worth a full copy; the VACUUM also
// hands back every free page snapshot churn has left behind, so this is the
// one-off compaction too, and after it Maintain keeps the freelist down.
//
// A failure is logged, not returned: the database works the same either way,
// and a full disk refusing the copy must not stop the server starting.
func convertToIncrementalVacuum(db *sql.DB) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		log.Printf("store: compact database: %v", err)
		return
	}
	defer conn.Close()
	var mode, free, pages, tables int64
	for _, read := range []struct {
		query string
		into  *int64
	}{
		{`PRAGMA auto_vacuum`, &mode},
		{`PRAGMA freelist_count`, &free},
		{`PRAGMA page_count`, &pages},
		{`SELECT COUNT(*) FROM sqlite_master`, &tables},
	} {
		if err := conn.QueryRowContext(ctx, read.query).Scan(read.into); err != nil {
			log.Printf("store: %s: %v", read.query, err)
			return
		}
	}
	if mode == autoVacuumIncremental {
		return
	}
	fresh := tables == 0
	if !fresh && float64(free) <= convertThreshold*float64(pages) {
		return
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		log.Printf("store: set auto_vacuum: %v", err)
		return
	}
	if fresh {
		if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
			log.Printf("store: set auto_vacuum: %v", err)
		}
		return
	}
	log.Printf("store: compacting the database once for incremental vacuum (%d of %d pages free)", free, pages)
	start := time.Now()
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		log.Printf("store: compact database: %v", err)
		return
	}
	// VACUUM under WAL writes the whole database through the log; truncate it
	// now rather than leave a file the size of the database beside it.
	if _, err := conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
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
