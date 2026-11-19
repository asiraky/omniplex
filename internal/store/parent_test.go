package store

import (
	"context"
	"testing"

	"github.com/asiraky/omniplex/internal/ephemeral"
)

// A delegated thread keeps the thread that delegated it, on disk and in the
// private store, on both read paths; a thread the user started reads back
// with no parent.
func TestCreateThreadKeepsParentID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	privParent, privChild := ephemeral.NewID(), ephemeral.NewID()
	want := map[string]string{
		"parent":   "",
		"child":    "parent",
		privParent: "",
		privChild:  privParent,
	}
	for _, id := range []string{"parent", "child", privParent, privChild} {
		if err := s.CreateThread(ctx, ThreadMeta{ID: id, Cwd: "/tmp", Harness: "h", Phase: "idle", ParentID: want[id]}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	for id, parent := range want {
		got, err := s.Thread(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ParentID != parent {
			t.Errorf("Thread(%s).ParentID = %q, want %q", id, got.ParentID, parent)
		}
	}
	list, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(want) {
		t.Fatalf("listed %d threads, want %d", len(list), len(want))
	}
	for _, m := range list {
		if m.ParentID != want[m.ID] {
			t.Errorf("listed %s ParentID = %q, want %q", m.ID, m.ParentID, want[m.ID])
		}
	}
}
