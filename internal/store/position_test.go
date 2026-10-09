package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func listIDs(t *testing.T, s *Store) []string {
	t.Helper()
	list, err := s.ListThreads(context.Background())
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	ids := make([]string, len(list))
	for i, m := range list {
		ids[i] = m.ID
	}
	return ids
}

// A database from before the user could order threads opens with the order
// it had — newest created first, the id breaking a same-millisecond tie — and
// a thread created afterwards still lands on top.
func TestOpenBackfillsPositionInTheOldOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		schema,
		`INSERT INTO threads (id, cwd, harness, created_at, updated_at, phase) VALUES
			('oldest', '/w', 'h', 1, 99, 'idle'),
			('tie-a', '/w', 'h', 2, 1, 'idle'),
			('tie-b', '/w', 'h', 2, 1, 'idle'),
			('newest', '/w', 'h', 3, 1, 'idle')`,
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
	want := []string{"newest", "tie-b", "tie-a", "oldest"}
	if got := listIDs(t, s); !slices.Equal(got, want) {
		t.Fatalf("order after backfill = %v, want %v", got, want)
	}
	list, _ := s.ListThreads(context.Background())
	for i := 1; i < len(list); i++ {
		if list[i].Position <= list[i-1].Position {
			t.Fatalf("backfill left positions that do not order on their own: %+v", list)
		}
	}

	// The user's arrangement survives a restart: a second open must not
	// backfill again over it.
	if err := s.SetThreadPosition(context.Background(), "oldest", -10); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if got := listIDs(t, s); got[0] != "oldest" {
		t.Fatalf("reopening re-ran the backfill: %v", got)
	}

	mustCreateThread(t, s, "fresh")
	if got := listIDs(t, s); got[0] != "fresh" {
		t.Fatalf("a new thread did not land on top: %v", got)
	}
}

func TestNewThreadsLandOnTop(t *testing.T) {
	s := openTestStore(t)
	for _, id := range []string{"a", "b", "c"} {
		mustCreateThread(t, s, id)
	}
	if got, want := listIDs(t, s), []string{"c", "b", "a"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// One move writes one row: the neighbours keep their positions, activity
// does not move anything, and the moved thread's activity stamp holds.
func TestSetThreadPositionMovesOneThread(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		mustCreateThread(t, s, id)
	}
	before, _ := s.ListThreads(ctx) // c, b, a
	byID := map[string]ThreadMeta{}
	for _, m := range before {
		byID[m.ID] = m
	}

	// Drop "a" between "c" and "b".
	mid := (byID["c"].Position + byID["b"].Position) / 2
	if err := s.SetThreadPosition(ctx, "a", mid); err != nil {
		t.Fatalf("set position: %v", err)
	}
	if got, want := listIDs(t, s), []string{"c", "a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for _, id := range []string{"b", "c"} {
		if m, _ := s.Thread(ctx, id); m.Position != byID[id].Position {
			t.Fatalf("moving a rewrote %s: %v -> %v", id, byID[id].Position, m.Position)
		}
	}
	if m, _ := s.Thread(ctx, "a"); m.UpdatedAt != byID["a"].UpdatedAt || m.Position != mid {
		t.Fatalf("moved thread: %+v", m)
	}

	// Activity on the bottom thread leaves it at the bottom.
	if _, err := s.Append(ctx, "b", proto.Emit("message.chunk", map[string]any{"delta": "hi"})); err != nil {
		t.Fatal(err)
	}
	if got, want := listIDs(t, s), []string{"c", "a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("activity reordered the list: %v", got)
	}

	if err := s.SetThreadPosition(ctx, "ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("moving an unknown thread: got %v, want ErrNotFound", err)
	}
}
