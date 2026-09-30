package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// A database written before sessions were called threads opens with its rows,
// events and schedules intact under the new names.
func TestOpenMovesASessionsDatabaseToThreads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.NewReplacer("threads", "sessions", "thread_id", "session_id").Replace(schema)
	for _, stmt := range []string{
		old,
		`INSERT INTO sessions (id, cwd, harness, title, created_at, updated_at, head_seq, phase) VALUES ('s1', '/w', 'claude', 'old', 1, 1, 2, 'idle')`,
		`INSERT INTO events (session_id, seq, type, payload, created_at) VALUES ('s1', 1, 'session.created', '{}', 1), ('s1', 2, 'message.chunk', '{}', 1)`,
		`INSERT INTO snapshots (session_id, seq, state) VALUES ('s1', 2, '{"sessionId":"s1"}')`,
		`INSERT INTO scheduled_prompts (session_id, schedule_id, due_at, status) VALUES ('s1', 'p1', 5, 'pending')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	m, err := s.Thread(ctx, "s1")
	if err != nil || m.Title != "old" {
		t.Fatalf("thread row: %+v, %v", m, err)
	}
	evs, err := s.ReadEvents(ctx, "s1", 0, 10)
	if err != nil || len(evs) != 2 || evs[0].Type != "thread.created" || evs[1].Type != "message.chunk" {
		t.Fatalf("events: %+v, %v", evs, err)
	}
	if seq, _, err := s.LatestSnapshot(ctx, "s1"); err != nil || seq != 0 {
		t.Fatalf("snapshot with old field names survived: seq %d, %v", seq, err)
	}
	due, err := s.DueScheduleThreads(ctx, 10)
	if err != nil || len(due) != 1 || due[0] != "s1" {
		t.Fatalf("schedules: %v, %v", due, err)
	}

	// Opening again is a no-op, not a second rename.
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

// The move is one way, so the database as it was is kept beside it, and it
// still holds the old shape.
func TestOpenKeepsACopyFromBeforeTheMove(t *testing.T) {
	path := oldSessionsDB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Close()

	raw, err := sql.Open("sqlite", path+".before-threads")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var title string
	if err := raw.QueryRow(`SELECT title FROM sessions WHERE id = 's1'`).Scan(&title); err != nil || title != "old" {
		t.Fatalf("copy: %q, %v", title, err)
	}
}

// A binary from before the move cannot open a moved database, and its failed
// attempt leaves an empty sessions table. The next start must still open, or
// a rolled back deploy could never roll forward.
func TestOpenAfterAPreThreadsBinaryTouchedIt(t *testing.T) {
	path := oldSessionsDB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Close()
	leftover := func(rows ...string) {
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		for _, stmt := range append([]string{`CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, harness TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, head_seq INTEGER NOT NULL DEFAULT 0, phase TEXT NOT NULL)`}, rows...) {
			if _, err := raw.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
	}

	leftover()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("open after an empty leftover: %v", err)
	}
	if m, err := s.Thread(context.Background(), "s1"); err != nil || m.Title != "old" {
		t.Fatalf("thread row: %+v, %v", m, err)
	}
	s.Close()

	// Rows in it mean something wrote there that the threads table lacks;
	// dropping them would lose it, so the start refuses instead.
	leftover(`INSERT INTO sessions (id, cwd, harness, created_at, updated_at, phase) VALUES ('s2', '/w', 'claude', 1, 1, 'idle')`)
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("opened with rows in a leftover sessions table")
	}
}

func oldSessionsDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	old := strings.NewReplacer("threads", "sessions", "thread_id", "session_id").Replace(schema)
	for _, stmt := range []string{
		old,
		`INSERT INTO sessions (id, cwd, harness, title, created_at, updated_at, head_seq, phase) VALUES ('s1', '/w', 'claude', 'old', 1, 1, 0, 'idle')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return path
}
