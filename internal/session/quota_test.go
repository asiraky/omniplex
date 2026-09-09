package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// quotaAdapter is a fakeAdapter whose sessions answer quota reads and whose
// out-of-band read is scriptable, so both refresh paths are testable.
type quotaAdapter struct {
	fakeAdapter
	readMu     chan struct{} // guards the fields below
	readErr    error
	readSnap   adapter.QuotaSnapshot
	sessionErr error
}

func (f *quotaAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	s := &quotaFakeSession{fakeSession: &fakeSession{host: host, events: make(chan proto.Emission, 64), prompts: make(chan adapter.PromptInput, 16), actions: make(chan adapter.ComposerActionInput, 16)}}
	f.mu.Lock()
	s.err = f.sessionErr
	f.last = s.fakeSession
	f.mu.Unlock()
	return s, nil
}

func (f *quotaAdapter) ReadQuota(ctx context.Context, env map[string]string) (adapter.QuotaSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return adapter.QuotaSnapshot{}, f.readErr
	}
	return f.readSnap, nil
}

type quotaFakeSession struct {
	*fakeSession
	err error
}

func (s *quotaFakeSession) Quota(ctx context.Context) (adapter.QuotaSnapshot, error) {
	if s.err != nil {
		return adapter.QuotaSnapshot{}, s.err
	}
	return adapter.QuotaSnapshot{
		CheckedAt: time.Now().UnixMilli(),
		Windows:   []adapter.QuotaWindow{{ID: "five_hour", Kind: adapter.QuotaSession, Label: "Session", UsedPercent: pct(30)}},
	}, nil
}

func pct(v float64) *float64 { return &v }

func quotaTestManager(t *testing.T) (*Manager, *quotaAdapter, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &quotaAdapter{}
	return NewManager(st, func(string, ...any) {}, fa), fa, st
}

func TestQuotaSparseMergeKeepsUnmentionedWindows(t *testing.T) {
	mgr, _, _ := quotaTestManager(t)
	full := adapter.QuotaSnapshot{
		CheckedAt: 1000,
		Windows: []adapter.QuotaWindow{
			{ID: "five_hour", Kind: adapter.QuotaSession, Label: "Session", UsedPercent: pct(40), ResetsAt: 5000},
			{ID: "seven_day", Kind: adapter.QuotaWeekly, Label: "Weekly", UsedPercent: pct(70), ResetsAt: 9000},
		},
	}
	mgr.reportQuota("fake", "fake", full, true)

	// A live push that names only the session window, and only its new
	// reading: the weekly window survives untouched, and the session
	// window's reset time survives because the update did not carry one.
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{
		CheckedAt: 2000,
		Windows:   []adapter.QuotaWindow{{ID: "five_hour", UsedPercent: pct(55)}},
	}, false)

	status := mgr.Quotas()[0]
	windows := map[string]adapter.QuotaWindow{}
	for _, w := range status.Snapshot.Windows {
		windows[w.ID] = w
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v, want both after a sparse merge", status.Snapshot.Windows)
	}
	if got := *windows["five_hour"].UsedPercent; got != 55 {
		t.Fatalf("session used = %v, want the sparse update's 55", got)
	}
	if windows["five_hour"].ResetsAt != 5000 {
		t.Fatalf("session reset = %v, want the read's value preserved", windows["five_hour"].ResetsAt)
	}
	if got := *windows["seven_day"].UsedPercent; got != 70 {
		t.Fatalf("weekly used = %v, want the untouched 70", got)
	}
	if status.Snapshot.CheckedAt != 2000 {
		t.Fatalf("checkedAt = %v, want the update's", status.Snapshot.CheckedAt)
	}
}

