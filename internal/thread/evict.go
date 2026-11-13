package thread

import (
	"context"
	"time"

	"github.com/asiraky/omniplex/internal/projection"
)

// Evicting idle actors.
//
// Opening a thread to look at it restores its whole projection into an actor,
// and before this nothing ever let go of one: every thread opened since boot
// stayed in memory until the next restart. An actor restored only for viewing
// holds nothing the log does not, so once nobody is watching it and nobody has
// used it for a while it is dropped, and the next open restores it again —
// from a snapshot taken on the way out, so that costs no replay.
//
// Only view-only actors qualify, and only while they are inert: no harness, no
// one waiting on a human, no turn or queued prompt, and nothing in the thread
// list that would read differently once the actor is gone.

const (
	// idleEvictAfter is how long a view-only actor may sit unused.
	idleEvictAfter = 10 * time.Minute
	// evictSweepEvery is how often the manager looks for one.
	evictSweepEvery = time.Minute
)

// touch records that a caller reached this actor just now.
func (a *Actor) touch() {
	a.mu.Lock()
	a.lastUsed = time.Now()
	a.mu.Unlock()
}

// StartIdleEvictor drops view-only actors nobody has used for idleEvictAfter.
// Called once at startup; Shutdown stops it.
func (m *Manager) StartIdleEvictor() {
	ctx, cancel := context.WithCancel(context.Background())
	m.evictorCancel = cancel
	m.evictorWG.Add(1)
	go func() {
		defer m.evictorWG.Done()
		ticker := time.NewTicker(evictSweepEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				m.evictIdle(now)
			}
		}
	}()
}

// evictIdle asks every actor that looks idle as of now to evict itself. The
// actor decides, in its own loop, since only it can see whether it is inert.
func (m *Manager) evictIdle(now time.Time) {
	m.mu.RLock()
	actors := make([]*Actor, 0, len(m.actors))
	for _, a := range m.actors {
		actors = append(actors, a)
	}
	m.mu.RUnlock()
	for _, a := range actors {
		if !m.idle(a, now) {
			continue
		}
		// Never wait on a busy actor: a full inbox means it is being used,
		// and the next sweep will ask again.
		select {
		case a.inbox <- m.evictCmd(a, now):
		default:
		}
	}
}

// idle reports whether a is a view-only actor with no subscriber that nobody
// has reached for idleEvictAfter as of now.
func (m *Manager) idle(a *Actor, now time.Time) bool {
	if !a.evictable {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.subs) == 0 && now.Sub(a.lastUsed) >= m.idleEvictAfter
}

// evictCmd is the command that evicts a if it is still idle when its loop
// gets to it. The commit forgets the actor under mu, re-checking idleness
// there: get looks up and touches under the same lock, so either a caller got
// the actor before the commit and its touch refuses it, or the actor is gone
// from the map and the caller restores a fresh one. now is the sweep's, so a
// touch any time after the sweep refuses too.
func (m *Manager) evictCmd(a *Actor, now time.Time) command {
	return command{kind: cmdEvict, evict: func() (time.Duration, bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.actors[a.ID] != a || !m.idle(a, now) {
			return 0, false
		}
		delete(m.actors, a.ID)
		a.mu.Lock()
		idle := now.Sub(a.lastUsed)
		a.mu.Unlock()
		return idle, true
	}}
}

// evict stops the actor if it is inert and the manager lets go of it. Runs in
// the actor loop; true means the loop is over.
func (a *Actor) evict(commit func() (time.Duration, bool)) bool {
	if !a.evictable || a.sess != nil || a.preparing || a.turnActive != "" ||
		len(a.pendingPerm)+len(a.pendingElicit) > 0 || len(a.state.Queued) > 0 {
		return false
	}
	a.mu.Lock()
	recovering := a.recovery != nil
	a.mu.Unlock()
	if recovering {
		return false
	}
	// With no actor, the thread list derives attention from the stored phase.
	// Anything the projection knows beyond it — a permission still pending in
	// the log, a subagent still working — would vanish from the list.
	// TurnFailed vanishes too, on purpose: it only classifies a working →
	// idle edge the client sees live, and that edge is long past by now.
	ctx := context.Background()
	meta, err := a.store.Thread(ctx, a.ID)
	if err != nil || a.state.Attention() != projection.AttentionForPhase(meta.Phase) {
		return false
	}
	idle, ok := commit()
	if !ok {
		return false
	}
	// The next open starts from here instead of replaying what came since the
	// last snapshot. Most view-only actors appended nothing, and write none.
	if a.lastSnapAt < a.state.Seq {
		a.snapshot(a.state.Seq)
	}
	// The manager has already forgotten this actor, and nothing the list
	// shows changed. The stored phase is left alone: it is already right for
	// a thread with no process, and may say more than idle (ready, a failed
	// workspace) that shutdown would overwrite.
	a.mu.Lock()
	a.onExit = nil
	a.mu.Unlock()
	a.stop(false)
	a.logf("actor_evicted thread=%s idle_s=%d", a.ID, int64(idle/time.Second))
	return true
}
