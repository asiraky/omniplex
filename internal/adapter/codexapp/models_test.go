package codexapp

import (
	"encoding/json"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

func effort(levels ...string) []struct {
	ReasoningEffort string `json:"reasoningEffort"`
} {
	out := make([]struct {
		ReasoningEffort string `json:"reasoningEffort"`
	}, 0, len(levels))
	for _, l := range levels {
		out = append(out, struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{ReasoningEffort: l})
	}
	return out
}

// The catalogue codex-cli 0.148.0 actually returns.
func liveRows() []codexModel {
	return []codexModel{
		{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol", Description: "Latest frontier agentic coding model.", IsDefault: true, Efforts: effort("low", "medium", "high", "xhigh", "max", "ultra")},
		{ID: "gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Description: "Balanced agentic coding model for everyday work."},
		{ID: "gpt-5.6-luna", DisplayName: "GPT-5.6-Luna", Description: "Fast and affordable agentic coding model."},
		{ID: "gpt-5.5", DisplayName: "GPT-5.5", Description: "Frontier model for complex coding, research, and real-world work."},
		{ID: "gpt-5.4", DisplayName: "GPT-5.4", Description: "Strong model for everyday coding."},
		{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4-Mini", Description: "Small, fast, and cost-efficient model for simpler coding tasks."},
		{ID: "gpt-5.3-codex-spark", DisplayName: "GPT-5.3-Codex-Spark", Description: "Ultra-fast coding model."},
	}
}

func TestMapCodexModelsFoldsOlderGenerations(t *testing.T) {
	got := mapCodexModels(liveRows())

	current := map[string]bool{"gpt-5.6-sol": true, "gpt-5.6-terra": true, "gpt-5.6-luna": true}
	for _, m := range got {
		wantLegacy := !current[m.ID]
		if isLegacy := m.Group == adapter.GroupLegacy; isLegacy != wantLegacy {
			t.Errorf("%s group = %q, want legacy = %v", m.ID, m.Group, wantLegacy)
		}
	}
}

// Codex flags its own default, so omniplex never has to guess which row to
// preselect — and must not invent an empty-id "Default" row of its own.
func TestMapCodexModelsCarriesTheHarnessDefault(t *testing.T) {
	got := mapCodexModels(liveRows())

	var defaults []string
	for _, m := range got {
		if m.ID == "" {
			t.Errorf("an empty model id reached the UI: %+v", m)
		}
		if m.Default {
			defaults = append(defaults, m.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != "gpt-5.6-sol" {
		t.Fatalf("default rows = %v, want exactly [gpt-5.6-sol]", defaults)
	}
}

func TestMapCodexModelsKeepsPerModelEfforts(t *testing.T) {
	got := mapCodexModels(liveRows())

	sol := got[0]
	if sol.ID != "gpt-5.6-sol" {
		t.Fatalf("first row = %q", sol.ID)
	}
	// The point of per-model efforts: only some models offer "ultra", which a
	// fixed low…max set would drop.
	if len(sol.Efforts) == 0 || sol.Efforts[len(sol.Efforts)-1] != "ultra" {
		t.Errorf("efforts = %v, want the harness's own list including ultra", sol.Efforts)
	}
	if len(got[1].Efforts) != 0 {
		t.Errorf("a model advertising no efforts should carry none, got %v", got[1].Efforts)
	}
}

func TestMapCodexModelsSkipsHiddenModels(t *testing.T) {
	rows := append(liveRows(), codexModel{ID: "gpt-9.9-internal", DisplayName: "Internal", Hidden: true})
	got := mapCodexModels(rows)

	for _, m := range got {
		if m.ID == "gpt-9.9-internal" {
			t.Fatal("a hidden model was offered")
		}
	}
	// A hidden newer generation must not redefine what counts as current
	// either, or every visible model would be folded into legacy.
	if findCodexModel(t, got, "gpt-5.6-sol").Group != "" {
		t.Error("the newest visible generation was marked legacy")
	}
}

// Version ordering is read from the id, so it has to compare like a version
// and not like a decimal: 5.10 supersedes 5.6.
func TestMapCodexModelsComparesVersionParts(t *testing.T) {
	got := mapCodexModels([]codexModel{
		{ID: "gpt-5.10-next", DisplayName: "Next"},
		{ID: "gpt-5.6-sol", DisplayName: "Sol"},
	})

	if g := findCodexModel(t, got, "gpt-5.10-next"); g.Group != "" || g.Version != "5.10" {
		t.Errorf("5.10 mapped to group %q version %q, want current and 5.10", g.Group, g.Version)
	}
	if g := findCodexModel(t, got, "gpt-5.6-sol"); g.Group != adapter.GroupLegacy {
		t.Errorf("5.6 group = %q, want legacy once 5.10 exists", g.Group)
	}
}

// An id with no version cannot be ranked, so it stays current rather than
// being folded away on a guess.
func TestMapCodexModelsKeepsUnversionedIDsCurrent(t *testing.T) {
	got := mapCodexModels([]codexModel{
		{ID: "gpt-5.6-sol", DisplayName: "Sol"},
		{ID: "house-model", DisplayName: "House"},
	})

	house := findCodexModel(t, got, "house-model")
	if house.Group != "" || house.Version != "" {
		t.Errorf("unversioned id mapped to group %q version %q", house.Group, house.Version)
	}
}

// The Fast tier is found by its name, wherever it sits and however Codex
// cases it, and its id is what comes back; a model with no tiers, or none
// called Fast, offers nothing to toggle.
func TestMapCodexModelsFindsTheFastTier(t *testing.T) {
	var rows []codexModel
	if err := json.Unmarshal([]byte(`[
		{"id": "only", "serviceTiers": [{"id": "priority", "name": "Fast", "description": "1.5x speed"}]},
		{"id": "second", "serviceTiers": [{"id": "flex", "name": "Slow"}, {"id": "turbo", "name": "fast"}]},
		{"id": "none"},
		{"id": "empty", "serviceTiers": []},
		{"id": "other", "serviceTiers": [{"id": "flex", "name": "Flex"}]}
	]`), &rows); err != nil {
		t.Fatal(err)
	}
	got := mapCodexModels(rows)

	for id, want := range map[string]string{"only": "priority", "second": "turbo", "none": "", "empty": "", "other": ""} {
		if tier := findCodexModel(t, got, id).FastTier; tier != want {
			t.Errorf("%s FastTier = %q, want %q", id, tier, want)
		}
	}
}

func findCodexModel(t *testing.T, in []adapter.ModelMeta, id string) adapter.ModelMeta {
	t.Helper()
	for _, m := range in {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no model %q in %v", id, in)
	return adapter.ModelMeta{}
}
