package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/asiraky/omniplex/internal/ephemeral"
)

// What is owed and the wakes on their way outlive the process that wrote
// them, replaced and deleted as they change; a private thread's stay in
// memory and are gone with it.
func TestDelegationsSurviveAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	priv := ephemeral.NewID()
	for _, o := range []OwedResult{
		{Child: "a", Parent: "p", Since: 0},
		{Child: "b", Parent: "p", Since: 1},
		{Child: "a", Parent: "p", Since: 3}, // replaces
		{Child: "gone", Parent: "p"},
		{Child: priv, Parent: "p"},
	} {
		if err := s.SaveOwed(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteOwed(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	wake := PendingWake{Parent: "p", QueueID: "q1", Sent: 1, Entries: []WakeEntry{
		{ID: "a", Title: "A", Status: "completed", Text: "done"},
		{ID: "b", Title: "B", Status: "failed", Text: "boom", Gone: true},
	}}
	for _, w := range []PendingWake{
		{Parent: "p", Entries: []WakeEntry{{ID: "x"}}},
		wake, // replaces
		{Parent: "q", Entries: []WakeEntry{{ID: "y"}}},
		{Parent: priv, Entries: []WakeEntry{{ID: "z"}}},
	} {
		if err := s.SaveWake(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteWake(ctx, "q"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.OwedResults(ctx); len(got) != 3 {
		t.Fatalf("owed before reopen = %+v, want a, b and the private one", got)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owed, err := s.OwedResults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []OwedResult{{Child: "a", Parent: "p", Since: 3}, {Child: "b", Parent: "p", Since: 1}}; !reflect.DeepEqual(owed, want) {
		t.Fatalf("owed = %+v, want %+v", owed, want)
	}
	wakes, err := s.PendingWakes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []PendingWake{wake}; !reflect.DeepEqual(wakes, want) {
		t.Fatalf("wakes = %+v, want %+v", wakes, want)
	}
}
