package netinfo

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Rebinder keeps the set of bound addresses in step with the machine's.
//
// Addresses are enumerated once, at startup, which is wrong for exactly the
// case that matters most here: a VPN or tailnet interface that comes up
// afterwards. The symptom is silent and confusing — the server is running,
// the tunnel is up, the reverse proxy in front of it is configured correctly,
// and every request still fails to connect, because nothing is listening on
// the address that appeared a few seconds after the process started. Nothing
// short of a restart fixed it, and restarting the server is the one thing you
// cannot casually do to a machine that is running your agents.
//
// So the address set is watched instead of read. New addresses are bound and
// served; addresses that go away have their listeners closed, so what is
// advertised stays true.
type Rebinder struct {
	port          int
	includePublic bool
	interval      time.Duration

	// serve hands a newly opened listener to the HTTP server.
	serve func(net.Listener)
	// local enumerates the machine's addresses. Injectable so a test can
	// drive the logic without binding this machine's real interfaces, which
	// on a developer's laptop means the live server's port.
	local func() ([]Addr, error)
	// changed is called with the new full plan whenever the bound set moves,
	// so the banner's advertised addresses do not go stale.
	changed func(BindPlan)
	logf    func(string, ...any)

	mu    sync.Mutex
	bound map[string]boundAddr
}

type boundAddr struct {
	addr     Addr
	listener net.Listener
}

// NewRebinder watches for address changes on top of an initial bound plan.
func NewRebinder(initial BindPlan, listeners []net.Listener, includePublic bool, serve func(net.Listener), changed func(BindPlan), logf func(string, ...any)) *Rebinder {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	r := &Rebinder{
		port:          initial.Port,
		includePublic: includePublic,
		interval:      15 * time.Second,
		serve:         serve,
		changed:       changed,
		logf:          logf,
		local:         Local,
		bound:         map[string]boundAddr{},
	}
	// Listen returns listeners in plan order, so they pair up positionally.
	for i, a := range initial.Addrs {
		if i < len(listeners) {
			r.bound[a.IP.String()] = boundAddr{addr: a, listener: listeners[i]}
		}
	}
	return r
}

// Run polls until ctx is cancelled.
func (r *Rebinder) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sync()
		}
	}
}

// sync binds addresses that appeared and drops those that went away.
func (r *Rebinder) sync() {
	enumerate := r.local
	if enumerate == nil {
		enumerate = Local
	}
	local, err := enumerate()
	if err != nil {
		return
	}

	want := map[string]Addr{}
	for _, a := range local {
		if a.Kind == KindPublic && !r.includePublic {
			continue
		}
		want[a.IP.String()] = a
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var opened, closed []string

	for key, a := range want {
		if _, ok := r.bound[key]; ok {
			continue
		}
		host := key
		if a.IP.To4() == nil {
			host = "[" + host + "]"
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, r.port))
		if err != nil {
			// Common and not worth shouting about: an address can appear in
			// the interface list a moment before it is usable.
			continue
		}
		r.bound[key] = boundAddr{addr: a, listener: ln}
		opened = append(opened, key)
		if r.serve != nil {
			go r.serve(ln)
		}
	}

	for key, b := range r.bound {
		if _, ok := want[key]; ok {
			continue
		}
		// Loopback is never dropped. If enumeration ever returns nothing —
		// and it can, transiently — closing the last listener would leave the
		// operator with no way back in.
		if b.addr.Kind == KindLoopback {
			continue
		}
		_ = b.listener.Close()
		delete(r.bound, key)
		closed = append(closed, key)
	}

	if len(opened) == 0 && len(closed) == 0 {
		return
	}
	for _, key := range opened {
		r.logf("now also listening on %s:%d", key, r.port)
	}
	for _, key := range closed {
		r.logf("stopped listening on %s:%d (address went away)", key, r.port)
	}
	if r.changed != nil {
		r.changed(r.planLocked())
	}
}

// planLocked renders the currently bound set as a plan.
func (r *Rebinder) planLocked() BindPlan {
	plan := BindPlan{Port: r.port}
	for _, b := range r.bound {
		plan.Addrs = append(plan.Addrs, b.addr)
		if b.addr.Kind != KindLoopback {
			plan.Reachable = true
		}
	}
	sortAddrs(plan.Addrs)
	return plan
}
