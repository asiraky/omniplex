// Package auth gates access to the server.
//
// The policy is decided by how the server is reachable, not by who is asking.
// Bound only to loopback, the operating system is already the boundary: no
// other machine can connect at all, so there is nothing to pair and no
// friction. Bound to anything wider, every request from another machine must
// carry a paired device token — being on the same network is not
// authorisation.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/store"
)

// cookiePrefix carries the device token in browsers. It is httpOnly, so page
// scripts cannot read it, and it rides WebSocket upgrades to the same origin
// automatically, which is why the browser client needs no token handling of
// its own.
const cookiePrefix = "omniplex_device"

// DefaultPort is the port a server listens on when nothing says otherwise. The
// instance on it owns the unsuffixed cookie name.
const DefaultPort = 8787

// CookieName is the device token cookie for a server listening on port.
//
// Cookies are scoped by host and never by port, so two Omniplex instances on
// one machine share a jar: pairing with a worktree on :8800 overwrites the
// token the instance on :8787 issued, that instance then sees a token it never
// minted, and sends the browser back to /pair. Pairing there overwrites the
// first one straight back. It presents as pairing "randomly falling out",
// which is a miserable thing to debug, and it is guaranteed on any machine
// running a dev instance beside a real one.
//
// The port is what tells instances apart — it is already how worktrees are
// distinguished — so it is what the cookie keys on. The default port keeps the
// bare name so the primary instance is untouched by this change and nothing
// has to pair again.
func CookieName(port int) string {
	if port == DefaultPort {
		return cookiePrefix
	}
	return fmt.Sprintf("%s_%d", cookiePrefix, port)
}

// PairingTTL bounds how long a printed code stays valid. Long enough to walk
// to another room and find your phone, short enough that a code left on a
// screen goes stale.
const PairingTTL = 10 * time.Minute

// Policy is how the server is reachable.
type Policy string

const (
	// PolicyLoopback: nothing but this machine can connect.
	PolicyLoopback Policy = "loopback"
	// PolicyReachable: another machine can connect, so tokens are required.
	PolicyReachable Policy = "reachable"
)

// Guard applies the policy to requests.
type Guard struct {
	store  *store.Store
	policy Policy

	// cookie is this instance's device-token cookie name. See CookieName.
	cookie string

	limiter *limiter

	// proxied records that a reverse proxy is publishing this server to other
	// machines. It is the difference between "the peer is loopback, therefore
	// this is a local process" and "the peer is loopback because a proxy is
	// relaying the internet at us", and it is set by whoever knows about the
	// proxy — see SetProxied.
	proxied atomic.Bool
}

// New builds a guard. reachable says whether any bound address can be reached
// from another machine; the caller works that out when it decides what to bind.
func New(st *store.Store, reachable bool, port int) *Guard {
	policy := PolicyLoopback
	if reachable {
		policy = PolicyReachable
	}
	return &Guard{store: st, policy: policy, limiter: newLimiter(), cookie: CookieName(port)}
}

// CookieName is the cookie this guard issues and reads.
func (g *Guard) CookieName() string { return g.cookie }

func (g *Guard) Policy() Policy { return g.policy }

// SetProxied tells the guard whether a reverse proxy is currently forwarding
// remote traffic to a loopback address of ours.
//
// This exists because of `tailscale serve`, which proxies the tailnet to
// http://127.0.0.1:<port>. Every request through it arrives with a loopback
// peer address, so without this the loopback shortcut would hand any member of
// the tailnet full control with no pairing at all. The mapping also persists
// across reboots, so it can be in force on a later run that took no flags.
//
// The caller is responsible for keeping this current: set it when enabling or
// disabling the proxy, and re-check periodically, because the proxy can be
// configured from outside this process.
func (g *Guard) SetProxied(v bool) { g.proxied.Store(v) }

// Proxied reports the last known proxy state.
func (g *Guard) Proxied() bool { return g.proxied.Load() }

// ---- tokens ----

func hashOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// pairingAlphabet omits the four characters that are read wrong off a screen:
// the letters I and O, and the digits 0 and 1. Note that L is *in* the
// alphabet and must never be "corrected" to 1 — roughly two in five codes
// contain one, and rewriting it would break them.
const pairingAlphabetChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var pairingAlphabet = base32.NewEncoding(pairingAlphabetChars).WithPadding(base32.NoPadding)

// pairingCodeBytes is 80 bits of entropy: far beyond guessing within the
// ten-minute window, even without the rate limiter.
const pairingCodeBytes = 10

// pairingCodeLen is how many characters that encodes to.
const pairingCodeLen = (pairingCodeBytes*8 + 4) / 5 // 16

