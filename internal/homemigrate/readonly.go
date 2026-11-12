package homemigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// ReadOnlyStore reads the event log without writing to it, for a dry run
// beside a live server. store.Open is not that: it migrates the schema and
// frees the server's unfinished commands, which would break the commands the
// live server is running.
type ReadOnlyStore struct{ db *sql.DB }

// OpenReadOnly opens the database at path read-only. It takes no lock and
// needs none: SQLite readers do not block the server's writes.
func OpenReadOnly(path string) (*ReadOnlyStore, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s read-only: %w", path, err)
	}
	return &ReadOnlyStore{db: db}, nil
}

func (s *ReadOnlyStore) Close() error { return s.db.Close() }

// ListThreads returns the fields the migration reads.
func (s *ReadOnlyStore) ListThreads(ctx context.Context) ([]store.ThreadMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, harness, provider_instance FROM threads ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ThreadMeta
	for rows.Next() {
		var m store.ThreadMeta
		if err := rows.Scan(&m.ID, &m.Harness, &m.ProviderInstance); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestSnapshot is store.Store's, read-only.
func (s *ReadOnlyStore) LatestSnapshot(ctx context.Context, threadID string) (int64, json.RawMessage, error) {
	var seq int64
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT seq, state FROM snapshots WHERE thread_id = ? ORDER BY seq DESC LIMIT 1`, threadID).Scan(&seq, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	return seq, json.RawMessage(blob), nil
}

// ReadEvents is store.Store's, read-only.
func (s *ReadOnlyStore) ReadEvents(ctx context.Context, threadID string, afterSeq int64, limit int) ([]proto.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, type, payload, created_at FROM events
		 WHERE thread_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`, threadID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []proto.Event
	for rows.Next() {
		ev := proto.Event{ThreadID: threadID}
		var payload []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &payload, &ev.Timestamp); err != nil {
			return nil, err
		}
		ev.Payload = json.RawMessage(payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}
