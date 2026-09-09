package usage

import (
	"encoding/json"
	"testing"
	"time"
)

func ev(session, harness, typ string, ts int64, payload any) EventRow {
	raw, _ := json.Marshal(payload)
	return EventRow{SessionID: session, Harness: harness, Type: typ, Timestamp: ts, Payload: raw}
}

func usageEv(session, harness string, ts int64, in, out, cr, cw int64) EventRow {
	return ev(session, harness, "usage.updated", ts, map[string]any{
		"input": in, "output": out, "cacheRead": cr, "cacheWrite": cw,
	})
}

// accountingEv is a usage.updated event the adapter marked as fresh
// accounting (the flag every claude result carries since it was added).
func accountingEv(session, harness string, ts int64, in, out, cr, cw int64) EventRow {
	return ev(session, harness, "usage.updated", ts, map[string]any{
		"input": in, "output": out, "cacheRead": cr, "cacheWrite": cw, "accounting": true,
	})
}

var now = time.UnixMilli(1800000000000) // a fixed round number

func TestClaudeDoubleEmissionCollapses(t *testing.T) {
	// One claude turn: the flagged result event, then the occupancy
	// re-emission of the same numbers (no flag). The turn must count once.
	rows := []EventRow{
		ev("s1", "claude", "session.created", now.UnixMilli()-time.Hour.Milliseconds(), map[string]any{"model": "claude-opus-5"}),
		accountingEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds(), 1000, 2000, 500, 100),
		usageEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds()+5, 1000, 2000, 500, 100),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (the occupancy re-emission must not double-count)", len(rep.Rows))
	}
	if rep.Totals.Input != 1000 || rep.Totals.Output != 2000 {
		t.Fatalf("totals = %+v", rep.Totals)
	}
}

func TestClaudeLegacyIdenticalEventsDedupe(t *testing.T) {
	// Events recorded before the accounting flag existed: the only signal is
	// that the occupancy report restates the result verbatim, so consecutive
	// identical counts collapse.
	rows := []EventRow{
		usageEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds(), 1000, 2000, 500, 100),
		usageEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds()+5, 1000, 2000, 500, 100),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if rep.Totals.Input != 1000 {
		t.Fatalf("totals = %+v, want the legacy pair counted once", rep.Totals)
	}
}

func TestClaudeIdenticalConsecutiveTurnsBothCount(t *testing.T) {
	// Two flagged turns with byte-identical usage are genuinely two turns; the
	// flag is what says so, where the legacy heuristic would have folded them.
	rows := []EventRow{
		accountingEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds(), 100, 200, 0, 0),
		usageEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds()+1, 100, 200, 0, 0),
		accountingEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds()+2, 100, 200, 0, 0),
		usageEv("s1", "claude", now.UnixMilli()-time.Hour.Milliseconds()+3, 100, 200, 0, 0),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if rep.Totals.Input != 200 || rep.Totals.Output != 400 {
		t.Fatalf("totals = %+v, want both turns counted", rep.Totals)
	}
}

