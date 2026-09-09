// Codex account usage limits: the app-server's account/rateLimits/read
// request and account/rateLimits/updated notification, normalised onto the
// same window ids so a live update merges onto the rows a read drew.

package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
)

// Window-duration thresholds, in minutes, that turn a window's width into a
// kind. Codex reports the duration; when it does not, the plan says it (paid
// plans run the 5-hour/weekly pair, free and go a single monthly allowance).
const (
	weeklyMins  = 7 * 24 * 60
	monthlyMins = 30 * 24 * 60
)

// codexLimitWindow is one position (primary/secondary) of a rate-limit
// snapshot. Fields are pointers because the updated notification is sparse:
// a field it omits is one the cache keeps.
type codexLimitWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int     `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"` // epoch seconds
}

func (w *codexLimitWindow) quotaWindow(id, labelPrefix string, fallbackMins int) (adapter.QuotaWindow, bool) {
	if w == nil {
		return adapter.QuotaWindow{}, false
	}
	mins := fallbackMins
	if w.WindowDurationMins != nil {
		mins = *w.WindowDurationMins
	}
	kind := kindForDuration(mins)
	out := adapter.QuotaWindow{ID: id, Kind: kind, Label: labelPrefix + kindLabel(kind), WindowMins: mins}
	if w.UsedPercent != nil {
		out.UsedPercent = w.UsedPercent
	}
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		out.ResetsAt = *w.ResetsAt * 1000
	}
	return out, true
}

func kindForDuration(mins int) string {
	switch {
	case mins >= monthlyMins:
		return adapter.QuotaMonthly
	case mins >= weeklyMins:
		return adapter.QuotaWeekly
	default:
		return adapter.QuotaSession
	}
}

func kindLabel(kind string) string {
	switch kind {
	case adapter.QuotaMonthly:
		return "Monthly"
	case adapter.QuotaWeekly:
		return "Weekly"
	default:
		return "Session"
	}
}

// codexLimitSnapshot is one limit's snapshot — the main "codex" allowance or
// a model-specific one (GPT-5.3-Codex-Spark and friends), which report the
// same shape under their own limitId.
type codexLimitSnapshot struct {
	LimitID              string            `json:"limitId"`
	LimitName            string            `json:"limitName"`
	PlanType             string            `json:"planType"`
	Primary              *codexLimitWindow `json:"primary"`
	Secondary            *codexLimitWindow `json:"secondary"`
	RateLimitReachedType string            `json:"rateLimitReachedType"`
}

// windows normalises one snapshot's positions. The ids are limitId-scoped so
// a model-specific limit's windows never collide with the main allowance's.
// primary/secondary are positions, not durations: when the CLI omits the
// width, the plan says it — paid plans run the 5-hour/weekly pair, free and
// go a single monthly allowance.
func (s *codexLimitSnapshot) windows() []adapter.QuotaWindow {
	if s == nil {
		return nil
	}
	prefix := ""
	if s.LimitName != "" {
		prefix = s.LimitName + " · "
	}
	monthlyPlan := s.PlanType == "free" || s.PlanType == "go"
	primaryFallback := 5 * 60
	if monthlyPlan {
		primaryFallback = monthlyMins
	}
	var out []adapter.QuotaWindow
	if w, ok := s.Primary.quotaWindow(s.LimitID+"/primary", prefix, primaryFallback); ok {
		out = append(out, w)
	}
	if w, ok := s.Secondary.quotaWindow(s.LimitID+"/secondary", prefix, weeklyMins); ok {
		out = append(out, w)
	}
	return out
}

// codexRateLimitsRead is the account/rateLimits/read result, narrowed to what
// the quota snapshot reads. Verified against codex-cli 0.153.4.
type codexRateLimitsRead struct {
	RateLimits            *codexLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID   map[string]*codexLimitSnapshot `json:"rateLimitsByLimitId"`
	RateLimitResetCredits *struct {
		AvailableCount int `json:"availableCount"`
		Credits        []struct {
			Status    string `json:"status"`
			ExpiresAt int64  `json:"expiresAt"` // epoch seconds
		} `json:"credits"`
	} `json:"rateLimitResetCredits"`
	AccountID string `json:"accountId"`
}

