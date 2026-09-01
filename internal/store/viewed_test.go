package store

import (
	"context"
	"errors"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func TestMarkSessionViewed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	mustCreateSession(t, s, "s1")

	if err := s.MarkSessionViewed(ctx, "ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewing an unknown session: got %v, want ErrNotFound", err)
	}

	before, _ := s.Session(ctx, "s1")
	if err := s.MarkSessionViewed(ctx, "s1", 5); err != nil {
		t.Fatalf("mark viewed: %v", err)
	}
	got, _ := s.Session(ctx, "s1")
	if got.LastViewedSeq != 5 {
		t.Fatalf("viewed cursor not stored: %+v", got)
	}
	// Looking is not activity: the row must not move for being read.
	if got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("updated_at moved on viewing: %d -> %d", before.UpdatedAt, got.UpdatedAt)
	}

	// Monotonic: a stale device reporting an old head un-reads nothing.
	if err := s.MarkSessionViewed(ctx, "s1", 3); err != nil {
		t.Fatalf("stale mark viewed: %v", err)
	}
	got, _ = s.Session(ctx, "s1")
	if got.LastViewedSeq != 5 {
		t.Fatalf("stale report moved the cursor backwards: %+v", got)
	}

	// Mark unread is the one legal way backwards.
	if err := s.MarkSessionUnread(ctx, "s1"); err != nil {
		t.Fatalf("mark unread: %v", err)
	}
	got, _ = s.Session(ctx, "s1")
	if got.LastViewedSeq != 0 {
		t.Fatalf("mark unread did not reset the cursor: %+v", got)
	}
	if err := s.MarkSessionUnread(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unreading an unknown session: got %v, want ErrNotFound", err)
	}

	// The list carries the cursor, so the sidebar can compare it to headSeq.
	if err := s.MarkSessionViewed(ctx, "s1", 7); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListSessions(ctx)
	if len(list) != 1 || list[0].LastViewedSeq != 7 {
		t.Fatalf("list does not carry the viewed cursor: %+v", list)
	}
}

func TestListSessionsOrdersByCreation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, m := range []SessionMeta{
		{ID: "old", Cwd: "/tmp", Harness: "h", Phase: "idle", CreatedAt: 1, UpdatedAt: 1},
		{ID: "new", Cwd: "/tmp", Harness: "h", Phase: "idle", CreatedAt: 2, UpdatedAt: 2},
	} {
		if err := s.CreateSession(ctx, m); err != nil {
			t.Fatalf("create session %s: %v", m.ID, err)
		}
	}

	// Activity on the older session bumps its updated_at well past the newer
	// one's. The anchor rule says that must not move it: the list only changes
	// shape when a session enters or leaves it.
	if _, err := s.Append(ctx, "old", proto.Emit("message.chunk", map[string]any{"delta": "hi"})); err != nil {
		t.Fatalf("append: %v", err)
	}

	list, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(list) != 2 || list[0].ID != "new" || list[1].ID != "old" {
		t.Fatalf("activity reordered the list: %+v", list)
	}
}
