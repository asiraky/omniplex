package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// oaSecrets is an in-memory Secrets that enforces the secret store's name
// rule on both keys, so a key scheme that would not fit the real store fails
// here.
type oaSecrets struct {
	mu sync.Mutex
	m  map[string]string
}

var oaKeyRule = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func newOASecrets() *oaSecrets { return &oaSecrets{m: map[string]string{}} }

func (s *oaSecrets) check(id, name string) error {
	if !oaKeyRule.MatchString(id) || !oaKeyRule.MatchString(name) {
		return fmt.Errorf("bad key %q/%q", id, name)
	}
	return nil
}

func (s *oaSecrets) Get(id, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id+"/"+name]
	return v, ok
}

func (s *oaSecrets) Put(id, name, value string) error {
	if err := s.check(id, name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id+"/"+name] = value
	return nil
}

func (s *oaSecrets) Delete(id, name string) error {
	if err := s.check(id, name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id+"/"+name)
	return nil
}

// oaFake plays an MCP server (the resource) and its authorization server, on
// separate origins.
type oaFake struct {
	t   *testing.T
	mcp *httptest.Server
	as  *httptest.Server

	// knobs
	noResourceMetadataHint bool   // 401 without resource_metadata
	challengeScope         string // scope in the 401
	expiresIn              int
	refreshStatus          int    // non-zero: refresh answers this
	exchangeError          string // non-empty: code exchange answers 400 with it

	mu            sync.Mutex
	registrations map[string]string // client_id -> redirect uri
	codes         map[string]oaGrant
	refresh       map[string]bool
	issued        int
	exchanges     int
	refreshes     int
}

type oaGrant struct {
	clientID, redirect, challenge, resource string
}

func newOAFake(t *testing.T) *oaFake {
	f := &oaFake{
		t:             t,
		expiresIn:     3600,
		registrations: map[string]string{},
		codes:         map[string]oaGrant{},
		refresh:       map[string]bool{},
	}
	f.as = httptest.NewServer(http.HandlerFunc(f.serveAS))
	f.mcp = httptest.NewServer(http.HandlerFunc(f.serveMCP))
	t.Cleanup(f.mcp.Close)
	t.Cleanup(f.as.Close)
	return f
}

func (f *oaFake) server() Server { return Server{Name: "cf", URL: f.mcp.URL + "/mcp"} }

func (f *oaFake) serveMCP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/mcp":
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusOK)
			return
		}
		ch := `Bearer realm="mcp"`
		if !f.noResourceMetadataHint {
			ch += `, resource_metadata="` + f.mcp.URL + `/custom-prm"`
		}
		if f.challengeScope != "" {
			ch += `, scope="` + f.challengeScope + `"`
		}
		w.Header().Set("WWW-Authenticate", ch)
		w.WriteHeader(http.StatusUnauthorized)
	case "/custom-prm", "/.well-known/oauth-protected-resource/mcp":
		if r.URL.Path == "/custom-prm" && f.noResourceMetadataHint {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"resource":              f.mcp.URL + "/mcp",
			"authorization_servers": []string{f.as.URL},
			"scopes_supported":      []string{"read", "write"},
		})
	default:
		http.NotFound(w, r)
	}
}