func randomPairingCode() (string, error) {
	buf := make([]byte, pairingCodeBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return pairingAlphabet.EncodeToString(buf), nil
}

// FormatCode groups a code for display, because sixteen unbroken characters
// are hard to read off a screen and type without losing your place.
func FormatCode(code string) string {
	var b strings.Builder
	for i, r := range code {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// normaliseCode turns what a human typed into what was generated, or returns
// empty if it cannot be one of our codes.
//
// Case and the separators we print are noise and are stripped. Nothing else is
// rewritten. It is tempting to "fix" look-alikes, but there is no safe mapping
// here: I, O, 0 and 1 are all outside the alphabet, so none of them has a
// valid character to become, and L — which looks like 1 — is a legitimate
// character that must be left exactly as typed.
func normaliseCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_', '\t':
			return -1
		}
		return r
	}, code)

	if len(code) != pairingCodeLen {
		return ""
	}
	// DecodeString is the authority on membership: anything outside the
	// alphabet, including a mistyped I, O, 0 or 1, fails here.
	if _, err := pairingAlphabet.DecodeString(code); err != nil {
		return ""
	}
	return code
}

// ---- pairing ----

// NewPairingCode mints a short-lived, single-use code. Only its hash is
// stored, so the database never holds anything that grants access.
func (g *Guard) NewPairingCode(ctx context.Context) (string, error) {
	// Codes are cheap and short-lived; clearing spent ones here keeps the
	// table from growing without a background job.
	_ = g.store.PurgePairings(ctx)

	code, err := randomPairingCode()
	if err != nil {
		return "", err
	}
	if err := g.store.CreatePairing(ctx, hashOf(code), time.Now().Add(PairingTTL).UnixMilli()); err != nil {
		return "", err
	}
	return code, nil
}

// ErrBadCode covers every rejection of a pairing attempt, deliberately without
// distinguishing wrong, malformed, expired and already-used: a caller guessing
// codes learns nothing from the difference.
var ErrBadCode = errors.New("that pairing code is not valid")

// ErrTooManyAttempts is returned when a client has guessed too often.
var ErrTooManyAttempts = errors.New("too many pairing attempts; wait a minute and try again")

// Redeem exchanges a pairing code for a long-lived device token. The token is
// returned once and never stored in the clear.
func (g *Guard) Redeem(ctx context.Context, remote, code, label string) (string, store.Device, error) {
	if !g.limiter.allow(remote) {
		return "", store.Device{}, ErrTooManyAttempts
	}

	normalised := normaliseCode(code)
	if normalised == "" {
		return "", store.Device{}, ErrBadCode
	}

	token, err := randomToken()
	if err != nil {
		return "", store.Device{}, err
	}
	if label == "" {
		label = "paired device"
	}

	// One transaction, so a failure cannot spend the code without issuing the
	// device it paid for. Single-use is enforced inside by a conditional
	// update, so two devices racing one code cannot both win.
	device := store.Device{ID: uuid.NewString(), Label: label}
	if err := g.store.RedeemPairingForDevice(ctx, hashOf(normalised), device.ID, hashOf(token), label); err != nil {
		return "", store.Device{}, ErrBadCode
	}
	// Read back rather than reporting the half-built value: the store owns the
	// timestamps, and a caller rendering a device list wants the real ones.
	if stored, err := g.store.DeviceByToken(ctx, hashOf(token)); err == nil {
		device = stored
	}

	// A successful pairing clears the attempt budget: the client has proved
	// it is not guessing.
	g.limiter.reset(remote)
	return token, device, nil
}

func (g *Guard) Devices(ctx context.Context) ([]store.Device, error) { return g.store.ListDevices(ctx) }

func (g *Guard) Revoke(ctx context.Context, id string) error { return g.store.RevokeDevice(ctx, id) }

// ---- request gating ----

type ctxKey struct{}

// DeviceFrom returns the device that authorised this request.
func DeviceFrom(ctx context.Context) (store.Device, bool) {
	d, ok := ctx.Value(ctxKey{}).(store.Device)
	return d, ok
}

// LocalDevice is the pseudo-device attributed to requests from this machine.
var LocalDevice = store.Device{ID: "local", Label: "this machine"}

// IsLocal reports whether a request arrived from this machine. It reads the
// TCP peer address only: forwarding headers are attacker-controlled and are
// never consulted to *grant* anything.
func IsLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// proxyHeaders are set by `tailscale serve` when it relays a request, verified
// against the shipping binary rather than assumed.
//
// Reading them is safe in a way that reading them to grant access would not
// be: an attacker can add a header, and doing so only ever costs them the
// loopback shortcut. A header can take trust away here; it can never confer
// it. This is the second line of defence behind SetProxied, and it closes the
// window between a proxy appearing and the periodic check noticing.
var proxyHeaders = []string{
	"Tailscale-User-Login",
	"Tailscale-User-Name",
	"Tailscale-User-Profile-Pic",
	"Tailscale-App-Capabilities",
	// Set on every relayed request as a pointer to Tailscale's own docs, so
	// it marks a relay even if the identity headers above ever change.
	"Tailscale-Headers-Info",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
}

