package claudecode

import (
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

// The SDK's own rows, as observed from claude CLI 2.1.238 — including the twin
// Opus aliases (a generic "default" and a named "opus[1m]") that resolve to the
// same model, and a Haiku row.
func liveRows() []modelInfo {
	return []modelInfo{
		{Value: "default", ResolvedModel: "claude-opus-5[1m]", DisplayName: "Default (recommended)", Description: "Opus 5 with 1M context · Best for everyday, complex tasks", SupportedEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
		{Value: "opus[1m]", ResolvedModel: "claude-opus-5[1m]", DisplayName: "Opus (1M context)", Description: "Opus 5 with 1M context · Best for everyday, complex tasks", SupportedEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
		{Value: "fable", ResolvedModel: "claude-fable-5", DisplayName: "Fable", Description: "Fable 5 · Most capable for your hardest and longest-running tasks", SupportedEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
		{Value: "fable[1m]", ResolvedModel: "claude-fable-5[1m]", DisplayName: "Fable (1M context)", Description: "Fable 5 with 1M context · Most capable for your hardest and longest-running tasks", SupportedEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
		{Value: "sonnet", ResolvedModel: "claude-sonnet-5", DisplayName: "Sonnet", Description: "Sonnet 5 · Efficient for routine tasks", SupportedEffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
		{Value: "haiku", ResolvedModel: "claude-haiku-4-5-20251001", DisplayName: "Haiku", Description: "Haiku 4.5 · Fastest for quick answers"},
	}
}

// Each current row is the bare concrete model (so it defaults to 200k and
// matches what the harness reports), labelled by its generation, with the
// purpose kept as the description.
func TestMapClaudeModelsCleanRows(t *testing.T) {
	got := mapClaudeModels(liveRows())

	fable := findModel(t, got, "claude-fable-5")
	if fable.Label != "Fable 5" {
		t.Errorf("label = %q, want Fable 5", fable.Label)
	}
	if fable.Description != "Most capable for your hardest and longest-running tasks" {
		t.Errorf("description = %q, still carries the version", fable.Description)
	}
	if fable.Resolves != "claude-fable-5" {
		t.Errorf("resolves = %q, want claude-fable-5", fable.Resolves)
	}
	// No current row carries the "[1m]" tag: the tag is the 1M opt-in, chosen
	// separately, not part of the default id.
	for _, m := range got {
		if m.Group == "" && stringsContains(m.ID, "[1m]") {
			t.Errorf("current row %q carries a context tag", m.ID)
		}
	}
}

// Haiku is a quick-answer model, not a coding one, so it is dropped.
func TestMapClaudeModelsDropsHaiku(t *testing.T) {
	for _, m := range mapClaudeModels(liveRows()) {
		if stringsContains(m.ID, "haiku") {
			t.Fatalf("haiku was offered: %+v", m)
		}
	}
}

// The two Opus aliases resolve to one model, so the picker shows one "Opus 5"
// row — the bare id, carrying the recommended flag. Exactly one row is default.
func TestMapClaudeModelsMergesOpusToOneRow(t *testing.T) {
	got := mapClaudeModels(liveRows())

	var opus []adapter.ModelMeta
	var defaults []string
	for _, m := range got {
		if m.Resolves == "claude-opus-5" {
			opus = append(opus, m)
		}
		if m.Default {
			defaults = append(defaults, m.ID)
		}
	}
	if len(opus) != 1 {
		t.Fatalf("rows resolving to Opus 5 = %d, want 1", len(opus))
	}
	if opus[0].ID != "claude-opus-5" || opus[0].Label != "Opus 5" {
		t.Errorf("opus row = {id:%q label:%q}, want {claude-opus-5, Opus 5}", opus[0].ID, opus[0].Label)
	}
	if len(defaults) != 1 || defaults[0] != "claude-opus-5" {
		t.Fatalf("default rows = %v, want exactly [claude-opus-5]", defaults)
	}
}

// Current models are ordered by strength — Fable, Opus, Sonnet — and the
// curated legacy group is appended, folded away.
func TestMapClaudeModelsOrdersByStrengthThenLegacy(t *testing.T) {
	got := mapClaudeModels(liveRows())

	var currentIDs []string
	for _, m := range got {
		if m.Group == "" {
			currentIDs = append(currentIDs, m.ID)
		}
	}
	want := []string{"claude-fable-5", "claude-opus-5", "claude-sonnet-5"}
	if len(currentIDs) != len(want) {
		t.Fatalf("current ids = %v, want %v", currentIDs, want)
	}
	for i := range want {
		if currentIDs[i] != want[i] {
			t.Fatalf("current order = %v, want %v", currentIDs, want)
		}
	}
	firstLegacy := len(currentIDs)
	if len(got) != firstLegacy+len(legacyModels) {
		t.Fatalf("len = %d, want %d current + %d legacy", len(got), firstLegacy, len(legacyModels))
	}
	for _, want := range legacyModels {
		m := findModel(t, got, want.ID)
		if m.Group != adapter.GroupLegacy {
			t.Errorf("%s group = %q, want legacy", m.ID, m.Group)
		}
		if m.Version == m.Label && m.Version != "" {
			t.Errorf("%s renders its name twice (label==version)", m.ID)
		}
	}
}

func TestMapClaudeModelsIgnoresEmptyAndUnlisted(t *testing.T) {
	if got := mapClaudeModels(nil); got != nil {
		t.Errorf("no live rows should map to nothing, got %v", got)
	}
	got := mapClaudeModels([]modelInfo{{Value: ""}, {Value: "sonnet", ResolvedModel: "claude-sonnet-5", DisplayName: "Sonnet", Description: "Sonnet 5"}})
	if len(got) != 1+len(legacyModels) {
		t.Fatalf("len = %d, want the one real row plus legacy", len(got))
	}
	sonnet := findModel(t, got, "claude-sonnet-5")
	// No " · " separator means no generation to read, so the label falls back
	// to the harness's display name rather than inventing one.
	if sonnet.Label != "Sonnet" {
		t.Errorf("label = %q, want the display-name fallback Sonnet", sonnet.Label)
	}
}

func findModel(t *testing.T, in []adapter.ModelMeta, id string) adapter.ModelMeta {
	t.Helper()
	for _, m := range in {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no model %q in %v", id, in)
	return adapter.ModelMeta{}
}

func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Signed out, the SDK's "default" alias describes itself — "Use the default
// model (currently Opus 5)" — rather than the model. The named alias for the
// same model is the row to keep; the only thing the default alias adds is the
// recommended flag.
func TestDefaultAliasDoesNotDescribeItself(t *testing.T) {
	got := mapClaudeModels([]modelInfo{
		{Value: "default", ResolvedModel: "claude-opus-5", DisplayName: "Default (recommended)", Description: "Use the default model (currently Opus 5) · $5/$25 per Mtok"},
		{Value: "opus", ResolvedModel: "claude-opus-5", DisplayName: "Opus", Description: "Opus 5 · Best for everyday, complex tasks · $5/$25 per Mtok"},
	})
	opus := findModel(t, got, "claude-opus-5")
	if !opus.Default {
		t.Error("the recommended flag was lost")
	}
	if opus.Label != "Opus 5" {
		t.Errorf("label = %q, want Opus 5", opus.Label)
	}
	if strings.Contains(opus.Description, "default model") {
		t.Errorf("description = %q, still describes the alias", opus.Description)
	}
}

// The collapse of the "[1m]" aliases into the bare row must keep the one thing
// only it knows: that the harness offers a 1M window for that model. Every
// model with a tagged alias carries the flag, and only those — a UI reads it
// instead of matching on the model's name.
func TestMapClaudeModelsMarks1MFromAliases(t *testing.T) {
	got := mapClaudeModels(liveRows())

	// Opus learns it from the named "opus[1m]" alias, Fable from "fable[1m]",
	// and Sonnet has none in this listing.
	for id, want := range map[string]bool{
		"claude-opus-5":   true,
		"claude-fable-5":  true,
		"claude-sonnet-5": false,
	} {
		if m := findModel(t, got, id); m.Supports1M != want {
			t.Errorf("%s supports1m = %v, want %v", id, m.Supports1M, want)
		}
	}
}

// The generic "default" alias carries the tag only on its resolved id, and it
// is the row that gets replaced wholesale when a named alias for the same
// model turns up. Neither may lose the flag.
func TestMapClaudeModels1MSurvivesDefaultAliasMerge(t *testing.T) {
	// Only the "default" row is tagged here, and it is replaced by the plain
	// "opus" alias, which is not.
	got := mapClaudeModels([]modelInfo{
		{Value: "default", ResolvedModel: "claude-opus-5[1m]", DisplayName: "Default (recommended)", Description: "Opus 5 with 1M context · Best for everyday, complex tasks"},
		{Value: "opus", ResolvedModel: "claude-opus-5", DisplayName: "Opus", Description: "Opus 5 · Best for everyday, complex tasks"},
	})

	opus := findModel(t, got, "claude-opus-5")
	if !opus.Supports1M {
		t.Error("supports1m was dropped when the named alias replaced the default row")
	}
	if !opus.Default {
		t.Error("the recommended flag was dropped in the same merge")
	}
}

// Only the 1M tag says a 1M window is on offer. Another bracketed variant is
// still collapsed onto the bare model — that is what keeps a running session
// resolving to its row — but it must not be advertised as 1M, or the UI would
// submit a "[1m]" id the harness never offered.
func TestMapClaudeModelsIgnoresOtherContextTags(t *testing.T) {
	got := mapClaudeModels([]modelInfo{
		{Value: "sonnet[beta]", ResolvedModel: "claude-sonnet-5[beta]", DisplayName: "Sonnet (beta)", Description: "Sonnet 5 · Efficient for routine tasks"},
	})

	m := findModel(t, got, "claude-sonnet-5")
	if m.Supports1M {
		t.Error("a [beta] alias was read as a 1M one")
	}
}
