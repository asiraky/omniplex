package homemigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// ThreadSource is the part of the event log the migration reads: which
// threads exist, on which account, and the harness conversation each one
// resumes. *store.Store is one.
type ThreadSource interface {
	ListThreads(ctx context.Context) ([]store.ThreadMeta, error)
	LatestSnapshot(ctx context.Context, threadID string) (int64, json.RawMessage, error)
	ReadEvents(ctx context.Context, threadID string, afterSeq int64, limit int) ([]proto.Event, error)
}

// thread is one Omniplex thread as the migration sees it.
type thread struct {
	ID, Title, Harness, Instance string
	// SessionID is the harness's conversation id; empty when the thread
	// never started one.
	SessionID string
}

// loadThreads lists every thread with the conversation it resumes, derived
// the way the thread manager rebuilds a thread: the newest snapshot, then
// every event after it.
func loadThreads(ctx context.Context, src ThreadSource) ([]thread, error) {
	if src == nil {
		return nil, nil
	}
	metas, err := src.ListThreads(ctx)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	out := make([]thread, 0, len(metas))
	for _, m := range metas {
		state := projection.New(m.ID)
		if seq, blob, err := src.LatestSnapshot(ctx, m.ID); err != nil {
			return nil, fmt.Errorf("thread %s: %w", m.ID, err)
		} else if blob != nil {
			if s, err := projection.FromSnapshot(blob); err == nil {
				state = s
				state.Seq = seq
			}
		}
		for {
			evs, err := src.ReadEvents(ctx, m.ID, state.Seq, 1000)
			if err != nil {
				return nil, fmt.Errorf("thread %s: %w", m.ID, err)
			}
			if len(evs) == 0 {
				break
			}
			for _, ev := range evs {
				state.Apply(ev)
			}
		}
		instance := m.ProviderInstance
		if instance == "" {
			instance = m.Harness
		}
		out = append(out, thread{ID: m.ID, Title: m.Title, Harness: m.Harness, Instance: instance, SessionID: state.HarnessSessionID})
	}
	return out, nil
}

// OpenThreads opens the event log read-only, for the command line: a server
// may be running on it, and a dry run must change nothing, so no schema is
// applied and nothing is written. A database that does not exist has no
// threads.
func OpenThreads(path string) (ThreadSource, func() error, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return emptySource{}, func() error { return nil }, nil
	} else if err != nil {
		return nil, nil, err
	}
	// With no write-ahead log beside it nothing is writing the database (a
	// running server keeps its log open), and a plain read-only open would
	// leave an empty log and index behind; immutable reads the file as it is.
	mode := "mode=ro"
	if _, err := os.Stat(path + "-wal"); errors.Is(err, os.ErrNotExist) {
		mode = "immutable=1"
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+mode+"&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, err
	}
	return readOnly{db}, db.Close, nil
}

type emptySource struct{}

func (emptySource) ListThreads(context.Context) ([]store.ThreadMeta, error) { return nil, nil }
func (emptySource) LatestSnapshot(context.Context, string) (int64, json.RawMessage, error) {
	return 0, nil, nil
}
func (emptySource) ReadEvents(context.Context, string, int64, int) ([]proto.Event, error) {
	return nil, nil
}

type readOnly struct{ db *sql.DB }

func (r readOnly) ListThreads(ctx context.Context) ([]store.ThreadMeta, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, cwd, harness, provider_instance, title FROM threads ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ThreadMeta
	for rows.Next() {
		var m store.ThreadMeta
		if err := rows.Scan(&m.ID, &m.Cwd, &m.Harness, &m.ProviderInstance, &m.Title); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r readOnly) LatestSnapshot(ctx context.Context, id string) (int64, json.RawMessage, error) {
	var seq int64
	var blob []byte
	err := r.db.QueryRowContext(ctx, `SELECT seq, state FROM snapshots WHERE thread_id = ? ORDER BY seq DESC LIMIT 1`, id).Scan(&seq, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	return seq, blob, err
}

func (r readOnly) ReadEvents(ctx context.Context, id string, after int64, limit int) ([]proto.Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT seq, type, payload, created_at FROM events WHERE thread_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []proto.Event
	for rows.Next() {
		ev := proto.Event{ThreadID: id}
		var payload []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &payload, &ev.Timestamp); err != nil {
			return nil, err
		}
		ev.Payload = payload
		out = append(out, ev)
	}
	return out, rows.Err()
}
