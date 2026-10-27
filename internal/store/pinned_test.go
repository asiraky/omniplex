package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "hy.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCreateThread(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateThread(context.Background(), ThreadMeta{ID: id, Cwd: "/tmp", Harness: "h", Phase: "idle"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
}

func TestSetThreadPinned(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateThread(t, s, "s1")
	mustCreateThread(t, s, "s2")

	if err := s.SetThreadPinned(ctx, "ghost", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pinning an unknown thread: got %v, want ErrNotFound", err)
	}

	before, _ := s.Thread(ctx, "s1")
	if before.Pinned {
		t.Fatalf("a new thread starts pinned: %+v", before)
	}
	if err := s.SetThreadPinned(ctx, "s1", true); err != nil {
		t.Fatalf("pin: %v", err)
	}
	got, _ := s.Thread(ctx, "s1")
	if !got.Pinned {
		t.Fatalf("pin not stored: %+v", got)
	}
	// Pinning is not activity: nothing sorted by updated_at may move.
	if got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("updated_at moved on pinning: %d -> %d", before.UpdatedAt, got.UpdatedAt)
	}
	list, _ := s.ListThreads(ctx)
	pinned := map[string]bool{}
	for _, m := range list {
		pinned[m.ID] = m.Pinned
	}
	if !pinned["s1"] || pinned["s2"] {
		t.Fatalf("list does not carry the pin on the right thread: %+v", list)
	}

	if err := s.SetThreadPinned(ctx, "s1", false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if got, _ = s.Thread(ctx, "s1"); got.Pinned {
		t.Fatalf("unpin not stored: %+v", got)
	}
}

// A thread created pinned is stored pinned, with no second write.
func TestCreateThreadPinned(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.CreateThread(ctx, ThreadMeta{ID: "p", Cwd: "/tmp", Harness: "h", Phase: "idle", Pinned: true}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	mustCreateThread(t, s, "u")
	if got, _ := s.Thread(ctx, "p"); !got.Pinned {
		t.Fatalf("created pinned, stored unpinned: %+v", got)
	}
	if got, _ := s.Thread(ctx, "u"); got.Pinned {
		t.Fatalf("created unpinned, stored pinned: %+v", got)
	}
}

// A database from the labels era opens with the label column and table gone
// and every thread unpinned: labels do not turn into pins.
func TestOpenDropsLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		schema,
		`ALTER TABLE threads ADD COLUMN label_id TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE labels (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, color TEXT NOT NULL DEFAULT '',
			position INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`,
		`INSERT INTO labels (id, name, created_at) VALUES ('l1', 'Parked', 1)`,
		`INSERT INTO threads (id, cwd, harness, created_at, updated_at, phase, label_id) VALUES
			('labelled', '/w', 'h', 1, 1, 'idle', 'l1'),
			('plain', '/w', 'h', 2, 2, 'idle', '')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()

	// Twice: the second open sees a database with nothing left to drop.
	for range 2 {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		list, err := s.ListThreads(context.Background())
		if err != nil {
			t.Fatalf("list threads: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("threads lost in the migration: %+v", list)
		}
		for _, m := range list {
			if m.Pinned {
				t.Fatalf("thread %s came out pinned", m.ID)
			}
		}
		var cols, tables int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('threads') WHERE name='label_id'`).Scan(&cols); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='labels'`).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		if cols != 0 || tables != 0 {
			t.Fatalf("labels survived: label_id columns=%d, labels tables=%d", cols, tables)
		}
		s.Close()
	}
}
