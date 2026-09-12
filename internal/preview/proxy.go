package preview

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The published surface of a preview.
//
// A preview is served from its own origin — <id>.<preview domain> — rather
// than a path under the main one. That decision is forced by what dev servers
// actually do: an app that asks for /assets/app.js means the root of its own
// origin, and no amount of prefix rewriting makes that reliable for arbitrary
// projects. A separate origin costs one wildcard DNS record and one wildcard
// certificate, both configured once, and in exchange absolute paths, cookies,
// redirects and WebSockets all work without a single rewrite.
//
// The cost of a separate origin is that the device cookie does not travel to
// it: it is host-only by design, and widening it to the parent domain would
// hand every preview a credential that controls the whole server. So a
// preview gets a credential of its own, scoped to itself, obtained by a
// redirect through the main origin. See EnterPath.

const (
	// EnterPath is where a browser lands on the preview origin to exchange a
	// ticket for a cookie. Namespaced under __omniplex so it cannot collide
	// with a route the proxied app wants.
	EnterPath = "/__omniplex/enter"

	// ticketTTL is how long the redirect from the main origin stays valid.
	// It only has to survive one hop.
	ticketTTL = 60 * time.Second

	// sessionTTL is how long a preview cookie lasts before the user has to
	// open it from Omniplex again.
	sessionTTL = 12 * time.Hour

	// cookieName is the preview credential. Host-only, so a cookie for one
	// preview is never sent to another.
	cookieName = "omniplex_preview"
)

// Auth mints and verifies preview credentials.
//
// The signing key lives in memory and is regenerated on restart, which is
// deliberate: the registry is in-memory too, so a restart re-detects services
// and re-mints their ids. A credential outliving the thing it names would be
// a credential pointing at whatever took the port next.
type Auth struct {
	key []byte

	mu   sync.Mutex
	used map[string]time.Time
}

func NewAuth() (*Auth, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &Auth{key: key, used: map[string]time.Time{}}, nil
}

// Ticket mints a single-use token authorising one device to enter one
// preview.
func (a *Auth) Ticket(previewID, deviceID string) string {
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	return a.sign("t", previewID, deviceID, time.Now().Add(ticketTTL), base64.RawURLEncoding.EncodeToString(nonce))
}

