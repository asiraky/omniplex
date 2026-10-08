package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// This file is Omniplex as an OAuth client of remote MCP servers, following
// the MCP authorization spec: protected-resource discovery from the server's
// 401, authorization-server metadata, dynamic client registration, PKCE S256
// with the RFC 8707 resource, and refresh. One sign-in per server, stored in
// the secret store and handed to every agent as a bearer token.

// Secrets is the credential store the OAuth client keeps tokens and client
// registrations in. A *provider.SecretStore satisfies it.
type Secrets interface {
	Get(id, name string) (string, bool)
	Put(id, name, value string) error
	Delete(id, name string) error
}

// CallbackPath is where the authorization server sends the browser back.
const CallbackPath = "/oauth/callback"

const (
	// registrationID holds client registrations, which belong to an
	// authorization server rather than to one MCP server. It is the reserved
	// server name, so no user server's secrets can collide with it.
	registrationID = ReservedName
)

// Tunables; tests shorten them.
var (
	// refreshWindow is how close to expiry a token is refreshed before use.
	refreshWindow = 5 * time.Minute
	// oauthRequestTimeout bounds each request to a resource or authorization
	// server.
	oauthRequestTimeout = 15 * time.Second
	// callbackWait bounds how long the callback page waits for the code
	// exchange before answering.
	callbackWait = 30 * time.Second
)

const oauthMaxBody = 1 << 20

// OAuth signs in to remote MCP servers and keeps their tokens fresh.
type OAuth struct {
	secrets Secrets
	client  *http.Client

	mu sync.Mutex
	// pending are the sign-ins waiting for their code, keyed by state.
	// Completing one removes it, so a state is good for exactly one code.
	pending map[string]*pendingFlow
	// locks serialise token writes per server key, so two sessions
	// starting at once refresh once (a rotated refresh token is single use).
	locks map[string]*sync.Mutex
	// current, when set, runs a token write only while key is still the key
	// of a server at url, and refuses it otherwise. A sign-in or a refresh
	// that finishes after the server was removed, renamed or moved must not
	// write its tokens back.
	current func(key, url string, write func() error) error
}

// NewOAuth returns a client storing credentials in secrets. A nil client
// means http.DefaultClient.
func NewOAuth(secrets Secrets, client *http.Client) *OAuth {
	return &OAuth{
		secrets: secrets,
		client:  staysHome(client),
		pending: map[string]*pendingFlow{},
		locks:   map[string]*sync.Mutex{},
	}
}

// RedirectURI is the callback address for a sign-in started from a browser
// at origin. An https origin gets its own callback; anything else gets the
// loopback address of this server's port, which authorization servers accept
// over plain http.
func RedirectURI(origin string, port int) string {
	if u, err := url.Parse(origin); err == nil && u.Scheme == "https" && u.Host != "" {
		return "https://" + u.Host + CallbackPath
	}
	return "http://localhost:" + strconv.Itoa(port) + CallbackPath
}

// tokenRecord is the `oauth` secret of one server, under its key.
type tokenRecord struct {
	AccessToken   string    `json:"accessToken"`
	RefreshToken  string    `json:"refreshToken,omitempty"`
	Expiry        time.Time `json:"expiry,omitzero"`
	TokenEndpoint string    `json:"tokenEndpoint"`
	ClientID      string    `json:"clientId"`
	ClientSecret  string    `json:"clientSecret,omitempty"`
	AuthMethod    string    `json:"authMethod,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	Resource      string    `json:"resource,omitempty"`
	Issuer        string    `json:"issuer,omitempty"`
	// URL is the server address signed in to. A token is never handed out
	// for a server whose address has since moved to another origin.
	URL string `json:"url"`
}

// registration is one dynamic client registration, reused for every sign-in
// against the same issuer with the same redirect URI.
type registration struct {
	Issuer       string `json:"issuer"`
	RedirectURI  string `json:"redirectUri"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	AuthMethod   string `json:"authMethod,omitempty"`
	// SecretExpiresAt is client_secret_expires_at: unix seconds, 0 for never.
	SecretExpiresAt int64 `json:"secretExpiresAt,omitempty"`
}

