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
