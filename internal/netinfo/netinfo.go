// Package netinfo works out where the server should listen and how it can be
// reached.
//
// The guiding rule is that omniplex should need no flags to be useful: it binds
// everything a device you own could plausibly reach — loopback, your LAN, an
// overlay network like Tailscale — and deliberately does not bind globally
// routable addresses, because a public IP means the open internet and that is
// a decision the operator should make explicitly.
package netinfo

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"time"
)

// Kind classifies an address by who can reach it.
type Kind string

const (
	// KindLoopback is reachable only from this machine.
	KindLoopback Kind = "loopback"
	// KindPrivate is an RFC1918 or IPv6 ULA address: the local network.
	KindPrivate Kind = "private"
	// KindOverlay is the 100.64.0.0/10 shared-address range, which is what
	// Tailscale and other WireGuard overlays hand out. Reachable from your
	// other devices wherever they are, and from nowhere else.
	KindOverlay Kind = "overlay"
	// KindPublic is globally routable: the internet can reach it.
	KindPublic Kind = "public"
	// KindLinkLocal is fe80::/10 or 169.254.0.0/16. Not useful to advertise:
	// it needs a zone identifier and is unreachable off the segment.
	KindLinkLocal Kind = "link-local"
)

// Addr is one address this machine holds.
type Addr struct {
	IP   net.IP
	Kind Kind
}

// Classify decides who can reach an address.
func Classify(ip net.IP) Kind {
	switch {
	case ip.IsLoopback():
		return KindLoopback
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return KindLinkLocal
	case isOverlay(ip):
		return KindOverlay
	case ip.IsPrivate():
		return KindPrivate
	default:
		return KindPublic
	}
}

// tailscaleULA is the IPv6 prefix Tailscale assigns from. It is a unique local
// address, so IsPrivate would claim it first and the address would be
// advertised as "local network" when in fact it is reachable from anywhere the
// tailnet reaches.
var tailscaleULA = &net.IPNet{
	IP:   net.ParseIP("fd7a:115c:a1e0::"),
	Mask: net.CIDRMask(48, 128),
}

// isOverlay reports an address handed out by a WireGuard overlay: 100.64.0.0/10
// for IPv4, which IsPrivate does not cover because it is "shared address
// space" rather than private, and Tailscale's own ULA prefix for IPv6.
func isOverlay(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
	}
	return tailscaleULA.Contains(ip)
}

// Local returns every usable address this machine currently holds, ordered
// loopback first so the banner reads from nearest to furthest.
func Local() ([]Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var out []Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			kind := Classify(ipnet.IP)
			if kind == KindLinkLocal {
				continue
			}
			out = append(out, Addr{IP: ipnet.IP, Kind: kind})
		}
	}

	sortAddrs(out)
	return out, nil
}

// sortAddrs orders addresses nearest first, so a banner and an endpoint list
// read from this machine outwards.
func sortAddrs(addrs []Addr) {
	sort.SliceStable(addrs, func(i, j int) bool {
		return rank(addrs[i].Kind) < rank(addrs[j].Kind)
	})
}

func rank(k Kind) int {
	switch k {
	case KindLoopback:
		return 0
	case KindPrivate:
		return 1
	case KindOverlay:
		return 2
	default:
		return 3
	}
}

// BindPlan is the set of addresses to listen on, plus what it implies.
type BindPlan struct {
	Addrs []Addr
	Port  int
	// Reachable is true when at least one address can be reached from
	// another machine. It decides the auth policy: bound only to loopback,
	// the operating system is already the boundary.
	Reachable bool
}

// Options configures address selection.
type Options struct {
	// Override binds exactly one address, e.g. "192.168.1.20:8787". Empty
	// selects automatically.
	Override string
	// Port is used when Override is empty.
	Port int
	// IncludePublic opts into binding globally routable addresses.
	IncludePublic bool
}

// ErrPublicNotAllowed is returned when binding would expose the server to the
// internet without the operator having said so.
var ErrPublicNotAllowed = fmt.Errorf("that address is reachable from the internet; pass --bind-public to bind it deliberately")

// Plan chooses what to bind.
//
// Public addresses are bound only with IncludePublic, whether they were
// chosen automatically or named explicitly. Naming one is a clear intent, but
// exposing an agent that holds your credentials to the internet is the single
// decision worth making twice, and there is one flag that means it.
func Plan(o Options) (BindPlan, error) {
	if o.Override != "" {
		host, portStr, err := net.SplitHostPort(o.Override)
		if err != nil {
			return BindPlan{}, fmt.Errorf("parse -addr %q: %w", o.Override, err)
		}
		port, err := net.LookupPort("tcp", portStr)
		if err != nil {
			return BindPlan{}, fmt.Errorf("parse port in -addr %q: %w", o.Override, err)
		}

		// The unspecified address means "everything", which we express as the
		// automatic plan so the banner can still enumerate real addresses.
		// Public addresses stay behind their own flag even here: there is one
		// rule for exposing omniplex to the internet, and it is not a side effect of
		// asking for a wildcard bind.
		if host == "" || host == "0.0.0.0" || host == "::" {
			return automatic(port, o.IncludePublic)
		}

		ip := net.ParseIP(host)
		if ip == nil {
			// A hostname has to be resolved to addresses before it can be
			// bound. Passing it through as an empty host would make
			// net.Listen bind ":port" — every interface, including a public
			// one — which is far wider than what was asked for.
			return hostnamePlan(host, port, o.IncludePublic)
		}

		kind := Classify(ip)
		if kind == KindPublic && !o.IncludePublic {
			return BindPlan{}, fmt.Errorf("-addr %s: %w", o.Override, ErrPublicNotAllowed)
		}
		return BindPlan{
			Addrs:     []Addr{{IP: ip, Kind: kind}},
			Port:      port,
			Reachable: kind != KindLoopback,
		}, nil
	}

	return automatic(o.Port, o.IncludePublic)
}