// registrationKey names a registration's secret. Issuers and redirect URIs
// are full URLs, which the store's name rule does not allow, so the key is a
// digest of the pair.
func registrationKey(issuer, redirectURI string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + redirectURI))
	return "client." + hex.EncodeToString(sum[:16])
}

func (o *OAuth) lockFor(key string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	l, ok := o.locks[key]
	if !ok {
		l = &sync.Mutex{}
		o.locks[key] = l
	}
	return l
}

func (o *OAuth) load(key string) (tokenRecord, bool) {
	raw, ok := o.secrets.Get(secretID(key), OAuthKey)
	if !ok {
		return tokenRecord{}, false
	}
	var rec tokenRecord
	if json.Unmarshal([]byte(raw), &rec) != nil || rec.AccessToken == "" {
		return tokenRecord{}, false
	}
	return rec, true
}

func (o *OAuth) save(server Server, rec tokenRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := server.Key()
	put := func() error { return o.secrets.Put(secretID(key), OAuthKey, string(b)) }
	if o.current == nil {
		return put()
	}
	return o.current(key, server.URL, put)
}

// SignedIn reports whether the server with this key (see Server.Key) holds
// tokens from a sign-in through Omniplex.
func (o *OAuth) SignedIn(key string) bool {
	_, ok := o.load(key)
	return ok
}

// SignOut forgets the tokens of the server with this key. Its other secrets
// stay.
func (o *OAuth) SignOut(key string) error {
	l := o.lockFor(key)
	l.Lock()
	defer l.Unlock()
	return o.secrets.Delete(secretID(key), OAuthKey)
}

// Token returns a usable access token for the server, refreshing it when it
// expires within refreshWindow. ErrSignInNeeded means there is no token, or
// the authorization server refused the refresh (the stored tokens are then
// dropped). A refresh that fails for another reason returns the old token
// while it is still valid, else the error.
func (o *OAuth) Token(ctx context.Context, server Server) (string, error) {
	return o.token(ctx, server, "")
}

// Refresh is Token for a token the server has just turned away, which by its
// own expiry still looks good: rejected is refreshed whatever its expiry
// says. When the stored token is no longer rejected (another caller already
// refreshed it) that one comes back as it is.
func (o *OAuth) Refresh(ctx context.Context, server Server, rejected string) (string, error) {
	return o.token(ctx, server, rejected)
}

func (o *OAuth) token(ctx context.Context, server Server, rejected string) (string, error) {
	key := server.Key()
	l := o.lockFor(key)
	l.Lock()
	defer l.Unlock()

	rec, ok := o.load(key)
	if !ok || server.URL == "" || !sameOrigin(rec.URL, server.URL) {
		return "", ErrSignInNeeded
	}
	now := time.Now()
	force := rejected != "" && rec.AccessToken == rejected
	if !force && (rec.Expiry.IsZero() || rec.Expiry.After(now.Add(refreshWindow))) {
		return rec.AccessToken, nil
	}
	if force && rec.RefreshToken == "" {
		return "", ErrSignInNeeded
	}
	if rec.RefreshToken == "" {
		if rec.Expiry.After(now) {
			return rec.AccessToken, nil
		}
		return "", ErrSignInNeeded
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rec.RefreshToken},
	}
	if rec.Resource != "" {
		form.Set("resource", rec.Resource)
	}
	tr, err := o.tokenRequest(ctx, rec.TokenEndpoint, form, rec.ClientID, rec.ClientSecret, rec.AuthMethod)
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && te.refused() {
			_ = o.secrets.Delete(secretID(key), OAuthKey)
			return "", fmt.Errorf("%w: %v", ErrSignInNeeded, err)
		}
		if !force && rec.Expiry.After(now) {
			return rec.AccessToken, nil
		}
		return "", err
	}
	rec.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		rec.RefreshToken = tr.RefreshToken
	}
	rec.Expiry = tr.expiry(now)
	if tr.Scope != "" {
		rec.Scope = tr.Scope
	}
	if err := o.save(server, rec); err != nil {
		if errors.Is(err, ErrServerChanged) {
			return "", ErrSignInNeeded
		}
		return "", err
	}
	return rec.AccessToken, nil
}

// ---- Sign-in ----

type pendingFlow struct {
	state string
	// codes carries the callback's result; buffered, and only the claimer
	// of the state ever sends.
	codes chan callbackMsg
	// done closes when the flow stops waiting.
	done chan struct{}
}

