package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/push"
	"github.com/asiraky/omniplex/internal/session"
)

// Notifications.
//
// The rule is about attention, not about events. A session moving from
// "working" to one of the states that wants a human is the only thing worth
// interrupting somebody for, and internal/projection already derives exactly
// that — so this watches transitions of projection.Attention rather than
// trying to recognise the shape of a turn from the event stream.
//
// Where a notification goes is decided globally, across every paired device,
// because there is one user:
//
//   - Somebody is looking at this very session — a connection that is both
//     attached to it and visible. Nothing is sent. They watched it happen.
//   - Somebody is looking at the app, but at something else. Every visible
//     connection gets an in-app toast and no push is sent anywhere. The user
//     is at a screen; a phone buzzing in their pocket at the same moment is
//     noise, not news.
//   - Nobody is looking at anything. Every subscribed device gets a web push,
//     because we have no idea which one they will pick up.
//
// The middle case is why presence is reported per connection rather than
// inferred from the socket being open: a laptop with the tab open behind an
// editor is not "looking at the app", and treating it as such is how the
// notification you actually wanted gets swallowed.

// notifiable reports whether an attention transition is worth telling somebody
// about, and with what urgency.
//
// Only transitions out of work-in-progress count. Arriving at needs_prompt
// from an already-idle state is bookkeeping — a session being adopted, a list
// refresh — and notifying on it would fire on every restart.
func notifiable(prev, next, prevPhase string) (kind string, urgency push.Urgency, ok bool) {
	switch next {
	case projection.AttentionNeedsPermission:
		// Blocking: the harness has stopped and will not move until answered.
		// Worth waking a radio for.
		return "needs_permission", push.UrgencyHigh, true
	case projection.AttentionNeedsAnswer:
		return "needs_answer", push.UrgencyHigh, true
	case projection.AttentionNeedsPrompt:
		// The turn finished. Only interesting if something was actually
		// running: idle → idle is not news, and neither is a session that was
		// waiting on a permission the user just answered on another device.
		switch prev {
		case projection.AttentionBackground:
			return "turn_finished", push.UrgencyNormal, true
		case projection.AttentionWorking:
			// "Working" is also what provisioning and cleaning a workspace
			// look like, and a workspace going ready is not a turn finishing:
			// it is a session the user just opened becoming usable, which
			// they are already watching happen. Only a real turn counts.
			if prevPhase == "turn" {
				return "turn_finished", push.UrgencyNormal, true
			}
			return "", "", false
		}
		return "", "", false
	default:
		return "", "", false
	}
}

// notification is one thing to tell the user, already rendered.
type notification struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// bodyFor is the one line the user reads. It says what is wanted of them,
// because "Omniplex" and a session name do not distinguish "come and look" from
// "answer this before anything else happens".
func bodyFor(kind string) string {
	switch kind {
	case "needs_permission":
		return "Waiting for permission"
	case "needs_answer":
		return "Asked you a question"
	case "turn_finished":
		return "Finished — your turn"
	default:
		return ""
	}
}

// sessionTitle is what the notification is headed with. A session that has not
// been titled yet still has to be identifiable, so it falls back to the same
// wording the UI uses rather than to an id.
func (s *Server) sessionTitle(ctx context.Context, sessionID string) string {
	meta, err := s.store.Session(ctx, sessionID)
	if err != nil {
		return "Untitled session"
	}
	if title := strings.TrimSpace(meta.Title); title != "" {
		return title
	}
	return "Untitled session"
}

// handleAttention is the manager's attention watcher. It runs on its own
// goroutine per transition — see session.Manager.OnAttention — so it may block
// on the network.
func (s *Server) handleAttention(change session.AttentionChange) {
	kind, urgency, ok := notifiable(change.Prev, change.Next, change.PrevPhase)
	if !ok {
		return
	}

	// A short context: this is a courtesy, and a push service that is not
	// answering must not hold a goroutine open indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Watching it happen is the strongest signal there is; nothing else is
	// checked once it holds.
	if s.watchingSession(change.SessionID) {
		return
	}

	note := notification{
		Kind:      kind,
		Title:     s.sessionTitle(ctx, change.SessionID),
		Body:      bodyFor(kind),
		SessionID: change.SessionID,
	}

	// Somebody is at a screen: tell them there, and stop. Pushing as well
	// would buzz a pocket for something already on the monitor in front of
	// them.
	if s.notifyVisible(note) > 0 {
		return
	}

	s.pushAll(ctx, note, urgency)
}

// handleNotice is the manager's notice watcher — the other way something
// becomes news. Attention cannot carry this one: a session that resumed itself
// when a usage limit lifted has gone *into* working, and nothing about "it is
// busy" wants a human. It is still the thing they would want to know at 3am,
// because the last they heard, the work had stopped.
//
// Delivery is the same three-way choice handleAttention makes, and for the
// same reasons; only the decision about what is worth saying differs, and the
// session layer has already made that by sending the notice at all.
func (s *Server) handleNotice(n session.Notice) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if s.watchingSession(n.SessionID) {
		return
	}
	note := notification{
		Kind:      n.Kind,
		Title:     s.sessionTitle(ctx, n.SessionID),
		Body:      n.Body,
		SessionID: n.SessionID,
	}
	if s.notifyVisible(note) > 0 {
		return
	}
	// Normal urgency: the session is working again, which is good news rather
	// than something anyone has to answer. It should be waiting on the phone
	// when it is next picked up, not waking it.
	s.pushAll(ctx, note, push.UrgencyNormal)
}

// pushAll delivers to every subscribed device. There is one user and no way to
// know which device they will pick up, so all of them are told; the tag on the
// message keeps a session that finishes twice from stacking up.
func (s *Server) pushAll(ctx context.Context, note notification, urgency push.Urgency) {
	sender := s.pushSender()
	if sender == nil {
		return
	}
	subs, err := s.store.ListSubscriptions(ctx)
	if err != nil {
		s.logf("push: list subscriptions: %v", err)
		return
	}

	msg := push.Message{
		Kind:      note.Kind,
		Title:     note.Title,
		Body:      note.Body,
		SessionID: note.SessionID,
		// Tagged by session, so three turns finishing while the phone is in a
		// pocket leave one notification saying the latest thing rather than a
		// stack saying the same thing three times.
		Tag: "session:" + note.SessionID,
		// A blocked turn re-buzzes; a finished one quietly replaces.
		Renotify: urgency == push.UrgencyHigh,
		SentAt:   time.Now().UnixMilli(),
	}

	for _, sub := range subs {
		err := sender.Send(ctx, push.Subscription{
			Endpoint: sub.Endpoint,
			P256dh:   sub.P256dh,
			Auth:     sub.Auth,
		}, msg, urgency)

		switch {
		case err == nil:
		case errors.Is(err, push.ErrGone):
			// The browser dropped this subscription without telling us —
			// uninstalled, cleared its data, or the push service retired the
			// endpoint. It will never work again, so it goes now rather than
			// being retried on every future turn.
			if err := s.store.DeleteSubscription(ctx, sub.Endpoint); err != nil {
				s.logf("push: prune %s: %v", endpointHost(sub.Endpoint), err)
			}
		default:
			// Transient. Not retried: the next attention change produces
			// fresher news than the one that just failed.
			s.logf("push: %s: %v", endpointHost(sub.Endpoint), err)
		}
	}
}

// endpointHost trims a push endpoint to its host for logging. The full URL is
// a bearer credential — anyone holding it can send this device notifications —
// so it never reaches a log file.
func endpointHost(endpoint string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}
