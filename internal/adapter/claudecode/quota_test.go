package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseClaudeUsageFullResponse(t *testing.T) {
	raw := json.RawMessage(`{
	  "rate_limits_available": true,
	  "subscription_type": "max",
	  "rate_limits": {
	    "five_hour": {"utilization": 42.5, "resets_at": "2026-04-12T10:00:00Z"},
	    "seven_day": {"utilization": 71.0, "resets_at": "2026-04-15T10:00:00Z"},
	    "model_scoped": [{"display_name": "Fable", "utilization": 12, "resets_at": "2026-04-15T10:00:00Z"}]
	  }
	}`)
	snap, err := parseClaudeUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Unavailable != "" {
		t.Fatalf("unavailable = %q", snap.Unavailable)
	}
	if snap.Plan != "max" {
		t.Fatalf("plan = %q", snap.Plan)
	}
	want := map[string]float64{
		"five_hour":       42.5,
		"seven_day":       71,
		"seven_day_fable": 12,
	}
	if len(snap.Windows) != len(want) {
		t.Fatalf("windows = %+v", snap.Windows)
	}
	for _, w := range snap.Windows {
		if w.UsedPercent == nil || *w.UsedPercent != want[w.ID] {
			t.Errorf("window %s = %+v, want %v%%", w.ID, w, want[w.ID])
		}
	}
	if snap.CheckedAt == 0 {
		t.Fatal("checkedAt must be stamped")
	}
}

func TestParseClaudeUsageUnsupportedIsNotAnError(t *testing.T) {
	// An API-key session has no plan limits: a legible negative, not a failure.
	snap, err := parseClaudeUsage(json.RawMessage(`{"rate_limits_available": false, "rate_limits": null}`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Unavailable != "unsupported" {
		t.Fatalf("unavailable = %q", snap.Unavailable)
	}
	if len(snap.Windows) != 0 {
		t.Fatalf("windows = %+v", snap.Windows)
	}
}

func TestClaudeRateLimitEventScalesFraction(t *testing.T) {
	// The streamed event's utilization is 0-1; the read's is 0-100. The
	// event must land on the same id and the same scale as the read.
	snap, ok := parseClaudeRateLimitEvent(json.RawMessage(`{
	  "rateLimitType": "seven_day", "utilization": 0.42, "resetsAt": 1789435909
	}`), "")
	if !ok {
		t.Fatal("event should parse")
	}
	if len(snap.Windows) != 1 {
		t.Fatalf("windows = %+v", snap.Windows)
	}
	w := snap.Windows[0]
	if w.ID != "seven_day" || w.UsedPercent == nil || *w.UsedPercent != 42 {
		t.Fatalf("window = %+v", w)
	}
	if w.ResetsAt != 1789435909_000 {
		t.Fatalf("resetsAt = %v", w.ResetsAt)
	}
}

func TestClaudeOverageEventNeedsLearnedName(t *testing.T) {
	raw := json.RawMessage(`{"rateLimitType": "seven_day_overage_included", "utilization": 0.1, "resetsAt": 100}`)
	// Without a read behind it, the model bucket cannot be named: dropped.
	if _, ok := parseClaudeRateLimitEvent(raw, ""); ok {
		t.Fatal("an overage event with no learned name must be dropped, not guessed")
	}
	// With one, it lands on the slug the read established.
	snap, ok := parseClaudeRateLimitEvent(raw, "Fable")
	if !ok {
		t.Fatal("event should parse with a learned name")
	}
	if snap.Windows[0].ID != scopedSlug("Fable") {
		t.Fatalf("id = %q", snap.Windows[0].ID)
	}
	if !strings.Contains(snap.Windows[0].Label, "Fable") {
		t.Fatalf("label = %q", snap.Windows[0].Label)
	}
}

func TestScopedSlugStable(t *testing.T) {
	if scopedSlug("Fable 5") != "seven_day_fable_5" {
		t.Fatalf("slug = %q", scopedSlug("Fable 5"))
	}
}
