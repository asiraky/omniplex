package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/push"
	"github.com/asiraky/omniplex/internal/store"
)

// Push notifications: devices subscribe here, and an agent's notify tool
// sends to all of them unless the thread is already on someone's screen.

const noPush = "this server does not have push notifications"

// maxSubscriptionBody bounds a subscribe request. A real one is a few hundred
// bytes.
const maxSubscriptionBody = 16 << 10

// Limits on what notify sends. A Web Push payload must fit in 4 KB once
// encrypted, and a phone shows a few lines; these keep the payload well
// inside that so the push service never refuses it.
const (
	maxNotifyTitle = 100
	maxNotifyBody  = 500
	maxThreadTitle = 100
	maxLabel       = 100
)

func (s *Server) routePush(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/push", s.handlePushList)
	mux.HandleFunc("POST /api/push/subscriptions", s.handlePushSubscribe)
	mux.HandleFunc("DELETE /api/push/subscriptions/{id}", s.handlePushUnsubscribe)
	mux.HandleFunc("POST /api/push/test", s.handlePushTest)
}

func (s *Server) pushOff(w http.ResponseWriter) bool {
	if s.push == nil {
		writeError(w, http.StatusNotImplemented, noPush)
		return true
	}
	return false
}

func (s *Server) handlePushList(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	subs, err := s.store.ListPushSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"publicKey": s.push.PublicKey(), "subscriptions": subs})
}

// handlePushSubscribe takes what the browser's PushSubscription.toJSON()
// gives, plus a label for the list in Settings.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSubscriptionBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "the subscription is not valid JSON")
		return
	}
	// The server will POST to this URL, so it has to be a push service's:
	// every browser's is https.
	if u, err := url.Parse(in.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" {
		writeError(w, http.StatusBadRequest, "endpoint must be an https URL")
		return
	}
	if in.Keys.P256dh == "" || in.Keys.Auth == "" {
		writeError(w, http.StatusBadRequest, "keys.p256dh and keys.auth are required")
		return
	}
	// The loopback pseudo-device is not a paired device; it is recorded as
	// none.
	deviceID := ""
	if d, ok := auth.DeviceFrom(r.Context()); ok && d.ID != auth.LocalDevice.ID {
		deviceID = d.ID
	}
	id, err := s.store.SavePushSubscription(r.Context(), store.PushSubscription{
		Endpoint: in.Endpoint,
		P256dh:   in.Keys.P256dh,
		Auth:     in.Keys.Auth,
		Label:    clip(strings.TrimSpace(in.Label), maxLabel),
		DeviceID: deviceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeletePushSubscription(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no such subscription")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"removed": id})
}

// handlePushTest sends to every device, whatever is on screen: the user
// asking for a test is looking at the app.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if s.pushOff(w) {
		return
	}
	res, err := s.push.Send(r.Context(), push.Message{Title: "Omniplex", Body: "Test notification"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]int{"sent": res.Sent, "removed": res.Removed, "failed": res.Failed})
}

// ---- presence ----

// setPresence records what a connection's client reports it is showing.
// It is called on the read loop, so it only takes a lock.
func (s *Server) setPresence(c *conn, visible bool, threadID string) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	c.visible, c.viewing = visible, threadID
}

// watched reports whether any live connection has the thread open on a
// visible page. A connection's presence goes with it when it closes.
func (s *Server) watched(threadID string) bool {
	if threadID == "" {
		return false
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	for c := range s.live {
		if c.visible && c.viewing == threadID {
			return true
		}
	}
	return false
}

// ---- notify tool ----

// notify pushes the agent's message to every subscribed device, unless the
// thread is already on one of their screens.
func (s *Server) notify(ctx context.Context, t toolThread, body []byte) (string, error) {
	if s.push == nil {
		return "", &toolError{code: http.StatusNotImplemented, msg: "Push notifications are not set up on this Omniplex server, so the user cannot be notified."}
	}
	var in struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Urgency string `json:"urgency"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return "", badArgs("the arguments are not valid: %v", err)
	}
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	switch {
	case in.Title == "":
		return "", badArgs("title is required")
	case in.Body == "":
		return "", badArgs("body is required")
	case utf8.RuneCountInString(in.Title) > maxNotifyTitle:
		return "", badArgs("title is longer than %d characters", maxNotifyTitle)
	case utf8.RuneCountInString(in.Body) > maxNotifyBody:
		return "", badArgs("body is longer than %d characters", maxNotifyBody)
	}
	switch in.Urgency {
	case "", "low", "normal", "high":
	default:
		return "", badArgs("urgency must be low, normal or high")
	}

	if s.watched(t.id) {
		return "Not sent: the user has this thread open on a device right now, so they will see your message here.", nil
	}
	msg := in.Body
	if t.title != "" {
		msg += "\n" + clip(t.title, maxThreadTitle)
	}
	res, err := s.push.Send(ctx, push.Message{Title: in.Title, Body: msg, ThreadID: t.id, Urgency: in.Urgency})
	if err != nil {
		return "", err
	}
	if res == (push.Result{}) {
		return "Not sent: no device has notifications turned on. The user can turn them on in Omniplex under Settings > Notifications.", nil
	}
	return notifyReport(res)
}

// notifyReport tells the agent what reached the user. Failing everywhere
// is an error, so the agent knows the user was not told.
func notifyReport(res push.Result) (string, error) {
	var b strings.Builder
	if res.Sent > 0 {
		fmt.Fprintf(&b, "Sent to %s.", devices(res.Sent))
	} else {
		b.WriteString("Not delivered to any device.")
	}
	if res.Removed > 0 {
		fmt.Fprintf(&b, " Removed %s whose subscription had expired.", devices(res.Removed))
	}
	if res.Failed > 0 {
		fmt.Fprintf(&b, " Failed on %s.", devices(res.Failed))
	}
	if res.Sent == 0 && res.Failed > 0 {
		return "", &toolError{code: http.StatusBadGateway, msg: b.String()}
	}
	if res.Sent == 0 {
		b.WriteString(" The user needs to turn notifications on again in Omniplex under Settings > Notifications.")
	}
	return b.String(), nil
}

func devices(n int) string { return fmt.Sprintf("%d %s", n, plural(n, "device", "devices")) }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// clip cuts s to at most n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
