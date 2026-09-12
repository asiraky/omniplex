package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/asiraky/omniplex/internal/push"
	"github.com/asiraky/omniplex/internal/store"
)

// The subscription API.
//
// Everything here is behind the pairing gate, and every route works out which
// device is calling from the same cookie the WebSocket upgrade uses. A
// subscription is therefore never something a caller can register on another
// device's behalf — which matters, because a push endpoint is a capability:
// whoever holds it can send that phone notifications until it is revoked.

// pushSender returns the sender, minting and storing a VAPID keypair the first
// time it is asked for.
//
// Lazy rather than at startup because most runs never send a notification, and
// generating a keypair is not something to do on the boot path of a server
// somebody is waiting on. The pair is generated once and kept forever:
// rotating it silently invalidates every subscription in existence.
func (s *Server) pushSender() *push.Sender {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.push != nil {
		return s.push
	}

	ctx := s.baseCtx()
	public, private, err := s.store.VAPIDKeys(ctx)
	if err != nil {
		s.logf("push: read keys: %v", err)
		return nil
	}
	if public == "" || private == "" {
		keys, err := push.GenerateKeys()
		if err != nil {
			s.logf("push: %v", err)
			return nil
		}
		if err := s.store.SaveVAPIDKeys(ctx, keys.Public, keys.Private); err != nil {
			s.logf("push: save keys: %v", err)
			return nil
		}
		// Re-read rather than trusting what we just generated: the insert does
		// nothing if another start won the race, and signing with the pair
		// that lost would produce notifications no browser could verify.
		public, private, err = s.store.VAPIDKeys(ctx)
		if err != nil || public == "" {
			s.logf("push: re-read keys: %v", err)
			return nil
		}
	}

	// The VAPID subject identifies the operator to the push service. This is
	// a personal server with no contact address to give, so it names the
	// software instead — which is what the field is for when there is no
	// mailto: worth publishing.
	s.push = push.NewSender(push.Keys{Public: public, Private: private},
		"https://github.com/asiraky/omniplex")
	return s.push
}

// handlePushKey hands a browser the VAPID public key it must subscribe with,
// and tells it whether this device already holds a subscription.
//
// The second half is why this is not a static file. A browser can have been
// granted notification permission and still not be subscribed here — the
// database was moved, the device was revoked and re-paired — and permission
// alone cannot tell the difference. The UI needs the server's answer to show
// an honest toggle.
func (s *Server) handlePushKey(w http.ResponseWriter, r *http.Request) {
	deviceID, ok := s.callerDevice(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not paired")
		return
	}

	sender := s.pushSender()
	if sender == nil {
		writeJSON(w, map[string]any{"available": false})
		return
	}

	subs, err := s.store.DeviceSubscriptions(r.Context(), deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	endpoints := make([]string, 0, len(subs))
	for _, sub := range subs {
		endpoints = append(endpoints, sub.Endpoint)
	}

	writeJSON(w, map[string]any{
		"available": true,
		"publicKey": sender.PublicKey(),
		// The endpoints this device has registered. The client compares its
		// own live subscription against these; it never needs to display them.
		"endpoints": endpoints,
	})
}

type subscribeBody struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	deviceID, ok := s.callerDevice(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not paired")
		return
	}

	var body subscribeBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "malformed subscription")
		return
	}
	// A push endpoint is a URL we will later POST to. Refusing anything that
	// is not https keeps a malformed or hostile registration from turning
	// this server into a request generator pointed wherever the caller likes.
	if !strings.HasPrefix(body.Endpoint, "https://") {
		writeError(w, http.StatusBadRequest, "endpoint must be https")
		return
	}
	if body.Keys.P256dh == "" || body.Keys.Auth == "" {
		writeError(w, http.StatusBadRequest, "subscription is missing its keys")
		return
	}

	if err := s.store.SaveSubscription(r.Context(), store.PushSubscription{
		Endpoint: body.Endpoint,
		DeviceID: deviceID,
		P256dh:   body.Keys.P256dh,
		Auth:     body.Keys.Auth,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"subscribed": true})
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	deviceID, ok := s.callerDevice(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not paired")
		return
	}

	var body struct {
		Endpoint string `json:"endpoint"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)

	// An empty endpoint means "this device, whatever it registered". That is
	// the case that matters: a browser turning notifications off may no longer
	// be able to name its own subscription, because unsubscribing in the
	// service worker destroys it before it can tell us.
	var err error
	if body.Endpoint == "" {
		err = s.store.DeleteDeviceSubscriptions(r.Context(), deviceID)
	} else {
		err = s.store.DeleteSubscription(r.Context(), body.Endpoint)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"subscribed": false})
}

// handlePushTest sends this device a notification on demand.
//
// Worth a route of its own: every part of this stack fails silently and
// somewhere else — a service worker that did not activate, a key mismatch, a
// push service refusing the endpoint, iOS not having been added to the home
// screen. Without a button that goes the whole way and reports what happened,
// the only test is to start a session and wait.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	deviceID, ok := s.callerDevice(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not paired")
		return
	}
	sender := s.pushSender()
	if sender == nil {
		writeError(w, http.StatusServiceUnavailable, "push is not configured")
		return
	}

	subs, err := s.store.DeviceSubscriptions(r.Context(), deviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(subs) == 0 {
		writeError(w, http.StatusNotFound, "this device is not subscribed")
		return
	}

	msg := push.Message{
		Kind:  "test",
		Title: "Omniplex",
		Body:  "Notifications are working.",
		Tag:   "omniplex-test",
		// A test the user asked for should always arrive, even if they press
		// the button twice.
		Renotify: true,
	}

	var lastErr error
	delivered := 0
	for _, sub := range subs {
		err := sender.Send(r.Context(), push.Subscription{
			Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth,
		}, msg, push.UrgencyHigh)
		if err == nil {
			delivered++
			continue
		}
		lastErr = err
		if err == push.ErrGone {
			_ = s.store.DeleteSubscription(r.Context(), sub.Endpoint)
		}
	}

	if delivered == 0 {
		message := "the push service refused the message"
		if lastErr != nil {
			message = lastErr.Error()
		}
		writeError(w, http.StatusBadGateway, message)
		return
	}
	writeJSON(w, map[string]any{"delivered": delivered})
}

// callerDevice resolves the device behind a request.
//
// A browser on this machine authorises on locality rather than a pairing and
// comes back as auth.LocalDevice, whose id is a stable sentinel. That is used
// as-is: a desktop browser at the loopback address is a real browser that can
// hold a real subscription, and refusing it would mean notifications only ever
// worked on the phone.
func (s *Server) callerDevice(r *http.Request) (string, bool) {
	device, paired := s.guard.Authorize(r)
	if !paired || device.ID == "" {
		return "", false
	}
	return device.ID, true
}
