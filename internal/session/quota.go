package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/usage"
)

// QuotaStatus is one provider instance's usage limits as presented to a UI:
// the last good snapshot, plus how the last refresh attempt went. A failed
// refresh never blanks the snapshot — it stays on screen, and the error and
// its timestamp say how much to trust it.
type QuotaStatus struct {
	Provider    string                `json:"provider"`
	Instance    string                `json:"instance"`
	DisplayName string                `json:"displayName"`
	Snapshot    adapter.QuotaSnapshot `json:"snapshot"`
	// LastError explains the most recent failed refresh, if any.
	LastError string `json:"lastError,omitempty"`
	// LastAttempt is when the quota was last observed, successful or not.
	LastAttempt int64 `json:"lastAttempt,omitempty"`
}

// quotaTimeout bounds one quota read. The adapters apply their own deadlines;
// this is the backstop, and it is generous because a refresh can wait on a
// cold CLI start.
const quotaTimeout = 60 * time.Second

// Quota asks the live harness process for the account's usage limits, when
// it supports the question. Routed through the actor loop like every other
// harness call, so it never races a close or a resume.
func (a *Actor) Quota(ctx context.Context) (adapter.QuotaSnapshot, error) {
	v, err := a.call(ctx, command{kind: cmdQuota})
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}
	return v.(adapter.QuotaSnapshot), nil
}

// Quotas lists every provider instance's cached usage limits, in registration
// order. Instances that have never reported anything are listed with an empty
// snapshot: the Limits page says "unknown" rather than hiding a provider.
func (m *Manager) Quotas() []QuotaStatus {
	out := make([]QuotaStatus, 0, len(m.instanceOrder))
	for _, id := range m.instanceOrder {
		reg := m.instances[id]
		m.quotaMu.Lock()
		cached, ok := m.quotas[reg.inst.ID]
		var status QuotaStatus
		if ok {
			status = *cached
		} else {
			status = QuotaStatus{Provider: reg.inst.Driver, Instance: reg.inst.ID, DisplayName: reg.inst.DisplayName}
		}
		m.quotaMu.Unlock()
		out = append(out, status)
	}
	return out
}

// reportQuota caches one instance's snapshot. A full snapshot replaces the
// windows wholesale — a window the provider stopped reporting is gone, not
// lingering at a stale reading. A sparse one merges window-by-window, so a
// live update that names one window cannot drop the others.
func (m *Manager) reportQuota(driver, instance string, snap adapter.QuotaSnapshot, full bool) {
	m.quotaMu.Lock()
	status, ok := m.quotas[instance]
	if !ok {
		status = &QuotaStatus{Provider: driver, Instance: instance}
		if reg, known := m.instances[instance]; known {
			status.DisplayName = reg.inst.DisplayName
		}
		m.quotas[instance] = status
	}
	if full {
		status.Snapshot = snap
	} else {
		merged := status.Snapshot
		merged.CheckedAt = snap.CheckedAt
		if snap.Plan != "" {
			merged.Plan = snap.Plan
		}
		if snap.AccountID != "" {
			merged.AccountID = snap.AccountID
		}
		for _, w := range snap.Windows {
			replaced := false
			for i := range merged.Windows {
				if merged.Windows[i].ID != w.ID {
					continue
				}
				// Sparse: each field moves only when the update carried it.
				if w.UsedPercent != nil {
					merged.Windows[i].UsedPercent = w.UsedPercent
				}
				if w.ResetsAt > 0 {
					merged.Windows[i].ResetsAt = w.ResetsAt
				}
				if w.Kind != "" {
					merged.Windows[i].Kind = w.Kind
				}
				if w.Label != "" {
					merged.Windows[i].Label = w.Label
				}
				if w.WindowMins > 0 {
					merged.Windows[i].WindowMins = w.WindowMins
				}
				if w.Count != nil {
					merged.Windows[i].Count = w.Count
				}
				replaced = true
				break
			}
			if !replaced {
				merged.Windows = append(merged.Windows, w)
			}
		}
		if len(merged.Windows) > 0 {
			merged.Unavailable = ""
		}
		status.Snapshot = merged
	}
	if len(status.Snapshot.Windows) > 0 || status.Snapshot.Unavailable != "" {
		status.LastError = ""
	}
	status.LastAttempt = snap.CheckedAt
	m.quotaMu.Unlock()
	m.notifyQuota()
}

// forgetQuota drops one instance's cached quota. Called when a probe shows
// the instance answering as a different account: the old account's allowance
// must never be presented under the new one's name.
func (m *Manager) forgetQuota(instanceID string) {
	m.quotaMu.Lock()
	_, had := m.quotas[instanceID]
	delete(m.quotas, instanceID)
	m.quotaMu.Unlock()
	if had {
		m.notifyQuota()
	}
}

