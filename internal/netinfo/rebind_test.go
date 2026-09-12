package netinfo

import (
	"net"
	"testing"
)

// listenLoopback opens a real listener so the rebinder has something closable.
func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// The rebinder drops addresses that go away, but dropping loopback would
// leave the operator with no way back into a running server — and interface
// enumeration can come back empty transiently, so this is a real path, not a
// hypothetical one.
func TestSyncNeverClosesLoopback(t *testing.T) {
	r := &Rebinder{
		port:  8788,
		bound: map[string]boundAddr{},
		logf:  func(string, ...any) {},
		// Only loopback exists as far as this test is concerned. Enumerating
		// the real machine here would bind its actual addresses on the real
		// port, which is the running server's.
		local: func() ([]Addr, error) {
			return []Addr{{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback}}, nil
		},
	}
	loop := listenLoopback(t)
	r.bound["127.0.0.1"] = boundAddr{
		addr:     Addr{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback},
		listener: loop,
	}
	// An address that is definitely not in the machine's real interface list,
	// so sync will want to drop it.
	gone := listenLoopback(t)
	r.bound["10.255.255.254"] = boundAddr{
		addr:     Addr{IP: net.ParseIP("10.255.255.254"), Kind: KindPrivate},
		listener: gone,
	}

	r.sync()

	if _, ok := r.bound["127.0.0.1"]; !ok {
		t.Fatal("loopback was dropped; the operator would have no way in")
	}
	if _, ok := r.bound["10.255.255.254"]; ok {
		t.Error("an address that went away should have been dropped")
	}
	// The listener for a dropped address must actually be closed, or the
	// server keeps serving on something it no longer advertises.
	if err := gone.Close(); err == nil {
		t.Error("the dropped listener should already have been closed")
	}
}

// The plan the rebinder reports is what the banner and the client are told,
// so it has to describe what is bound now rather than what was bound at boot.
func TestPlanLockedReportsWhatIsBound(t *testing.T) {
	r := &Rebinder{port: 8788, bound: map[string]boundAddr{}}
	r.bound["127.0.0.1"] = boundAddr{addr: Addr{IP: net.ParseIP("127.0.0.1"), Kind: KindLoopback}}

	if plan := r.planLocked(); plan.Reachable {
		t.Error("loopback alone is not reachable from another machine")
	}

	r.bound["10.8.0.4"] = boundAddr{addr: Addr{IP: net.ParseIP("10.8.0.4"), Kind: KindPrivate}}
	plan := r.planLocked()
	if !plan.Reachable {
		t.Error("a private address makes the server reachable")
	}
	if len(plan.Addrs) != 2 {
		t.Fatalf("got %d addresses, want 2", len(plan.Addrs))
	}
	// Nearest first, so the banner reads outwards from this machine.
	if plan.Addrs[0].Kind != KindLoopback {
		t.Errorf("first address is %s, want loopback first", plan.Addrs[0].Kind)
	}
}