func (f *oaFake) serveAS(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/.well-known/oauth-authorization-server":
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.as.URL,
			"authorization_endpoint":                f.as.URL + "/authorize",
			"token_endpoint":                        f.as.URL + "/token",
			"registration_endpoint":                 f.as.URL + "/register",
			"scopes_supported":                      []string{"read", "write", "offline_access"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	case "/register":
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) != 1 || body.AuthMethod != "none" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id := fmt.Sprintf("client-%d", len(f.registrations)+1)
		f.registrations[id] = body.RedirectURIs[0]
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"client_id": id, "token_endpoint_auth_method": "none"})
	case "/token":
		r.ParseForm()
		form := r.PostForm
		fail := func(status int, code string) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "rejected " + form.Get("code") + form.Get("refresh_token")})
		}
		switch form.Get("grant_type") {
		case "authorization_code":
			f.exchanges++
			if f.exchangeError != "" {
				fail(http.StatusBadRequest, f.exchangeError)
				return
			}
			g, ok := f.codes[form.Get("code")]
			delete(f.codes, form.Get("code"))
			sum := sha256.Sum256([]byte(form.Get("code_verifier")))
			if !ok || g.clientID != form.Get("client_id") || g.redirect != form.Get("redirect_uri") ||
				g.resource != form.Get("resource") || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
				fail(http.StatusBadRequest, "invalid_grant")
				return
			}
		case "refresh_token":
			f.refreshes++
			if f.refreshStatus != 0 {
				fail(f.refreshStatus, "invalid_grant")
				return
			}
			if !f.refresh[form.Get("refresh_token")] || form.Get("client_id") == "" {
				fail(http.StatusBadRequest, "invalid_grant")
				return
			}
			delete(f.refresh, form.Get("refresh_token"))
		default:
			fail(http.StatusBadRequest, "unsupported_grant_type")
			return
		}
		f.issued++
		rt := fmt.Sprintf("rt-%d", f.issued)
		f.refresh[rt] = true
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fmt.Sprintf("at-%d", f.issued),
			"token_type":    "Bearer",
			"refresh_token": rt,
			"expires_in":    f.expiresIn,
		})
	default:
		http.NotFound(w, r)
	}
}

// authorize stands in for the person approving in a browser: it checks the
// authorize request and returns the address the browser would land on.
func (f *oaFake) authorize(authURL string) string {
	f.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || u.Scheme+"://"+u.Host+u.Path != f.as.URL+"/authorize" {
		f.t.Fatalf("authorize url %q", authURL)
	}
	q := u.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	redirect, ok := f.registrations[q.Get("client_id")]
	if !ok || redirect != q.Get("redirect_uri") || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("response_type") != "code" {
		f.t.Fatalf("bad authorize request %v", q)
	}
	code := fmt.Sprintf("code-%d", len(f.codes)+f.exchanges+1)
	f.codes[code] = oaGrant{clientID: q.Get("client_id"), redirect: redirect, challenge: q.Get("code_challenge"), resource: q.Get("resource")}
	return redirect + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(q.Get("state"))
}

// oaIA is the person on the other end of the flow.
type oaIA struct {
	events  chan adapter.AuthEvent
	prompts chan oaPrompt
}

type oaPrompt struct {
	p      adapter.AuthPrompt
	answer chan string
}

func newOAIA() *oaIA {
	return &oaIA{events: make(chan adapter.AuthEvent, 16), prompts: make(chan oaPrompt, 4)}
}

func (ia *oaIA) Notify(ev adapter.AuthEvent) { ia.events <- ev }

