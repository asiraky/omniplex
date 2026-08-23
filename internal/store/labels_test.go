package store

import (
	"context"
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

func mustCreateSession(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateSession(context.Background(), SessionMeta{ID: id, Cwd: "/tmp", Harness: "h", Phase: "idle"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

func TestLabelsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// The store assigns positions in creation order; a caller-supplied
	// position is ignored — it would be a value read before the insert, and
	// two devices can read the same one.
	for i, l := range []Label{
		{ID: "a", Name: "Parked", Color: "#8d8d8d", Position: 99, CreatedAt: 1},
		{ID: "b", Name: "In progress", Color: "#0091ff", Position: 99, CreatedAt: 2},
	} {
		got, err := s.CreateLabel(ctx, l)
		if err != nil {
			t.Fatalf("create label %s: %v", l.ID, err)
		}
		if got.Position != i {
			t.Fatalf("label %s: got position %d, want %d", l.ID, got.Position, i)
		}
	}
	labels, err := s.ListLabels(ctx)
	if err != nil {
		t.Fatalf("list labels: %v", err)
	}
	if len(labels) != 2 || labels[0].ID != "a" || labels[1].ID != "b" {
		t.Fatalf("wrong order: %+v", labels)
	}
	// Save rewrites in place; an unknown id is refused, not upserted.
	if err := s.SaveLabel(ctx, Label{ID: "a", Name: "Iced", Color: "#46a758", Position: 5}); err != nil {
		t.Fatalf("save label: %v", err)
	}
	if err := s.SaveLabel(ctx, Label{ID: "ghost", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("saving an unknown label: got %v, want ErrNotFound", err)
	}
	labels, _ = s.ListLabels(ctx)
	if labels[1].Name != "Iced" || labels[1].Position != 5 {
		t.Fatalf("save did not stick: %+v", labels[1])
	}
}

func TestSessionLabelAssignment(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateSession(t, s, "s1")
	if _, err := s.CreateLabel(ctx, Label{ID: "l1", Name: "Parked"}); err != nil {
		t.Fatal(err)
	}

	// A label must exist to be assigned; a session must exist to be labelled.
	if err := s.SetSessionLabel(ctx, "s1", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assigning an unknown label: got %v, want ErrNotFound", err)
	}
	if err := s.SetSessionLabel(ctx, "ghost", "l1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("labelling an unknown session: got %v, want ErrNotFound", err)
	}

	before, _ := s.Session(ctx, "s1")
	if err := s.SetSessionLabel(ctx, "s1", "l1"); err != nil {
		t.Fatalf("set label: %v", err)
	}
	got, _ := s.Session(ctx, "s1")
	if got.LabelID != "l1" {
		t.Fatalf("label not stored: %+v", got)
	}
	// Filing a session is not activity: the most-recent-first list must not
	// reshuffle under the user.
	if got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("updated_at moved on labelling: %d -> %d", before.UpdatedAt, got.UpdatedAt)
	}
	list, _ := s.ListSessions(ctx)
	if len(list) != 1 || list[0].LabelID != "l1" {
		t.Fatalf("list does not carry the label: %+v", list)
	}

	// Clearing is an empty id, and always legal.
	if err := s.SetSessionLabel(ctx, "s1", ""); err != nil {
		t.Fatalf("clear label: %v", err)
	}
	got, _ = s.Session(ctx, "s1")
	if got.LabelID != "" {
		t.Fatalf("label not cleared: %+v", got)
	}
}

func TestDeleteLabelUnlabelsButKeepsSessions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateSession(t, s, "s1")
	mustCreateSession(t, s, "s2")
	if _, err := s.CreateLabel(ctx, Label{ID: "l1", Name: "Done"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if err := s.SetSessionLabel(ctx, id, "l1"); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteLabel(ctx, "l1"); err != nil {
		t.Fatalf("delete label: %v", err)
	}
	labels, _ := s.ListLabels(ctx)
	if len(labels) != 0 {
		t.Fatalf("label survived deletion: %+v", labels)
	}
	list, _ := s.ListSessions(ctx)
	if len(list) != 2 {
		t.Fatalf("deleting a label must never delete a session: %+v", list)
	}
	for _, m := range list {
		if m.LabelID != "" {
			t.Fatalf("session %s still labelled: %q", m.ID, m.LabelID)
		}
	}
}
