package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"github.com/asiraky/omniplex/internal/store"
)

// subscriber goes in the VAPID JWT's sub claim. Apple's push service rejects
// a push whose sub is not a real https: or mailto: URL.
const subscriber = "https://github.com/asiraky/omniplex"

// ttl is how long, in seconds, a push service holds a push for a device that
// is offline.
const ttl = 24 * 60 * 60

// Message is one notification.
type Message struct {
	Title, Body string
	ThreadID    string // "" for a test push
	Urgency     string // "low" | "normal" | "high"; "" = normal
}

// Store is what the sender needs; *store.Store satisfies it.
type Store interface {
	ListPushSubscriptions(ctx context.Context) ([]store.PushSubscription, error)
	DeletePushSubscription(ctx context.Context, id string) error
}

// Result counts what happened to each subscription in one Send.
type Result struct{ Sent, Removed, Failed int }

// Service sends pushes signed with the server's VAPID keys.
type Service struct {
	keys   Keys
	st     Store
	client *http.Client
}

// New makes a Service. A nil client gets a default one with a timeout, so a
// push service that hangs can't hold a Send forever.
func New(keys Keys, st Store, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Service{keys: keys, st: st, client: client}
}

// PublicKey is the VAPID public key browsers subscribe against.
func (s *Service) PublicKey() string { return s.keys.Public }

func urgency(u string) webpush.Urgency {
	switch u {
	case "low":
		return webpush.UrgencyLow
	case "high":
		return webpush.UrgencyHigh
	default:
		return webpush.UrgencyNormal
	}
}

// Send pushes to every subscription concurrently. A 404/410 from the push
// service means the subscription is gone, so it is deleted and counted in
// Removed. Other failures count in Failed and are logged. The error is only
// for failing to list subscriptions.
func (s *Service) Send(ctx context.Context, m Message) (Result, error) {
	subs, err := s.st.ListPushSubscriptions(ctx)
	if err != nil {
		return Result{}, err
	}
	u := urgency(m.Urgency)
	payload, err := json.Marshal(map[string]string{
		"title":    m.Title,
		"body":     m.Body,
		"threadId": m.ThreadID,
		"urgency":  string(u),
	})
	if err != nil {
		return Result{}, err
	}
	opts := &webpush.Options{
		Subscriber:      subscriber,
		VAPIDPublicKey:  s.keys.Public,
		VAPIDPrivateKey: s.keys.Private,
		TTL:             ttl,
		Urgency:         u,
		HTTPClient:      s.client,
	}

	var (
		mu  sync.Mutex
		res Result
		wg  sync.WaitGroup
	)
	count := func(f func(*Result)) {
		mu.Lock()
		f(&res)
		mu.Unlock()
	}
	for _, sub := range subs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each send gets its own copy: the library pads by appending to
			// the slice it is given, which would race on a shared one.
			resp, err := webpush.SendNotificationWithContext(ctx, bytes.Clone(payload), &webpush.Subscription{
				Endpoint: sub.Endpoint,
				Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
			}, opts)
			if err != nil {
				log.Printf("push to %s (%s) failed: %v", sub.ID, sub.Label, err)
				count(func(r *Result) { r.Failed++ })
				return
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			switch {
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				count(func(r *Result) { r.Sent++ })
			case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
				if err := s.st.DeletePushSubscription(ctx, sub.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
					log.Printf("push subscription %s (%s) is gone but deleting it failed: %v", sub.ID, sub.Label, err)
					count(func(r *Result) { r.Failed++ })
					return
				}
				count(func(r *Result) { r.Removed++ })
			default:
				log.Printf("push to %s (%s) failed: %s %s", sub.ID, sub.Label, resp.Status, body)
				count(func(r *Result) { r.Failed++ })
			}
		}()
	}
	wg.Wait()
	return res, nil
}

var _ Store = (*store.Store)(nil)