// looksProxied reports that a request bears the marks of having been relayed,
// so it must not be treated as local however loopback its peer address is.
func looksProxied(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// DirectlyLocal reports a request that came from this machine and was not
// relayed to reach it.
//
// It differs from the locality test inside Authorize in what it ignores: the
// standing proxied flag. That flag records that something sits in front of the
// server — a fact about the server, not about this request. Authorize is right
// to treat it as disqualifying, because a request it admits gets to act. This
// is for the narrower question of whether the caller is the box itself, where
// a loopback peer bearing none of a relay's marks is exactly that and the flag
// says nothing about it.
//
// Verified against `tailscale serve`: a relayed request arrives with six of
// the headers above set and a direct one with none of them.
func DirectlyLocal(r *http.Request) bool {
	return IsLocal(r) && !looksProxied(r)
}

// Authorize reports whether a request may proceed, and as which device.
func (g *Guard) Authorize(r *http.Request) (store.Device, bool) {
	// A reverse proxy in front of omniplex would otherwise defeat the whole scheme,
	// because every request it relays arrives with a loopback peer address.
	// While one is in front of us, nothing gets in on locality alone.
	proxied := g.proxied.Load() || looksProxied(r)

	// A direct request from this machine is already inside the boundary the
	// operating system enforces, whatever else is bound.
	//
	// The consequence worth knowing: any local process, and any other user on
	// a shared machine, is trusted. That is right for a personal machine and
	// wrong for a shared one.
	if IsLocal(r) && !proxied {
		return LocalDevice, true
	}

	// Bound only to loopback and not proxied, no other machine can reach us,
	// so a non-local peer is something unexpected and is not trusted.
	if !proxied && g.policy == PolicyLoopback {
		return store.Device{}, false
	}

	token := g.tokenFrom(r)
	if token == "" {
		return store.Device{}, false
	}
	// The lookup is by hash of a 256-bit secret, so an attacker cannot walk
	// it by timing: there is nothing to guess a prefix of.
	device, err := g.store.DeviceByToken(r.Context(), hashOf(token))
	if err != nil {
		return store.Device{}, false
	}
	return device, true
}

// WithDevice attaches the authorising device to a request's context.
func WithDevice(r *http.Request, d store.Device) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, d))
}

func (g *Guard) tokenFrom(r *http.Request) string {
	if c, err := r.Cookie(g.cookie); err == nil && c.Value != "" {
		return c.Value
	}
	// Bearer, for native clients that carry no cookie jar.
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	// A WebSocket subprotocol is the only header a browser lets a page set on
	// an upgrade, so it is the escape hatch for a cross-origin client that
	// cannot rely on the cookie.
	for _, part := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		part = strings.TrimSpace(part)
		if after, found := strings.CutPrefix(part, "omniplex.token."); found {
			return after
		}
	}
	return ""
}

// TokenSubprotocol returns the WebSocket subprotocol carrying a token, if the
// client offered one, so the accept side can echo it back. A browser refuses
// the connection unless the server selects one of the offered protocols.
func TokenSubprotocol(r *http.Request) (string, bool) {
	for _, part := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "omniplex.token.") {
			return part, true
		}
	}
	return "", false
}

// SetCookie issues the device token to a browser.
//
// Secure is set only when this request itself arrived over TLS. Marking the
// cookie Secure on a plain-HTTP origin would stop the browser sending it back
// at all, which is the common case here: a LAN address and an overlay address
// are both http://, and over an overlay the transport is already encrypted.
func (g *Guard) SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     g.cookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(365 * 24 * time.Hour),
	})
}

// ClearCookie removes a device token from a browser.
func (g *Guard) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     g.cookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// ---- rate limiting ----

// attemptBudget is how many pairing attempts one peer may make per window.
// A code is 80 bits, so this is defence in depth rather than the thing
// standing between an attacker and the server.
const (
	attemptBudget = 10
	attemptWindow = time.Minute
)

// maxBuckets caps how many peers the limiter tracks at once. Sweeping expired
// entries is not enough on its own: an attacker holding an IPv6 prefix can
// present a fresh source address per request, so every bucket is live and
// nothing is reclaimed. The cap is what turns unbounded growth into a fixed
// ceiling.
const maxBuckets = 4096

type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	count int
	reset time.Time
}

func newLimiter() *limiter { return &limiter{buckets: map[string]*bucket{}} }

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if ok && now.Before(b.reset) {
		if b.count >= attemptBudget {
			return false
		}
		b.count++
		return true
	}

	if len(l.buckets) >= maxBuckets {
		l.evict(now)
	}
	l.buckets[key] = &bucket{count: 1, reset: now.Add(attemptWindow)}
	return true
}

// evict reclaims space, expired entries first. If none have expired — the
// flood case — it drops those closest to expiry, which are the peers that
// have been quiet longest. The budget those peers lose is defence in depth
// over an 80-bit code, and keeping memory bounded matters more.
func (l *limiter) evict(now time.Time) {
	for k, v := range l.buckets {
		if now.After(v.reset) {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) < maxBuckets {
		return
	}

	type entry struct {
		key   string
		reset time.Time
	}
	entries := make([]entry, 0, len(l.buckets))
	for k, v := range l.buckets {
		entries = append(entries, entry{k, v.reset})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].reset.Before(entries[j].reset) })

	for i := 0; i < len(entries)/2; i++ {
		delete(l.buckets, entries[i].key)
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// PeerKey identifies the caller for rate limiting: the TCP peer address
// without its port, since a client gets a fresh port per connection.
func PeerKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
