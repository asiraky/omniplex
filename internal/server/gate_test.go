package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/store"
)

// testServer builds a server whose guard treats requests as remote, so the
// gate is exercised. httptest dials over loopback, which the guard trusts, so
// requests are rewritten to look remote before they reach the handler.
func testServer(t *testing.T) (http.Handler, *auth.Guard) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	guard := auth.New(st, true)
	mgr := session.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)

	srv := New(Options{Manager: mgr, Store: st, Guard: guard, DefaultCwd: t.TempDir()})
	return srv.Handler(), guard
}

// asRemote rewrites the peer address so the guard does not treat the test's
// own loopback connection as local.
func asRemote(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "10.25.170.99:52000"
		h.ServeHTTP(w, r)
	})
}

func TestPrivateRoutesRefuseUnpairedDevices(t *testing.T) {
	handler, _ := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	for _, path := range []string{"/api/sessions", "/api/harnesses", "/api/fs", "/api/devices"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s from an unpaired device returned %d, want 401", path, res.StatusCode)
		}
	}
}

func TestPublicRoutesStayReachable(t *testing.T) {
	handler, _ := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	for _, path := range []string{"/pair", "/api/health"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s returned %d; an unpaired device must be able to reach it", path, res.StatusCode)
		}
	}
}

// The upgrade must be refused before it becomes a socket, so an unpaired
// client never gets a channel it can send commands on.
func TestWebSocketUpgradeRefusedWithoutPairing(t *testing.T) {
	handler, _ := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, res, err := websocket.Dial(ctx, wsURL+"/ws", nil)
	if err == nil {
		conn.Close(websocket.StatusInternalError, "should not have connected")
		t.Fatal("an unpaired device completed a WebSocket upgrade")
	}
	if res != nil && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("upgrade rejected with %d, want 401", res.StatusCode)
	}
}

func TestPairingGrantsAccess(t *testing.T) {
	handler, guard := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	code, err := guard.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"code": auth.FormatCode(code), "label": "iPhone test"})
	res, err := client.Post(ts.URL+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pairing returned %d, want 200", res.StatusCode)
	}

	// The same client, now holding the cookie, reaches a private route.
	res2, err := client.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("a paired device got %d on /api/sessions, want 200", res2.StatusCode)
	}

	// The code cannot be used twice.
	res3, err := client.Post(ts.URL+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	if res3.StatusCode == http.StatusOK {
		t.Fatal("a pairing code was redeemed twice")
	}
}

// A browser navigating is sent to the pairing page rather than shown JSON.
func TestBrowserNavigationRedirectsToPairing(t *testing.T) {
	handler, _ := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusFound {
		t.Fatalf("navigation returned %d, want a redirect", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/pair" {
		t.Fatalf("redirected to %q, want /pair", loc)
	}
}

// The pairing page must never carry the code in a place the server sees.
func TestPairPageKeepsCodeOutOfRequests(t *testing.T) {
	handler, _ := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/pair")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "location.hash") {
		t.Error("the pairing page must read the code from the fragment")
	}
	if strings.Contains(string(page), "location.search") {
		t.Error("the pairing page must not read the code from the query string, which reaches server logs")
	}
	if res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Error("the pairing page must suppress the Referer header")
	}
}

// Revoking a device must cut the connection it already holds. Authorisation is
// checked once, at upgrade, so a socket otherwise outlives the credential that
// opened it: a user revoking a stolen phone would keep serving that phone
// until it chose to reconnect, which it need never do.
func TestRevokingADeviceClosesItsWebSocket(t *testing.T) {
	handler, guard := testServer(t)
	ts := httptest.NewServer(asRemote(handler))
	defer ts.Close()

	code, err := guard.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"code": auth.FormatCode(code), "label": "stolen phone"})
	res, err := client.Post(ts.URL+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var paired struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.NewDecoder(res.Body).Decode(&paired); err != nil {
		t.Fatal(err)
	}
	if paired.Device.ID == "" {
		t.Fatal("pairing returned no device id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.Dial(ctx, wsURL+"/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("a paired device could not open a socket: %v", err)
	}
	defer conn.CloseNow()

	// The socket is live: a hello is answered.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","protocolVersion":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("the socket was not usable before revocation: %v", err)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/devices/"+paired.Device.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer revoked.Body.Close()
	if revoked.StatusCode != http.StatusOK {
		t.Fatalf("revoke returned %d, want 200", revoked.StatusCode)
	}

	// The read must now fail because the server closed the connection, not
	// because the test gave up waiting: those are different outcomes and only
	// one of them is the fix working.
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	for {
		_, _, err := conn.Read(readCtx)
		if err == nil {
			continue // a frame that was already queued; keep reading
		}
		if readCtx.Err() != nil {
			t.Fatal("the socket stayed open after its device was revoked")
		}
		return // the server closed it, as required
	}
}

// The health probe is the one unauthenticated window into a running server, and
// the commit it can carry names the exact build. A deploy reads it over
// loopback to tell a real restart from one that left the old binary running;
// nobody else has any business knowing. If this test starts failing because the
// commit leaked to an unpaired caller, the fix is the handler, not the test.
func TestHealthWithholdsCommitFromUnpairedDevices(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	guard := auth.New(st, true)
	mgr := session.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)

	handler := New(Options{
		Manager:    mgr,
		Store:      st,
		Guard:      guard,
		DefaultCwd: t.TempDir(),
		Commit:     "deadbeef",
	}).Handler()

	read := func(h http.Handler) map[string]any {
		ts := httptest.NewServer(h)
		defer ts.Close()
		res, err := http.Get(ts.URL + "/api/health")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	if got := read(asRemote(handler))["commit"]; got != nil {
		t.Errorf("an unpaired device saw commit %v; the probe must stay mute", got)
	}
	// Loopback is how the deploy asks, and it must get a straight answer or it
	// cannot verify anything.
	if got := read(handler)["commit"]; got != "deadbeef" {
		t.Errorf("a local request saw commit %v, want deadbeef", got)
	}
}
