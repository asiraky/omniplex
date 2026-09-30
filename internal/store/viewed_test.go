package store

import (
	"context"
	"errors"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func TestMarkThreadViewed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateThread(t, s, "s1")
	// A log to be read: head_seq = 5.
	for range 5 {
		if _, err := s.Append(ctx, "s1", proto.Emit("message.chunk", map[string]any{"delta": "hi"})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	if err := s.MarkThreadViewed(ctx, "ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewing an unknown thread: got %v, want ErrNotFound", err)
	}

	before, _ := s.Thread(ctx, "s1")
	if err := s.MarkThreadViewed(ctx, "s1", 5); err != nil {
		t.Fatalf("mark viewed: %v", err)
	}
	got, _ := s.Thread(ctx, "s1")
	if got.LastViewedSeq != 5 {
		t.Fatalf("viewed cursor not stored: %+v", got)
	}
	// Looking is not activity: the row must not move for being read.
	if got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("updated_at moved on viewing: %d -> %d", before.UpdatedAt, got.UpdatedAt)
	}

	// Monotonic: a stale device reporting an old head un-reads nothing.
	if err := s.MarkThreadViewed(ctx, "s1", 3); err != nil {
		t.Fatalf("stale mark viewed: %v", err)
	}
	got, _ = s.Thread(ctx, "s1")
	if got.LastViewedSeq != 5 {
		t.Fatalf("stale report moved the cursor backwards: %+v", got)
	}

	// Capped at the head: nobody has seen events that do not exist, and a
	// cursor past the head would keep future completions read until the log
	// caught up to a number a buggy client invented.
	if err := s.MarkThreadViewed(ctx, "s1", 99); err != nil {
		t.Fatalf("overshooting mark viewed: %v", err)
	}
	got, _ = s.Thread(ctx, "s1")
	if got.LastViewedSeq != 5 {
		t.Fatalf("cursor ran past the head: %+v", got)
	}

	// Mark unread is the one legal way backwards.
	if err := s.MarkThreadUnread(ctx, "s1"); err != nil {
		t.Fatalf("mark unread: %v", err)
	}
	got, _ = s.Thread(ctx, "s1")
	if got.LastViewedSeq != 0 {
		t.Fatalf("mark unread did not reset the cursor: %+v", got)
	}
	if err := s.MarkThreadUnread(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreading an unknown thread: got %v, want ErrNotFound", err)
	}

	// The list carries the cursor, so the sidebar can compare it to headSeq.
	if err := s.MarkThreadViewed(ctx, "s1", 4); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListThreads(ctx)
	if len(list) != 1 || list[0].LastViewedSeq != 4 {
		t.Fatalf("list does not carry the viewed cursor: %+v", list)
	}
}

func TestListThreadsOrdersByCreation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, m := range []ThreadMeta{
		{ID: "old", Cwd: "/tmp", Harness: "h", Phase: "idle", CreatedAt: 1, UpdatedAt: 1},
		{ID: "new", Cwd: "/tmp", Harness: "h", Phase: "idle", CreatedAt: 2, UpdatedAt: 2},
	} {
		if err := s.CreateThread(ctx, m); err != nil {
			t.Fatalf("create thread %s: %v", m.ID, err)
		}
	}

	// Activity on the older thread bumps its updated_at well past the newer
	// one's. The anchor rule says that must not move it: the list only changes
	// shape when a thread enters or leaves it.
	if _, err := s.Append(ctx, "old", proto.Emit("message.chunk", map[string]any{"delta": "hi"})); err != nil {
		t.Fatalf("append: %v", err)
	}

	list, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatalf("list threads: %v", err)
	}
	if len(list) != 2 || list[0].ID != "new" || list[1].ID != "old" {
		t.Fatalf("activity reordered the list: %+v", list)
	}
}