func TestQuotaFullReadReplacesWindows(t *testing.T) {
	mgr, _, _ := quotaTestManager(t)
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{CheckedAt: 1000, Windows: []adapter.QuotaWindow{
		{ID: "one", UsedPercent: pct(1)}, {ID: "two", UsedPercent: pct(2)},
	}}, true)
	// A provider that stopped reporting "two" drops it, rather than leaving
	// it frozen at a stale reading.
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{CheckedAt: 2000, Windows: []adapter.QuotaWindow{
		{ID: "one", UsedPercent: pct(3)},
	}}, true)
	status := mgr.Quotas()[0]
	if len(status.Snapshot.Windows) != 1 || status.Snapshot.Windows[0].ID != "one" {
		t.Fatalf("windows = %+v, want the read's set exactly", status.Snapshot.Windows)
	}
}

func TestQuotaRefreshFailureKeepsLastGood(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	good := adapter.QuotaSnapshot{CheckedAt: 1000, Plan: "pro", Windows: []adapter.QuotaWindow{
		{ID: "seven_day", Kind: adapter.QuotaWeekly, Label: "Weekly", UsedPercent: pct(70)},
	}}
	fa.mu.Lock()
	fa.readSnap = good
	fa.mu.Unlock()
	if _, err := mgr.RefreshQuota(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}

	fa.mu.Lock()
	fa.readErr = errors.New("codex did not answer")
	fa.mu.Unlock()
	status, err := mgr.RefreshQuota(context.Background(), "fake")
	if err == nil {
		t.Fatal("the refresh must report its failure")
	}
	if status.LastError == "" {
		t.Fatal("the failure must be visible on the status")
	}
	if len(status.Snapshot.Windows) != 1 || *status.Snapshot.Windows[0].UsedPercent != 70 {
		t.Fatalf("snapshot = %+v, want the last good one kept", status.Snapshot)
	}
	if status.Snapshot.Plan != "pro" {
		t.Fatalf("plan = %q, want the last good one kept", status.Snapshot.Plan)
	}
}

func TestQuotaProviderFailureIsolation(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	mgr.ConfigureInstances([]provider.Instance{{ID: "fake-work", Driver: "fake", DisplayName: "Fake Work", Enabled: true}}, nil)

	fa.mu.Lock()
	fa.readSnap = adapter.QuotaSnapshot{CheckedAt: 1, Windows: []adapter.QuotaWindow{{ID: "w", UsedPercent: pct(9)}}}
	fa.mu.Unlock()
	if _, err := mgr.RefreshQuota(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}

	fa.mu.Lock()
	fa.readErr = errors.New("no answer")
	fa.mu.Unlock()
	if _, err := mgr.RefreshQuota(context.Background(), "fake-work"); err == nil {
		t.Fatal("the second instance must fail on its own")
	}
	// And its failure must not have touched the first instance's snapshot.
	statuses := mgr.Quotas()
	byInstance := map[string]QuotaStatus{}
	for _, s := range statuses {
		byInstance[s.Instance] = s
	}
	if byInstance["fake"].LastError != "" {
		t.Fatalf("fake must be unaffected: %+v", byInstance["fake"])
	}
	if len(byInstance["fake"].Snapshot.Windows) != 1 {
		t.Fatalf("fake snapshot = %+v", byInstance["fake"].Snapshot)
	}
	if byInstance["fake-work"].LastError == "" {
		t.Fatalf("fake-work must carry its error: %+v", byInstance["fake-work"])
	}
}

func TestQuotaAccountIsolation(t *testing.T) {
	mgr, _, _ := quotaTestManager(t)
	mgr.ConfigureInstances([]provider.Instance{{ID: "fake-work", Driver: "fake", DisplayName: "Fake Work", Enabled: true}}, nil)

	mgr.reportQuota("fake", "fake-work", adapter.QuotaSnapshot{CheckedAt: 1, Windows: []adapter.QuotaWindow{
		{ID: "w", UsedPercent: pct(10)},
	}}, true)

	byInstance := map[string]QuotaStatus{}
	for _, s := range mgr.Quotas() {
		byInstance[s.Instance] = s
	}
	if len(byInstance["fake"].Snapshot.Windows) != 0 {
		t.Fatalf("the default instance must not see the work account's quota: %+v", byInstance["fake"])
	}
	if len(byInstance["fake-work"].Snapshot.Windows) != 1 {
		t.Fatalf("the work instance must see its own: %+v", byInstance["fake-work"])
	}

	// A sign-out or account switch invalidates the cache outright: the old
	// account's allowance must never be presented under the new one.
	mgr.forgetQuota("fake-work")
	for _, s := range mgr.Quotas() {
		if s.Instance == "fake-work" && len(s.Snapshot.Windows) != 0 {
			t.Fatalf("quota survived forgetQuota: %+v", s)
		}
	}
}