// RedeemTicket verifies a ticket and spends it. A ticket is single-use so
// that a URL captured from a browser history, a proxy log or a shoulder
// cannot be replayed.
func (a *Auth) RedeemTicket(token, previewID string) bool {
	claims, ok := a.verify(token, "t")
	if !ok || claims.previewID != previewID {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweepLocked()
	if _, spent := a.used[token]; spent {
		return false
	}
	a.used[token] = claims.expires
	return true
}

// Cookie mints the longer-lived credential a redeemed ticket is exchanged for.
func (a *Auth) Cookie(previewID, deviceID string) string {
	return a.sign("c", previewID, deviceID, time.Now().Add(sessionTTL), "")
}

// CheckCookie reports whether a cookie authorises this preview.
func (a *Auth) CheckCookie(token, previewID string) bool {
	claims, ok := a.verify(token, "c")
	return ok && claims.previewID == previewID
}

type claims struct {
	previewID string
	deviceID  string
	expires   time.Time
}

func (a *Auth) sign(kind, previewID, deviceID string, expires time.Time, nonce string) string {
	body := strings.Join([]string{kind, previewID, deviceID, strconv.FormatInt(expires.Unix(), 10), nonce}, "|")
	encoded := base64.RawURLEncoding.EncodeToString([]byte(body))
	return encoded + "." + a.mac(encoded)
}

func (a *Auth) verify(token, kind string) (claims, bool) {
	encoded, sig, found := strings.Cut(token, ".")
	if !found {
		return claims{}, false
	}
	// Constant time: the signature is the only thing standing between a
	// stranger and a dev server, and it is compared on every proxied request.
	if subtle.ConstantTimeCompare([]byte(sig), []byte(a.mac(encoded))) != 1 {
		return claims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return claims{}, false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 5 || parts[0] != kind {
		return claims{}, false
	}
	unix, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return claims{}, false
	}
	expires := time.Unix(unix, 0)
	if time.Now().After(expires) {
		return claims{}, false
	}
	return claims{previewID: parts[1], deviceID: parts[2], expires: expires}, true
}

func (a *Auth) mac(s string) string {
	h := hmac.New(sha256.New, a.key)
	h.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// sweepLocked drops spent tickets that have expired anyway, so the replay set
// cannot grow without bound.
func (a *Auth) sweepLocked() {
	now := time.Now()
	for token, expires := range a.used {
		if now.After(expires) {
			delete(a.used, token)
		}
	}
}

// ---- routing ----

// Router serves preview origins. It is installed in front of the main mux and
// declines anything that is not addressed to a preview host, so the rest of
// the server is untouched.
type Router struct {
	registry *Registry
	auth     *Auth
	// domain is the parent the previews live under, e.g.
	// "agent.example.net". Empty disables host routing entirely: with
	// no wildcard configured, a preview hostname would not resolve and
	// pretending otherwise only produces broken links.
	domain string
	logf   func(string, ...any)
}

func NewRouter(registry *Registry, auth *Auth, domain string, logf func(string, ...any)) *Router {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Router{registry: registry, auth: auth, domain: strings.ToLower(strings.TrimSpace(domain)), logf: logf}
}

// Domain reports the configured parent domain, empty when previews are not
// published.
func (r *Router) Domain() string { return r.domain }

// PreviewID returns the preview a request is addressed to, if any. The port
// is ignored: the host arrives as whatever the browser sent, and a direct
// connection carries one while a proxied connection does not.
func (r *Router) PreviewID(host string) (string, bool) {
	if r.domain == "" {
		return "", false
	}
	host = strings.ToLower(host)
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	suffix := "." + r.domain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(host, suffix)
	// One label only. A dotted id is not something we ever mint, and a
	// wildcard certificate could not cover it anyway.
	if id == "" || strings.Contains(id, ".") {
		return "", false
	}
	return id, true
}

// URL is the published address of a preview, valid only when a preview domain
// is configured.
func (r *Router) URL(previewID string) string {
	if r.domain == "" {
		return ""
	}
	return "https://" + previewID + "." + r.domain
}

// URLFor is the address to hand a browser that reached us at requestHost.
//
// The right answer depends on how the user got here, and only the server
// knows: a phone arriving through Caddy needs the published subdomain, while
// the machine itself wants the dev server directly, because routing loopback
// traffic out to a cloud VPS and back is absurd. Deciding here rather than in
// the client also means no guessing from a UI that cannot see the network.
func (r *Router) URLFor(requestHost string, p Preview) string {
	host := requestHost
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSpace(host))

	// Published only when the user is already talking to us on the domain the
	// wildcard covers. Handing out a subdomain of a domain they did not
	// arrive on is how you produce a link that resolves nowhere.
	if r.domain != "" && (host == r.domain || strings.HasSuffix(host, "."+r.domain)) {
		return r.URL(p.ID)
	}

	scheme := p.Scheme
	if scheme == "" {
		scheme = "http"
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + ":" + strconv.Itoa(p.Port)
}

// Published reports whether a URL from URLFor is one we serve ourselves, and
// therefore needs the ticket handshake. A direct loopback or LAN address does
// not: the browser talks to the dev server itself and there is no Omniplex in
// the path to authorise anything.
func (r *Router) Published(target string) bool {
	// URLFor returns exactly https://<id>.<domain> for a published preview,
	// with no port and no path, so these two tests are enough to tell it
	// apart from the direct http://host:port form.
	return r.domain != "" && strings.HasPrefix(target, "https://") && strings.HasSuffix(target, "."+r.domain)
}

// EnterURL is where a browser must land to exchange a ticket for a cookie.
func (r *Router) EnterURL(previewID, ticket string) string {
	return r.URL(previewID) + EnterPath + "?t=" + url.QueryEscape(ticket)
}

// ServeHTTP handles a request addressed to a preview host.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	id, ok := r.PreviewID(req.Host)
	if !ok {
		http.NotFound(w, req)
		return
	}
	p, live := r.registry.Lookup(id)
	if !live {
		// Deliberately the same answer as an id that never existed. A
		// retired preview must not be a way to reach whatever process has
		// since taken its port.
		http.Error(w, "This preview is no longer running.", http.StatusNotFound)
		return
	}

	if req.URL.Path == EnterPath {
		r.enter(w, req, id)
		return
	}

	cookie, err := req.Cookie(cookieName)
	if err != nil || !r.auth.CheckCookie(cookie.Value, id) {
		r.deny(w, req)
		return
	}

	r.proxyTo(p).ServeHTTP(w, req)
}

// enter exchanges a ticket minted on the main origin for a cookie on this
// one. This is the whole reason a preview is reachable at all from a phone:
// the device cookie is host-only and never arrives here.
func (r *Router) enter(w http.ResponseWriter, req *http.Request, id string) {
	token := req.URL.Query().Get("t")
	if token == "" || !r.auth.RedeemTicket(token, id) {
		r.deny(w, req)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    r.auth.Cookie(id, ""),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})

	// Land on the app's own root rather than leaving the ticket in the
	// address bar, where it would be bookmarked, shared and replayed.
	http.Redirect(w, req, "/", http.StatusFound)
}

