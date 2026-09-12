package preview

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testAuth(t *testing.T) *Auth {
	t.Helper()
	a, err := NewAuth()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTicketRoundTrip(t *testing.T) {
	a := testAuth(t)
	ticket := a.Ticket("web-main", "device-1")
	if !a.RedeemTicket(ticket, "web-main") {
		t.Fatal("a fresh ticket should redeem")
	}
}

// A ticket ends up in browser history, in any proxy log between here and the
// phone, and on the screen. Spending it once is what stops that being a way in.
func TestTicketIsSingleUse(t *testing.T) {
	a := testAuth(t)
	ticket := a.Ticket("web-main", "device-1")
	if !a.RedeemTicket(ticket, "web-main") {
		t.Fatal("first redemption should succeed")
	}
	if a.RedeemTicket(ticket, "web-main") {
		t.Error("a ticket must not be redeemable twice")
	}
}

// A ticket for one preview must not open another, or any authorised user
// could reach every service on the machine.
func TestTicketIsBoundToItsPreview(t *testing.T) {
	a := testAuth(t)
	ticket := a.Ticket("web-main", "device-1")
	if a.RedeemTicket(ticket, "api-main") {
		t.Error("a ticket redeemed against the wrong preview must fail")
	}
}

func TestForgedAndTamperedTokensAreRejected(t *testing.T) {
	a := testAuth(t)
	cookie := a.Cookie("web-main", "device-1")

	cases := map[string]string{
		"empty":            "",
		"no signature":     strings.Split(cookie, ".")[0],
		"wrong signature":  strings.Split(cookie, ".")[0] + ".not-a-real-mac",
		"garbage":          "aaaa.bbbb",
		"signed elsewhere": testAuth(t).Cookie("web-main", "device-1"),
	}
	for name, token := range cases {
		if a.CheckCookie(token, "web-main") {
			t.Errorf("%s: must be rejected", name)
		}
	}

	// Swapping the body for another preview's invalidates the signature.
	other := testAuth(t)
	other.key = a.key
	if a.CheckCookie(other.Cookie("api-main", "device-1"), "web-main") {
		t.Error("a cookie for another preview must be rejected")
	}
}

// A ticket is not a cookie and a cookie is not a ticket: the kind is signed
// in, so a long-lived cookie cannot be replayed as an entry ticket and a
// ticket cannot be used directly as a session credential.
func TestCredentialKindsDoNotCross(t *testing.T) {
	a := testAuth(t)
	if a.CheckCookie(a.Ticket("web-main", "d"), "web-main") {
		t.Error("a ticket must not pass as a cookie")
	}
	if a.RedeemTicket(a.Cookie("web-main", "d"), "web-main") {
		t.Error("a cookie must not pass as a ticket")
	}
}

func TestPreviewIDFromHost(t *testing.T) {
	r := NewRouter(NewRegistry(), testAuth(t), "agent.example.net", nil)

	cases := map[string]string{
		"web-main.agent.example.net":      "web-main",
		"web-main.agent.example.net:8788": "web-main",
		"WEB-MAIN.agent.example.net":      "web-main",
		// Not a preview host.
		"agent.example.net": "",
		"example.com":             "",
		// Two labels: no wildcard certificate can cover this, and we never
		// mint it.
		"a.b.agent.example.net": "",
		".agent.example.net":    "",
		// A near-miss suffix must not match.
		"web-main.notagent.example.net": "",
	}
	for host, want := range cases {
		got, ok := r.PreviewID(host)
		if want == "" && ok {
			t.Errorf("PreviewID(%q) = %q, want no match", host, got)
		}
		if want != "" && got != want {
			t.Errorf("PreviewID(%q) = %q, want %q", host, got, want)
		}
	}
}

// With no domain configured there is no wildcard, so a preview hostname would
// not resolve. Routing must be off rather than producing broken links.
func TestNoDomainDisablesRouting(t *testing.T) {
	r := NewRouter(NewRegistry(), testAuth(t), "", nil)
	if _, ok := r.PreviewID("web-main.agent.example.net"); ok {
		t.Error("host routing must be off without a configured domain")
	}
	if url := r.URL("web-main"); url != "" {
		t.Errorf("got url %q, want none", url)
	}
}

// routerWithApp stands up a real app on loopback, registers it as a preview,
// and returns the router in front of it plus the preview's id.
func routerWithApp(t *testing.T, handler http.Handler) (*Router, *Auth, string) {
	t.Helper()

	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	port := app.Listener.Addr().(*net.TCPAddr).Port

	registry, auth := NewRegistry(), testAuth(t)
	registry.Refresh(context.Background(), "s1", t.TempDir(), "main",
		[]Found{{Port: port, Label: "web", Scheme: "http", Source: SourceDeclared}})

	previews := registry.ForSession("s1")
	if len(previews) != 1 {
		t.Fatalf("got %d previews, want 1", len(previews))
	}
	return NewRouter(registry, auth, "agent.example.net", nil), auth, previews[0].ID
}

func previewRequest(id, path string) *http.Request {
	req := httptest.NewRequest("GET", "https://"+id+".agent.example.net"+path, nil)
	req.Host = id + ".agent.example.net"
	return req
}

// The end-to-end path a phone takes: land on the enter URL with a ticket, get
// a cookie, then reach the app with it.
func TestEnterThenProxy(t *testing.T) {
	r, auth, id := routerWithApp(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("the app at " + req.URL.Path))
	}))

	// Without a credential, nothing.
	blocked := httptest.NewRecorder()
	r.ServeHTTP(blocked, previewRequest(id, "/"))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated request: got %d, want 403", blocked.Code)
	}

	// Redeem a ticket for a cookie.
	entered := httptest.NewRecorder()
	r.ServeHTTP(entered, previewRequest(id, EnterPath+"?t="+auth.Ticket(id, "device-1")))
	if entered.Code != http.StatusFound {
		t.Fatalf("enter: got %d, want 302", entered.Code)
	}
	if loc := entered.Header().Get("Location"); loc != "/" {
		t.Errorf("enter redirected to %q, want / (the ticket must not stay in the address bar)", loc)
	}
	cookies := entered.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("enter did not set the preview cookie: %+v", cookies)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Errorf("cookie must be HttpOnly and Secure: %+v", cookies[0])
	}

	// Now the app is reachable.
	got := httptest.NewRecorder()
	req := previewRequest(id, "/some/path")
	req.AddCookie(cookies[0])
	r.ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("proxied request: got %d, want 200", got.Code)
	}
	if body := got.Body.String(); body != "the app at /some/path" {
		t.Errorf("got %q, want the app's own response", body)
	}
}

