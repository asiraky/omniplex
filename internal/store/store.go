// Package store is the durable event log. It is the single source of truth;
// projections and snapshots are derived and may be rebuilt at any time.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=NORMAL;

CREATE TABLE IF NOT EXISTS sessions (
  id            TEXT PRIMARY KEY,
  cwd           TEXT NOT NULL,
  harness       TEXT NOT NULL,
  title         TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  head_seq      INTEGER NOT NULL DEFAULT 0,
  phase         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
  id            TEXT PRIMARY KEY,
  root          TEXT NOT NULL UNIQUE,
  config        BLOB NOT NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  session_id    TEXT NOT NULL,
  seq           INTEGER NOT NULL,
  type          TEXT NOT NULL,
  payload       BLOB NOT NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (session_id, seq)
);

CREATE TABLE IF NOT EXISTS snapshots (
  session_id    TEXT NOT NULL,
  seq           INTEGER NOT NULL,
  state         BLOB NOT NULL,
  PRIMARY KEY (session_id, seq)
);

CREATE TABLE IF NOT EXISTS commands (
  command_id    TEXT PRIMARY KEY,
  session_id    TEXT NOT NULL,
  result        BLOB,
  created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS labels (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  color         TEXT NOT NULL DEFAULT '',
  position      INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
`

// SessionMeta is the row-level view of a session, enough for a session list.
type SessionMeta struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd"`
	Harness string `json:"harness"`
	// ProviderInstance is the provider instance the session was created under.
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
	// projection.Attention. It is not a column: the session manager fills it
	// from the live projection (or from Phase for a session with no running
	// actor) when it serves a list. The stored row never holds it, so it can
	// never go stale in the database.
	Attention     string `json:"attention,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Model         string `json:"model,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Effort        string `json:"effort,omitempty"`
	WorkspaceMode string `json:"workspaceMode,omitempty"`
	// BaseRef is the ref a managed worktree was branched from, chosen per
	// session. Empty falls back to the project's default base branch, which is
	// what every session created before this field existed did.
	BaseRef string `json:"baseRef,omitempty"`
	// LabelID is the user-defined label this session sits under, or "" for
	// unlabelled. It is the user's own workflow marker, not lifecycle: nothing
	// in the server reads it, and the sidebar groups by it.
	LabelID string `json:"labelId,omitempty"`
	// LastViewedSeq is the head the user had seen when they last looked at the
	// session, on any paired device. HeadSeq beyond it means something happened
	// that nobody has read — the sidebar's unread signal. Stored, unlike
	// attention, because "seen" is a fact about the user, not derivable from
	// the log. Never omitted: zero is a meaning ("nothing read" — a fresh
	// session, or an explicit mark-unread), not an absence.
	LastViewedSeq     int64           `json:"lastViewedSeq"`
	ProvisionScript   string          `json:"-"`
	DeprovisionScript string          `json:"-"`
	ProvisionResult   json.RawMessage `json:"-"`
}

// Store wraps the database. Writes are serialised through a single mutex-held
// connection; reads use the pool. The session actor is the only writer per
// session in-process, and this guards the cross-session case.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	for _, migration := range []string{
		`ALTER TABLE sessions ADD COLUMN project_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN branch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN effort TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN workspace_mode TEXT NOT NULL DEFAULT 'local'`,
		`ALTER TABLE sessions ADD COLUMN provision_script TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN deprovision_script TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN provision_result BLOB`,
		`ALTER TABLE sessions ADD COLUMN provider_instance TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN base_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN label_id TEXT NOT NULL DEFAULT ''`,
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
	} else if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN last_viewed_seq INTEGER NOT NULL DEFAULT 0`); err != nil {
		tx.Rollback()
		if !strings.Contains(err.Error(), "duplicate column name") {
			return nil, fmt.Errorf("migrate schema: %w", err)
		}
	} else if _, err := tx.Exec(`UPDATE sessions SET last_viewed_seq = head_seq`); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("backfill last_viewed_seq: %w", err)
	} else if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.initAuth(); err != nil {
		return nil, fmt.Errorf("apply auth schema: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// CreateSession inserts the session, checking in the same transaction that its
// project is still there. That check is what makes DeleteProject's refusal
// hold: creating a session reads the project long before it writes the row —
// probing the harness and resolving a workspace happen in between — and a
// delete landing in that gap would otherwise count zero sessions, commit, and
// leave this insert to succeed against a project that no longer exists.
func (s *Store) CreateSession(ctx context.Context, m SessionMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// A session with no project is the pre-project shape and still legal;
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
		`INSERT INTO sessions (id, cwd, harness, provider_instance, title, created_at, updated_at, head_seq, phase, project_id, branch, model, mode, effort, workspace_mode, base_ref, provision_script, deprovision_script)
		 VALUES (?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Cwd, m.Harness, m.ProviderInstance, m.Title, m.CreatedAt, m.UpdatedAt, m.Phase, m.ProjectID, m.Branch, m.Model, m.Mode, m.Effort, m.WorkspaceMode, m.BaseRef, m.ProvisionScript, m.DeprovisionScript); err != nil {
		return err
	}
	return tx.Commit()
}

