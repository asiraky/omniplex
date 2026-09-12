package server

import (
	"context"
	"testing"
	"time"
)

// A connection as the presence rules see one: registered as live, with a
// visibility the browser reported and a set of sessions it is streaming. No
// socket, because none of what is under test writes to one.
//
// Visibility goes through setVisible so these connections hold a real,
// unexpired lease rather than a hand-set field.
func liveConn(s *Server, visible bool, attachedTo ...string) *conn {
	c := &conn{
		srv:      s,
		ctx:      context.Background(),
		attached: map[string]context.CancelFunc{},
	}
	c.setVisible(visible)
	for _, id := range attachedTo {
		c.attached[id] = func() {}
	}
	s.register(c)
	return c
}

func presenceServer() *Server {
	return &Server{live: map[*conn]struct{}{}, logf: func(string, ...any) {}}
}

// Nothing is sent when somebody is watching the session it happened in — the
// one case where a notification would be telling a user what they just saw.
func TestWatchingSession(t *testing.T) {
	t.Run("visible and attached is somebody watching", func(t *testing.T) {
		s := presenceServer()
		liveConn(s, true, "session-a")

		if !s.watchingSession("session-a") {
			t.Error("a visible connection attached to the session was not counted as watching")
		}
	})

	t.Run("attached but hidden is not watching", func(t *testing.T) {
		s := presenceServer()
		liveConn(s, false, "session-a")

		// The tab is open behind an editor. This is the case the whole
		// feature exists for: an open socket is not a pair of eyes.
		if s.watchingSession("session-a") {
			t.Error("a hidden tab was treated as somebody watching, so no notification would be sent")
		}
	})

	t.Run("visible but on another session is not watching this one", func(t *testing.T) {
		s := presenceServer()
		liveConn(s, true, "session-b")

		if s.watchingSession("session-a") {
			t.Error("a device looking at a different session suppressed session-a's notification")
		}
	})

	t.Run("one watcher among several devices is enough", func(t *testing.T) {
		s := presenceServer()
		liveConn(s, false, "session-a") // phone, pocketed
		liveConn(s, true, "session-b")  // laptop, elsewhere in the app
		liveConn(s, true, "session-a")  // desktop, actually looking

		if !s.watchingSession("session-a") {
			t.Error("a watching device was missed because other devices were not watching")
		}
	})

	t.Run("nothing connected at all", func(t *testing.T) {
		s := presenceServer()

		if s.watchingSession("session-a") {
			t.Error("an empty server claimed somebody was watching")
		}
	})

	t.Run("a connection that has said nothing about presence is not watching", func(t *testing.T) {
		s := presenceServer()
		c := &conn{srv: s, ctx: context.Background(), attached: map[string]context.CancelFunc{}}
		c.attached["session-a"] = func() {}
		s.register(c)

		// Presence defaults to false deliberately: a socket opens before the
		// client says anything, and guessing "visible" there would swallow a
		// notification. A missed notification is the worse failure.
		if s.watchingSession("session-a") {
			t.Error("a connection with no reported presence was assumed visible")
		}
	})

	t.Run("a disconnected device stops counting", func(t *testing.T) {
		s := presenceServer()
		c := liveConn(s, true, "session-a")
		s.unregister(c)

		if s.watchingSession("session-a") {
			t.Error("a closed connection still suppressed notifications")
		}
	})
}

// Presence is per connection and mutable: a phone going into a pocket has to
// change the answer without reconnecting.
func TestVisibilityFollowsTheClient(t *testing.T) {
	s := presenceServer()
	c := liveConn(s, true, "session-a")

	if !s.watchingSession("session-a") {
		t.Fatal("setup: the connection should start as watching")
	}

	c.setVisible(false)

	if s.watchingSession("session-a") {
		t.Error("the session was still considered watched after the client reported itself hidden")
	}

	c.setVisible(true)

	if !s.watchingSession("session-a") {
		t.Error("coming back to the foreground did not restore watching")
	}
}

// The case a flag could not express: a client that stops renewing stops
// counting, without having said anything. This is the locked iPhone — iOS
// suspends the app, so no presence update and no close frame ever arrive.
func TestPresenceLeaseExpires(t *testing.T) {
	s := presenceServer()
	c := liveConn(s, true, "session-a")

	if !s.watchingSession("session-a") {
		t.Fatal("setup: a freshly reported visible connection should be watching")
	}

	// The lease it took on the way in, now in the past.
	c.pmu.Lock()
	c.visibleUntil = time.Now().Add(-time.Second)
	c.pmu.Unlock()

	if c.isVisible() {
		t.Error("an expired lease still reported the connection as visible")
	}
	if s.watchingSession("session-a") {
		t.Error("a client that stopped renewing still suppressed the notification")
	}
	if n := s.notifyVisible(notification{Title: "t"}); n != 0 {
		t.Errorf("an expired lease was still sent an in-app notification (%d sent)", n)
	}

	// A renewal from a client that woke up puts it back.
	c.setVisible(true)
	if !s.watchingSession("session-a") {
		t.Error("renewing the lease did not restore watching")
	}
}

func TestIsAttached(t *testing.T) {
	s := presenceServer()
	c := liveConn(s, true, "session-a", "session-b")

	if !c.isAttached("session-a") || !c.isAttached("session-b") {
		t.Error("an attached session was not reported as attached")
	}
	if c.isAttached("session-c") {
		t.Error("an unattached session was reported as attached")
	}
}