// A retired preview must be a dead end, not a route to whatever has since
// taken the port. This is the property that keeps the proxy from being an
// open one.
func TestRetiredPreviewIsNotProxied(t *testing.T) {
	r, auth, id := routerWithApp(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("still here"))
	}))

	entered := httptest.NewRecorder()
	r.ServeHTTP(entered, previewRequest(id, EnterPath+"?t="+auth.Ticket(id, "device-1")))
	cookie := entered.Result().Cookies()[0]

	r.registry.Forget("s1")

	got := httptest.NewRecorder()
	req := previewRequest(id, "/")
	req.AddCookie(cookie)
	r.ServeHTTP(got, req)
	if got.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 for a retired preview even with a valid cookie", got.Code)
	}
}

// A cookie for one preview must not open another.
func TestCookieDoesNotCrossPreviews(t *testing.T) {
	r, auth, id := routerWithApp(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))

	// A second service, registered so it resolves.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("the other app"))
	}))
	t.Cleanup(other.Close)
	r.registry.Refresh(context.Background(), "s2", t.TempDir(), "other", []Found{{
		Port: other.Listener.Addr().(*net.TCPAddr).Port, Label: "web", Scheme: "http", Source: SourceDeclared,
	}})
	otherID := r.registry.ForSession("s2")[0].ID

	entered := httptest.NewRecorder()
	r.ServeHTTP(entered, previewRequest(id, EnterPath+"?t="+auth.Ticket(id, "device-1")))
	cookie := entered.Result().Cookies()[0]

	got := httptest.NewRecorder()
	req := previewRequest(otherID, "/")
	req.AddCookie(cookie)
	r.ServeHTTP(got, req)
	if got.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403: one preview's cookie must not open another", got.Code)
	}
}

// The app must never see our credential.
func TestPreviewCookieIsNotForwarded(t *testing.T) {
	var seen *http.Request
	r, auth, id := routerWithApp(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = req
	}))

	entered := httptest.NewRecorder()
	r.ServeHTTP(entered, previewRequest(id, EnterPath+"?t="+auth.Ticket(id, "device-1")))
	cookie := entered.Result().Cookies()[0]

	req := previewRequest(id, "/")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: "app_session", Value: "keep-me"})
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("the app was never reached")
	}
	if _, err := seen.Cookie(cookieName); err == nil {
		t.Error("the preview credential must be stripped before the app sees it")
	}
	if c, err := seen.Cookie("app_session"); err != nil || c.Value != "keep-me" {
		t.Error("the app's own cookies must be passed through")
	}
}

// A redirect written against the host we handed the app points at
// 127.0.0.1, which the browser cannot follow. Frameworks do this constantly —
// an absolute Location built from the request's own Host is the normal output
// of a login redirect.
func TestRedirectsArePointedBackAtThePublicOrigin(t *testing.T) {
	r, auth, id := routerWithApp(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Absolute, built from the Host we handed it: this is the case that
		// breaks without rewriting.
		http.Redirect(w, req, "http://"+req.Host+"/login", http.StatusFound)
	}))

	entered := httptest.NewRecorder()
	r.ServeHTTP(entered, previewRequest(id, EnterPath+"?t="+auth.Ticket(id, "device-1")))
	cookie := entered.Result().Cookies()[0]

	got := httptest.NewRecorder()
	req := previewRequest(id, "/")
	req.AddCookie(cookie)
	r.ServeHTTP(got, req)

	location := got.Header().Get("Location")
	if strings.Contains(location, "127.0.0.1") {
		t.Fatalf("Location %q still points at loopback: the browser cannot follow it", location)
	}
	if location != "https://"+id+".agent.example.net/login" {
		t.Errorf("Location = %q, want the public origin's /login", location)
	}
}

// The forwarded headers must carry the truth even though Host does not, or a
// framework that builds absolute URLs from them gets loopback.
func TestForwardedHeadersCarryThePublicHost(t *testing.T) {
	var seen *http.Request
	r, auth, id := routerWithApp(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = req
	}))

	entered := httptest.NewRecorder()
	r.ServeHTTP(entered, previewRequest(id, EnterPath+"?t="+auth.Ticket(id, "device-1")))
	cookie := entered.Result().Cookies()[0]

	req := previewRequest(id, "/")
	req.AddCookie(cookie)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("the app was never reached")
	}
	if got := seen.Header.Get("X-Forwarded-Host"); got != id+".agent.example.net" {
		t.Errorf("X-Forwarded-Host = %q, want the public host", got)
	}
	if got := seen.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want https", got)
	}
	if !strings.HasPrefix(seen.Host, "127.0.0.1:") {
		t.Errorf("Host = %q, want the loopback target so dev servers accept it", seen.Host)
	}
}
