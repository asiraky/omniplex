package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/asiraky/omniplex/internal/ephemeral"
	"github.com/asiraky/omniplex/internal/proto"
)

func createThread(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateThread(context.Background(), ThreadMeta{ID: id, Cwd: "/w", Harness: "h", Phase: "idle", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

// A private thread works like any other while the server runs, sits in the
// one list with the rest, and is nowhere in the database file: reopening it
// finds only the threads that were on disk.
func TestPrivateThreadNeverReachesTheDatabaseFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	priv := ephemeral.NewID()
	createThread(t, s, "disk-1")
	createThread(t, s, priv)
	createThread(t, s, "disk-2")

	if _, err := s.Append(ctx, priv, proto.Emission{Type: "x", Payload: map[string]string{"a": "b"}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.PutSnapshot(ctx, priv, 1, map[string]int{"n": 1}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	evs, err := s.ReadEvents(ctx, priv, 0, 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("read events = %d, %v; want 1", len(evs), err)
	}
	m, err := s.Thread(ctx, priv)
	if err != nil || !m.Private || m.HeadSeq != 1 {
		t.Fatalf("thread = %+v, %v; want private at head 1", m, err)
	}

	// Newest on top, private or not.
	if got, want := listIDs(t, s), []string{"disk-2", priv, "disk-1"}; !slices.Equal(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}

	var onDisk int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM threads WHERE id = ?`, priv).Scan(&onDisk); err != nil || onDisk != 0 {
		t.Fatalf("private rows in the file = %d, %v", onDisk, err)
	}

	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, want := listIDs(t, s), []string{"disk-2", "disk-1"}; !slices.Equal(got, want) {
		t.Fatalf("after reopen list = %v, want %v", got, want)
	}
	if _, err := s.Thread(ctx, priv); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private thread after reopen: %v, want not found", err)
	}
}

func TestDeletingAPrivateThreadForgetsIt(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	priv := ephemeral.NewID()
	createThread(t, s, priv)
	if err := s.DeleteThread(ctx, priv); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Thread(ctx, priv); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v, want not found", err)
	}
	if ids := listIDs(t, s); len(ids) != 0 {
		t.Fatalf("list after delete = %v", ids)
	}
}

// A command claimed for a private thread completes, and its retry gets the
// stored result, through calls that only carry the command id.
func TestPrivateCommandCompletesByID(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	priv := ephemeral.NewID()
	createThread(t, s, priv)
	if _, done, err := s.ClaimCommand(ctx, "c1", priv); err != nil || done {
		t.Fatalf("claim = %v, %v", done, err)
	}
	if err := s.CompleteCommand(ctx, "c1", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	res, done, err := s.ClaimCommand(ctx, "c1", priv)
	if err != nil || !done || string(res) != `{"ok":true}` {
		t.Fatalf("retry = %s, %v, %v", res, done, err)
	}
}
