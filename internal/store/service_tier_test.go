package store

import (
	"context"
	"testing"
)

// A thread's service tier survives the round trip through the database on
// both read paths, and a thread created without one reads back as "".
func TestCreateThreadKeepsServiceTier(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.CreateThread(ctx, ThreadMeta{ID: "fast", Cwd: "/tmp", Harness: "h", Phase: "idle", ServiceTier: "priority"}); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	mustCreateThread(t, s, "plain")

	want := map[string]string{"fast": "priority", "plain": ""}
	for id, tier := range want {
		got, err := s.Thread(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.ServiceTier != tier {
			t.Errorf("Thread(%s).ServiceTier = %q, want %q", id, got.ServiceTier, tier)
		}
	}
	list, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.ServiceTier != want[m.ID] {
			t.Errorf("listed %s ServiceTier = %q, want %q", m.ID, m.ServiceTier, want[m.ID])
		}
	}
}
