// Package endpoints assembles the list of ways this server is currently
// reachable, which the client receives on connect.
//
// The list is ordered by how durable each address is, not by how fast it is.
// A LAN address comes from DHCP and changes; an overlay hostname does not, so
// it is the one a device should remember.
package endpoints

import (
	"context"
	"strconv"
	"sync"

	"github.com/asiraky/omniplex/internal/netinfo"
	"github.com/asiraky/omniplex/internal/overlay"
)

// Reachability says who can reach an endpoint.
type Reachability string

const (
	Loopback Reachability = "loopback"
	LAN      Reachability = "lan"
	Overlay  Reachability = "overlay"
	Public   Reachability = "public"
)

// Endpoint is one advertised way in.
type Endpoint struct {
	ID    string       `json:"id"`
	Label string       `json:"label"`
	URL   string       `json:"url"`
	Reach Reachability `json:"reachability"`
	// Stable marks an address that survives a DHCP lease or a change of
	// network — in practice, an overlay hostname. A client should prefer it
	// when offering the user something to bookmark.
	Stable bool `json:"stable"`
	// Encrypted says the transport protects the traffic: TLS for an HTTPS
	// endpoint, WireGuard for an overlay one. A plain LAN address is not, so
	// anything crossing it — the pairing code, and afterwards the device
	// token — is readable by whoever controls the network. Saying so is the
	// honest alternative to a certificate we cannot issue for a LAN IP.
	Encrypted bool `json:"encrypted"`
}

// Set is the advertised list plus what we know about the overlay.
type Set struct {
	Endpoints []Endpoint `json:"endpoints"`
	// Overlay describes the Tailscale state, so a UI can explain why no
	// remote endpoint is offered and what to do about it.
	Overlay OverlayInfo `json:"overlay"`
}

// OverlayInfo is the UI-facing view of the overlay network.
type OverlayInfo struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	DNSName   string `json:"dnsName,omitempty"`
	// HTTPS reports whether `tailscale serve` is already publishing us on a
	// real certificate.
	HTTPS    bool   `json:"https"`
	HTTPSURL string `json:"httpsUrl,omitempty"`
}

// Builder produces the endpoint list on demand. It is rebuilt per request
// rather than cached at startup because a laptop changes networks: the LAN
// address it had when the process began may be gone.
type Builder struct {
	mu   sync.RWMutex
	plan netinfo.BindPlan
	port int
}

// SetPlan replaces the bound addresses. Called when an interface appears or
// disappears after startup, so what a client is told it can reach stays true
// rather than describing the machine as it was at boot.
func (b *Builder) SetPlan(plan netinfo.BindPlan) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.plan = plan
}

// currentPlan reads the plan under the lock.
func (b *Builder) currentPlan() netinfo.BindPlan {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.plan
}

func NewBuilder(plan netinfo.BindPlan, port int) *Builder {
	return &Builder{plan: plan, port: port}
}

// Build assembles the current list. Overlay detection is best-effort: a
// missing or wedged CLI yields a list without an overlay hostname rather than
// an error.
func (b *Builder) Build(ctx context.Context) Set {
	set := Set{Endpoints: []Endpoint{}}

	ts := overlay.Detect(ctx)
	set.Overlay = OverlayInfo{
		Installed: ts.CLI != "" || b.hasOverlayAddr(),
		Running:   ts.Running,
		DNSName:   ts.DNSName,
	}

	// The overlay hostname first: it is the only address that is both
	// reachable from anywhere and stable enough to bookmark.
	if ts.Running && ts.DNSName != "" {
		serve := overlay.CheckServe(ctx, ts.CLI, b.port, ts.DNSName)
		set.Overlay.HTTPS = serve.Enabled
		set.Overlay.HTTPSURL = serve.URL

		if serve.Enabled {
			set.Endpoints = append(set.Endpoints, Endpoint{
				ID:        "overlay-https",
				Label:     "Tailscale (HTTPS)",
				URL:       serve.URL,
				Reach:     Overlay,
				Stable:    true,
				Encrypted: true,
			})
		}
		set.Endpoints = append(set.Endpoints, Endpoint{
			ID:    "overlay-dns",
			Label: "Tailscale",
			URL:   "http://" + ts.DNSName + ":" + strconv.Itoa(b.port),
			Reach: Overlay,
			// Plain HTTP, but the tailnet carries it inside WireGuard, so it
			// is encrypted end to end regardless of the scheme.
			Stable:    true,
			Encrypted: true,
		})
	}

	for _, a := range b.currentPlan().Addrs {
		if a.IP == nil {
			continue
		}
		reach := reachabilityOf(a.Kind)
		// An overlay IP is redundant once its hostname is advertised, but
		// without a hostname it is still the only way in from another
		// network — which is the normal state for the macOS App Store build,
		// where the interface works and the CLI is absent.
		if reach == Overlay && set.Overlay.DNSName != "" {
			continue
		}
		set.Endpoints = append(set.Endpoints, Endpoint{
			ID:        string(reach) + "-" + a.IP.String(),
			Label:     a.Label(),
			URL:       a.URL(b.port),
			Reach:     reach,
			Encrypted: encryptedFor(reach),
		})
	}

	return set
}

// encryptedFor reports whether the transport protects traffic to an endpoint.
// Loopback never leaves the machine; an overlay is inside WireGuard; a LAN or
// public address over plain HTTP is in the clear.
func encryptedFor(r Reachability) bool {
	return r == Loopback || r == Overlay
}

func (b *Builder) hasOverlayAddr() bool {
	for _, a := range b.currentPlan().Addrs {
		if a.Kind == netinfo.KindOverlay {
			return true
		}
	}
	return false
}

func reachabilityOf(k netinfo.Kind) Reachability {
	switch k {
	case netinfo.KindLoopback:
		return Loopback
	case netinfo.KindPrivate:
		return LAN
	case netinfo.KindOverlay:
		return Overlay
	default:
		return Public
	}
}

// BestPairingURL is the address a QR code should carry: the one most likely to
// work from the scanning device, and to keep working afterwards.
//
// The overlay hostname wins outright. Pairing binds a device token to an
// origin, so pairing once on a name that reaches the server both at home and
// away means the user never has to pair a second time.
func (s Set) BestPairingURL() string {
	for _, want := range []Reachability{Overlay, LAN, Public, Loopback} {
		for _, e := range s.Endpoints {
			if e.Reach == want {
				return e.URL
			}
		}
	}
	return ""
}

// Port is the port the server listens on, needed by callers that shell out to
// configure a proxy in front of it.
func (b *Builder) Port() int { return b.port }
