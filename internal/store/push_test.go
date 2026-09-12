package store

import (
	"context"
	"testing"
)

func sub(endpoint, device string) PushSubscription {
	return PushSubscription{Endpoint: endpoint, DeviceID: device, P256dh: "key-" + endpoint, Auth: "auth-" + endpoint}
}

// Re-registering has to be idempotent. A browser sends its subscription on
// every load, and re-registering is the only repair path a user has when the
// three pieces of push state drift apart — so it must update rather than
// duplicate or fail.
func TestSaveSubscriptionIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.SaveSubscription(ctx, sub("https://push.example/aaa", "phone")); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.SaveSubscription(ctx, sub("https://push.example/aaa", "phone")); err != nil {
		t.Fatalf("second save: %v", err)
	}

	all, err := s.ListSubscriptions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("re-registering the same endpoint left %d rows, want 1", len(all))
	}
}

// The push service can rotate the keys under a stable endpoint. Sending with
// the old ones produces a payload the browser cannot decrypt, and it fails
// silently — so the fresh keys have to win.
func TestSaveSubscriptionUpdatesRotatedKeys(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.SaveSubscription(ctx, sub("https://push.example/aaa", "phone")); err != nil {
		t.Fatalf("save: %v", err)
	}
	rotated := sub("https://push.example/aaa", "phone")
	rotated.P256dh = "rotated-p256dh"
	rotated.Auth = "rotated-auth"
	if err := s.SaveSubscription(ctx, rotated); err != nil {
		t.Fatalf("re-save: %v", err)
	}

	all, _ := s.ListSubscriptions(ctx)
	if len(all) != 1 {
		t.Fatalf("got %d rows, want 1", len(all))
	}
	if all[0].P256dh != "rotated-p256dh" || all[0].Auth != "rotated-auth" {
		t.Errorf("kept the stale keys: %+v", all[0])
	}
}

func TestSaveSubscriptionRejectsIncompleteRows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.SaveSubscription(ctx, PushSubscription{DeviceID: "phone"}); err == nil {
		t.Error("a subscription with no endpoint was accepted; it could never be sent to")
	}
	if err := s.SaveSubscription(ctx, PushSubscription{Endpoint: "https://push.example/a"}); err == nil {
		t.Error("a subscription with no device was accepted; revoking could never remove it")
	}
}

// Revoking a device has to take its notifications with it. A push endpoint
// keeps working after the device token is gone — the push service has never
// heard of our pairing — so a revoked phone would otherwise carry on being
// told what the agent is doing.
func TestDeleteDeviceSubscriptionsLeavesOtherDevicesAlone(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for _, sb := range []PushSubscription{
		sub("https://push.example/phone-1", "phone"),
		sub("https://push.example/phone-2", "phone"),
		sub("https://push.example/laptop-1", "laptop"),
	} {
		if err := s.SaveSubscription(ctx, sb); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	if err := s.DeleteDeviceSubscriptions(ctx, "phone"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	all, _ := s.ListSubscriptions(ctx)
	if len(all) != 1 {
		t.Fatalf("got %d rows, want only the laptop's", len(all))
	}
	if all[0].DeviceID != "laptop" {
		t.Errorf("revoking the phone removed the wrong row: %+v", all[0])
	}
}

func TestDeviceSubscriptions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_ = s.SaveSubscription(ctx, sub("https://push.example/phone-1", "phone"))
	_ = s.SaveSubscription(ctx, sub("https://push.example/laptop-1", "laptop"))

	got, err := s.DeviceSubscriptions(ctx, "phone")
	if err != nil {
		t.Fatalf("device subscriptions: %v", err)
	}
	if len(got) != 1 || got[0].Endpoint != "https://push.example/phone-1" {
		t.Errorf("got %+v, want only the phone's endpoint", got)
	}
}

// A single endpoint goes on its own when the push service reports it gone.
func TestDeleteSubscription(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_ = s.SaveSubscription(ctx, sub("https://push.example/aaa", "phone"))
	_ = s.SaveSubscription(ctx, sub("https://push.example/bbb", "phone"))

	if err := s.DeleteSubscription(ctx, "https://push.example/aaa"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	all, _ := s.ListSubscriptions(ctx)
	if len(all) != 1 || all[0].Endpoint != "https://push.example/bbb" {
		t.Errorf("got %+v, want only bbb", all)
	}
}

// Absent keys are an empty answer, not an error: the first ever call happens
// before anything has been generated, and it is the caller that mints them.
func TestVAPIDKeysStartEmpty(t *testing.T) {
	s := openTestStore(t)

	public, private, err := s.VAPIDKeys(context.Background())
	if err != nil {
		t.Fatalf("read keys: %v", err)
	}
	if public != "" || private != "" {
		t.Errorf("a fresh database already held keys: %q / %q", public, private)
	}
}

// Rotating the pair invalidates every subscription in existence, so a second
// write must never replace the first. Two servers starting at once both try.
func TestSaveVAPIDKeysNeverReplacesThePair(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.SaveVAPIDKeys(ctx, "first-public", "first-private"); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.SaveVAPIDKeys(ctx, "second-public", "second-private"); err != nil {
		t.Fatalf("second save: %v", err)
	}

	public, private, err := s.VAPIDKeys(ctx)
	if err != nil {
		t.Fatalf("read keys: %v", err)
	}
	if public != "first-public" || private != "first-private" {
		t.Errorf("the keypair was replaced: got %q / %q — every existing subscription would be dead", public, private)
	}
}
