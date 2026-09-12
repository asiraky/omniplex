// Package push delivers Web Push messages to subscribed browsers.
//
// It knows nothing about sessions or attention. It holds the VAPID identity
// this server signs with, encrypts a payload to one subscription's keys, and
// reports whether the subscription is still worth keeping. Deciding *what* is
// worth a notification, and which device should get one rather than a toast,
// is the server's job — see internal/server/notify.go.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Subscription is one browser's push endpoint and the keys a payload for it
// must be encrypted to.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Keys is the VAPID keypair, base64url-encoded as the browser expects to
// receive the public half.
type Keys struct {
	Public  string
	Private string
}

// GenerateKeys mints a fresh VAPID keypair.
func GenerateKeys() (Keys, error) {
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return Keys{}, fmt.Errorf("generate VAPID keys: %w", err)
	}
	return Keys{Public: public, Private: private}, nil
}

// Message is what a browser's service worker receives. It is deliberately
// small: a push payload crosses a third-party service, and everything here is
// readable by the device that receives it, so it carries what a notification
// needs to render and an id to look the rest up with — never transcript text
// beyond the one line being shown.
type Message struct {
	// Kind names the event so the service worker and the UI can treat
	// families of notification differently without parsing the body.
	Kind string `json:"kind"`
	// Title and Body are shown as written.
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// SessionID is what a tap opens.
	SessionID string `json:"sessionId,omitempty"`
	// Tag collapses successive notifications about the same thing, so a
	// session that finishes three turns while the phone is away leaves one
	// notification rather than three.
	Tag string `json:"tag,omitempty"`
	// Renotify buzzes again when a notification replaces one with the same
	// tag. Off for chatty kinds, on for the ones that block a turn.
	Renotify bool `json:"renotify,omitempty"`
	// SentAt lets a service worker drop a message that a push service sat on
	// for hours, rather than announcing stale news.
	SentAt int64 `json:"sentAt"`
}

// Urgency maps to the Web Push urgency header, which is what tells a phone's
// push service whether this is worth waking the radio for.
type Urgency string

const (
	UrgencyNormal Urgency = "normal"
	UrgencyHigh   Urgency = "high"
)

// Sender posts encrypted payloads to push services.
type Sender struct {
	keys    Keys
	subject string
	client  *http.Client
	// ttl is how long a push service should hold an undelivered message. A
	// day is long enough for a phone that was off overnight and short enough
	// that nobody is told about a turn from last week.
	ttl int
}

// NewSender builds a sender. The subject is the VAPID `sub` claim: a mailto:
// or https: URL identifying whoever operates this server, which push services
// require and use to contact an operator whose server misbehaves.
func NewSender(keys Keys, subject string) *Sender {
	return &Sender{
		keys:    keys,
		subject: subject,
		client:  &http.Client{Timeout: 15 * time.Second},
		ttl:     86400,
	}
}

// ErrGone reports a subscription the push service has retired. The caller's
// only correct response is to delete it: it will never work again, and
// retrying it forever is how a notification backlog turns into a hot loop.
var ErrGone = errors.New("push subscription is gone")

// Send delivers one message to one subscription.
//
// A failure that is not ErrGone is transient — the push service is down, the
// network is out — and is reported so the caller can log it. It is never
// retried here: the next attention change will produce a fresher notification
// than the one that failed, and re-sending stale news is worse than dropping
// it.
func (s *Sender) Send(ctx context.Context, sub Subscription, msg Message, urgency Urgency) error {
	if s == nil {
		return errors.New("push is not configured")
	}
	if msg.SentAt == 0 {
		msg.SentAt = time.Now().UnixMilli()
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.keys.Public,
		VAPIDPrivateKey: s.keys.Private,
		TTL:             s.ttl,
		Urgency:         webpush.Urgency(urgency),
		// RecordSize is left at the library default: some push services
		// reject the larger records, and nothing sent here is big enough to
		// benefit from them.
	})
	if err != nil {
		return fmt.Errorf("send push: %w", err)
	}
	defer resp.Body.Close()
	// The body is drained so the connection can be reused; push services send
	// a short error document that is only useful in a log.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	default:
		return fmt.Errorf("push service answered %d: %s", resp.StatusCode,
			bytes.TrimSpace(body))
	}
}

// PublicKey is what a browser needs to subscribe.
func (s *Sender) PublicKey() string {
	if s == nil {
		return ""
	}
	return s.keys.Public
}