type callbackMsg struct {
	code     string
	oauthErr string
	// reply receives the outcome of the exchange, for the callback page.
	reply chan error
}

// take claims a pending flow by state for the callback route.
func (o *OAuth) take(state string) *pendingFlow {
	if state == "" {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	pf, ok := o.pending[state]
	if !ok {
		return nil
	}
	delete(o.pending, state)
	return pf
}

// claim claims pf for the paste path; false when the callback got it first.
func (o *OAuth) claim(pf *pendingFlow) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pending[pf.state] != pf {
		return false
	}
	delete(o.pending, pf.state)
	return true
}

// SignIn runs the browser sign-in for a URL server and stores its tokens. It
// narrates the authorize URL through ia, then takes the code from whichever
// arrives first: the callback route seeing this flow's state, or the address
// the person pastes from their browser.
//
// fresh asks the authorization server to sign the person in again rather
// than take the browser's current session (OpenID Connect's prompt=login;
// one that does not know it ignores it).
func (o *OAuth) SignIn(ctx context.Context, ia adapter.AuthInteraction, server Server, redirectURI string, fresh bool) error {
	if server.URL == "" {
		return fmt.Errorf("%s has no address to sign in to", server.Name)
	}
	if ru, err := url.Parse(redirectURI); err != nil || ru.Host == "" || (ru.Scheme != "https" && ru.Scheme != "http") {
		return fmt.Errorf("invalid redirect address")
	}
	d, err := o.discover(ctx, server.URL)
	if err != nil {
		return err
	}
	reg, err := o.register(ctx, d, redirectURI)
	if err != nil {
		return err
	}

	verifier := oauthRandom()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := oauthRandom()

	authURL, err := url.Parse(d.as.AuthorizationEndpoint)
	if err != nil {
		return fmt.Errorf("invalid authorization endpoint")
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", reg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("resource", d.resource)
	if d.scope != "" {
		q.Set("scope", d.scope)
	}
	if fresh {
		q.Set("prompt", "login")
	}
	authURL.RawQuery = q.Encode()

	pf := &pendingFlow{state: state, codes: make(chan callbackMsg, 1), done: make(chan struct{})}
	o.mu.Lock()
	o.pending[state] = pf
	o.mu.Unlock()
	defer func() {
		o.claim(pf)
		close(pf.done)
	}()

	ia.Notify(adapter.AuthEvent{
		Type:    adapter.AuthEventURL,
		URL:     authURL.String(),
		Message: "Open the link to sign in to " + server.Name + ".",
	})

	code, reply, err := o.awaitCode(ctx, ia, pf, redirectURI)
	if err != nil {
		return err
	}
	err = o.exchange(ctx, server, d, reg, code, verifier, redirectURI)
	reply(err)
	return err
}

// awaitCode waits for the code from the callback or a pasted address. reply
// tells the callback page how the exchange went; it is a no-op for a paste.
func (o *OAuth) awaitCode(ctx context.Context, ia adapter.AuthInteraction, pf *pendingFlow, redirectURI string) (string, func(error), error) {
	fromCallback := func(msg callbackMsg) (string, func(error), error) {
		reply := func(err error) { msg.reply <- err }
		if msg.oauthErr != "" {
			err := fmt.Errorf("the sign-in was refused (%s)", safeCode(msg.oauthErr))
			reply(err)
			return "", nil, err
		}
		if msg.code == "" {
			err := errors.New("the sign-in came back without a code")
			reply(err)
			return "", nil, err
		}
		return msg.code, reply, nil
	}
	prompt := adapter.AuthPrompt{
		Message:     "Paste the address your browser ended up on",
		Placeholder: redirectURI + "?code=...",
	}
	for {
		pctx, cancel := context.WithCancel(ctx)
		answers := make(chan promptAnswer, 1)
		go func() {
			v, err := ia.Prompt(pctx, prompt)
			answers <- promptAnswer{v, err}
		}()
		select {
		case msg := <-pf.codes:
			cancel()
			return fromCallback(msg)
		case <-ctx.Done():
			cancel()
			return "", nil, ctx.Err()
		case a := <-answers:
			cancel()
			if a.err != nil {
				return "", nil, a.err
			}
			code, state, oauthErr := parsePasted(a.value)
			if state == "" || (code == "" && oauthErr == "") {
				ia.Notify(adapter.AuthEvent{Type: adapter.AuthEventInfo, Message: "That address has no sign-in code in it."})
				continue
			}
			if subtle.ConstantTimeCompare([]byte(state), []byte(pf.state)) != 1 {
				ia.Notify(adapter.AuthEvent{Type: adapter.AuthEventInfo, Message: "That address is from a different sign-in."})
				continue
			}
			if !o.claim(pf) {
				// The callback took this state first; its code wins.
				select {
				case msg := <-pf.codes:
					return fromCallback(msg)
				case <-ctx.Done():
					return "", nil, ctx.Err()
				}
			}
			if oauthErr != "" {
				return "", nil, fmt.Errorf("the sign-in was refused (%s)", safeCode(oauthErr))
			}
			return code, func(error) {}, nil
		}
	}
}

type promptAnswer struct {
	value string
	err   error
}

// parsePasted reads code, state and error from a pasted callback address. It
// takes the query wherever it is, so a pasted address missing its scheme
// still works.
func parsePasted(s string) (code, state, oauthErr string) {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, '?')
	if i < 0 {
		return "", "", ""
	}
	query, _, _ := strings.Cut(s[i+1:], "#")
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", "", ""
	}
	return q.Get("code"), q.Get("state"), q.Get("error")
}

