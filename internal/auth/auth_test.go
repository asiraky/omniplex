package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/store"
)

func testGuard(t *testing.T, reachable bool) (*Guard, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, reachable, DefaultPort), st
}

func TestPolicyFollowsReachability(t *testing.T) {
	g, _ := testGuard(t, false)
	if g.Policy() != PolicyLoopback {
		t.Fatalf("policy = %q; a loopback-only bind must not require tokens", g.Policy())
	}
	g, _ = testGuard(t, true)
	if g.Policy() != PolicyReachable {
		t.Fatalf("policy = %q; a reachable bind must require tokens", g.Policy())
	}
}

// A code containing L must survive normalisation. L is in the alphabet, and an
// earlier version mapped it to 1, which silently broke about two in five
// codes.
func TestNormaliseKeepsLIntact(t *testing.T) {
	code := "LLLLMNPQRSTUVWXY" // 16 chars, all in the alphabet
	if got := normaliseCode(code); got != code {
		t.Fatalf("normaliseCode(%q) = %q; L is a valid character and must not be rewritten", code, got)
	}
}

func TestNormaliseCode(t *testing.T) {
	valid := "ABCDEFGHJKLMNPQR"

	cases := []struct {
		name, in, want string
	}{
		{"exact", valid, valid},
		{"lowercase", strings.ToLower(valid), valid},
		{"hyphenated as displayed", FormatCode(valid), valid},
		{"spaces", "ABCD EFGH JKLM NPQR", valid},
		{"surrounding whitespace", "  " + valid + "\t", valid},
		{"too short", "ABCD", ""},
		{"too long", valid + "AB", ""},
		{"contains excluded I", "IBCDEFGHJKLMNPQR", ""},
		{"contains excluded O", "OBCDEFGHJKLMNPQR", ""},
		{"contains excluded 0", "0BCDEFGHJKLMNPQR", ""},
		{"contains excluded 1", "1BCDEFGHJKLMNPQR", ""},
		{"empty", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normaliseCode(c.in); got != c.want {
				t.Fatalf("normaliseCode(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestGeneratedCodesNormaliseToThemselves(t *testing.T) {
	g, _ := testGuard(t, true)
	for i := 0; i < 200; i++ {
		code, err := g.NewPairingCode(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != pairingCodeLen {
			t.Fatalf("code %q has length %d, want %d", code, len(code), pairingCodeLen)
		}
		if got := normaliseCode(code); got != code {
			t.Fatalf("generated code %q does not survive normalisation (got %q)", code, got)
		}
		// The displayed form must round-trip too, since that is what a human
		// reads off the screen and types.
		if got := normaliseCode(FormatCode(code)); got != code {
			t.Fatalf("displayed code %q does not normalise back to %q (got %q)", FormatCode(code), code, got)
		}
	}
}

func TestRedeemIssuesAWorkingToken(t *testing.T) {
	g, _ := testGuard(t, true)
	ctx := context.Background()

	code, err := g.NewPairingCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token, device, err := g.Redeem(ctx, "10.0.0.5", code, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || device.ID == "" {
		t.Fatal("redeem returned an empty token or device")
	}

	r := remoteRequest("/api/sessions")
	r.AddCookie(&http.Cookie{Name: cookiePrefix, Value: token})
	got, ok := g.Authorize(r)
	if !ok {
		t.Fatal("a freshly issued token was refused")
	}
	if got.ID != device.ID {
		t.Fatalf("token authorised as device %q, want %q", got.ID, device.ID)
	}
}

// A pairing code is single use. Two devices racing the same code must not both
// end up paired, or a code shoulder-surfed off a screen would pair an
// onlooker as well as its owner.
func TestPairingCodeIsSingleUseUnderConcurrency(t *testing.T) {
	g, _ := testGuard(t, true)
	ctx := context.Background()

	code, err := g.NewPairingCode(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const racers = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		wins   int
		tokens []string
		start  = make(chan struct{})
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Distinct peers, so the rate limiter cannot be what rejects them.
			token, _, err := g.Redeem(ctx, "10.0.0."+string(rune('1'+i)), code, "racer")
			if err != nil {
				return
			}
			mu.Lock()
			wins++
			tokens = append(tokens, token)
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d of %d racers redeemed one code; exactly 1 must win", wins, racers)
	}

	// And the winner's token really works.
	r := remoteRequest("/api/sessions")
	r.AddCookie(&http.Cookie{Name: cookiePrefix, Value: tokens[0]})
	if _, ok := g.Authorize(r); !ok {
		t.Fatal("the winning token was refused")
	}
}

func TestExpiredCodeIsRefused(t *testing.T) {
	g, st := testGuard(t, true)
	ctx := context.Background()

	code, err := g.NewPairingCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Reach past the guard to age the code, rather than sleeping for the TTL.
	if err := st.CreatePairing(ctx, hashOf(code), time.Now().Add(-time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Redeem(ctx, "10.0.0.5", code, ""); err == nil {
		t.Fatal("an expired code was accepted")
	}
}

func TestRevokedDeviceLosesAccess(t *testing.T) {
	g, _ := testGuard(t, true)
	ctx := context.Background()

	code, _ := g.NewPairingCode(ctx)
	token, device, err := g.Redeem(ctx, "10.0.0.5", code, "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Revoke(ctx, device.ID); err != nil {
		t.Fatal(err)
	}

	r := remoteRequest("/api/sessions")
	r.AddCookie(&http.Cookie{Name: cookiePrefix, Value: token})
	if _, ok := g.Authorize(r); ok {
		t.Fatal("a revoked device still authorised")
	}
}

func TestRateLimitStopsGuessing(t *testing.T) {
	g, _ := testGuard(t, true)
	ctx := context.Background()

	var lastErr error
	for i := 0; i < attemptBudget+5; i++ {
		_, _, lastErr = g.Redeem(ctx, "10.0.0.9", "AAAABBBBCCCCDDDD", "")
	}
	if lastErr != ErrTooManyAttempts {
		t.Fatalf("after %d guesses the error was %v; want ErrTooManyAttempts", attemptBudget+5, lastErr)
	}

	// A different peer is unaffected: the budget is per-caller.
	if _, _, err := g.Redeem(ctx, "10.0.0.10", "AAAABBBBCCCCDDDD", ""); err != ErrBadCode {
		t.Fatalf("a different peer got %v; want ErrBadCode", err)
	}
}

func TestUnauthorisedRemoteIsRefused(t *testing.T) {
	g, _ := testGuard(t, true)

	for _, path := range []string{"/api/sessions", "/ws"} {
		if _, ok := g.Authorize(remoteRequest(path)); ok {
			t.Fatalf("%s was authorised without a token", path)
		}
	}

	// A wrong token is refused just as a missing one is.
	r := remoteRequest("/api/sessions")
	r.AddCookie(&http.Cookie{Name: cookiePrefix, Value: "not-a-real-token"})
	if _, ok := g.Authorize(r); ok {
		t.Fatal("an invented token authorised")
	}
}

func TestLoopbackIsTrusted(t *testing.T) {
	for _, reachable := range []bool{false, true} {
		g, _ := testGuard(t, reachable)
		r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		r.RemoteAddr = "127.0.0.1:51000"
		device, ok := g.Authorize(r)
		if !ok {
			t.Fatalf("reachable=%v: a request from this machine was refused", reachable)
		}
		if device.ID != LocalDevice.ID {
			t.Fatalf("local request attributed to %q", device.ID)
		}
	}

	// IPv6 loopback counts too.
	g, _ := testGuard(t, true)
	r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	r.RemoteAddr = "[::1]:51000"
	if _, ok := g.Authorize(r); !ok {
		t.Fatal("an IPv6 loopback request was refused")
	}
}

// Forwarding headers are attacker-controlled: a remote caller must not be able
// to claim to be local.
func TestForwardedHeadersCannotForgeLocality(t *testing.T) {
	g, _ := testGuard(t, true)

	r := remoteRequest("/api/sessions")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	r.Header.Set("Forwarded", "for=127.0.0.1")
	if _, ok := g.Authorize(r); ok {
		t.Fatal("a spoofed forwarding header granted access")
	}
}

func TestTokenTransports(t *testing.T) {
	g, _ := testGuard(t, true)
	ctx := context.Background()

	code, _ := g.NewPairingCode(ctx)
	token, _, err := g.Redeem(ctx, "10.0.0.5", code, "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("bearer", func(t *testing.T) {
		r := remoteRequest("/api/sessions")
		r.Header.Set("Authorization", "Bearer "+token)
		if _, ok := g.Authorize(r); !ok {
			t.Fatal("a bearer token was refused")
		}
	})

	t.Run("websocket subprotocol", func(t *testing.T) {
		r := remoteRequest("/ws")
		r.Header.Set("Sec-WebSocket-Protocol", "omniplex.sync, omniplex.token."+token)
		if _, ok := g.Authorize(r); !ok {
			t.Fatal("a subprotocol token was refused")
		}
		proto, ok := TokenSubprotocol(r)
		if !ok || proto != "omniplex.token."+token {
			t.Fatalf("TokenSubprotocol = %q, %v; the server must echo the offered protocol", proto, ok)
		}
	})
}

func TestCookieIsNotSecureOverPlainHTTP(t *testing.T) {
	// Over http:// — a LAN or overlay address — a Secure cookie would never
	// be sent back, locking the device out of the session it just created.
	w := httptest.NewRecorder()
	r := remoteRequest("/api/pair")
	New(nil, true, DefaultPort).SetCookie(w, r, "token-value")

	cookie := w.Result().Cookies()[0]
	if cookie.Secure {
		t.Fatal("cookie marked Secure on a plain-HTTP request; the browser would never return it")
	}
	if !cookie.HttpOnly {
		t.Fatal("cookie must be HttpOnly so page scripts cannot read the token")
	}
}

func remoteRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "10.25.170.99:52000"
	return r
}

// The critical finding from the adversarial review: `tailscale serve` proxies
// the tailnet to http://127.0.0.1:<port>, so every remote request arrives with
// a loopback peer address. Trusting locality alone therefore handed any member
// of the tailnet full control with no pairing at all.
func TestProxiedLoopbackIsNotTrusted(t *testing.T) {
	g, _ := testGuard(t, true)

	local := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	local.RemoteAddr = "127.0.0.1:54321"

	if _, ok := g.Authorize(local); !ok {
		t.Fatal("a direct loopback request must be trusted when nothing is proxying")
	}

	g.SetProxied(true)
	if _, ok := g.Authorize(local); ok {
		t.Fatal("while a proxy is publishing us, a loopback peer proves nothing and must be refused")
	}

	// A paired device still gets in through the proxy; only the free pass goes.
	code, err := g.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := g.Redeem(context.Background(), "10.0.0.9", code, "phone")
	if err != nil {
		t.Fatal(err)
	}
	withToken := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	withToken.RemoteAddr = "127.0.0.1:54322"
	withToken.Header.Set("Authorization", "Bearer "+token)
	if _, ok := g.Authorize(withToken); !ok {
		t.Fatal("a paired device must still be authorised through a proxy")
	}

	g.SetProxied(false)
	if _, ok := g.Authorize(local); !ok {
		t.Fatal("turning the proxy off must restore local trust")
	}
}

// The headers Tailscale Serve adds are the second line of defence, covering
// the window between a proxy appearing and the periodic check noticing. They
// can only ever remove trust, never confer it, so honouring them is safe even
// though a caller can set them.
func TestRelayHeadersDefeatLocalTrust(t *testing.T) {
	g, _ := testGuard(t, true)

	for _, h := range []string{
		"Tailscale-User-Login",
		"Tailscale-User-Name",
		"X-Forwarded-For",
		"X-Forwarded-Proto",
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		r.RemoteAddr = "127.0.0.1:54321"
		r.Header.Set(h, "someone@example.com")

		if _, ok := g.Authorize(r); ok {
			t.Fatalf("a loopback request carrying %s was trusted; a relayed request must not be", h)
		}
	}
}

// Spending the code and creating the device have to happen together: a failure
// between them would burn a single-use code and issue nothing, leaving the
// user unable to retry.
func TestRedeemIsAtomic(t *testing.T) {
	g, _ := testGuard(t, true)

	code, err := g.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// A cancelled context fails the transaction; the code must survive.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := g.Redeem(ctx, "10.0.0.1", code, "phone"); err == nil {
		t.Fatal("redeeming with a cancelled context should fail")
	}

	token, _, err := g.Redeem(context.Background(), "10.0.0.2", code, "phone")
	if err != nil {
		t.Fatalf("the code was consumed by the failed attempt: %v", err)
	}
	if token == "" {
		t.Fatal("no token issued")
	}
}

// An attacker holding an IPv6 prefix can present a fresh source address per
// request, so sweeping only expired buckets never reclaims anything. The cap
// is what bounds the memory.
func TestLimiterIsBounded(t *testing.T) {
	l := newLimiter()
	for i := 0; i < maxBuckets*3; i++ {
		l.allow(fmt.Sprintf("2001:db8::%x", i))
	}

	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()

	if n > maxBuckets {
		t.Fatalf("limiter holds %d buckets, want at most %d", n, maxBuckets)
	}
}

// Cookies are scoped by host and never by port, so two instances on one
// machine share a jar. Before this, pairing with a worktree on :8800 overwrote
// the token the instance on :8787 had issued, that instance saw a token it
// never minted and sent the browser to /pair, and pairing there overwrote the
// first one straight back — pairing appearing to fall out at random on a
// machine that runs a dev instance beside a real one.
func TestInstancesOnDifferentPortsDoNotShareACookie(t *testing.T) {
	if got, want := CookieName(DefaultPort), "omniplex_device"; got != want {
		t.Errorf("CookieName(%d) = %q, want %q — the primary instance keeps the "+
			"bare name so nothing has to pair again", DefaultPort, got, want)
	}
	if CookieName(8800) == CookieName(DefaultPort) {
		t.Fatalf("a worktree on :8800 shares %q with the primary instance",
			CookieName(8800))
	}
	if got, want := CookieName(8800), "omniplex_device_8800"; got != want {
		t.Errorf("CookieName(8800) = %q, want %q", got, want)
	}

	// The end of it that actually bites: a guard must ignore another
	// instance's cookie rather than treat it as a token it failed to find.
	primary, st := testGuard(t, true)
	token, _, err := primary.Redeem(context.Background(), "peer", mustCode(t, primary), "laptop")
	if err != nil {
		t.Fatal(err)
	}
	worktree := New(st, true, 8800)

	r := httptest.NewRequest("GET", "/api/sessions", nil)
	r.RemoteAddr = "10.0.0.9:1234"
	r.AddCookie(&http.Cookie{Name: primary.CookieName(), Value: token})
	if _, ok := worktree.Authorize(r); ok {
		t.Error("the :8800 instance accepted the primary instance's cookie")
	}
	if _, ok := primary.Authorize(r); !ok {
		t.Error("the primary instance rejected its own cookie")
	}
}

func mustCode(t *testing.T, g *Guard) string {
	t.Helper()
	code, err := g.NewPairingCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return code
}