// RefreshQuota re-reads one instance's usage limits: from a live session of
// that instance when one is running, else by asking the harness out-of-band.
// A failure keeps the last good snapshot and records the error against it, so
// one provider failing never blanks the other.
func (m *Manager) RefreshQuota(ctx context.Context, instanceID string) (QuotaStatus, error) {
	reg, ok := m.instances[instanceID]
	if !ok {
		return QuotaStatus{}, fmt.Errorf("unknown provider instance %q", instanceID)
	}

	snap, err := m.readQuota(ctx, reg)

	m.quotaMu.Lock()
	status, had := m.quotas[instanceID]
	if !had {
		status = &QuotaStatus{Provider: reg.inst.Driver, Instance: instanceID, DisplayName: reg.inst.DisplayName}
		m.quotas[instanceID] = status
	}
	status.LastAttempt = time.Now().UnixMilli()
	if err != nil {
		status.LastError = err.Error()
	} else {
		status.LastError = ""
		status.Snapshot = snap
	}
	out := *status
	m.quotaMu.Unlock()
	m.notifyQuota()
	return out, err
}

// ErrNoLiveQuota says there is no live session to ask, which is a routing
// outcome rather than a failure: the caller falls back to the adapter.
var ErrNoLiveQuota = errors.New("no live session for this instance")

// readQuota performs one quota read, preferring a live session of the
// instance — the process is already authenticated as the account the quota
// belongs to — and falling back to the adapter's out-of-band read.
func (m *Manager) readQuota(ctx context.Context, reg registered) (adapter.QuotaSnapshot, error) {
	snap, err := m.readQuotaFromLive(ctx, reg)
	if err == nil {
		return snap, nil
	}
	if !errors.Is(err, ErrNoLiveQuota) {
		return adapter.QuotaSnapshot{}, err
	}
	reader, ok := reg.ad.(adapter.QuotaReader)
	if !ok {
		return adapter.QuotaSnapshot{}, fmt.Errorf("%s does not report usage limits", reg.inst.DisplayName)
	}
	env, err := m.envFor(reg.inst)
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	return reader.ReadQuota(readCtx, env)
}

func (m *Manager) readQuotaFromLive(ctx context.Context, reg registered) (adapter.QuotaSnapshot, error) {
	m.mu.RLock()
	actors := make([]*Actor, 0, len(m.actors))
	for _, a := range m.actors {
		actors = append(actors, a)
	}
	m.mu.RUnlock()

	for _, a := range actors {
		meta, err := m.store.Session(ctx, a.ID)
		if err != nil {
			continue
		}
		instance := meta.ProviderInstance
		if instance == "" {
			instance = meta.Harness
		}
		if instance != reg.inst.ID {
			continue
		}
		quotaCtx, cancel := context.WithTimeout(ctx, quotaTimeout)
		snap, err := a.Quota(quotaCtx)
		cancel()
		if err == nil {
			return snap, nil
		}
		if errors.Is(err, ErrNotReady) || errors.Is(err, ErrClosed) {
			continue // this process is not live; try the next session
		}
		return adapter.QuotaSnapshot{}, err
	}
	return adapter.QuotaSnapshot{}, ErrNoLiveQuota
}

// UsageReport aggregates the durable event log into an account-level usage
// report for one range. All of the work — the query, the per-harness
// accounting semantics, the pricing — happens server-side; what travels is
// the bucketed result.
func (m *Manager) UsageReport(ctx context.Context, rng string) (usage.Report, error) {
	spec, err := usage.RangeFor(rng)
	if err != nil {
		return usage.Report{}, err
	}
	now := time.Now()
	rows, err := m.store.UsageEvents(ctx, now.Add(-spec.Window).UnixMilli())
	if err != nil {
		return usage.Report{}, err
	}
	return usage.Aggregate(rows, spec, now), nil
}

// ---- quota change notifications ----

func (m *Manager) SubscribeQuota() (string, chan struct{}) {
	id := uuid.NewString()
	ch := make(chan struct{}, 1)
	m.listMu.Lock()
	m.quotaSub[id] = ch
	m.listMu.Unlock()
	return id, ch
}

func (m *Manager) UnsubscribeQuota(id string) {
	m.listMu.Lock()
	delete(m.quotaSub, id)
	m.listMu.Unlock()
}

func (m *Manager) notifyQuota() {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	for _, ch := range m.quotaSub {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
