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
	"github.com/asiraky/omniplex/internal/proto"
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

// mainLimitID is the limitId of the account's main allowance. Every other
// limit is model-specific.
const mainLimitID = "codex"

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
	snap := adapter.QuotaSnapshot{Full: true, CheckedAt: time.Now().UnixMilli()}
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
		if s != nil && s.PlanType != "" && (snap.Plan == "" || id == mainLimitID) {
			snap.Plan = s.PlanType
		}
	}

	if rc := res.RateLimitResetCredits; rc != nil {
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
	s, ok := updateSnapshot(params)
	if !ok {
		return adapter.QuotaSnapshot{}, false
	}
	windows := s.windows()
	for i := range windows {
		w := s.Primary
		if windows[i].ID == s.LimitID+"/secondary" {
			w = s.Secondary
		}
		if w.WindowDurationMins == nil {
			windows[i].WindowMins = 0
			windows[i].Kind = ""
		}
		// A partial name or duration cannot reconstruct the previous label.
		if s.LimitName == "" || w.WindowDurationMins == nil {
			windows[i].Label = ""
		}
	}
	if len(windows) == 0 {
		return adapter.QuotaSnapshot{}, false
	}
	return adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli(), Plan: s.PlanType, Windows: windows}, true
}

// updateSnapshot decodes the one limit an account/rateLimits/updated
// notification carries, in either of its shapes.
func updateSnapshot(params json.RawMessage) (*codexLimitSnapshot, bool) {
	var wrapper struct {
		RateLimits json.RawMessage `json:"rateLimits"`
	}
	_ = json.Unmarshal(params, &wrapper)
	raw := params
	if len(wrapper.RateLimits) > 0 {
		raw = wrapper.RateLimits
	}
	var s codexLimitSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	if s.LimitID == "" {
		s.LimitID = mainLimitID // an older CLI that omits it
	}
	return &s, true
}

// mergeLimits folds an update or a read onto the session's snapshot of the
// main allowance, and reports whether it did. A model-specific limit is never
// merged: the reset a limited turn waits for is the main allowance's. A window
// the update names replaces the old one whole — a reset carried over from an
// earlier window would name a time that has already passed.
func mergeLimits(prev, update *codexLimitSnapshot) (*codexLimitSnapshot, bool) {
	if update == nil || (update.LimitID != "" && update.LimitID != mainLimitID) {
		return prev, false
	}
	if prev == nil {
		next := *update
		return &next, true
	}
	next := *prev
	if update.LimitID != "" {
		next.LimitID = update.LimitID
	}
	if update.PlanType != "" {
		next.PlanType = update.PlanType
	}
	if update.RateLimitReachedType != "" {
		next.RateLimitReachedType = update.RateLimitReachedType
	}
	if update.Primary != nil {
		next.Primary = update.Primary
	}
	if update.Secondary != nil {
		next.Secondary = update.Secondary
	}
	return &next, true
}

// resetAt is when a limited turn can run again, in epoch ms: the latest reset
// among the exhausted windows, since every one of them must reopen first.
// Zero when nothing is exhausted, or when an exhausted window does not say
// when it resets — a guess from the others would wake the turn into the same
// limit.
func (s *codexLimitSnapshot) resetAt() int64 {
	if s == nil {
		return 0
	}
	var latest int64
	exhausted := false
	for _, w := range []*codexLimitWindow{s.Primary, s.Secondary} {
		if w == nil || w.UsedPercent == nil || *w.UsedPercent < 100 {
			continue
		}
		exhausted = true
		if w.ResetsAt == nil || *w.ResetsAt <= 0 {
			return 0
		}
		latest = max(latest, *w.ResetsAt*1000)
	}
	if !exhausted {
		return 0
	}
	return latest
}

// mergeUpdate folds a rateLimits/updated notification onto the session's
// snapshot.
func (s *session) mergeUpdate(params json.RawMessage) {
	update, ok := updateSnapshot(params)
	if !ok {
		return
	}
	s.mu.Lock()
	s.applyLimitsLocked(update)
	fill := s.takeResetLocked()
	s.mu.Unlock()
	s.emitReset(fill)
}

// applyLimitsLocked merges a snapshot under s.mu. Only a merge that applied
// counts as one: a model-specific update changes nothing a read in flight
// would overwrite, so it must not make that read yield.
func (s *session) applyLimitsLocked(update *codexLimitSnapshot) {
	if next, ok := mergeLimits(s.limits, update); ok {
		s.limits = next
		s.limitsGen++
	}
}

// awaitReset remembers a turn that finished on a limit with its reset
// unknown, so the first snapshot that says when it reopens can back-fill it.
// The snapshot may already say: an update can land between the turn's finish
// reading it and this.
func (s *session) awaitReset(turn string) {
	s.mu.Lock()
	// A turn that has since started makes the old one's reset moot.
	if s.turnID == "" {
		s.resetTurn = turn
	}
	fill := s.takeResetLocked()
	s.mu.Unlock()
	s.emitReset(fill)
}

// takeResetLocked hands over the remembered turn's back-fill, under s.mu,
// once the snapshot knows its reset. Once only: the turn is forgotten.
func (s *session) takeResetLocked() proto.TurnLimitResetPayload {
	if s.resetTurn == "" {
		return proto.TurnLimitResetPayload{}
	}
	reset := s.limits.resetAt()
	if reset == 0 {
		return proto.TurnLimitResetPayload{}
	}
	fill := proto.TurnLimitResetPayload{TurnID: s.resetTurn, ResetAt: reset}
	s.resetTurn = ""
	return fill
}

func (s *session) emitReset(fill proto.TurnLimitResetPayload) {
	if fill.TurnID != "" {
		s.emit(proto.Emit(proto.TurnLimitReset, fill))
	}
}

// limitResetAt is resetAt over the session's current snapshot.
func (s *session) limitResetAt() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limits.resetAt()
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
	raw, err := s.readLimits(ctx)
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}
	return parseCodexRead(raw)
}

// readLimits calls account/rateLimits/read and merges the main allowance onto
// the session's snapshot. An update that landed while the read was in flight
// is newer than the read, so the read then yields to it.
func (s *session) readLimits(ctx context.Context) (json.RawMessage, error) {
	s.mu.Lock()
	gen := s.limitsGen
	s.mu.Unlock()
	var raw json.RawMessage
	if err := s.conn.Call(ctx, "account/rateLimits/read", map[string]any{}, &raw); err != nil {
		return nil, fmt.Errorf("codex rate limits: %w", err)
	}
	var res codexRateLimitsRead
	if json.Unmarshal(raw, &res) == nil {
		read := res.RateLimitsByLimitID[mainLimitID]
		if read == nil {
			read = res.RateLimits
		}
		s.mu.Lock()
		if s.limitsGen == gen {
			s.applyLimitsLocked(read)
		}
		fill := s.takeResetLocked()
		s.mu.Unlock()
		s.emitReset(fill)
	}
	return raw, nil
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
		nil,
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