func (ia *oaIA) Prompt(ctx context.Context, p adapter.AuthPrompt) (string, error) {
	ans := make(chan string, 1)
	select {
	case ia.prompts <- oaPrompt{p, ans}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case v := <-ans:
		return v, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (ia *oaIA) authURL(t *testing.T) string {
	t.Helper()
	for {
		select {
		case ev := <-ia.events:
			if ev.Type == adapter.AuthEventURL {
				return ev.URL
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no auth_url event")
		}
	}
}

func (ia *oaIA) prompt(t *testing.T) oaPrompt {
	t.Helper()
	select {
	case p := <-ia.prompts:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no prompt")
	}
	return oaPrompt{}
}

const oaRedirect = "http://localhost:4321/oauth/callback"

func startSignIn(o *OAuth, ia adapter.AuthInteraction, s Server, redirect string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- o.SignIn(context.Background(), ia, s, redirect, false) }()
	return done
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("sign-in never finished")
	}
	return nil
}

func callback(o *OAuth, landed string) *httptest.ResponseRecorder {
	u, _ := url.Parse(landed)
	rec := httptest.NewRecorder()
	o.HandleCallback(rec, httptest.NewRequest(http.MethodGet, CallbackPath+"?"+u.RawQuery, nil))
	return rec
}

func TestOAuthSignInThroughCallback(t *testing.T) {
	f := newOAFake(t)
	sec := newOASecrets()
	o := NewOAuth(sec, nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)

	authURL := ia.authURL(t)
	q, _ := url.Parse(authURL)
	if got := q.Query().Get("resource"); got != f.mcp.URL+"/mcp" {
		t.Errorf("resource = %q", got)
	}
	// No scope in the challenge: the resource's scopes, plus offline_access
	// because the authorization server lists it.
	if got := q.Query().Get("scope"); got != "read write offline_access" {
		t.Errorf("scope = %q", got)
	}
	landed := f.authorize(authURL)

	if rec := callback(o, landed); rec.Code != http.StatusOK {
		t.Fatalf("callback status %d", rec.Code)
	}
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if !o.SignedIn("cf") {
		t.Fatal("not signed in after the flow")
	}
	tok, err := o.Token(context.Background(), f.server())
	if err != nil || tok != "at-1" {
		t.Fatalf("token = %q, %v", tok, err)
	}

	// The state was single use: replaying the callback is refused and
	// changes nothing.
	if rec := callback(o, landed); rec.Code != http.StatusBadRequest {
		t.Errorf("replayed callback status %d", rec.Code)
	}
	if f.exchanges != 1 {
		t.Errorf("exchanges = %d", f.exchanges)
	}
}

func TestOAuthSignInThroughPaste(t *testing.T) {
	f := newOAFake(t)
	f.challengeScope = "read"
	o := NewOAuth(newOASecrets(), nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)

	authURL := ia.authURL(t)
	if got, _ := url.Parse(authURL); got.Query().Get("scope") != "read offline_access" {
		t.Errorf("scope = %q", got.Query().Get("scope"))
	}
	landed := f.authorize(authURL)

	// A pasted address from another sign-in is refused and asked again.
	u, _ := url.Parse(landed)
	q := u.Query()
	q.Set("state", "someone-elses")
	u.RawQuery = q.Encode()
	ia.prompt(t).answer <- u.String()
	// So is one with no code in it.
	ia.prompt(t).answer <- "http://localhost:4321/oauth/callback"
	// The right one, pasted without its scheme, works.
	ia.prompt(t).answer <- strings.TrimPrefix(landed, "http://")

	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if f.exchanges != 1 {
		t.Errorf("exchanges = %d, want only the matching paste exchanged", f.exchanges)
	}
	if tok, err := o.Token(context.Background(), f.server()); err != nil || tok != "at-1" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	// The callback for a state the paste already used finds nothing.
	if rec := callback(o, landed); rec.Code != http.StatusBadRequest {
		t.Errorf("late callback status %d", rec.Code)
	}
}

func TestOAuthUnknownStateChangesNothing(t *testing.T) {
	f := newOAFake(t)
	o := NewOAuth(newOASecrets(), nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)
	landed := f.authorize(ia.authURL(t))

	u, _ := url.Parse(landed)
	forged := CallbackPath + "?code=" + url.QueryEscape(u.Query().Get("code")) + "&state=forged"
	rec := httptest.NewRecorder()
	o.HandleCallback(rec, httptest.NewRequest(http.MethodGet, forged, nil))
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Fatalf("forged callback: status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	if f.exchanges != 0 || o.SignedIn("cf") {
		t.Fatal("a forged state must not exchange or sign in")
	}

	// The real one still completes.
	if rec := callback(o, landed); rec.Code != http.StatusOK {
		t.Fatalf("callback status %d", rec.Code)
	}
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthCallbackAndPasteRace(t *testing.T) {
	for i := range 20 {
		f := newOAFake(t)
		o := NewOAuth(newOASecrets(), nil)
		ia := newOAIA()
		done := startSignIn(o, ia, f.server(), oaRedirect)
		landed := f.authorize(ia.authURL(t))
		p := ia.prompt(t)

		var wg sync.WaitGroup
		var code int
		wg.Add(1)
		go func() { defer wg.Done(); code = callback(o, landed).Code }()
		p.answer <- landed
		if err := waitErr(t, done); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		wg.Wait()
		if f.exchanges != 1 {
			t.Fatalf("run %d: exchanges = %d", i, f.exchanges)
		}
		// Whichever path lost: the callback either completed the flow
		// itself or found its state already used.
		if code != http.StatusOK && code != http.StatusBadRequest {
			t.Fatalf("run %d: callback status %d", i, code)
		}
		if !o.SignedIn("cf") {
			t.Fatalf("run %d: not signed in", i)
		}
	}
}

func TestOAuthRefusedSignIn(t *testing.T) {
	f := newOAFake(t)
	o := NewOAuth(newOASecrets(), nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)
	landed := f.authorize(ia.authURL(t))
	u, _ := url.Parse(landed)

	rec := httptest.NewRecorder()
	o.HandleCallback(rec, httptest.NewRequest(http.MethodGet, CallbackPath+"?error=access_denied&state="+url.QueryEscape(u.Query().Get("state")), nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("callback status %d", rec.Code)
	}
	if err := waitErr(t, done); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err = %v", err)
	}
	if o.SignedIn("cf") || f.exchanges != 0 {
		t.Error("a refused sign-in must store nothing")
	}
}

func TestOAuthExchangeErrorHidesCode(t *testing.T) {
	f := newOAFake(t)
	f.exchangeError = "invalid_grant"
	o := NewOAuth(newOASecrets(), nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)
	landed := f.authorize(ia.authURL(t))
	u, _ := url.Parse(landed)
	ia.prompt(t).answer <- landed
	err := waitErr(t, done)
	if err == nil {
		t.Fatal("exchange failure must fail the sign-in")
	}
	if strings.Contains(err.Error(), u.Query().Get("code")) {
		t.Errorf("error carries the code: %v", err)
	}
}

func TestOAuthReusesRegistrationPerRedirect(t *testing.T) {
	f := newOAFake(t)
	o := NewOAuth(newOASecrets(), nil)
	signIn := func(redirect string) {
		ia := newOAIA()
		done := startSignIn(o, ia, f.server(), redirect)
		ia.prompt(t).answer <- f.authorize(ia.authURL(t))
		if err := waitErr(t, done); err != nil {
			t.Fatal(err)
		}
	}
	signIn(oaRedirect)
	signIn(oaRedirect)
	if len(f.registrations) != 1 {
		t.Fatalf("registrations = %d after two sign-ins with one redirect", len(f.registrations))
	}
	signIn("https://box.example.ts.net/oauth/callback")
	if len(f.registrations) != 2 {
		t.Fatalf("registrations = %d, a new redirect needs its own", len(f.registrations))
	}
}

func TestOAuthDiscoveryFallsBackToWellKnown(t *testing.T) {
	f := newOAFake(t)
	f.noResourceMetadataHint = true
	o := NewOAuth(newOASecrets(), nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)
	ia.prompt(t).answer <- f.authorize(ia.authURL(t))
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthRejectsInsecureAuthorizationServer(t *testing.T) {
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			// Plain http on a host that is not this machine: never contacted.
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{"http://auth.invalid"}})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcp.Close()
	o := NewOAuth(newOASecrets(), nil)
	err := o.SignIn(context.Background(), newOAIA(), Server{Name: "x", URL: mcp.URL}, oaRedirect, false)
	if err == nil {
		t.Fatal("an http authorization server off this machine must be refused")
	}
}

// signedIn runs a whole sign-in and returns the fake and client.
func signedIn(t *testing.T, expiresIn int) (*oaFake, *OAuth, *oaSecrets) {
	t.Helper()
	f := newOAFake(t)
	f.expiresIn = expiresIn
	sec := newOASecrets()
	o := NewOAuth(sec, nil)
	ia := newOAIA()
	done := startSignIn(o, ia, f.server(), oaRedirect)
	ia.prompt(t).answer <- f.authorize(ia.authURL(t))
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	return f, o, sec
}

func TestOAuthTokenRefreshesNearExpiry(t *testing.T) {
	// Expires inside the refresh window: the next Token refreshes.
	f, o, _ := signedIn(t, 60)
	tok, err := o.Token(context.Background(), f.server())
	if err != nil || tok != "at-2" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	// The rotated refresh token was stored: refreshing again works.
	tok, err = o.Token(context.Background(), f.server())
	if err != nil || tok != "at-3" {
		t.Fatalf("second refresh token = %q, %v", tok, err)
	}
}

// A project server's sign-in and refreshes live under its key, apart from a
// server of the same name that goes everywhere.
func TestOAuthProjectServerKeepsTokensUnderItsKey(t *testing.T) {
	f := newOAFake(t)
	f.expiresIn = 60
	sec := newOASecrets()
	o := NewOAuth(sec, nil)
	srv := f.server()
	srv.Project = "p1"
	ia := newOAIA()
	done := startSignIn(o, ia, srv, oaRedirect)
	ia.prompt(t).answer <- f.authorize(ia.authURL(t))
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	stored := func(id string) string {
		raw, _ := sec.Get(id, OAuthKey)
		var rec tokenRecord
		json.Unmarshal([]byte(raw), &rec)
		return rec.AccessToken
	}
	if got := stored("p1.cf"); got != "at-1" {
		t.Fatalf("p1.cf holds %q", got)
	}
	if !o.SignedIn("p1/cf") || o.SignedIn("cf") {
		t.Errorf("signed in: p1/cf %v, cf %v", o.SignedIn("p1/cf"), o.SignedIn("cf"))
	}

	// Near expiry: the refresh is stored under the same key.
	if tok, err := o.Token(context.Background(), srv); err != nil || tok != "at-2" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if got := stored("p1.cf"); got != "at-2" {
		t.Errorf("after refresh p1.cf holds %q", got)
	}
	if _, err := o.Token(context.Background(), f.server()); !errors.Is(err, ErrSignInNeeded) {
		t.Errorf("the cf that goes everywhere got p1's token: %v", err)
	}
	if err := o.SignOut("p1/cf"); err != nil || o.SignedIn("p1/cf") {
		t.Errorf("sign out: %v", err)
	}
}

func TestOAuthTokenFreshIsNotRefreshed(t *testing.T) {
	f, o, _ := signedIn(t, 3600)
	if tok, err := o.Token(context.Background(), f.server()); err != nil || tok != "at-1" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if f.refreshes != 0 {
		t.Errorf("refreshes = %d", f.refreshes)
	}
}

func TestOAuthConcurrentTokensRefreshOnce(t *testing.T) {
	f, o, _ := signedIn(t, 60)
	f.expiresIn = 3600
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := o.Token(context.Background(), f.server()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if f.refreshes != 1 {
		t.Errorf("refreshes = %d, want one", f.refreshes)
	}
}

func TestOAuthRefusedRefreshNeedsSignIn(t *testing.T) {
	f, o, _ := signedIn(t, 60)
	f.refreshStatus = http.StatusBadRequest
	_, err := o.Token(context.Background(), f.server())
	if !errors.Is(err, ErrSignInNeeded) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "rt-1") {
		t.Errorf("error carries the refresh token: %v", err)
	}
	if o.SignedIn("cf") {
		t.Error("a refused refresh must leave the server needing sign-in")
	}
}

func TestOAuthRefreshOutage(t *testing.T) {
	f, o, sec := signedIn(t, 60)
	f.refreshStatus = http.StatusServiceUnavailable

	// Still valid for a minute: the old token is used.
	if tok, err := o.Token(context.Background(), f.server()); err != nil || tok != "at-1" {
		t.Fatalf("token = %q, %v", tok, err)
	}

	// Expired: an error, but not a sign-in, and the tokens are kept.
	raw, _ := sec.Get("cf", OAuthKey)
	var rec tokenRecord
	json.Unmarshal([]byte(raw), &rec)
	rec.Expiry = time.Now().Add(-time.Minute)
	b, _ := json.Marshal(rec)
	sec.Put("cf", OAuthKey, string(b))
	_, err := o.Token(context.Background(), f.server())
	if err == nil || errors.Is(err, ErrSignInNeeded) {
		t.Fatalf("err = %v", err)
	}
	if !o.SignedIn("cf") {
		t.Error("an outage must not drop the sign-in")
	}
}

func TestOAuthTokenNeedsSignIn(t *testing.T) {
	o := NewOAuth(newOASecrets(), nil)
	if _, err := o.Token(context.Background(), Server{Name: "none", URL: "https://x.example/mcp"}); !errors.Is(err, ErrSignInNeeded) {
		t.Fatalf("no sign-in: err = %v", err)
	}

	// A server whose address moved to another origin does not get the old
	// server's token.
	f, o, _ := signedIn(t, 3600)
	moved := f.server()
	moved.URL = "https://elsewhere.example/mcp"
	if _, err := o.Token(context.Background(), moved); !errors.Is(err, ErrSignInNeeded) {
		t.Fatalf("moved server: err = %v", err)
	}
}

func TestOAuthSignOutKeepsOtherSecrets(t *testing.T) {
	_, o, sec := signedIn(t, 3600)
	sec.Put("cf", "header.X-Api-Key", "k")
	if err := o.SignOut("cf"); err != nil {
		t.Fatal(err)
	}
	if o.SignedIn("cf") {
		t.Error("still signed in")
	}
	if _, ok := sec.Get("cf", "header.X-Api-Key"); !ok {
		t.Error("sign out deleted another secret")
	}
}

func TestOAuthCancelledSignInReleasesState(t *testing.T) {
	f := newOAFake(t)
	o := NewOAuth(newOASecrets(), nil)
	ia := newOAIA()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.SignIn(ctx, ia, f.server(), oaRedirect, false) }()
	landed := f.authorize(ia.authURL(t))
	cancel()
	if err := waitErr(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if rec := callback(o, landed); rec.Code != http.StatusBadRequest {
		t.Errorf("callback after cancel: status %d", rec.Code)
	}
	if f.exchanges != 0 {
		t.Error("a cancelled sign-in must not exchange")
	}
}

func TestRedirectURI(t *testing.T) {
	cases := map[string]string{
		"https://box.tail.ts.net":         "https://box.tail.ts.net/oauth/callback",
		"https://box.tail.ts.net:8443/x/": "https://box.tail.ts.net:8443/oauth/callback",
		"http://192.168.1.4:7000":         "http://localhost:7000/oauth/callback",
		"":                                "http://localhost:7000/oauth/callback",
	}
	for origin, want := range cases {
		if got := RedirectURI(origin, 7000); got != want {
			t.Errorf("RedirectURI(%q) = %q, want %q", origin, got, want)
		}
	}
}

func TestBearerParams(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{`Bearer resource_metadata="https://a/prm", scope="x y"`, map[string]string{"resource_metadata": "https://a/prm", "scope": "x y"}},
		{`Basic realm="r", Bearer error="invalid_token", Resource_Metadata="https://b/prm"`, map[string]string{"error": "invalid_token", "resource_metadata": "https://b/prm"}},
		{`Bearer realm="a \"q\"" , scope=read`, map[string]string{"realm": `a "q"`, "scope": "read"}},
		{`Basic realm="r"`, nil},
	}
	for _, c := range cases {
		got := bearerParams(c.in)
		if len(got) != len(c.want) || (c.want == nil) != (got == nil) {
			t.Errorf("bearerParams(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("bearerParams(%q)[%s] = %q, want %q", c.in, k, got[k], v)
			}
		}
	}
}