// hostnamePlan resolves a hostname to the addresses it names and binds those.
func hostnamePlan(host string, port int, includePublic bool) (BindPlan, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return BindPlan{}, fmt.Errorf("resolve -addr host %q: %w", host, err)
	}

	plan := BindPlan{Port: port}
	seen := map[string]bool{}
	for _, ip := range ips {
		kind := Classify(ip)
		if kind == KindPublic && !includePublic {
			return BindPlan{}, fmt.Errorf("-addr %s resolves to %s: %w", host, ip, ErrPublicNotAllowed)
		}
		if kind == KindLinkLocal {
			continue
		}
		if seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		plan.Addrs = append(plan.Addrs, Addr{IP: ip, Kind: kind})
		if kind != KindLoopback {
			plan.Reachable = true
		}
	}
	if len(plan.Addrs) == 0 {
		return BindPlan{}, fmt.Errorf("-addr host %q resolved to no usable address", host)
	}
	return plan, nil
}

func automatic(port int, includePublic bool) (BindPlan, error) {
	local, err := Local()
	if err != nil {
		return BindPlan{}, err
	}

	plan := BindPlan{Port: port}
	seen := map[string]bool{}
	for _, a := range local {
		if a.Kind == KindPublic && !includePublic {
			continue
		}
		key := a.IP.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		plan.Addrs = append(plan.Addrs, a)
		if a.Kind != KindLoopback {
			plan.Reachable = true
		}
	}

	// A machine with no addresses at all still has to serve the operator.
	if len(plan.Addrs) == 0 {
		plan.Addrs = []Addr{{IP: net.IPv4(127, 0, 0, 1), Kind: KindLoopback}}
	}
	return plan, nil
}

// URL renders the address as something a browser can open.
func (a Addr) URL(port int) string {
	host := a.IP.String()
	if a.IP.To4() == nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// Label describes the address for a human reading the banner.
func (a Addr) Label() string {
	switch a.Kind {
	case KindLoopback:
		return "this machine"
	case KindPrivate:
		return "local network"
	case KindOverlay:
		return "overlay network"
	case KindPublic:
		return "public"
	default:
		return string(a.Kind)
	}
}

// Listen opens a listener per planned address. Binding each address
// individually rather than the wildcard is what keeps a public IP closed: the
// port is not merely refused there, it is not open at all.
//
// It returns the plan that actually bound, which can be narrower than the one
// asked for. Advertising the original would send a phone to an address nothing
// is listening on — the QR prefers an overlay address, and an overlay
// interface is exactly the kind that can vanish between enumeration and bind.
//
// Every returned listener is the caller's to close, including when this
// returns an error.
func (p BindPlan) Listen() ([]net.Listener, BindPlan, error) {
	bound := BindPlan{Port: p.Port}

	var out []net.Listener
	for _, a := range p.Addrs {
		host := ""
		if a.IP != nil {
			host = a.IP.String()
			if a.IP.To4() == nil {
				host = "[" + host + "]"
			}
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, p.Port))
		if err != nil {
			// A single address failing (an interface disappearing between
			// enumeration and bind, a permission problem on one address) must
			// not take down the whole server, as long as something is
			// listening. Say so, rather than quietly serving less than asked.
			log.Printf("not listening on %s: %v", a.IP, err)
			continue
		}
		out = append(out, ln)
		bound.Addrs = append(bound.Addrs, a)
		if a.Kind != KindLoopback {
			bound.Reachable = true
		}
	}
	if len(out) == 0 {
		return nil, BindPlan{}, fmt.Errorf("could not listen on any address on port %d", p.Port)
	}
	return out, bound, nil
}

// Endpoints renders the plan as URLs for the banner, nearest first.
func (p BindPlan) Endpoints() []Endpoint {
	out := make([]Endpoint, 0, len(p.Addrs))
	for _, a := range p.Addrs {
		if a.IP == nil {
			continue
		}
		out = append(out, Endpoint{URL: a.URL(p.Port), Kind: a.Kind, Label: a.Label()})
	}
	return out
}

// Endpoint is one advertised way to reach the server.
type Endpoint struct {
	URL   string `json:"url"`
	Kind  Kind   `json:"kind"`
	Label string `json:"label"`
}

// BestPairingURL picks the address a phone is most likely to be able to reach.
// An overlay address works from anywhere the device is; a private one only on
// the same network; loopback is useless to another device and is the last
// resort only so that something is always printed.
func (p BindPlan) BestPairingURL() string {
	for _, want := range []Kind{KindOverlay, KindPrivate, KindPublic, KindLoopback} {
		for _, a := range p.Addrs {
			if a.Kind == want && a.IP != nil {
				return a.URL(p.Port)
			}
		}
	}
	return ""
}
