package thread

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/usage"
)

// quotaAdapter is a fakeAdapter whose threads answer quota reads and whose
// out-of-band read is scriptable, so both refresh paths are testable.
type quotaAdapter struct {
	fakeAdapter
	readMu    chan struct{} // guards the fields below
	readErr   error
	readSnap  adapter.QuotaSnapshot
	threadErr error
}

func (f *quotaAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	s := &quotaFakeThread{fakeThread: &fakeThread{host: host, events: make(chan proto.Emission, 64), prompts: make(chan adapter.PromptInput, 16), actions: make(chan adapter.ComposerActionInput, 16)}}
	f.mu.Lock()
	s.err = f.threadErr
	f.last = s.fakeThread
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

type quotaFakeThread struct {
	*fakeThread
	err error
}

func (s *quotaFakeThread) Quota(ctx context.Context) (adapter.QuotaSnapshot, error) {
	if s.err != nil {
		return adapter.QuotaSnapshot{}, s.err
	}
	return adapter.QuotaSnapshot{
		CheckedAt: time.Now().UnixMilli(),
		Windows:   []adapter.QuotaWindow{{ID: "five_hour", Kind: adapter.QuotaSession, Label: "Thread", UsedPercent: pct(30)}},
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
			{ID: "five_hour", Kind: adapter.QuotaSession, Label: "Thread", UsedPercent: pct(40), ResetsAt: 5000},
			{ID: "seven_day", Kind: adapter.QuotaWeekly, Label: "Weekly", UsedPercent: pct(70), ResetsAt: 9000},
		},
	}
	mgr.reportQuota("fake", "fake", full, true)

	// A live push that names only the thread window, and only its new
	// reading: the weekly window survives untouched, and the thread
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
		t.Fatalf("thread used = %v, want the sparse update's 55", got)
	}
	if windows["five_hour"].ResetsAt != 5000 {
		t.Fatalf("thread reset = %v, want the read's value preserved", windows["five_hour"].ResetsAt)
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

func TestLiveThreadQuotaPushRoutesToInstance(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = actor

	// The thread pushes a quota update through its host services; the
	// manager's cache must file it under the instance the thread runs
	// under — the default one here.
	fa.mu.Lock()
	s := fa.last
	fa.mu.Unlock()
	reporter := s.host.(adapter.QuotaReporter)
	reporter.ReportQuota(adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli(), Windows: []adapter.QuotaWindow{
		{ID: "five_hour", Kind: adapter.QuotaSession, Label: "Thread", UsedPercent: pct(20)},
	}})

	status := mgr.Quotas()[0]
	if status.Instance != "fake" {
		t.Fatalf("instance = %q", status.Instance)
	}
	if len(status.Snapshot.Windows) != 1 || *status.Snapshot.Windows[0].UsedPercent != 20 {
		t.Fatalf("snapshot = %+v, want the pushed window", status.Snapshot)
	}
}