// Append writes one event at seq = head_seq+1 and bumps head_seq in the same
// transaction. Returns the sequenced event.
func (s *Store) Append(ctx context.Context, sessionID string, em proto.Emission) (proto.Event, error) {
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
	if err := tx.QueryRowContext(ctx, `SELECT head_seq FROM sessions WHERE id = ?`, sessionID).Scan(&head); err != nil {
		return proto.Event{}, fmt.Errorf("load head_seq for %s: %w", sessionID, err)
	}
	seq := head + 1
	ts := proto.NowMillis()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO events (session_id, seq, type, payload, created_at) VALUES (?,?,?,?,?)`,
		sessionID, seq, em.Type, payload, ts); err != nil {
		return proto.Event{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET head_seq = ?, updated_at = ? WHERE id = ?`, seq, ts, sessionID); err != nil {
		return proto.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return proto.Event{}, err
	}

	return proto.Event{SessionID: sessionID, Seq: seq, Timestamp: ts, Type: em.Type, Payload: payload}, nil
}

// SetPhase records idle | turn | closing.
func (s *Store) SetPhase(ctx context.Context, sessionID, phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET phase = ?, updated_at = ? WHERE id = ?`,
		phase, proto.NowMillis(), sessionID)
	return err
}

func (s *Store) SetTitle(ctx context.Context, sessionID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET title = ? WHERE id = ? AND title = ''`, title, sessionID)
	return err
}

// ReadEvents returns events in (afterSeq, afterSeq+limit], ordered by seq.
func (s *Store) ReadEvents(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]proto.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, type, payload, created_at FROM events
		 WHERE session_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []proto.Event
	for rows.Next() {
		ev := proto.Event{SessionID: sessionID}
		var payload []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &payload, &ev.Timestamp); err != nil {
			return nil, err
		}
		ev.Payload = json.RawMessage(payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) Session(ctx context.Context, id string) (SessionMeta, error) {
	var m SessionMeta
	var provisionResult []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT id, cwd, harness, provider_instance, title, created_at, updated_at, head_seq, phase, project_id, branch, model, mode, effort, workspace_mode, base_ref, label_id, last_viewed_seq, provision_script, deprovision_script, provision_result FROM sessions WHERE id = ?`, id).
		Scan(&m.ID, &m.Cwd, &m.Harness, &m.ProviderInstance, &m.Title, &m.CreatedAt, &m.UpdatedAt, &m.HeadSeq, &m.Phase, &m.ProjectID, &m.Branch, &m.Model, &m.Mode, &m.Effort, &m.WorkspaceMode, &m.BaseRef, &m.LabelID, &m.LastViewedSeq, &m.ProvisionScript, &m.DeprovisionScript, &provisionResult)
	m.ProvisionResult = json.RawMessage(provisionResult)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// ListSessions returns every session, newest anchor first. The anchor is
// created_at on purpose (T3 Code's rule): activity must never reorder the
// list. A session emitting an event bumps updated_at but holds its position,
// so the sidebar only moves when a session enters or leaves the list — the
// sort a user can keep a mental map of. The id tie-break keeps two sessions
// created in the same millisecond in one stable order.
func (s *Store) ListSessions(ctx context.Context) ([]SessionMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, cwd, harness, provider_instance, title, created_at, updated_at, head_seq, phase, project_id, branch, model, mode, effort, workspace_mode, base_ref, label_id, last_viewed_seq
		 FROM sessions ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SessionMeta{}
	for rows.Next() {
		var m SessionMeta
		if err := rows.Scan(&m.ID, &m.Cwd, &m.Harness, &m.ProviderInstance, &m.Title, &m.CreatedAt, &m.UpdatedAt, &m.HeadSeq, &m.Phase, &m.ProjectID, &m.Branch, &m.Model, &m.Mode, &m.Effort, &m.WorkspaceMode, &m.BaseRef, &m.LabelID, &m.LastViewedSeq); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateWorkspace(ctx context.Context, id, cwd, branch, phase string, result json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET cwd=?, branch=?, phase=?, provision_result=?, updated_at=? WHERE id=?`, cwd, branch, phase, []byte(result), proto.NowMillis(), id)
	return err
}

