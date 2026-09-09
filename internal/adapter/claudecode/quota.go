// Claude account usage limits: the structured data behind the CLI's /usage,
// read through the SDK's get_usage control method, plus the rate-limit events
// the CLI streams while a turn runs. Both normalise onto the same window ids,
// so a streamed event merges onto the row the read established.

package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// claudeUsageResponse is the SDK's get_usage response, narrowed to the fields
// the quota snapshot reads. The SDK calls this API experimental; the shapes
// here are structural so a renamed method is the only thing that can break.
type claudeUsageResponse struct {
	RateLimitsAvailable bool `json:"rate_limits_available"`
	RateLimits          *struct {
		FiveHour          *claudeLimitWindow   `json:"five_hour"`
		SevenDay          *claudeLimitWindow   `json:"seven_day"`
		SevenDayOpus      *claudeLimitWindow   `json:"seven_day_opus"`
		SevenDaySonnet    *claudeLimitWindow   `json:"seven_day_sonnet"`
		SevenDayOAuthApps *claudeLimitWindow   `json:"seven_day_oauth_apps"`
		ModelScoped       []claudeScopedWindow `json:"model_scoped"`
		ExtraUsage        *claudeExtraUsage    `json:"extra_usage"`
	} `json:"rate_limits"`
	SubscriptionType string `json:"subscription_type"`
}

type claudeLimitWindow struct {
	Utilization *float64 `json:"utilization"` // 0-100
	ResetsAt    string   `json:"resets_at"`   // ISO 8601
}

type claudeScopedWindow struct {
	DisplayName string   `json:"display_name"`
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type claudeExtraUsage struct {
	IsEnabled   bool     `json:"is_enabled"`
	Utilization *float64 `json:"utilization"`
}

// isoToMillis parses the ISO timestamps the read response carries. Zero when
// absent or unparseable — an unknown reset is rendered as such, not guessed.
func isoToMillis(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

func pct(v float64) *float64 { return &v }

// The per-family weeklies are pointers that may be absent; these keep the
// row-drawing calls free of nil guards.
func utilOf(w *claudeLimitWindow) *float64 {
	if w == nil {
		return nil
	}
	return w.Utilization
}

func resetOf(w *claudeLimitWindow) string {
	if w == nil {
		return ""
	}
	return w.ResetsAt
}

func window(id, kind, label string, u *float64, resetsAt string, mins int) (adapter.QuotaWindow, bool) {
	if u == nil {
		return adapter.QuotaWindow{}, false
	}
	w := adapter.QuotaWindow{ID: id, Kind: kind, Label: label, UsedPercent: pct(*u), WindowMins: mins}
	if r := isoToMillis(resetsAt); r > 0 {
		w.ResetsAt = r
	}
	return w, true
}

// scopedSlug turns a model display name into a stable window id: the streamed
// events cannot name the model, so the id is derived the same way every read
// derives it, and both land on one row.
func scopedSlug(name string) string {
	lower := strings.ToLower(name)
	return "seven_day_" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, lower)
}

// parseClaudeUsage normalises a get_usage response. A response whose
// rate_limits_available is false is a legible negative — an API-key or
// third-party session has no plan limits — and comes back as an unsupported
// snapshot rather than an error.
func parseClaudeUsage(raw json.RawMessage) (adapter.QuotaSnapshot, error) {
	snap := adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli()}
	var res claudeUsageResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return snap, fmt.Errorf("parse claude usage: %w", err)
	}
	snap.Plan = res.SubscriptionType
	if !res.RateLimitsAvailable || res.RateLimits == nil {
		snap.Unavailable = "unsupported"
		return snap, nil
	}
	rl := res.RateLimits
	add := func(w adapter.QuotaWindow, ok bool) {
		if ok {
			snap.Windows = append(snap.Windows, w)
		}
	}
	add(window("five_hour", adapter.QuotaSession, "Session", rl.FiveHour.Utilization, rl.FiveHour.ResetsAt, 5*60))
	add(window("seven_day", adapter.QuotaWeekly, "Weekly", rl.SevenDay.Utilization, rl.SevenDay.ResetsAt, 7*24*60))
	add(window("seven_day_opus", adapter.QuotaWeekly, "Weekly · Opus", utilOf(rl.SevenDayOpus), resetOf(rl.SevenDayOpus), 7*24*60))
	add(window("seven_day_sonnet", adapter.QuotaWeekly, "Weekly · Sonnet", utilOf(rl.SevenDaySonnet), resetOf(rl.SevenDaySonnet), 7*24*60))
	add(window("seven_day_oauth_apps", adapter.QuotaWeekly, "Weekly · Connected apps", utilOf(rl.SevenDayOAuthApps), resetOf(rl.SevenDayOAuthApps), 7*24*60))
	for _, m := range rl.ModelScoped {
		if w, ok := window(scopedSlug(m.DisplayName), adapter.QuotaWeekly, "Weekly · "+m.DisplayName, m.Utilization, m.ResetsAt, 7*24*60); ok {
			snap.Windows = append(snap.Windows, w)
		}
	}
	if rl.ExtraUsage != nil && rl.ExtraUsage.IsEnabled && rl.ExtraUsage.Utilization != nil {
		snap.Windows = append(snap.Windows, adapter.QuotaWindow{
			ID: "extra_usage", Kind: adapter.QuotaMonthly, Label: "Extra usage (monthly)",
			UsedPercent: pct(*rl.ExtraUsage.Utilization), WindowMins: 30 * 24 * 60,
		})
	}
	return snap, nil
}