func TestCodexCumulativeDeltas(t *testing.T) {
	// Codex totals grow within a thread; each event's usage is the delta.
	rows := []EventRow{
		usageEv("s1", "codex", now.UnixMilli()-2*time.Hour.Milliseconds(), 1000, 2000, 100, 0),
		usageEv("s1", "codex", now.UnixMilli()-time.Hour.Milliseconds(), 1500, 3000, 200, 0),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if rep.Totals.Input != 1500 || rep.Totals.Output != 3000 || rep.Totals.CacheRead != 200 {
		t.Fatalf("totals = %+v, want cumulative deltas", rep.Totals)
	}
}

func TestCodexResetCountsFreshTotal(t *testing.T) {
	// A compaction or thread reset drops the cumulative counter; the reading
	// after it is new usage, not a negative delta.
	rows := []EventRow{
		usageEv("s1", "codex", now.UnixMilli()-2*time.Hour.Milliseconds(), 5000, 6000, 0, 0),
		usageEv("s1", "codex", now.UnixMilli()-time.Hour.Milliseconds(), 100, 200, 0, 0),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if rep.Totals.Input != 5100 || rep.Totals.Output != 6200 {
		t.Fatalf("totals = %+v, want reset counted as fresh usage", rep.Totals)
	}
}

func TestOutOfWindowBaselineStillDeltas(t *testing.T) {
	// The first reading is outside the window and must not be counted — but
	// the in-window reading still deltas against it, so only the growth
	// lands in the report.
	rows := []EventRow{
		usageEv("s1", "codex", now.Add(-48*time.Hour).UnixMilli(), 9000, 9000, 0, 0),
		usageEv("s1", "codex", now.Add(-time.Hour).UnixMilli(), 9500, 9100, 0, 0),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if rep.Totals.Input != 500 || rep.Totals.Output != 100 {
		t.Fatalf("totals = %+v, want only in-window growth", rep.Totals)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (the out-of-window reading must not bucket)", len(rep.Rows))
	}
}

func TestModelAttributionFollowsSwitches(t *testing.T) {
	ts := now.Add(-time.Hour).UnixMilli()
	rows := []EventRow{
		ev("s1", "claude", "session.created", ts, map[string]any{"model": "claude-opus-5"}),
		accountingEv("s1", "claude", ts+1, 1_000_000, 0, 0, 0),
		ev("s1", "claude", "session.config_changed", ts+2, map[string]any{"model": "claude-sonnet-5"}),
		accountingEv("s1", "claude", ts+3, 1_000_000, 0, 0, 0),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	byModel := map[string]Row{}
	for _, r := range rep.Rows {
		byModel[r.Model] = r
	}
	if len(byModel) != 2 {
		t.Fatalf("want two model rows, got %+v", rep.Rows)
	}
	// Opus 5 input is $5/M, Sonnet 5 is $2/M.
	if got := byModel["claude-opus-5"].Totals.Cost; got != 5 {
		t.Fatalf("opus cost = %v, want 5", got)
	}
	if got := byModel["claude-sonnet-5"].Totals.Cost; got != 2 {
		t.Fatalf("sonnet cost = %v, want 2", got)
	}
}

func TestSessionsWalkIndependently(t *testing.T) {
	ts := now.Add(-time.Hour).UnixMilli()
	rows := []EventRow{
		// Session one walks first (the store orders by session id), ends
		// mid-stream; session two must not inherit its model or baseline.
		ev("s1", "claude", "session.created", ts, map[string]any{"model": "claude-opus-5"}),
		usageEv("s1", "claude", ts+1, 100, 0, 0, 0),
		ev("s2", "claude", "session.created", ts, map[string]any{"model": "claude-sonnet-5"}),
		usageEv("s2", "claude", ts+1, 100, 0, 0, 0),
	}
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if len(rep.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rep.Rows))
	}
	for _, r := range rep.Rows {
		if r.Model == "" {
			t.Fatalf("session two inherited a blank model: %+v", rep.Rows)
		}
	}
}

func TestBucketsAreFlooredAndGaplessRowsOnlyForUsage(t *testing.T) {
	spec := mustSpec(t, "24h")
	// 90 minutes ago: hourly buckets, so this lands in the bucket starting
	// two hours ago (floored to the hour).
	ts := now.Add(-90 * time.Minute).UnixMilli()
	rows := []EventRow{usageEv("s1", "claude", ts, 10, 0, 0, 0)}
	rep := Aggregate(rows, spec, now)
	if len(rep.Rows) != 1 || rep.Rows[0].Start != bucketStart(ts, spec.Bucket.Milliseconds()) {
		t.Fatalf("rows = %+v", rep.Rows)
	}
	if rep.To-rep.From != spec.Window.Milliseconds() {
		t.Fatalf("window = %v", rep.To-rep.From)
	}
}

func TestUnpricedUsageSurfacesButStaysOutOfCost(t *testing.T) {
	// A model with no catalogue entry: tokens counted, cost untouched, and
	// the unpriced count says so.
	rows := []EventRow{usageEv("s1", "claude", now.Add(-time.Hour).UnixMilli(), 1000, 2000, 0, 0)}
	rows[0].Type = "usage.updated"
	rep := Aggregate(rows, mustSpec(t, "24h"), now)
	if rep.Totals.Cost != 0 {
		t.Fatalf("cost = %v, want 0 for an unpriced model", rep.Totals.Cost)
	}
	if rep.Totals.Unpriced != 3000 {
		t.Fatalf("unpriced = %v, want 3000", rep.Totals.Unpriced)
	}
	if rep.Totals.Input != 1000 {
		t.Fatalf("token counts must still accumulate: %+v", rep.Totals)
	}
}

func TestReportCarriesPriceVersion(t *testing.T) {
	rep := Aggregate(nil, mustSpec(t, "7d"), now)
	if rep.PriceVersion != PriceVersion {
		t.Fatalf("price version = %q", rep.PriceVersion)
	}
	if rep.BucketMs != 24*3600*1000 {
		t.Fatalf("7d bucket = %v, want daily", rep.BucketMs)
	}
}

func mustSpec(t *testing.T, id string) RangeSpec {
	t.Helper()
	spec, err := RangeFor(id)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
