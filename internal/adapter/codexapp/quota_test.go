package codexapp

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

// The read result exactly as codex-cli 0.153.4 answered a live request.
const liveRead = `{
  "rateLimits": {
    "limitId": "codex", "limitName": null,
    "primary": {"usedPercent": 8, "windowDurationMins": 10080, "resetsAt": 1789435909},
    "secondary": null,
    "credits": {"hasCredits": false, "unlimited": false, "balance": "0"},
    "individualLimit": null, "spendControlReached": false,
    "planType": "pro", "rateLimitReachedType": null
  },
  "rateLimitsByLimitId": {
    "codex": {
      "limitId": "codex", "limitName": null,
      "primary": {"usedPercent": 8, "windowDurationMins": 10080, "resetsAt": 1789435909},
      "secondary": null,
      "planType": "pro", "rateLimitReachedType": null
    },
    "codex_bengalfox": {
      "limitId": "codex_bengalfox", "limitName": "GPT-5.3-Codex-Spark",
      "primary": {"usedPercent": 0, "windowDurationMins": 300, "resetsAt": 1789013349},
      "secondary": {"usedPercent": 0, "windowDurationMins": 10080, "resetsAt": 1789600149},
      "planType": "pro", "rateLimitReachedType": null
    }
  },
  "rateLimitResetCredits": {
    "availableCount": 2,
    "credits": [
      {"status": "available", "expiresAt": 1791079783},
      {"status": "available", "expiresAt": 1791173944},
      {"status": "used", "expiresAt": 1790000000}
    ]
  },
  "accountId": "8c2294a9-d49d-4b8c-9b89-8fb89bef1140",
  "rateLimitUpsell": null
}`

func windowIDs(snap adapter.QuotaSnapshot) []string {
	ids := make([]string, 0, len(snap.Windows))
	for _, w := range snap.Windows {
		ids = append(ids, w.ID)
	}
	return ids
}

func TestParseCodexReadRendersTheCompleteSet(t *testing.T) {
	snap, err := parseCodexRead(json.RawMessage(liveRead))
	if err != nil {
		t.Fatal(err)
	}
	if snap.AccountID == "" {
		t.Fatal("accountId must be recorded")
	}
	if snap.Plan != "pro" {
		t.Fatalf("plan = %q", snap.Plan)
	}
	byID := map[string]adapter.QuotaWindow{}
	for _, w := range snap.Windows {
		byID[w.ID] = w
	}
	// The main allowance, the model-specific pair, and the credits: all of
	// them, not a hard-coded "exactly two windows".
	for _, id := range []string{"codex/primary", "codex_bengalfox/primary", "codex_bengalfox/secondary", "credits"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("window %q missing from %v", id, windowIDs(snap))
		}
	}
	main := byID["codex/primary"]
	if main.Kind != adapter.QuotaWeekly || main.UsedPercent == nil || *main.UsedPercent != 8 {
		t.Errorf("main window = %+v", main)
	}
	if main.ResetsAt != 1789435909_000 {
		t.Errorf("resetsAt = %v, want seconds converted to ms", main.ResetsAt)
	}
	spark := byID["codex_bengalfox/primary"]
	if spark.Kind != adapter.QuotaSession {
		t.Errorf("spark session window kind = %q", spark.Kind)
	}
	if spark.Label != "GPT-5.3-Codex-Spark · Session" {
		t.Errorf("spark label = %q", spark.Label)
	}
	credits := byID["credits"]
	if credits.Kind != adapter.QuotaCredits || credits.Count == nil || *credits.Count != 2 {
		t.Errorf("credits window = %+v", credits)
	}
	// The earliest-expiring *available* credit is the row's reset.
	if credits.ResetsAt != 1791079783_000 {
		t.Errorf("credits reset = %v, want the earliest available expiry", credits.ResetsAt)
	}
}

func TestParseCodexReadFallsBackToTopLevelSnapshot(t *testing.T) {
	// An older CLI without rateLimitsByLimitId: the top-level snapshot is the
	// whole answer and must still draw.
	snap, err := parseCodexRead(json.RawMessage(`{
	  "rateLimits": {
	    "limitId": "codex", "planType": "go",
	    "primary": {"usedPercent": 55, "resetsAt": 1789435909}
	  }
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Windows) != 1 {
		t.Fatalf("windows = %+v", snap.Windows)
	}
	if snap.Plan != "go" {
		t.Fatalf("plan = %q", snap.Plan)
	}
	// No windowDurationMins on a go plan's single allowance: it is monthly.
	if snap.Windows[0].Kind != adapter.QuotaMonthly {
		t.Fatalf("kind = %q", snap.Windows[0].Kind)
	}
}

func TestParseCodexUpdateSparseFields(t *testing.T) {
	// A live update that only names the secondary window's new reading: the
	// ids match what a read drew, so the cache merges rather than duplicating,
	// and fields the update omits stay absent for the merge to preserve.
	snap, ok := parseCodexUpdate(json.RawMessage(`{
	  "limitId": "codex",
	  "secondary": {"usedPercent": 91, "windowDurationMins": 10080, "resetsAt": 1789600149}
	}`))
	if !ok {
		t.Fatal("update should parse")
	}
	if len(snap.Windows) != 1 {
		t.Fatalf("windows = %+v", snap.Windows)
	}
	w := snap.Windows[0]
	if w.ID != "codex/secondary" {
		t.Fatalf("id = %q", w.ID)
	}
	if w.UsedPercent == nil || *w.UsedPercent != 91 {
		t.Fatalf("usedPercent = %+v", w.UsedPercent)
	}
}

func TestParseCodexUpdateAcceptsWrapperShape(t *testing.T) {
	// A CLI generation that wraps the snapshot under rateLimits lands on the
	// same path as the bare one.
	snap, ok := parseCodexUpdate(json.RawMessage(`{
	  "rateLimits": {"limitId": "codex", "primary": {"usedPercent": 3, "windowDurationMins": 300, "resetsAt": 1789013349}}
	}`))
	if !ok {
		t.Fatal("wrapped update should parse")
	}
	if len(snap.Windows) != 1 || snap.Windows[0].ID != "codex/primary" {
		t.Fatalf("windows = %+v", snap.Windows)
	}
}

func TestParseCodexUpdateWithoutUsableWindowIsDropped(t *testing.T) {
	if _, ok := parseCodexUpdate(json.RawMessage(`{"limitId": "codex"}`)); ok {
		t.Fatal("an update with nothing to say must be dropped, not merged as an empty snapshot")
	}
	if _, ok := parseCodexUpdate(json.RawMessage(`{"unrelated": true}`)); ok {
		t.Fatal("a non-snapshot notification must be ignored")
	}
}