func (r *Router) deny(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprint(w, `<!doctype html><meta name=viewport content="width=device-width,initial-scale=1">`+
		`<style>body{font:16px/1.5 system-ui;margin:0;display:grid;place-items:center;height:100dvh;padding:1.5rem;text-align:center;color:#111}`+
		`@media(prefers-color-scheme:dark){body{background:#111;color:#eee}}</style>`+
		`<div><h1 style="font-size:1.25rem">Not signed in</h1>`+
		`<p>Open this preview from Omniplex to get access.</p></div>`)
}

// proxyTo builds the reverse proxy for one service.
func (r *Router) proxyTo(p Preview) *httputil.ReverseProxy {
	target := &url.URL{Scheme: p.Scheme, Host: "127.0.0.1:" + strconv.Itoa(p.Port)}
	if target.Scheme == "" {
		target.Scheme = "http"
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	public := "https://" + p.ID + "." + r.domain

	inner := proxy.Director
	proxy.Director = func(req *http.Request) {
		inner(req)

		// The same trade as internal/server/devproxy.go, for the same
		// reason: a dev server checks the Host it was asked for against a
		// list it knows, and a preview hostname is not on it. Presenting the
		// target's own host gets past that without asking every project to
		// reconfigure. Frameworks that build absolute URLs should read the
		// forwarded headers instead, which carry the truth.
		req.Host = target.Host
		req.Header.Set("X-Forwarded-Host", p.ID+"."+r.domain)
		req.Header.Set("X-Forwarded-Proto", "https")
		// The preview credential is ours, not the app's.
		stripCookie(req, cookieName)
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		// A redirect the app writes points at the host we handed it —
		// 127.0.0.1:5050 — which the browser cannot follow. Point it back at
		// the origin the browser is actually on.
		if location := resp.Header.Get("Location"); location != "" {
			for _, prefix := range []string{target.Scheme + "://" + target.Host, "http://" + target.Host, "https://" + target.Host} {
				if strings.HasPrefix(location, prefix) {
					resp.Header.Set("Location", public+strings.TrimPrefix(location, prefix))
					break
				}
			}
		}
		return nil
	}

	proxy.Transport = &http.Transport{
		// A dev server's certificate is self-signed essentially always, and
		// this connection never leaves the machine: the hop the user needs
		// protected is browser-to-Caddy-to-here, which is TLS all the way.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		r.logf("preview %s: %v", p.ID, err)
		http.Error(w, "The preview did not respond. It may have stopped.", http.StatusBadGateway)
	}

	return proxy
}

// stripCookie removes one cookie from a request, leaving the rest.
func stripCookie(req *http.Request, name string) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != name {
			req.AddCookie(c)
		}
	}
}

// splitHostPort is net.SplitHostPort without the error on a missing port.
func splitHostPort(host string) (string, string, error) {
	i := strings.LastIndex(host, ":")
	if i < 0 || strings.Contains(host[i+1:], "]") {
		return "", "", fmt.Errorf("no port")
	}
	return strings.Trim(host[:i], "[]"), host[i+1:], nil
}