// parseClaudeRateLimitEvent normalises one streamed rate-limit event into a
// sparse snapshot: one window, merged onto whatever the last read drew. The
// utilization fraction is 0-1 here where the read response is 0-100 — the
// event and the read agree on ids and disagree on scale, so this is the one
// place the conversion happens.
//
// A "seven_day_overage_included" event names its bucket by type, not by
// model; the model is whatever model_scoped row the last read carried, and an
// event with no read behind it is dropped rather than drawn as a guess.
func parseClaudeRateLimitEvent(raw json.RawMessage, scopedName string) (adapter.QuotaSnapshot, bool) {
	var ev struct {
		RateLimitType string   `json:"rateLimitType"`
		Utilization   *float64 `json:"utilization"` // 0-1
		ResetsAt      int64    `json:"resetsAt"`    // epoch seconds
	}
	if err := json.Unmarshal(raw, &ev); err != nil || ev.Utilization == nil {
		return adapter.QuotaSnapshot{}, false
	}
	used := *ev.Utilization * 100
	resets := ev.ResetsAt * 1000
	switch ev.RateLimitType {
	case "five_hour":
		return oneWindow("five_hour", adapter.QuotaSession, "Session", used, resets, 5*60), true
	case "seven_day":
		return oneWindow("seven_day", adapter.QuotaWeekly, "Weekly", used, resets, 7*24*60), true
	case "seven_day_opus":
		return oneWindow("seven_day_opus", adapter.QuotaWeekly, "Weekly · Opus", used, resets, 7*24*60), true
	case "seven_day_sonnet":
		return oneWindow("seven_day_sonnet", adapter.QuotaWeekly, "Weekly · Sonnet", used, resets, 7*24*60), true
	case "seven_day_overage_included":
		if scopedName == "" {
			return adapter.QuotaSnapshot{}, false
		}
		return oneWindow(scopedSlug(scopedName), adapter.QuotaWeekly, "Weekly · "+scopedName, used, resets, 7*24*60), true
	}
	return adapter.QuotaSnapshot{}, false
}

func oneWindow(id, kind, label string, used float64, resets int64, mins int) adapter.QuotaSnapshot {
	w := adapter.QuotaWindow{ID: id, Kind: kind, Label: label, UsedPercent: pct(used), WindowMins: mins}
	if resets > 0 {
		w.ResetsAt = resets
	}
	return adapter.QuotaSnapshot{CheckedAt: time.Now().UnixMilli(), Windows: []adapter.QuotaWindow{w}}
}

// scopedLimitName reads the model name the usage response gave its
// model-scoped weekly bucket, for streaming events that cannot name it.
func scopedLimitName(raw json.RawMessage) string {
	var res claudeUsageResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return ""
	}
	if res.RateLimits == nil {
		return ""
	}
	for _, m := range res.RateLimits.ModelScoped {
		return m.DisplayName
	}
	return ""
}

// reportQuota pushes a snapshot at the host's quota cache, when the host
// implements the optional reporter. Adapters never learn what the host does
// with it.
func (s *session) reportQuota(snap adapter.QuotaSnapshot) {
	if reporter, ok := s.host.(adapter.QuotaReporter); ok {
		reporter.ReportQuota(snap)
	}
}

// Quota asks the running CLI for the account's usage limits through the
// bridge's getUsage control request — the same sidecar-control pattern as
// getContextUsage, over the live process.
func (s *session) Quota(ctx context.Context) (adapter.QuotaSnapshot, error) {
	var raw json.RawMessage
	if err := s.conn.Call(ctx, "getUsage", nil, &raw); err != nil {
		return adapter.QuotaSnapshot{}, fmt.Errorf("claude usage: %w", err)
	}
	snap, err := parseClaudeUsage(raw)
	if err != nil {
		return snap, err
	}
	s.mu.Lock()
	s.scopedLimit = scopedLimitName(raw)
	s.mu.Unlock()
	return snap, nil
}

// ReadQuota asks for the account's usage limits without a live session: one
// short-lived bridge run in its usage op, which starts a conversation, asks
// the control question, and exits without ever running a turn.
func (a *Adapter) ReadQuota(ctx context.Context, env map[string]string) (adapter.QuotaSnapshot, error) {
	r, avail := a.resolve(ctx)
	if !avail.OK() {
		return adapter.QuotaSnapshot{}, fmt.Errorf("claude is unavailable: %s", avail.Reason)
	}
	blob, err := json.Marshal(sidecarConfig{Op: "usage", Cwd: workingDir(), ClaudePath: r.claudePath})
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, quotaReadTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.runtime, append(append([]string{}, r.runtimeArgs...), string(blob))...)
	cmd.Dir = workingDir()
	cmd.Env = append(adapter.MergeEnv(os.Environ(), env), "CLAUDE_CODE_ENTRYPOINT=sdk-ts")
	cmd.Stderr = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return adapter.QuotaSnapshot{}, err
	}
	if err := cmd.Start(); err != nil {
		return adapter.QuotaSnapshot{}, fmt.Errorf("start claude bridge: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	var usage json.RawMessage
	var fatal string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var frame struct {
			Method string `json:"method"`
			Params struct {
				Usage   json.RawMessage `json:"usage"`
				Message string          `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &frame); err != nil {
			continue
		}
		switch frame.Method {
		case "usage":
			usage = frame.Params.Usage
		case "fatal":
			fatal = frame.Params.Message
		}
		if usage != nil || fatal != "" {
			break
		}
	}
	switch {
	case usage != nil:
		return parseClaudeUsage(usage)
	case fatal != "":
		return adapter.QuotaSnapshot{}, fmt.Errorf("claude usage read failed: %s", firstLine(strings.TrimSpace(fatal)))
	case ctx.Err() != nil:
		return adapter.QuotaSnapshot{}, fmt.Errorf("claude usage read: %w", ctx.Err())
	default:
		return adapter.QuotaSnapshot{}, fmt.Errorf("claude usage read returned nothing")
	}
}