// Two Omniplex servers on one store (the live one and a dev one) each have
// their own process lock. A refresh token is single use, so if both spend
// it, the loser is refused and drops the sign-in for both. The lock file
// lets one refresh and the other pick up its tokens.
func TestOAuthTwoServersOnOneStoreRefreshOnce(t *testing.T) {
	f, a, sec := signedIn(t, 60)
	f.expiresIn = 3600
	dir := t.TempDir()
	a.lockDir = dir
	b := NewOAuth(sec, nil)
	b.lockDir = dir

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		o := a
		if i%2 == 1 {
			o = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := o.Token(context.Background(), f.server()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if f.refreshes != 1 {
		t.Errorf("refreshes = %d, want one", f.refreshes)
	}
	if !a.SignedIn("cf") {
		t.Error("the sign-in was lost")
	}
}

// A caller that stops waiting must not abandon a refresh the authorization
// server may already have acted on: its answer carries the only refresh
// token that still works.
func TestOAuthAbandonedRefreshIsStillSaved(t *testing.T) {
	release := make(chan struct{})
	var used atomic.Value
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		used.Store(r.PostForm.Get("refresh_token"))
		<-release
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at-new","token_type":"Bearer","refresh_token":"rt-new","expires_in":3600}`)
	}))
	defer as.Close()
	sec := newOASecrets()
	o := NewOAuth(sec, as.Client())
	srv := Server{Name: "s", URL: "https://s.example.com/mcp"}
	b, _ := json.Marshal(tokenRecord{
		AccessToken: "at-old", RefreshToken: "rt-old", Expiry: time.Now().Add(-time.Minute),
		TokenEndpoint: as.URL + "/token", ClientID: "cid", URL: srv.URL,
	})
	sec.Put("s", OAuthKey, string(b))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := o.Token(ctx, srv); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired token and a slow refresh: err = %v", err)
	}
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if rec, ok := o.load("s"); ok && rec.RefreshToken == "rt-new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh's answer was never saved")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if used.Load() != "rt-old" {
		t.Errorf("refreshed with %v", used.Load())
	}
	if tok, err := o.Token(context.Background(), srv); err != nil || tok != "at-new" {
		t.Errorf("token after = %q, %v", tok, err)
	}
}