func TestLiveSessionQuotaPushRoutesToInstance(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = actor

	// The session pushes a quota update through its host services; the
	// manager's cache must file it under the instance the session runs
	// under — the default one here.
	fa.mu.Lock()
	s := fa.last
	fa.mu.Unlock()
	reporter := s.host.(adapter.QuotaReporter)
	reporter.ReportQuota(adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli(), Windows: []adapter.QuotaWindow{
		{ID: "five_hour", Kind: adapter.QuotaSession, Label: "Session", UsedPercent: pct(20)},
	}})

	status := mgr.Quotas()[0]
	if status.Instance != "fake" {
		t.Fatalf("instance = %q", status.Instance)
	}
	if len(status.Snapshot.Windows) != 1 || *status.Snapshot.Windows[0].UsedPercent != 20 {
		t.Fatalf("snapshot = %+v, want the pushed window", status.Snapshot)
	}
}

func TestRefreshQuotaPrefersLiveSession(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	// The adapter's out-of-band read is scripted to fail; a live session
	// answering must win over it.
	fa.mu.Lock()
	fa.readErr = errors.New("out-of-band read should not have run")
	fa.mu.Unlock()
	status, err := mgr.RefreshQuota(context.Background(), "fake")
	if err != nil {
		t.Fatalf("the live session should have answered: %v", err)
	}
	if len(status.Snapshot.Windows) != 1 || status.Snapshot.Windows[0].ID != "five_hour" {
		t.Fatalf("snapshot = %+v, want the live session's answer", status.Snapshot)
	}
}

func TestQuotaSubscribeNotifiesOnChange(t *testing.T) {
	mgr, _, _ := quotaTestManager(t)
	id, ch := mgr.SubscribeQuota()
	defer mgr.UnsubscribeQuota(id)
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{CheckedAt: 1}, true)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no quota notification arrived")
	}
}

func TestUsageReportFromDurableLog(t *testing.T) {
	mgr, _, st := quotaTestManager(t)
	meta := store.SessionMeta{ID: "s1", Cwd: t.TempDir(), Harness: "claude", CreatedAt: proto.NowMillis(), UpdatedAt: proto.NowMillis(), Phase: "idle"}
	if err := st.CreateSession(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(context.Background(), "s1", proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{Cwd: meta.Cwd, Harness: "claude", Model: "claude-opus-5"})); err != nil {
		t.Fatal(err)
	}
	// A claude turn: the accounting result, then the occupancy re-emission.
	if _, err := st.Append(context.Background(), "s1", proto.Emit(proto.UsageUpdated, proto.UsageUpdatedPayload{Input: 1_000_000, Output: 1_000_000, Accounting: true})); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(context.Background(), "s1", proto.Emit(proto.UsageUpdated, proto.UsageUpdatedPayload{Input: 1_000_000, Output: 1_000_000})); err != nil {
		t.Fatal(err)
	}

	rep, err := mgr.UsageReport(context.Background(), "24h")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Totals.Input != 1_000_000 {
		t.Fatalf("input = %v, want the turn counted once", rep.Totals.Input)
	}
	if rep.Totals.Cost != 30 { // 1M in + 1M out at $5/$25 per million
		t.Fatalf("cost = %v, want 30", rep.Totals.Cost)
	}
	if len(rep.Rows) != 1 || rep.Rows[0].Model != "claude-opus-5" {
		t.Fatalf("rows = %+v", rep.Rows)
	}
}