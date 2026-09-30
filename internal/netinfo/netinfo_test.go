package netinfo

import (
	"errors"
	"net"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		ip   string
		want Kind
	}{
		{"127.0.0.1", KindLoopback},
		{"::1", KindLoopback},
		{"10.25.170.23", KindPrivate},
		{"192.168.1.20", KindPrivate},
		{"172.16.0.1", KindPrivate},
		{"fd00::1", KindPrivate},
		// Tailscale hands out addresses from the shared range, which
		// IsPrivate does not cover.
		{"100.101.102.103", KindOverlay},
		{"100.64.0.1", KindOverlay},
		{"100.127.255.254", KindOverlay},
		// Just outside the overlay range on either side.
		{"100.63.255.255", KindPublic},
		{"100.128.0.1", KindPublic},
		{"8.8.8.8", KindPublic},
		{"2606:4700::1111", KindPublic},
		{"169.254.1.1", KindLinkLocal},
		{"fe80::1", KindLinkLocal},
	}

	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("bad test address %q", c.ip)
		}
		if got := Classify(ip); got != c.want {
			t.Errorf("Classify(%s) = %q, want %q", c.ip, got, c.want)
		}
	}
}

// A VPS has a public address. Binding it by default would put an agent with
// the operator's credentials on the open internet, so automatic selection must
// leave it out unless asked.
func TestPublicAddressesNeedOptIn(t *testing.T) {
	local, err := Local()
	if err != nil {
		t.Fatal(err)
	}
	var hasPublic bool
	for _, a := range local {
		if a.Kind == KindPublic {
			hasPublic = true
		}
	}

	plan, err := Plan(Options{Port: 8787})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plan.Addrs {
		if a.Kind == KindPublic {
			t.Fatalf("automatic plan bound the public address %s without --bind-public", a.IP)
		}
	}

	if hasPublic {
		opted, err := Plan(Options{Port: 8787, IncludePublic: true})
		if err != nil {
			t.Fatal(err)
		}
		var got bool
		for _, a := range opted.Addrs {
			if a.Kind == KindPublic {
				got = true
			}
		}
		if !got {
			t.Error("IncludePublic did not add the public address")
		}
	}
}

func TestPlanAlwaysBindsSomething(t *testing.T) {
	plan, err := Plan(Options{Port: 8787})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Addrs) == 0 {
		t.Fatal("the plan bound no addresses; the operator could not reach omniplex at all")
	}
	var loopback bool
	for _, a := range plan.Addrs {
		if a.Kind == KindLoopback {
			loopback = true
		}
	}
	if !loopback {
		t.Error("the plan omitted loopback, which must always work")
	}
}

