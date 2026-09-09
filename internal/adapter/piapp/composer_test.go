package piapp

import (
	"context"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

func TestPiComposerItemsMapping(t *testing.T) {
	items := piComposerItems([]piCommand{
		{
			Name: "skill:merge", Description: " Land a PR\n", Source: "skill",
			SourceInfo: &piSourceInfo{Scope: "user", Origin: "top-level"},
		},
		{
			Name: "fix-tests", Description: "Fix failing tests", Source: "prompt",
			SourceInfo: &piSourceInfo{Scope: "project", Origin: "top-level"},
		},
		{
			Name: "llama", Description: "Manage models", Source: "extension",
			SourceInfo: &piSourceInfo{Scope: "temporary", Origin: "top-level"},
		},
	})

	if len(items) != 3 {
		t.Fatalf("items = %d, want 3: %+v", len(items), items)
	}
	byID := map[string]adapter.ComposerItem{}
	for _, item := range items {
		byID[item.ID] = item
		if item.Trigger != "/" || item.Behavior != adapter.ComposerPrompt {
			t.Errorf("%s = trigger %q behavior %q, want / and prompt", item.ID, item.Trigger, item.Behavior)
		}
	}

	skill := byID["skill:merge"]
	// The qualified name is what pi expands, so it has to survive into the
	// text; the bare name is what the human reads and types.
	if skill.InsertText != "/skill:merge" || skill.Name != "merge" || skill.Kind != "skill" {
		t.Errorf("skill = %+v, want name merge inserting /skill:merge", skill)
	}
	if len(skill.Aliases) != 1 || skill.Aliases[0] != "skill:merge" {
		t.Errorf("skill aliases = %v, want [skill:merge] so /skill:merge still matches", skill.Aliases)
	}
	if skill.Description != "Land a PR" {
		t.Errorf("description = %q, want it trimmed", skill.Description)
	}

	if tmpl := byID["command:fix-tests"]; tmpl.InsertText != "/fix-tests" || tmpl.Kind != "command" || len(tmpl.Aliases) != 0 {
		t.Errorf("prompt template = %+v, want an unprefixed command", tmpl)
	}
	if ext := byID["command:llama"]; ext.InsertText != "/llama" || ext.Kind != "command" {
		t.Errorf("extension command = %+v, want an unprefixed command", ext)
	}
}

func TestPiComposerItemsSkipsJunkAndDuplicates(t *testing.T) {
	items := piComposerItems([]piCommand{
		{Name: "  ", Source: "skill"},
		{Name: "skill:merge", Source: "skill"},
		{Name: "skill:merge", Source: "skill", Description: "second copy"},
		// A skill whose name carries no prefix must still be usable rather
		// than collapse to an empty name.
		{Name: "bare", Source: "skill"},
	})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (blank dropped, duplicate collapsed): %+v", len(items), items)
	}
	if items[0].Name != "bare" || items[0].InsertText != "/bare" {
		t.Errorf("unprefixed skill = %+v, want name bare inserting /bare", items[0])
	}
	if items[1].Name != "merge" {
		t.Errorf("items[1] = %q, want merge (sorted by name)", items[1].Name)
	}
}

func TestPiCommandOrigin(t *testing.T) {
	tests := []struct {
		info *piSourceInfo
		want string
	}{
		{nil, ""},
		{&piSourceInfo{Scope: "user", Origin: "top-level"}, "personal"},
		{&piSourceInfo{Scope: "project", Origin: "top-level"}, "project"},
		{&piSourceInfo{Scope: "temporary", Origin: "top-level"}, "built-in"},
		// Package membership outranks scope: where it was installed matters
		// less than the fact that it came from a package.
		{&piSourceInfo{Scope: "user", Origin: "package"}, "package"},
		{&piSourceInfo{Scope: "future", Origin: "top-level"}, "other"},
	}
	for _, tt := range tests {
		if got := piCommandOrigin(tt.info); got != tt.want {
			t.Errorf("origin(%+v) = %q, want %q", tt.info, got, tt.want)
		}
	}
}

// The catalogue is nested under data.commands and is fetched from the live
// process, so exercise the decode against a pi that actually answers.
func TestComposerItemsOverRPC(t *testing.T) {
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{SessionID: "sess-1"})
	catalogue, ok := s.(adapter.ComposerCataloguer)
	if !ok {
		t.Fatal("pi session does not advertise a composer catalogue")
	}
	items, err := catalogue.ComposerItems(context.Background())
	if err != nil {
		t.Fatalf("ComposerItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want the skill and the extension command", items)
	}
	if items[0].Name != "llama" || items[1].Name != "merge" || items[1].Origin != "personal" {
		t.Errorf("items = %+v, want llama then merge[personal]", items)
	}
}