// HandleCallback is the public callback route. It only completes a sign-in
// that is waiting on the request's state; anything else gets an error page
// and changes nothing. It never redirects.
func (o *OAuth) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		callbackPage(w, http.StatusMethodNotAllowed, false)
		return
	}
	q := r.URL.Query()
	pf := o.take(q.Get("state"))
	if pf == nil {
		callbackPage(w, http.StatusBadRequest, false)
		return
	}
	reply := make(chan error, 1)
	pf.codes <- callbackMsg{code: q.Get("code"), oauthErr: q.Get("error"), reply: reply}
	timer := time.NewTimer(callbackWait)
	defer timer.Stop()
	select {
	case err := <-reply:
		if err != nil {
			callbackPage(w, http.StatusBadRequest, false)
			return
		}
		callbackPage(w, http.StatusOK, true)
	case <-pf.done:
		// The flow ended without taking the code (cancelled, timed out).
		select {
		case err := <-reply:
			if err == nil {
				callbackPage(w, http.StatusOK, true)
				return
			}
		default:
		}
		callbackPage(w, http.StatusBadRequest, false)
	case <-timer.C:
		callbackPage(w, http.StatusGatewayTimeout, false)
	case <-r.Context().Done():
	}
}

func callbackPage(w http.ResponseWriter, status int, ok bool) {
	msg := "Sign-in failed. Go back to Omniplex and try again."
	if ok {
		msg = "Signed in. You can close this tab."
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	// The address carries the code; keep it out of any onward request.
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Omniplex</title>`+
		`<style>body{font:16px system-ui,sans-serif;margin:3rem 1.5rem;text-align:center}</style></head><body><p>%s</p></body></html>`, msg)
}

// ---- Discovery ----

type resourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type serverMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

type discovery struct {
	issuer   string
	as       serverMetadata
	resource string
	scope    string
}

// discover finds the authorization server for an MCP server and what to ask
// it for.
func (o *OAuth) discover(ctx context.Context, serverURL string) (discovery, error) {
	su, err := url.Parse(serverURL)
	if err != nil || su.Host == "" || (su.Scheme != "https" && su.Scheme != "http") {
		return discovery{}, fmt.Errorf("invalid server address")
	}
	su.Fragment = ""
	// Plain http endpoints are accepted only when the server itself is on
	// this machine.
	allowHTTP := isLoopbackHost(su.Hostname())

	challenge := o.challenge(ctx, su.String())

	var candidates []string
	if rm := challenge["resource_metadata"]; rm != "" {
		candidates = append(candidates, rm)
	}
	candidates = append(candidates, wellKnown(su, "oauth-protected-resource", true)...)

	var prm resourceMetadata
	found := false
	for _, c := range candidates {
		cu, err := secureEndpoint(c, allowHTTP)
		if err != nil {
			continue
		}
		var m resourceMetadata
		if o.getJSON(ctx, cu.String(), &m) == nil && len(m.AuthorizationServers) > 0 {
			prm, found = m, true
			break
		}
	}

	d := discovery{resource: su.String()}
	if found {
		iu, err := secureEndpoint(prm.AuthorizationServers[0], allowHTTP)
		if err != nil {
			return discovery{}, fmt.Errorf("the server named an unusable authorization server")
		}
		d.issuer = strings.TrimSuffix(iu.String(), "/")
		if prm.Resource != "" {
			if !covers(prm.Resource, su) {
				return discovery{}, fmt.Errorf("the server's sign-in details are for a different address")
			}
			d.resource = prm.Resource
		}
	} else {
		// Servers from before protected-resource metadata are their own
		// authorization server.
		d.issuer = su.Scheme + "://" + su.Host
	}

	iu, _ := url.Parse(d.issuer)
	var meta serverMetadata
	ok := false
	for _, c := range wellKnownAS(iu) {
		var m serverMetadata
		if o.getJSON(ctx, c, &m) == nil && m.AuthorizationEndpoint != "" && m.TokenEndpoint != "" {
			meta, ok = m, true
			break
		}
	}
	if !ok {
		return discovery{}, fmt.Errorf("couldn't find how to sign in to this server")
	}
	for _, e := range []*string{&meta.AuthorizationEndpoint, &meta.TokenEndpoint, &meta.RegistrationEndpoint} {
		if *e == "" {
			continue
		}
		if _, err := secureEndpoint(*e, allowHTTP); err != nil {
			return discovery{}, fmt.Errorf("the authorization server has an insecure endpoint")
		}
	}
	if len(meta.CodeChallengeMethodsSupported) > 0 && !slices.Contains(meta.CodeChallengeMethodsSupported, "S256") {
		return discovery{}, fmt.Errorf("the authorization server doesn't support PKCE with S256")
	}
	d.as = meta

	switch {
	case challenge["scope"] != "":
		d.scope = challenge["scope"]
	case found && len(prm.ScopesSupported) > 0:
		d.scope = strings.Join(prm.ScopesSupported, " ")
	}
	if slices.Contains(meta.ScopesSupported, "offline_access") && !slices.Contains(strings.Fields(d.scope), "offline_access") {
		d.scope = strings.TrimSpace(d.scope + " offline_access")
	}
	return d, nil
}

// challenge asks the server unauthenticated and returns the parameters of
// its Bearer challenge, if it sent one.
func (o *OAuth) challenge(ctx context.Context, serverURL string) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, oauthRequestTimeout)
	defer cancel()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"Omniplex","version":"1"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, strings.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		return nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, oauthMaxBody))
	for _, h := range resp.Header.Values("WWW-Authenticate") {
		if p := bearerParams(h); p != nil {
			return p
		}
	}
	return nil
}

// bearerParams parses a WWW-Authenticate value and returns the auth-params
// of its Bearer challenge, or nil when there is none.
func bearerParams(h string) map[string]string {
	var params map[string]string
	inBearer := false
	i := 0
	for i < len(h) {
		for i < len(h) && (h[i] == ' ' || h[i] == ',' || h[i] == '\t') {
			i++
		}
		start := i
		for i < len(h) && h[i] != ' ' && h[i] != '=' && h[i] != ',' && h[i] != '\t' {
			i++
		}
		tok := h[start:i]
		if tok == "" {
			i++
			continue
		}
		j := i
		for j < len(h) && (h[j] == ' ' || h[j] == '\t') {
			j++
		}
		if j < len(h) && h[j] == '=' {
			// auth-param
			i = j + 1
			for i < len(h) && (h[i] == ' ' || h[i] == '\t') {
				i++
			}
			var val strings.Builder
			if i < len(h) && h[i] == '"' {
				i++
				for i < len(h) && h[i] != '"' {
					if h[i] == '\\' && i+1 < len(h) {
						i++
					}
					val.WriteByte(h[i])
					i++
				}
				i++
			} else {
				for i < len(h) && h[i] != ',' && h[i] != ' ' && h[i] != '\t' {
					val.WriteByte(h[i])
					i++
				}
			}
			if inBearer {
				params[strings.ToLower(tok)] = val.String()
			}
			continue
		}
		// A new challenge's scheme.
		if params != nil && inBearer {
			return params
		}
		inBearer = strings.EqualFold(tok, "Bearer")
		if inBearer {
			params = map[string]string{}
		}
	}
	return params
}

// wellKnown lists the metadata addresses for u: the path-inserted form first
// (RFC 8615/9728), then the root.
func wellKnown(u *url.URL, name string, withRoot bool) []string {
	base := u.Scheme + "://" + u.Host + "/.well-known/" + name
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	var out []string
	if path != "" {
		out = append(out, base+path)
	}
	if withRoot || path == "" {
		out = append(out, base)
	}
	return out
}

// wellKnownAS lists the metadata addresses for an issuer, OAuth before
// OpenID Connect, as the MCP spec orders them.
func wellKnownAS(issuer *url.URL) []string {
	path := strings.TrimSuffix(issuer.EscapedPath(), "/")
	origin := issuer.Scheme + "://" + issuer.Host
	if path == "" {
		return []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}
	}
	return []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + "/.well-known/openid-configuration" + path,
		origin + path + "/.well-known/openid-configuration",
	}
}

// covers reports whether a protected resource identifier is valid for the
// server address: same origin, and the server's path under the resource's.
func covers(resource string, server *url.URL) bool {
	ru, err := url.Parse(resource)
	if err != nil || !strings.EqualFold(ru.Scheme, server.Scheme) || !strings.EqualFold(ru.Host, server.Host) {
		return false
	}
	rp := strings.TrimSuffix(ru.Path, "/")
	sp := strings.TrimSuffix(server.Path, "/")
	return rp == "" || sp == rp || strings.HasPrefix(sp, rp+"/")
}

func sameOrigin(a, b string) bool {
	au, err1 := url.Parse(a)
	bu, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && au.Host != "" &&
		strings.EqualFold(au.Scheme, bu.Scheme) && strings.EqualFold(au.Host, bu.Host)
}

// endpoint parses a discovered address and insists on https, or loopback
// http when allowed.
func secureEndpoint(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid endpoint")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowHTTP && isLoopbackHost(u.Hostname()):
	default:
		return nil, errors.New("insecure endpoint")
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (o *OAuth) getJSON(ctx context.Context, u string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, oauthRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, oauthMaxBody)).Decode(out)
}

// ---- Registration ----

// register returns the client registration for the issuer and redirect URI,
// registering dynamically (RFC 7591) when there is none yet.
func (o *OAuth) register(ctx context.Context, d discovery, redirectURI string) (registration, error) {
	key := registrationKey(d.issuer, redirectURI)
	if raw, ok := o.secrets.Get(registrationID, key); ok {
		var reg registration
		if json.Unmarshal([]byte(raw), &reg) == nil && reg.ClientID != "" &&
			reg.Issuer == d.issuer && reg.RedirectURI == redirectURI &&
			(reg.SecretExpiresAt == 0 || time.Unix(reg.SecretExpiresAt, 0).After(time.Now().Add(time.Minute))) {
			return reg, nil
		}
	}
	if d.as.RegistrationEndpoint == "" {
		return registration{}, fmt.Errorf("this server doesn't let Omniplex register to sign in")
	}

	method := "none"
	if sup := d.as.TokenEndpointAuthMethodsSupported; len(sup) > 0 && !slices.Contains(sup, "none") {
		method = "client_secret_post"
		if slices.Contains(sup, "client_secret_basic") {
			method = "client_secret_basic"
		}
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "Omniplex",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
	})
	rctx, cancel := context.WithTimeout(ctx, oauthRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, d.as.RegistrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return registration{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return registration{}, fmt.Errorf("registering with the authorization server: %w", netErr(err))
	}
	defer resp.Body.Close()
	var out struct {
		ClientID              string `json:"client_id"`
		ClientSecret          string `json:"client_secret"`
		ClientSecretExpiresAt int64  `json:"client_secret_expires_at"`
		AuthMethod            string `json:"token_endpoint_auth_method"`
		Error                 string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, oauthMaxBody)).Decode(&out)
	if resp.StatusCode/100 != 2 || out.ClientID == "" {
		return registration{}, fmt.Errorf("the authorization server refused to register Omniplex (%s)", statusCode(resp.StatusCode, out.Error))
	}
	reg := registration{
		Issuer:          d.issuer,
		RedirectURI:     redirectURI,
		ClientID:        out.ClientID,
		ClientSecret:    out.ClientSecret,
		AuthMethod:      out.AuthMethod,
		SecretExpiresAt: out.ClientSecretExpiresAt,
	}
	if reg.AuthMethod == "" {
		reg.AuthMethod = method
	}
	b, _ := json.Marshal(reg)
	if err := o.secrets.Put(registrationID, key, string(b)); err != nil {
		return registration{}, err
	}
	return reg, nil
}

// ---- Tokens ----

func (o *OAuth) exchange(ctx context.Context, server Server, d discovery, reg registration, code, verifier, redirectURI string) error {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"resource":      {d.resource},
	}
	now := time.Now()
	tr, err := o.tokenRequest(ctx, d.as.TokenEndpoint, form, reg.ClientID, reg.ClientSecret, reg.AuthMethod)
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && te.code == "invalid_client" {
			// The registration is gone on the server's side; the next
			// attempt registers afresh.
			_ = o.secrets.Delete(registrationID, registrationKey(d.issuer, redirectURI))
		}
		return err
	}
	rec := tokenRecord{
		AccessToken:   tr.AccessToken,
		RefreshToken:  tr.RefreshToken,
		Expiry:        tr.expiry(now),
		TokenEndpoint: d.as.TokenEndpoint,
		ClientID:      reg.ClientID,
		ClientSecret:  reg.ClientSecret,
		AuthMethod:    reg.AuthMethod,
		Scope:         tr.Scope,
		Resource:      d.resource,
		Issuer:        d.issuer,
		URL:           server.URL,
	}
	if rec.Scope == "" {
		rec.Scope = d.scope
	}
	l := o.lockFor(server.Key())
	l.Lock()
	defer l.Unlock()
	return o.save(server, rec)
}

type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	TokenType    string          `json:"token_type"`
	RefreshToken string          `json:"refresh_token"`
	Scope        string          `json:"scope"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
	Error        string          `json:"error"`
}

func (t tokenResponse) expiry(now time.Time) time.Time {
	s := strings.Trim(string(t.ExpiresIn), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(n) * time.Second)
}

// tokenError is the token endpoint saying no. It carries only the status
// and the OAuth error code, never anything the request contained.
type tokenError struct {
	status int
	code   string
}

func (e *tokenError) Error() string {
	return "the authorization server refused (" + statusCode(e.status, e.code) + ")"
}

// refused means the grant itself was rejected, not that the server hiccuped.
func (e *tokenError) refused() bool {
	return e.status == http.StatusBadRequest || e.status == http.StatusUnauthorized
}

func (o *OAuth) tokenRequest(ctx context.Context, endpoint string, form url.Values, clientID, clientSecret, method string) (tokenResponse, error) {
	if endpoint == "" {
		return tokenResponse{}, ErrSignInNeeded
	}
	basic := clientSecret != "" && method == "client_secret_basic"
	if !basic {
		form.Set("client_id", clientID)
		if clientSecret != "" {
			form.Set("client_secret", clientSecret)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, oauthRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, errors.New("invalid token endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("reaching the authorization server: %w", netErr(err))
	}
	defer resp.Body.Close()
	var tr tokenResponse
	decErr := json.NewDecoder(io.LimitReader(resp.Body, oauthMaxBody)).Decode(&tr)
	if resp.StatusCode != http.StatusOK || tr.Error != "" {
		return tokenResponse{}, &tokenError{status: resp.StatusCode, code: tr.Error}
	}
	if decErr != nil || tr.AccessToken == "" {
		return tokenResponse{}, errors.New("the authorization server sent no token")
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "bearer") {
		return tokenResponse{}, fmt.Errorf("the authorization server sent an unsupported token type (%s)", safeCode(tr.TokenType))
	}
	return tr, nil
}

// netErr strips the request URL (which may carry a code) from a transport
// error.
func netErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func statusCode(status int, code string) string {
	if code = safeCode(code); code != "" {
		return code + ", " + strconv.Itoa(status)
	}
	return strconv.Itoa(status)
}

// safeCode keeps an OAuth error code printable and short; it comes from
// another server and may end up on screen.
func safeCode(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			return r
		}
		return -1
	}, s)
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func oauthRandom() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