func TestRefreshQuotaPrefersLiveThread(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	// The adapter's out-of-band read is scripted to fail; a live thread
	// answering must win over it.
	fa.mu.Lock()
	fa.readErr = errors.New("out-of-band read should not have run")
	fa.mu.Unlock()
	status, err := mgr.RefreshQuota(context.Background(), "fake")
	if err != nil {
		t.Fatalf("the live thread should have answered: %v", err)
	}
	if len(status.Snapshot.Windows) != 1 || status.Snapshot.Windows[0].ID != "five_hour" {
		t.Fatalf("snapshot = %+v, want the live thread's answer", status.Snapshot)
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

func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUsageReportReadsEveryAccountsTranscripts(t *testing.T) {
	mgr, _, _ := quotaTestManager(t)
	dir := t.TempDir()
	work, personal, codex, piHome := filepath.Join(dir, "work"), filepath.Join(dir, "personal"), filepath.Join(dir, "codex"), filepath.Join(dir, "pi")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	mgr.applyInstances([]provider.Instance{
		{ID: "work", Driver: "claude", Enabled: true, Env: []provider.EnvVar{{Name: "CLAUDE_CONFIG_DIR", Value: work}}},
		{ID: "personal", Driver: "claude", Enabled: true, Env: []provider.EnvVar{{Name: "CLAUDE_CONFIG_DIR", Value: personal}}},
		{ID: "oai", Driver: "codex", Enabled: true, Env: []provider.EnvVar{{Name: "CODEX_HOME", Value: codex}}},
		// An account given its own home writes under it, not the server's.
		{ID: "pi", Driver: "pi", Enabled: true, Env: []provider.EnvVar{{Name: "HOME", Value: piHome}}},
	})

	ts := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	claude := func(id string, in, out int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"r-%s","message":{"id":%q,"model":"claude-opus-5","usage":{"input_tokens":%d,"output_tokens":%d}}}`, ts, id, id, in, out)
	}
	writeTranscript(t, filepath.Join(work, "projects", "p", "s.jsonl"), claude("a", 1_000_000, 0))
	writeTranscript(t, filepath.Join(work, "projects", "p", "s", "subagents", "agent-1.jsonl"), claude("b", 0, 1_000_000))
	writeTranscript(t, filepath.Join(personal, "projects", "q", "s.jsonl"), claude("c", 1_000_000, 0))
	writeTranscript(t, filepath.Join(codex, "archived_sessions", "rollout.jsonl"),
		fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5.4"}}`, ts),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000},"last_token_usage":{"input_tokens":1000000}}}}`, ts))

	writeTranscript(t, filepath.Join(piHome, ".pi", "agent", "sessions", "--p--", "s.jsonl"),
		fmt.Sprintf(`{"type":"message","id":"e1","timestamp":%q,"message":{"role":"assistant","model":"m","responseId":"x","usage":{"input":7,"cost":{"total":0.5}}}}`, ts))

	rep, err := mgr.UsageReport(context.Background(), "24h")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Totals.Input != 3_000_007 || rep.Totals.Output != 1_000_000 {
		t.Fatalf("totals = %+v, want both Claude accounts, the subagent, archived Codex and Pi", rep.Totals)
	}
	want := usage.Price("claude-opus-5", usage.Counts{Input: 2_000_000, Output: 1_000_000}).CostUSD +
		usage.Price("gpt-5.4", usage.Counts{Input: 1_000_000}).CostUSD + 0.5
	if math.Abs(rep.Totals.Cost-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", rep.Totals.Cost, want)
	}
}

func TestQuotaPublishedSnapshotIsImmutable(t *testing.T) {
	mgr, _, _ := quotaTestManager(t)
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{CheckedAt: 1, Windows: []adapter.QuotaWindow{{ID: "w", UsedPercent: pct(1)}, {ID: "other", UsedPercent: pct(2)}}}, true)
	before := mgr.Quotas()[0]
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{CheckedAt: 2, Windows: []adapter.QuotaWindow{{ID: "w", UsedPercent: pct(3)}}}, false)
	if *before.Snapshot.Windows[0].UsedPercent != 1 {
		t.Fatal("published snapshot mutated")
	}
	after := mgr.Quotas()[0]
	if after.Snapshot.Windows[1].CheckedAt != 1 {
		t.Fatal("untouched window age renewed")
	}
}

func TestQuotaAccountSwitchRejectsOldThreadAndDropsWindows(t *testing.T) {
	mgr, fa, _ := quotaTestManager(t)
	if _, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	fa.mu.Lock()
	reporter := fa.last.host.(adapter.QuotaReporter)
	fa.mu.Unlock()
	reporter.ReportQuota(adapter.QuotaSnapshot{Full: true, AccountID: "a", Windows: []adapter.QuotaWindow{{ID: "old"}}})
	mgr.forgetQuota("fake")
	reporter.ReportQuota(adapter.QuotaSnapshot{AccountID: "a", Windows: []adapter.QuotaWindow{{ID: "old"}}})
	if len(mgr.Quotas()[0].Snapshot.Windows) != 0 {
		t.Fatal("old thread repopulated new account")
	}
	fa.mu.Lock()
	fa.readSnap = adapter.QuotaSnapshot{AccountID: "b", Windows: []adapter.QuotaWindow{{ID: "new"}}}
	fa.mu.Unlock()
	status, err := mgr.RefreshQuota(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	if status.Snapshot.AccountID != "b" {
		t.Fatalf("read old live thread: %+v", status)
	}
	mgr.reportQuota("fake", "fake", adapter.QuotaSnapshot{AccountID: "c", Windows: []adapter.QuotaWindow{{ID: "third"}}}, false)
	if got := mgr.Quotas()[0].Snapshot.Windows; len(got) != 1 || got[0].ID != "third" {
		t.Fatalf("mixed accounts: %+v", got)
	}
}