// parseCodexRead normalises a read result. Every limit the provider reports is
// drawn — the main allowance, model-specific ones, and the reset-credit
// balance — because a bucket omniplex has never heard of must still be
// visible, not silently dropped.
func parseCodexRead(raw json.RawMessage) (adapter.QuotaSnapshot, error) {
	snap := adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli()}
	var res codexRateLimitsRead
	if err := json.Unmarshal(raw, &res); err != nil {
		return snap, fmt.Errorf("parse codex rate limits: %w", err)
	}
	snap.AccountID = res.AccountID

	byID := res.RateLimitsByLimitID
	if len(byID) == 0 && res.RateLimits != nil {
		byID = map[string]*codexLimitSnapshot{res.RateLimits.LimitID: res.RateLimits}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic order for a deterministic cache
	for _, id := range ids {
		s := byID[id]
		snap.Windows = append(snap.Windows, s.windows()...)
		if s != nil && s.PlanType != "" && (snap.Plan == "" || id == "codex") {
			snap.Plan = s.PlanType
		}
	}

	if rc := res.RateLimitResetCredits; rc != nil && rc.AvailableCount > 0 {
		w := adapter.QuotaWindow{
			ID: "credits", Kind: adapter.QuotaCredits, Label: "Reset credits",
			Count: &rc.AvailableCount,
		}
		// The earliest-expiring available credit is the one that matters:
		// after it goes, the balance the row shows drops.
		var earliest int64
		for _, c := range rc.Credits {
			if c.Status != "available" || c.ExpiresAt <= 0 {
				continue
			}
			if earliest == 0 || c.ExpiresAt < earliest {
				earliest = c.ExpiresAt
			}
		}
		if earliest > 0 {
			w.ResetsAt = earliest * 1000
		}
		snap.Windows = append(snap.Windows, w)
	}
	return snap, nil
}

// parseCodexUpdate normalises an account/rateLimits/updated notification into
// a sparse snapshot. The notification carries one limit's snapshot, with
// fields it does not mention simply absent; the ids it does name match the
// ones a read drew, which is what lets the cache merge rather than duplicate.
// The observed shape is the snapshot itself; a wrapper naming rateLimits is
// accepted too, so either generation of the CLI lands on one path.
func parseCodexUpdate(params json.RawMessage) (adapter.QuotaSnapshot, bool) {
	var wrapper struct {
		RateLimits json.RawMessage `json:"rateLimits"`
	}
	_ = json.Unmarshal(params, &wrapper)
	raw := params
	if len(wrapper.RateLimits) > 0 {
		raw = wrapper.RateLimits
	}
	var s codexLimitSnapshot
	if err := json.Unmarshal(raw, &s); err != nil || s.LimitID == "" {
		return adapter.QuotaSnapshot{}, false
	}
	if s.LimitID == "" {
		s.LimitID = "codex" // an older CLI that omits it
	}
	windows := s.windows()
	if len(windows) == 0 {
		return adapter.QuotaSnapshot{}, false
	}
	return adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli(), Plan: s.PlanType, Windows: windows}, true
}

// reportQuota pushes a snapshot at the host's quota cache, when the host
// implements the optional reporter.
func (s *session) reportQuota(snap adapter.QuotaSnapshot) {
	if reporter, ok := s.host.(adapter.QuotaReporter); ok {
		reporter.ReportQuota(snap)
	}
}

// Quota asks the running app-server for the account's rate limits — the
// structured twin of the CLI's /status — over the live connection.
func (s *session) Quota(ctx context.Context) (adapter.QuotaSnapshot, error) {
	var raw json.RawMessage
	if err := s.conn.Call(ctx, "account/rateLimits/read", map[string]any{}, &raw); err != nil {
		return adapter.QuotaSnapshot{}, fmt.Errorf("codex rate limits: %w", err)
	}
	return parseCodexRead(raw)
}

// pushQuota reads the account's limits off the live process and hands them to
// the host — the session-start refresh, and again after completed turns.
func (s *session) pushQuota() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snap, err := s.Quota(ctx)
	if err != nil {
		s.host.Logf("codex quota read: %v", err)
		return
	}
	s.reportQuota(snap)
}

// ReadQuota asks for the account's usage limits without a live session: one
// short app-server run that initialises, reads, and exits. It never starts a
// thread, so no transcript and no turn is created.
func (a *Adapter) ReadQuota(ctx context.Context, env map[string]string) (adapter.QuotaSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.Bin, "app-server")
	cmd.Env = adapter.MergeEnv(os.Environ(), env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return adapter.QuotaSnapshot{}, fmt.Errorf("start %s app-server: %w", a.Bin, err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	conn := jsonrpc.NewConn(stdout, stdin,
		func(context.Context, string, json.RawMessage) (any, error) {
			return nil, fmt.Errorf("this connection only reads usage limits")
		},
		func(string, json.RawMessage) {},
	)

	if err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "omniplex", "version": "0.1.0"},
		"capabilities": map[string]any{},
	}, nil); err != nil {
		return adapter.QuotaSnapshot{}, fmt.Errorf("codex initialize: %w", err)
	}
	if err := conn.Notify("initialized", map[string]any{}); err != nil {
		return adapter.QuotaSnapshot{}, err
	}

	var raw json.RawMessage
	if err := conn.Call(ctx, "account/rateLimits/read", map[string]any{}, &raw); err != nil {
		return adapter.QuotaSnapshot{}, fmt.Errorf("codex account/rateLimits/read: %w", err)
	}
	return parseCodexRead(raw)
}
