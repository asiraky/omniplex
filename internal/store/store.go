// Package store is the durable event log. It is the single source of truth;
// projections and snapshots are derived and may be rebuilt at any time.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/usage"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=NORMAL;

CREATE TABLE IF NOT EXISTS threads (
  id            TEXT PRIMARY KEY,
  cwd           TEXT NOT NULL,
  harness       TEXT NOT NULL,
  title         TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  head_seq      INTEGER NOT NULL DEFAULT 0,
  phase         TEXT NOT NULL
);

` + projectsDDL + `
CREATE TABLE IF NOT EXISTS events (
  thread_id    TEXT NOT NULL,
  seq           INTEGER NOT NULL,
  type          TEXT NOT NULL,
  payload       BLOB NOT NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (thread_id, seq)
);

CREATE TABLE IF NOT EXISTS usage_pricing (
 thread_id TEXT NOT NULL,
 seq INTEGER NOT NULL,
 pricing BLOB NOT NULL,
 PRIMARY KEY(thread_id, seq)
);

CREATE TABLE IF NOT EXISTS snapshots (
  thread_id    TEXT NOT NULL,
  seq           INTEGER NOT NULL,
  state         BLOB NOT NULL,
  PRIMARY KEY (thread_id, seq)
);

CREATE TABLE IF NOT EXISTS commands (
  command_id    TEXT PRIMARY KEY,
  thread_id    TEXT NOT NULL,
  result        BLOB,
  created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS scheduled_prompts (
 thread_id TEXT NOT NULL,
 schedule_id TEXT NOT NULL,
 due_at INTEGER NOT NULL,
 status TEXT NOT NULL,
 PRIMARY KEY (thread_id, schedule_id)
);
CREATE INDEX IF NOT EXISTS scheduled_due ON scheduled_prompts(status, due_at);

CREATE INDEX IF NOT EXISTS events_type_time ON events(type, created_at);

CREATE TABLE IF NOT EXISTS labels (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  color         TEXT NOT NULL DEFAULT '',
  position      INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
`

// ThreadMeta is the row-level view of a thread, enough for a thread list.
type ThreadMeta struct {
	ScheduledCount int    `json:"scheduledCount,omitempty"`
	ID             string `json:"id"`
	Cwd            string `json:"cwd"`
	Harness        string `json:"harness"`
	// ProviderInstance is the provider instance the thread was created under.
	// It sits alongside Harness rather than repurposing it, so existing rows
	// keep meaning what they say; empty resolves to the default instance for
	// Harness, which is the migration.
	ProviderInstance string `json:"providerInstance,omitempty"`
	Title            string `json:"title"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	HeadSeq          int64  `json:"headSeq"`
	Phase            string `json:"phase"`
	// Attention is the derived whose-turn-is-it signal — see
	// projection.Attention. It is not a column: the thread manager fills it
	// from the live projection (or from Phase for a thread with no running
	// actor) when it serves a list. The stored row never holds it, so it can
	// never go stale in the database.
	Attention string `json:"attention,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	// FolderID is the project folder the thread works in. Empty in a project
	// means the thread's scope is everything: it starts in the home folder.
	FolderID      string `json:"folderId,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Model         string `json:"model,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Effort        string `json:"effort,omitempty"`
	WorkspaceMode string `json:"workspaceMode,omitempty"`
	// BaseRef is the ref a managed worktree was branched from, chosen per
	// thread. Empty falls back to the project's default base branch, which is
	// what every thread created before this field existed did.
	BaseRef string `json:"baseRef,omitempty"`
	// LabelID is the user-defined label this thread sits under, or "" for
	// unlabelled. It is the user's own workflow marker, not lifecycle: nothing
	// in the server reads it, and the sidebar groups by it.
	LabelID string `json:"labelId,omitempty"`
	// LastViewedSeq is the head the user had seen when they last looked at the
	// thread, on any paired device. HeadSeq beyond it means something happened
	// that nobody has read — the sidebar's unread signal. Stored, unlike
	// attention, because "seen" is a fact about the user, not derivable from
	// the log. Never omitted: zero is a meaning ("nothing read" — a fresh
	// thread, or an explicit mark-unread), not an absence.
	LastViewedSeq     int64           `json:"lastViewedSeq"`
	ProvisionScript   string          `json:"-"`
	DeprovisionScript string          `json:"-"`
	ProvisionResult   json.RawMessage `json:"-"`
}

// Store wraps the database. Writes are serialised through a single mutex-held
// connection; reads use the pool. The thread actor is the only writer per
// thread in-process, and this guards the cross-thread case.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := renameSessionsToThreads(db, path); err != nil {
		db.Close()
		return nil, fmt.Errorf("rename sessions to threads: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	for _, migration := range []string{
		`ALTER TABLE threads ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN branch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN effort TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN workspace_mode TEXT NOT NULL DEFAULT 'local'`,
		`ALTER TABLE threads ADD COLUMN provision_script TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN deprovision_script TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN provision_result BLOB`,
		`ALTER TABLE threads ADD COLUMN provider_instance TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN base_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN label_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE threads ADD COLUMN folder_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE projects ADD COLUMN home TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(migration); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return nil, fmt.Errorf("migrate schema: %w", err)
		}
	}
	// last_viewed_seq gets its backfill in the same breath as the column: a
	// database upgraded today has been looked at for months, and defaulting to
	// zero would greet the user with a wall of unread dots. One transaction,
	// because the duplicate-column error is the only "already migrated"
	// signal: a crash between ALTER and UPDATE would otherwise leave the
	// column added but unbackfilled, and every later start would see the
	// duplicate and skip the backfill forever.
	if tx, err := db.Begin(); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	} else if _, err := tx.Exec(`ALTER TABLE threads ADD COLUMN last_viewed_seq INTEGER NOT NULL DEFAULT 0`); err != nil {
		tx.Rollback()
		if !strings.Contains(err.Error(), "duplicate column name") {
			return nil, fmt.Errorf("migrate schema: %w", err)
		}
	} else if _, err := tx.Exec(`UPDATE threads SET last_viewed_seq = head_seq`); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("backfill last_viewed_seq: %w", err)
	} else if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	if err := importProjects(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("move projects to folders: %w", err)
	}
	s := &Store{db: db}
	if err := s.initAuth(); err != nil {
		return nil, fmt.Errorf("apply auth schema: %w", err)
	}
	if err := s.backfillUsagePricing(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// renameSessionsToThreads moves a database from before sessions were called
// threads: the table, every session_id column, and the event types. Snapshots
// are a cache holding the old field names, so they go and rebuild from the
// log. A database with no sessions table is new or already moved.
//
// The move is one way, so a copy of the database as it was goes beside it
// first, at path + ".before-threads". A binary from before the move cannot
// open a moved database, but trying leaves an empty sessions table behind
// (its CREATE TABLE IF NOT EXISTS); that one is dropped here, or a rolled
// back deploy could never roll forward again.
func renameSessionsToThreads(db *sql.DB, path string) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('sessions', 'threads')`).Scan(&n); err != nil {
		return err
	}
	var hasSessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sessions'`).Scan(&hasSessions); err != nil || hasSessions == 0 {
		return err
	}
	if n == 2 {
		var rows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&rows); err != nil {
			return err
		}
		if rows > 0 {
			return fmt.Errorf("both sessions and threads tables hold rows; a pre-threads binary wrote to a moved database (the copy from before the move is %s.before-threads)", path)
		}
		_, err := db.Exec(`DROP TABLE sessions`)
		return err
	}
	// Copied under a temp name and renamed, so a restart that kills the copy
	// halfway leaves no partial file to be mistaken for a finished one.
	backup := path + ".before-threads"
	if _, err := os.Stat(backup); errors.Is(err, fs.ErrNotExist) {
		os.Remove(backup + ".tmp")
		if _, err := db.Exec(`VACUUM INTO ?`, backup+".tmp"); err != nil {
			return fmt.Errorf("copy the database before moving it: %w", err)
		}
		if err := os.Rename(backup+".tmp", backup); err != nil {
			return fmt.Errorf("copy the database before moving it: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE sessions RENAME TO threads`,
		`ALTER TABLE events RENAME COLUMN session_id TO thread_id`,
		`ALTER TABLE usage_pricing RENAME COLUMN session_id TO thread_id`,
		`ALTER TABLE snapshots RENAME COLUMN session_id TO thread_id`,
		`ALTER TABLE commands RENAME COLUMN session_id TO thread_id`,
		`ALTER TABLE scheduled_prompts RENAME COLUMN session_id TO thread_id`,
		`UPDATE events SET type = 'thread.' || substr(type, 9) WHERE type LIKE 'session.%'`,
		`DELETE FROM snapshots`,
	} {
		if _, err := tx.Exec(stmt); err != nil && !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// CreateThread inserts the thread, checking in the same transaction that its
// project is still there. That check is what makes DeleteProject's refusal
// hold: creating a thread reads the project long before it writes the row —
// probing the harness and resolving a workspace happen in between — and a
// delete landing in that gap would otherwise count zero threads, commit, and
// leave this insert to succeed against a project that no longer exists.
func (s *Store) CreateThread(ctx context.Context, m ThreadMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// A thread with no project is the pre-project shape and still legal;
	// there is nothing to check for one.
	if m.ProjectID != "" {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, m.ProjectID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: project %s", ErrNotFound, m.ProjectID)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO threads (id, cwd, harness, provider_instance, title, created_at, updated_at, head_seq, phase, project_id, folder_id, branch, model, mode, effort, workspace_mode, base_ref, provision_script, deprovision_script)
		 VALUES (?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Cwd, m.Harness, m.ProviderInstance, m.Title, m.CreatedAt, m.UpdatedAt, m.Phase, m.ProjectID, m.FolderID, m.Branch, m.Model, m.Mode, m.Effort, m.WorkspaceMode, m.BaseRef, m.ProvisionScript, m.DeprovisionScript); err != nil {
		return err
	}
	return tx.Commit()
}

// Append writes one event at seq = head_seq+1 and bumps head_seq in the same
// transaction. Returns the sequenced event.
func (s *Store) Append(ctx context.Context, threadID string, em proto.Emission) (proto.Event, error) {
	payload, err := json.Marshal(em.Payload)
	if err != nil {
		return proto.Event{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return proto.Event{}, err
	}
	defer tx.Rollback()

	var head int64
	if err := tx.QueryRowContext(ctx, `SELECT head_seq FROM threads WHERE id = ?`, threadID).Scan(&head); err != nil {
		return proto.Event{}, fmt.Errorf("load head_seq for %s: %w", threadID, err)
	}
	seq := head + 1
	ts := proto.NowMillis()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (thread_id, seq, type, payload, created_at) VALUES (?,?,?,?,?)`,
		threadID, seq, em.Type, payload, ts); err != nil {
		return proto.Event{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE threads SET head_seq = ?, updated_at = ? WHERE id = ?`, seq, ts, threadID); err != nil {
		return proto.Event{}, err
	}
	if em.Type == proto.UsageUpdated {
		if err := recordUsagePricing(ctx, tx, threadID, seq); err != nil {
			return proto.Event{}, err
		}
	}
	if err := updateScheduleIndex(ctx, tx, threadID, em, payload); err != nil {
		return proto.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return proto.Event{}, err
	}

	return proto.Event{ThreadID: threadID, Seq: seq, Timestamp: ts, Type: em.Type, Payload: payload}, nil
}

// SetPhase records idle | turn | closing.
func (s *Store) SetPhase(ctx context.Context, threadID, phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET phase = ?, updated_at = ? WHERE id = ?`,
		phase, proto.NowMillis(), threadID)
	return err
}

// SetProviderInstance moves a thread to another account of its harness.
func (s *Store) SetProviderInstance(ctx context.Context, threadID, instance string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET provider_instance = ?, updated_at = ? WHERE id = ?`,
		instance, proto.NowMillis(), threadID)
	return err
}

func (s *Store) SetTitle(ctx context.Context, threadID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET title = ? WHERE id = ? AND title = ''`, title, threadID)
	return err
}

// ReadEvents returns events in (afterSeq, afterSeq+limit], ordered by seq.
func (s *Store) ReadEvents(ctx context.Context, threadID string, afterSeq int64, limit int) ([]proto.Event, error) {
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

// UsageEvents feeds the account-level usage aggregation: every
// usage.updated, thread.created, and thread.config_changed event of each
// thread that used tokens in the window, ordered for a per-thread walk.
// Only the latest pre-window usage and model events travel alongside the
// selected window: enough to establish cumulative baselines and attribution
// without loading the full lifetime history of a long-running thread.
func (s *Store) UsageEvents(ctx context.Context, from int64) ([]usage.EventRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.thread_id, s.harness, e.type, e.payload, e.created_at, p.pricing
		 FROM events e JOIN threads s ON s.id = e.thread_id
 LEFT JOIN usage_pricing p ON p.thread_id=e.thread_id AND p.seq=e.seq
		 WHERE e.thread_id IN (SELECT DISTINCT thread_id FROM events WHERE type = 'usage.updated' AND created_at >= ?)
		   AND e.type IN ('usage.updated', 'thread.created', 'thread.config_changed')
 AND (e.created_at >= ? OR e.seq = (
 SELECT MAX(b.seq) FROM events b WHERE b.thread_id=e.thread_id AND b.type=e.type AND b.created_at < ?
 AND (b.type='usage.updated' OR COALESCE(json_extract(b.payload, '$.model'), '') <> '')
 ))
 ORDER BY e.thread_id, e.seq`, from, from, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []usage.EventRow{}
	for rows.Next() {
		var r usage.EventRow
		var payload, pricing []byte
		if err := rows.Scan(&r.ThreadID, &r.Harness, &r.Type, &payload, &r.Timestamp, &pricing); err != nil {
			return nil, err
		}
		r.Payload = json.RawMessage(payload)
		if len(pricing) > 0 {
			if err := json.Unmarshal(pricing, &r.Pricing); err != nil {
				return nil, err
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Thread(ctx context.Context, id string) (ThreadMeta, error) {
	var m ThreadMeta
	var provisionResult []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id, cwd, harness, provider_instance, title, created_at, updated_at, head_seq, phase, project_id, folder_id, branch, model, mode, effort, workspace_mode, base_ref, label_id, last_viewed_seq, provision_script, deprovision_script, provision_result FROM threads WHERE id = ?`, id).
		Scan(&m.ID, &m.Cwd, &m.Harness, &m.ProviderInstance, &m.Title, &m.CreatedAt, &m.UpdatedAt, &m.HeadSeq, &m.Phase, &m.ProjectID, &m.FolderID, &m.Branch, &m.Model, &m.Mode, &m.Effort, &m.WorkspaceMode, &m.BaseRef, &m.LabelID, &m.LastViewedSeq, &m.ProvisionScript, &m.DeprovisionScript, &provisionResult)
	m.ProvisionResult = json.RawMessage(provisionResult)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// ListThreads returns every thread, newest anchor first. The anchor is
// created_at on purpose (T3 Code's rule): activity must never reorder the
// list. A thread emitting an event bumps updated_at but holds its position,
// so the sidebar only moves when a thread enters or leaves the list — the
// sort a user can keep a mental map of. The id tie-break keeps two threads
// created in the same millisecond in one stable order.
func (s *Store) ListThreads(ctx context.Context) ([]ThreadMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, cwd, harness, provider_instance, title, created_at, updated_at, head_seq, phase, project_id, folder_id, branch, model, mode, effort, workspace_mode, base_ref, label_id, last_viewed_seq
		 FROM threads ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ThreadMeta{}
	for rows.Next() {
		var m ThreadMeta
		if err := rows.Scan(&m.ID, &m.Cwd, &m.Harness, &m.ProviderInstance, &m.Title, &m.CreatedAt, &m.UpdatedAt, &m.HeadSeq, &m.Phase, &m.ProjectID, &m.FolderID, &m.Branch, &m.Model, &m.Mode, &m.Effort, &m.WorkspaceMode, &m.BaseRef, &m.LabelID, &m.LastViewedSeq); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateWorkspace(ctx context.Context, id, cwd, branch, phase string, result json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET cwd=?, branch=?, phase=?, provision_result=?, updated_at=? WHERE id=?`, cwd, branch, phase, []byte(result), proto.NowMillis(), id)
	return err
}

func (s *Store) DeleteThread(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM scheduled_prompts WHERE thread_id = ?`,
		`DELETE FROM events WHERE thread_id = ?`,
		`DELETE FROM usage_pricing WHERE thread_id = ?`,
		`DELETE FROM snapshots WHERE thread_id = ?`,
		`DELETE FROM commands WHERE thread_id = ?`,
		`DELETE FROM threads WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- Labels (user-defined thread groupings) ----

// Label is one user-defined grouping. Labels are user-level, not per-project:
// definitions live here so ordering and assignment survive restarts and fan
// out to paired devices, which the userconfig file cannot do.
type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	// Position is the user's chosen sidebar order, smallest first.
	Position  int   `json:"position"`
	CreatedAt int64 `json:"createdAt"`
}

func (s *Store) ListLabels(ctx context.Context) ([]Label, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, color, position, created_at FROM labels ORDER BY position ASC, created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.Position, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateLabel inserts a new definition at the end of the order. The position
// is claimed inside the INSERT itself — not read beforehand by the caller —
// so two devices creating at once cannot land on the same slot. The returned
// label carries the position the row actually got.
func (s *Store) CreateLabel(ctx context.Context, l Label) (Label, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO labels (id, name, color, position, created_at)
		 VALUES (?,?,?,(SELECT COALESCE(MAX(position),-1)+1 FROM labels),?)`,
		l.ID, l.Name, l.Color, l.CreatedAt)
	if err != nil {
		return Label{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT position FROM labels WHERE id=?`, l.ID).Scan(&l.Position); err != nil {
		return Label{}, err
	}
	return l, nil
}

// SaveLabel rewrites an existing definition — rename, recolour, reorder.
// Unknown ids are refused rather than upserted, so a stale client cannot
// resurrect a label another device just deleted.
func (s *Store) SaveLabel(ctx context.Context, l Label) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE labels SET name=?, color=?, position=? WHERE id=?`,
		l.Name, l.Color, l.Position, l.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteLabel removes a definition and unlabels every thread carrying it, in
// one transaction. It never deletes a thread.
func (s *Store) DeleteLabel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET label_id='' WHERE label_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetThreadLabel points a thread at a label, or "" to clear it. It leaves
// updated_at alone on purpose: filing a thread is not activity, and bumping
// the stamp would shuffle a most-recent-first list the user was just reading.
func (s *Store) SetThreadLabel(ctx context.Context, threadID, labelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if labelID != "" {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM labels WHERE id=?`, labelID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE threads SET label_id=? WHERE id=?`, labelID, threadID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkThreadViewed records that the user has seen the thread up to seq, on
// whatever device they were looking from. MAX keeps it monotonic: a stale
// client reporting an old head must not un-read events a fresher device has
// already seen. MIN caps it at the head: nobody has seen events that do not
// exist, and a cursor past the head would keep future completions read until
// the log caught up to a number a buggy client invented. updated_at is left
// alone — looking is not activity, and the stamp no longer orders the list
// anyway.
func (s *Store) MarkThreadViewed(ctx context.Context, threadID string, seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE threads SET last_viewed_seq = MAX(last_viewed_seq, MIN(?, head_seq)) WHERE id = ?`, seq, threadID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkThreadUnread drops the viewed cursor to zero — the explicit "come back
// to this" action, the one legal way the cursor moves backwards.
func (s *Store) MarkThreadUnread(ctx context.Context, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE threads SET last_viewed_seq = 0 WHERE id = ?`, threadID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Snapshots (a cache; deleting the table changes only latency) ----

func (s *Store) PutSnapshot(ctx context.Context, threadID string, seq int64, state any) error {
	blob, err := json.Marshal(state)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO snapshots (thread_id, seq, state) VALUES (?,?,?)`, threadID, seq, blob); err != nil {
		return err
	}
	// Keep only the newest snapshot per thread.
	_, err = s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE thread_id = ? AND seq < ?`, threadID, seq)
	return err
}

// LatestSnapshot returns the newest snapshot, or (0, nil, nil) if none exists.
func (s *Store) LatestSnapshot(ctx context.Context, threadID string) (int64, json.RawMessage, error) {
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

// ---- Command idempotency ----

var ErrNotFound = errors.New("not found")
var ErrCommandInProgress = errors.New("command is still in progress")

// ClaimCommand records a command id. A NULL result is an in-progress claim;
// completed commands always carry their JSON result. This distinction is what
// keeps a concurrent retry from mistaking a placeholder for a successful null
// result.
func (s *Store) ClaimCommand(ctx context.Context, commandID, threadID string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var existingThread string
	var result []byte
	err := s.db.QueryRowContext(ctx, `SELECT thread_id, result FROM commands WHERE command_id = ?`, commandID).
		Scan(&existingThread, &result)
	if err == nil {
		if existingThread != threadID {
			return nil, false, fmt.Errorf("command id already belongs to another thread")
		}
		if result == nil {
			return nil, false, ErrCommandInProgress
		}
		return json.RawMessage(result), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO commands (command_id, thread_id, result, created_at) VALUES (?,?,NULL,?)`,
		commandID, threadID, proto.NowMillis())
	return nil, false, err
}

// ReleaseCommand gives a failed command id back so the same client operation
// can be retried. A completed result is never removed.
func (s *Store) ReleaseCommand(ctx context.Context, commandID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM commands WHERE command_id = ? AND result IS NULL`, commandID)
	return err
}

func (s *Store) CompleteCommand(ctx context.Context, commandID string, result any) error {
	blob, err := json.Marshal(result)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `UPDATE commands SET result = ? WHERE command_id = ?`, blob, commandID)
	return err
}

// ---- Device pairing ----
//
// Auth state lives beside the event log: one file to back up, one file to
// delete to revoke everything.

const authSchema = `
CREATE TABLE IF NOT EXISTS devices (
  id          TEXT PRIMARY KEY,
  token_hash  BLOB NOT NULL UNIQUE,
  label       TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  last_seen   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pairings (
  code_hash   BLOB PRIMARY KEY,
  created_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  used_at     INTEGER
);
`

// Device is a paired client, individually revocable.
type Device struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
}

func (s *Store) initAuth() error {
	_, err := s.db.Exec(authSchema)
	return err
}

func (s *Store) CreateDevice(ctx context.Context, id string, tokenHash []byte, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := proto.NowMillis()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO devices (id, token_hash, label, created_at, last_seen) VALUES (?,?,?,?,?)`,
		id, tokenHash, label, now, now)
	return err
}

// DeviceByToken looks a device up by the hash of its token and refreshes
// last_seen. Returns ErrNotFound when the token is unknown or revoked.
func (s *Store) DeviceByToken(ctx context.Context, tokenHash []byte) (Device, error) {
	var d Device
	err := s.db.QueryRowContext(ctx,
		`SELECT id, label, created_at, last_seen FROM devices WHERE token_hash = ?`, tokenHash).
		Scan(&d.ID, &d.Label, &d.CreatedAt, &d.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}

	// Throttle the write: last_seen is for the device list, not an audit log.
	if now := proto.NowMillis(); now-d.LastSeen > 60_000 {
		s.mu.Lock()
		_, _ = s.db.ExecContext(ctx, `UPDATE devices SET last_seen = ? WHERE id = ?`, now, d.ID)
		s.mu.Unlock()
	}
	return d, nil
}

func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, label, created_at, last_seen FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Label, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RevokeDevice(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	return err
}

func (s *Store) CreatePairing(ctx context.Context, codeHash []byte, expiresAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO pairings (code_hash, created_at, expires_at, used_at) VALUES (?,?,?,NULL)`,
		codeHash, proto.NowMillis(), expiresAt)
	return err
}

// RedeemPairing consumes a pairing code exactly once. The single-statement
// UPDATE is what makes it atomic: two devices racing the same code cannot both
// win, because only one UPDATE can match the un-used row.
func (s *Store) RedeemPairing(ctx context.Context, codeHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := proto.NowMillis()
	res, err := s.db.ExecContext(ctx,
		`UPDATE pairings SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, codeHash, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RedeemPairingForDevice consumes a pairing code and creates the device it
// paid for, in one transaction.
//
// Doing these separately leaves a state where the code is spent but no device
// exists: the caller sees an error, the user retries, and the retry is
// rejected as already-used. Since the code is single-use by design, that is
// unrecoverable without minting a new one.
func (s *Store) RedeemPairingForDevice(ctx context.Context, codeHash []byte, id string, tokenHash []byte, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := proto.NowMillis()

	// The conditional UPDATE is what makes single-use race-safe: only one
	// statement can match the un-used, unexpired row.
	res, err := tx.ExecContext(ctx,
		`UPDATE pairings SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, codeHash, now)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO devices (id, token_hash, label, created_at, last_seen) VALUES (?,?,?,?,?)`,
		id, tokenHash, label, now, now); err != nil {
		return err
	}

	return tx.Commit()
}

// PurgePairings drops codes that are spent or expired.
func (s *Store) PurgePairings(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM pairings WHERE expires_at < ? OR used_at IS NOT NULL`, proto.NowMillis())
	return err
}
