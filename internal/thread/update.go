package thread

import (
	"context"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// A server that only runs harnesses through omniplex has nobody to update
// them: Claude Code's own updater runs from its interactive UI, which nobody
// opens here. So the server runs each harness's updater itself, on a timer.

// updatePacing is when the updater runs. first is the delay before the first
// pass after start: long enough that a server restarted over and over while
// it is being worked on does not run an update every time. busyRetry is how
// soon a driver skipped because a session was starting is tried again, at
// most busyAttempts times before it waits for the next pass.
type updatePacing struct {
	first        time.Duration
	interval     time.Duration
	busyRetry    time.Duration
	busyAttempts int
}

var defaultUpdatePacing = updatePacing{
	first:        2 * time.Minute,
	interval:     time.Hour,
	busyRetry:    30 * time.Second,
	busyAttempts: 10,
}

// binaryWatchInterval is how often the watcher looks at each harness binary.
// A look is one stat per driver, so this is about how long a client may show
// the old catalogue after someone runs an update by hand.
const binaryWatchInterval = 30 * time.Second

// UpdateStatus is the last background update of one harness's CLI. At is unix
// milliseconds. From and To are the versions before and after, equal when
// there was nothing newer; Error says why a run failed.
type UpdateStatus struct {
	At    int64  `json:"at"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Error string `json:"error,omitempty"`
}

// StartUpdater runs every harness's own updater shortly after start and then
// hourly, while enabled says so. enabled is asked on every pass so turning
// updates off takes effect without a restart; a pinned CLI is a legitimate
// choice while debugging one. Shutdown stops it.
func (m *Manager) StartUpdater(enabled func() bool) {
	m.updateMu.Lock()
	m.updatesEnabled = enabled
	m.updateMu.Unlock()
	ctx := m.background
	pacing := m.updatePacing
	m.backgroundWG.Add(1)
	go func() {
		defer m.backgroundWG.Done()
		wait := pacing.first
		for {
			if !sleepCtx(ctx, wait) {
				return
			}
			if enabled() {
				m.updatePass(ctx, pacing, enabled)
			}
			wait = pacing.interval
		}
	}()
}

// updatePass updates each driver once. A driver skipped because a session was
// starting is tried again shortly rather than an hour later: on a busy server
// there is nearly always something running, but rarely something starting.
func (m *Manager) updatePass(ctx context.Context, pacing updatePacing, enabled func() bool) {
	pending := append([]string(nil), m.driverOrder...)
	for attempt := 0; len(pending) > 0; attempt++ {
		if attempt > 0 {
			if attempt >= pacing.busyAttempts {
				m.logf("update: still starting sessions on %v; trying again next pass", pending)
				return
			}
			if !sleepCtx(ctx, pacing.busyRetry) || !enabled() {
				return
			}
		}
		var busy []string
		for _, driver := range pending {
			if ctx.Err() != nil {
				return
			}
			if m.updateDriver(ctx, driver) {
				busy = append(busy, driver)
			}
		}
		pending = busy
	}
}

// updateDriver runs one driver's updater, reporting true when it could not
// because a session of that driver was starting. The binary is machine-wide,
// so one instance stands for the driver: the first enabled one that is ready,
// whose environment is a known-good way to run it. A driver with none is
// skipped without a word, since a harness that is not installed has nothing
// to update and that is not a failure.
func (m *Manager) updateDriver(ctx context.Context, driver string) (busy bool) {
	up, ok := m.drivers[driver].(adapter.Updater)
	if !ok {
		return false
	}
	var chosen *registered
	for _, reg := range m.orderedInstances() {
		if reg.inst.Driver != driver || !reg.inst.Enabled || reg.ad == nil {
			continue
		}
		if m.availability(ctx, reg).OK() {
			chosen = &reg
			break
		}
	}
	if chosen == nil {
		return false
	}
	env, err := m.envFor(chosen.inst)
	if err != nil {
		// availability already turned this instance away for the same reason,
		// so this is a race with a config change; the next pass will see it.
		return false
	}

	done, ok := tryBeginUpdate(driver)
	if !ok {
		return true
	}
	result, err := up.Update(ctx, env)
	done()
	if ctx.Err() != nil {
		// Shut down mid-update: the run was cut short by us, not by the
		// harness, and there is no one left to tell.
		return false
	}

	status := UpdateStatus{At: time.Now().UnixMilli(), From: result.From, To: result.To}
	if err != nil {
		status.Error = err.Error()
	}
	previous := m.recordUpdate(driver, status)

	switch {
	case err != nil:
		m.logf("update: %s: %v", driver, err)
		m.notifyHarnesses()
	case result.Changed():
		m.logf("update: %s %s -> %s", driver, result.From, result.To)
		// The new binary has a new catalogue. The caches would notice the
		// binary changed on their next listing anyway; this pushes it to
		// every connected client now instead. Noting the binary first keeps
		// the watcher from acting on the same change a second time.
		m.noteBinary(driver)
		m.invalidateDriver(driver)
	case previous != nil && previous.Error != "":
		// Nothing installed, but the error a client is showing is over.
		m.notifyHarnesses()
	}
	return false
}

func (m *Manager) recordUpdate(driver string, status UpdateStatus) (previous *UpdateStatus) {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	if p, ok := m.updates[driver]; ok {
		previous = &p
	}
	m.updates[driver] = status
	return previous
}

// updatesShown says whether listings carry the last update run. With updates
// off, a status from before they were turned off would read as current: a
// failure nobody is retrying, or a version check that has stopped.
func (m *Manager) updatesShown() bool {
	m.updateMu.Lock()
	enabled := m.updatesEnabled
	m.updateMu.Unlock()
	return enabled != nil && enabled()
}

// updateStatus is a driver's last update run, or nil before its first.
func (m *Manager) updateStatus(driver string) *UpdateStatus {
	m.updateMu.Lock()
	defer m.updateMu.Unlock()
	s, ok := m.updates[driver]
	if !ok {
		return nil
	}
	return &s
}

// StartBinaryWatch notices a harness binary replaced while the server runs,
// by anyone: a `claude update` run by hand, a package manager, another
// server. Listings already compare the binary, but only when a client asks
// for one, so a browser left open would keep the old catalogue until it
// reconnected. The watcher runs whether or not updates are on, since noticing
// an update is not performing one. Shutdown stops it.
func (m *Manager) StartBinaryWatch() {
	interval := m.watchInterval
	m.backgroundWG.Add(1)
	go func() {
		defer m.backgroundWG.Done()
		for {
			m.checkBinaries()
			if !sleepCtx(m.background, interval) {
				return
			}
		}
	}()
}

// checkBinaries looks once at every driver's binary. The first look at a
// driver only records it: there is nothing to compare against yet.
func (m *Manager) checkBinaries() {
	for _, driver := range m.driverOrder {
		if _, ok := m.drivers[driver].(adapter.Executable); !ok {
			continue
		}
		if m.noteBinary(driver) {
			m.logf("harness binary: %s changed on disk; refreshing its catalogue", driver)
			m.invalidateDriver(driver)
		}
	}
}

// noteBinary records the identity of a driver's binary, reporting whether it
// differs from the one recorded before. The binary is machine-wide, so one
// identity stands for every instance of the driver. The stat happens under
// the lock so that the watcher and the updater record in the order they
// looked.
func (m *Manager) noteBinary(driver string) (changed bool) {
	m.binaryMu.Lock()
	defer m.binaryMu.Unlock()
	id := binaryIdentity(m.drivers[driver])
	prev, seen := m.binarySeen[driver]
	m.binarySeen[driver] = id
	return seen && prev != id
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// The start gate keeps updates away from sessions being started. A session
// already running is safe, because its process keeps the binary it started
// with, but one caught mid-start could find the binary half replaced. Starts
// are counted per driver, and an update only begins while none are in
// flight. Starts never wait on an update: one that begins during it gets the
// old binary or the new one, and an updater replaces the binary in a single
// step, so both are whole.
//
// It is package-level because sessions start in Start, Resume and actor
// activation, none of which can see the Manager. Adapters are singletons per
// driver, so one count per driver id is the same thing.
var startGate = struct {
	mu       sync.Mutex
	starting map[string]int
	updating map[string]bool
}{starting: map[string]int{}, updating: map[string]bool{}}

// createSession is the only way the package starts a harness session, so
// that every start is counted by the start gate.
func createSession(ctx context.Context, ad adapter.Adapter, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	driver := ad.ID()
	startGate.mu.Lock()
	startGate.starting[driver]++
	startGate.mu.Unlock()
	defer func() {
		startGate.mu.Lock()
		if startGate.starting[driver]--; startGate.starting[driver] <= 0 {
			delete(startGate.starting, driver)
		}
		startGate.mu.Unlock()
	}()
	return ad.CreateSession(ctx, host, o)
}

// tryBeginUpdate claims a driver for an update, failing while a session of
// that driver is starting or another update of it is running. done releases
// the claim.
func tryBeginUpdate(driver string) (done func(), ok bool) {
	startGate.mu.Lock()
	defer startGate.mu.Unlock()
	if startGate.starting[driver] > 0 || startGate.updating[driver] {
		return nil, false
	}
	startGate.updating[driver] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			startGate.mu.Lock()
			delete(startGate.updating, driver)
			startGate.mu.Unlock()
		})
	}, true
}
