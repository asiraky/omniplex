package store

import (
	"context"
	"errors"
	"testing"
)

func TestSubscribingTheSameEndpointAgainKeepsOneRowWithFreshKeysAndLabel(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	first, err := s.SavePushSubscription(ctx, PushSubscription{Endpoint: "https://push.example/a", P256dh: "k1", Auth: "a1", Label: "Phone"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	again, err := s.SavePushSubscription(ctx, PushSubscription{Endpoint: "https://push.example/a", P256dh: "k2", Auth: "a2", Label: "Pixel", DeviceID: "d1"})
	if err != nil {
		t.Fatalf("save again: %v", err)
	}
	if again != first {
		t.Fatalf("re-subscribing changed the id: %q then %q", first, again)
	}

	subs, err := s.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(subs), subs)
	}
	got := subs[0]
	if got.ID != first || got.P256dh != "k2" || got.Auth != "a2" || got.Label != "Pixel" || got.DeviceID != "d1" {
		t.Fatalf("row not refreshed: %+v", got)
	}
}

func TestPushSubscriptionsListNewestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	var ids []string
	for _, ep := range []string{"https://push.example/1", "https://push.example/2", "https://push.example/3"} {
		id, err := s.SavePushSubscription(ctx, PushSubscription{Endpoint: ep, P256dh: "k", Auth: "a"})
		if err != nil {
			t.Fatalf("save %s: %v", ep, err)
		}
		ids = append(ids, id)
	}

	subs, err := s.ListPushSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(subs) != 3 || subs[0].ID != ids[2] || subs[1].ID != ids[1] || subs[2].ID != ids[0] {
		t.Fatalf("want newest first %v, got %+v", ids, subs)
	}
}

func TestDeletingAPushSubscriptionRemovesItAndAnUnknownOneIsNotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	id, err := s.SavePushSubscription(ctx, PushSubscription{Endpoint: "https://push.example/a", P256dh: "k", Auth: "a"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.DeletePushSubscription(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if subs, _ := s.ListPushSubscriptions(ctx); len(subs) != 0 {
		t.Fatalf("subscription survived delete: %+v", subs)
	}
	if err := s.DeletePushSubscription(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting it twice: got %v, want ErrNotFound", err)
	}
}