func (s *Store) PutProject(ctx context.Context, p project.Project) error {
	b, err := json.Marshal(p.Config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects(id,root,config,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET root=excluded.root,config=excluded.config,updated_at=excluded.updated_at`, p.ID, p.Root, b, p.CreatedAt, p.UpdatedAt)
	return err
}

// UpdateProject writes back a project that must already exist. PutProject is
// an upsert, which is right for adding one and wrong for saving one: a save
// racing a delete would read the row, lose the race, and then insert the stale
// copy straight back. This updates or reports ErrNotFound, so the delete wins.
func (s *Store) UpdateProject(ctx context.Context, p project.Project) error {
	b, err := json.Marshal(p.Config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET root=?, config=?, updated_at=? WHERE id=?`, p.Root, b, p.UpdatedAt, p.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Project(ctx context.Context, id string) (project.Project, error) {
	var p project.Project
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,root,config,created_at,updated_at FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Root, &b, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(b, &p.Config)
	}
	if err == nil {
		p.Config, err = project.Normalize(p.Root, p.Config)
	}
	return p, err
}

func (s *Store) ListProjects(ctx context.Context) ([]project.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,root,config,created_at,updated_at FROM projects ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []project.Project{}
	for rows.Next() {
		var p project.Project
		var b []byte
		if err := rows.Scan(&p.ID, &p.Root, &b, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &p.Config); err != nil {
			return nil, err
		}
		p.Config, err = project.Normalize(p.Root, p.Config)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrProjectInUse is returned when a project still owns sessions. Deleting it
// anyway would leave those sessions pointing at a project that no longer
// exists, and the sessions are the thing with a transcript and a checkout
// behind them — so the sessions go first, deliberately, and the project after.
var ErrProjectInUse = errors.New("project still has sessions")

// DeleteProject forgets a project. Nothing on disk is touched: the project
// directory is the user's, not omniplex's, and .omniplex/project.json stays
// where it is so re-adding the directory restores its settings.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Counted inside the transaction, so a session created between the check
	// and the delete cannot be orphaned by it.
	var sessions int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE project_id = ?`, id).Scan(&sessions); err != nil {
		return err
	}
	if sessions > 0 {
		noun := "sessions"
		if sessions == 1 {
			noun = "session"
		}
		return fmt.Errorf("%w: delete its %d %s first", ErrProjectInUse, sessions, noun)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM events WHERE session_id = ?`,
		`DELETE FROM snapshots WHERE session_id = ?`,
		`DELETE FROM commands WHERE session_id = ?`,
		`DELETE FROM sessions WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- Labels (user-defined session groupings) ----

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

// DeleteLabel removes a definition and unlabels every session carrying it, in
// one transaction. It never deletes a session.
func (s *Store) DeleteLabel(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET label_id='' WHERE label_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetSessionLabel points a session at a label, or "" to clear it. It leaves
// updated_at alone on purpose: filing a session is not activity, and bumping
// the stamp would shuffle a most-recent-first list the user was just reading.
func (s *Store) SetSessionLabel(ctx context.Context, sessionID, labelID string) error {
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
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET label_id=? WHERE id=?`, labelID, sessionID)
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

// MarkSessionViewed records that the user has seen the session up to seq, on
// whatever device they were looking from. MAX keeps it monotonic: a stale
// client reporting an old head must not un-read events a fresher device has
// already seen. MIN caps it at the head: nobody has seen events that do not
// exist, and a cursor past the head would keep future completions read until
// the log caught up to a number a buggy client invented. updated_at is left
// alone — looking is not activity, and the stamp no longer orders the list
// anyway.
func (s *Store) MarkSessionViewed(ctx context.Context, sessionID string, seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_viewed_seq = MAX(last_viewed_seq, MIN(?, head_seq)) WHERE id = ?`, seq, sessionID)
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

// MarkSessionUnread drops the viewed cursor to zero — the explicit "come back
// to this" action, the one legal way the cursor moves backwards.
func (s *Store) MarkSessionUnread(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_viewed_seq = 0 WHERE id = ?`, sessionID)
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

func (s *Store) PutSnapshot(ctx context.Context, sessionID string, seq int64, state any) error {
	blob, err := json.Marshal(state)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO snapshots (session_id, seq, state) VALUES (?,?,?)`, sessionID, seq, blob); err != nil {
		return err
	}
	// Keep only the newest snapshot per session.
	_, err = s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE session_id = ? AND seq < ?`, sessionID, seq)
	return err
}

// LatestSnapshot returns the newest snapshot, or (0, nil, nil) if none exists.
func (s *Store) LatestSnapshot(ctx context.Context, sessionID string) (int64, json.RawMessage, error) {
	var seq int64
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT seq, state FROM snapshots WHERE session_id = ? ORDER BY seq DESC LIMIT 1`, sessionID).Scan(&seq, &blob)
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
func (s *Store) ClaimCommand(ctx context.Context, commandID, sessionID string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var existingSession string
	var result []byte
	err := s.db.QueryRowContext(ctx, `SELECT session_id, result FROM commands WHERE command_id = ?`, commandID).
		Scan(&existingSession, &result)
	if err == nil {
		if existingSession != sessionID {
			return nil, false, fmt.Errorf("command id already belongs to another session")
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
		`INSERT INTO commands (command_id, session_id, result, created_at) VALUES (?,?,NULL,?)`,
		commandID, sessionID, proto.NowMillis())
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