// A slow refresh of a token that is still good hands the caller that token
// rather than nothing.
func TestOAuthSlowRefreshFallsBackToAGoodToken(t *testing.T) {
	release := make(chan struct{})
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		as.Close()
	}()
	sec := newOASecrets()
	o := NewOAuth(sec, as.Client())
	srv := Server{Name: "s", URL: "https://s.example.com/mcp"}
	b, _ := json.Marshal(tokenRecord{
		AccessToken: "at-old", RefreshToken: "rt-old", Expiry: time.Now().Add(time.Minute),
		TokenEndpoint: as.URL + "/token", ClientID: "cid", URL: srv.URL,
	})
	sec.Put("s", OAuthKey, string(b))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if tok, err := o.Token(ctx, srv); err != nil || tok != "at-old" {
		t.Fatalf("token = %q, %v", tok, err)
	}
}

// A refusal of a refresh token another server has already replaced (one
// without the lock file) is about tokens nobody holds any more: the new ones
// are used and the sign-in kept.
func TestOAuthRefusalAfterAnotherServerRefreshedKeepsTheSignIn(t *testing.T) {
	sec := newOASecrets()
	srv := Server{Name: "s", URL: "https://s.example.com/mcp"}
	var as *httptest.Server
	record := func(at, rt string, expiry time.Time) string {
		b, _ := json.Marshal(tokenRecord{
			AccessToken: at, RefreshToken: rt, Expiry: expiry,
			TokenEndpoint: as.URL + "/token", ClientID: "cid", URL: srv.URL,
		})
		return string(b)
	}
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The other server got there first and saved its answer.
		sec.Put("s", OAuthKey, record("at-other", "rt-other", time.Now().Add(time.Hour)))
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer as.Close()
	sec.Put("s", OAuthKey, record("at-old", "rt-old", time.Now().Add(-time.Minute)))

	o := NewOAuth(sec, as.Client())
	tok, err := o.Token(context.Background(), srv)
	if err != nil || tok != "at-other" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if !o.SignedIn("s") {
		t.Error("the other server's sign-in was dropped")
	}
}