func TestOverrideIsObeyed(t *testing.T) {
	plan, err := Plan(Options{Override: "127.0.0.1:9999"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Addrs) != 1 || !plan.Addrs[0].IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("override bound %v, want only 127.0.0.1", plan.Addrs)
	}
	if plan.Port != 9999 {
		t.Fatalf("port = %d, want 9999", plan.Port)
	}
	if plan.Reachable {
		t.Error("a loopback override must not be considered reachable, or it would demand pairing for nothing")
	}

	// Naming a public address explicitly is still not enough on its own:
	// putting an agent that holds your credentials on the internet is the one
	// decision that has to be made with the flag that means it.
	if _, err := Plan(Options{Override: "8.8.8.8:8787"}); !errors.Is(err, ErrPublicNotAllowed) {
		t.Fatalf("a public -addr without --bind-public gave err = %v, want ErrPublicNotAllowed", err)
	}

	plan, err = Plan(Options{Override: "8.8.8.8:8787", IncludePublic: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Reachable {
		t.Error("a public override must be treated as reachable so pairing is enforced")
	}
}

// A hostname must be resolved to the addresses it names. Passing it through
// unresolved made net.Listen bind ":port" — every interface, including a
// VPS's public one — which is far wider than what was asked for.
func TestHostnameOverrideDoesNotBecomeAWildcard(t *testing.T) {
	plan, err := Plan(Options{Override: "localhost:8787"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Addrs) == 0 {
		t.Fatal("localhost resolved to no addresses")
	}
	for _, a := range plan.Addrs {
		if a.IP == nil {
			t.Fatal("a nil address binds every interface; the hostname must be resolved")
		}
		if a.Kind != KindLoopback {
			t.Fatalf("localhost resolved to %s (%s), want loopback only", a.IP, a.Kind)
		}
	}
	if plan.Reachable {
		t.Error("localhost must not be considered reachable")
	}
}

func TestUnresolvableHostnameIsAnError(t *testing.T) {
	if _, err := Plan(Options{Override: "no-such-host.invalid:8787"}); err == nil {
		t.Fatal("an unresolvable -addr host must fail rather than bind everything")
	}
}

func TestWildcardOverrideFallsBackToAutomatic(t *testing.T) {
	plan, err := Plan(Options{Override: "0.0.0.0:8787"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plan.Addrs {
		if a.Kind == KindPublic {
			t.Fatal("0.0.0.0 must not silently include public addresses")
		}
	}
	if plan.Port != 8787 {
		t.Fatalf("port = %d, want 8787", plan.Port)
	}
}

// The pairing QR should point at the address another device can actually
// reach, preferring an overlay because it works from anywhere.
func TestBestPairingURLPrefersReachableAddresses(t *testing.T) {
	plan := BindPlan{
		Port: 8787,
		Addrs: []Addr{
			{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback},
			{IP: net.ParseIP("192.168.1.20"), Kind: KindPrivate},
			{IP: net.ParseIP("100.101.102.103"), Kind: KindOverlay},
		},
	}
	if got, want := plan.BestPairingURL(), "http://100.101.102.103:8787"; got != want {
		t.Fatalf("BestPairingURL = %q, want %q", got, want)
	}

	plan.Addrs = plan.Addrs[:2]
	if got, want := plan.BestPairingURL(), "http://192.168.1.20:8787"; got != want {
		t.Fatalf("without an overlay, BestPairingURL = %q, want %q", got, want)
	}
}

func TestIPv6URLIsBracketed(t *testing.T) {
	a := Addr{IP: net.ParseIP("fd00::1"), Kind: KindPrivate}
	if got, want := a.URL(8787), "http://[fd00::1]:8787"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestListenOpensAndCloses(t *testing.T) {
	plan := BindPlan{Port: 0, Addrs: []Addr{{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback}}}
	listeners, bound, err := plan.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 1 {
		t.Fatalf("opened %d listeners, want 1", len(listeners))
	}
	if len(bound.Addrs) != 1 {
		t.Fatalf("bound plan has %d addresses, want 1", len(bound.Addrs))
	}
	for _, l := range listeners {
		l.Close()
	}
}

// What is advertised has to be what actually bound. An address that failed to
// bind but stayed in the plan would be printed in the banner and encoded in
// the QR, sending a phone to a port nothing is listening on.
func TestListenReportsOnlyWhatBound(t *testing.T) {
	// 203.0.113.1 is TEST-NET-3: this machine does not hold it, so binding
	// it fails while the loopback address beside it succeeds.
	plan := BindPlan{
		Port: 0,
		Addrs: []Addr{
			{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback},
			{IP: net.ParseIP("203.0.113.1"), Kind: KindPublic},
		},
	}
	listeners, bound, err := plan.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()

	if len(bound.Addrs) != 1 || !bound.Addrs[0].IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("bound plan = %v, want only 127.0.0.1", bound.Addrs)
	}
	if bound.Reachable {
		t.Error("only loopback bound, so the plan must not report itself reachable")
	}
}

// An installed copy binds loopback and the tailnet only: the local network on
// a laptop is whoever else is on the café wifi.
func TestPrivatePlanLeavesOutTheLocalNetwork(t *testing.T) {
	local := []Addr{
		{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback},
		{IP: net.ParseIP("192.168.1.20"), Kind: KindPrivate},
		{IP: net.ParseIP("100.101.102.103"), Kind: KindOverlay},
		{IP: net.ParseIP("203.0.113.9"), Kind: KindPublic},
	}

	plan := choose(local, 8787, true, true)
	var kinds []Kind
	for _, a := range plan.Addrs {
		kinds = append(kinds, a.Kind)
	}
	if len(kinds) != 2 || kinds[0] != KindLoopback || kinds[1] != KindOverlay {
		t.Fatalf("private plan bound %v, want loopback and overlay only", kinds)
	}
	if !plan.Reachable {
		t.Fatal("a tailnet address is reachable from another device; pairing must stay on")
	}

	// Without a tailnet it is loopback alone, and nothing else can reach it.
	if plan := choose(local[:2], 8787, false, true); plan.Reachable || len(plan.Addrs) != 1 {
		t.Fatalf("private plan without an overlay: %+v", plan)
	}

	// The default is unchanged: the local network is bound.
	if plan := choose(local, 8787, false, false); len(plan.Addrs) != 3 {
		t.Fatalf("default plan: %+v", plan.Addrs)
	}
}
