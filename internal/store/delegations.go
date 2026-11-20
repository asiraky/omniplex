package store

import (
	"context"
	"encoding/json"
)

// ---- Delegations: what parents are owed, and the wakes on their way ----
//
// The orchestrator keeps both in memory and writes every change through to
// these tables, so a restart finds a parent still owed its delegated
// threads' results, and a wake that had not reached it yet. A private
// thread's rows go to the in-memory store, as its other rows do: owed by
// the child, wakes by the parent.

const delegationsDDL = `
CREATE TABLE IF NOT EXISTS delegation_owed (
  child_id   TEXT PRIMARY KEY,
  parent_id  TEXT NOT NULL,
  since      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS delegation_wakes (
  parent_id  TEXT PRIMARY KEY,
  queue_id   TEXT NOT NULL DEFAULT '',
  sent       INTEGER NOT NULL,
  entries    TEXT NOT NULL
);
`

// OwedResult is a delegated thread whose result its parent has not had:
// the first turn it finishes from index Since on.
type OwedResult struct {
	Child, Parent string
	Since         int
}

// WakeEntry is one delegated thread's news in a parent's wake.
type WakeEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Text   string `json:"text"`
	Gone   bool   `json:"gone,omitempty"`
}

// PendingWake is the wake a parent is getting. QueueID is the prompt it was
// queued as, "" before it was sent; Sent counts the entries that prompt holds.
type PendingWake struct {
	Parent  string
	QueueID string
	Sent    int
	Entries []WakeEntry
}

// SaveOwed records that parent is owed child's result, replacing what was.
func (s *Store) SaveOwed(ctx context.Context, o OwedResult) error {
	if p := s.private(o.Child); p != nil {
		return p.SaveOwed(ctx, o)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO delegation_owed (child_id, parent_id, since) VALUES (?,?,?)
		 ON CONFLICT(child_id) DO UPDATE SET parent_id = excluded.parent_id, since = excluded.since`,
		o.Child, o.Parent, o.Since)
	return err
}

// DeleteOwed forgets what child owed.
func (s *Store) DeleteOwed(ctx context.Context, child string) error {
	if p := s.private(child); p != nil {
		return p.DeleteOwed(ctx, child)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM delegation_owed WHERE child_id = ?`, child)
	return err
}

// OwedResults is everything owed, on disk and in the private store.
func (s *Store) OwedResults(ctx context.Context) ([]OwedResult, error) {
	var out []OwedResult
	for _, db := range s.both() {
		rows, err := db.db.QueryContext(ctx, `SELECT child_id, parent_id, since FROM delegation_owed ORDER BY child_id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o OwedResult
			if err := rows.Scan(&o.Child, &o.Parent, &o.Since); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SaveWake records the wake a parent is getting, replacing what was.
func (s *Store) SaveWake(ctx context.Context, w PendingWake) error {
	if p := s.private(w.Parent); p != nil {
		return p.SaveWake(ctx, w)
	}
	entries, err := json.Marshal(w.Entries)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO delegation_wakes (parent_id, queue_id, sent, entries) VALUES (?,?,?,?)
		 ON CONFLICT(parent_id) DO UPDATE SET queue_id = excluded.queue_id, sent = excluded.sent, entries = excluded.entries`,
		w.Parent, w.QueueID, w.Sent, string(entries))
	return err
}

// DeleteWake forgets a parent's wake.
func (s *Store) DeleteWake(ctx context.Context, parent string) error {
	if p := s.private(parent); p != nil {
		return p.DeleteWake(ctx, parent)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM delegation_wakes WHERE parent_id = ?`, parent)
	return err
}

// PendingWakes is every wake on its way, on disk and in the private store.
func (s *Store) PendingWakes(ctx context.Context) ([]PendingWake, error) {
	var out []PendingWake
	for _, db := range s.both() {
		rows, err := db.db.QueryContext(ctx, `SELECT parent_id, queue_id, sent, entries FROM delegation_wakes ORDER BY parent_id`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var (
				w       PendingWake
				entries string
			)
			if err := rows.Scan(&w.Parent, &w.QueueID, &w.Sent, &entries); err != nil {
				rows.Close()
				return nil, err
			}
			if err := json.Unmarshal([]byte(entries), &w.Entries); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, w)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// both is the disk store and, when there is one, the private store.
func (s *Store) both() []*Store {
	if s.mem == nil {
		return []*Store{s}
	}
	return []*Store{s, s.mem}
}
