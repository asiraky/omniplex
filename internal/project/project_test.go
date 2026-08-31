package project

import "testing"

func TestNormalizeMovesLegacyAgentDefaultsIntoHarnessProfile(t *testing.T) {
	cfg := DefaultConfig("/tmp/worksauce")
	cfg.Defaults.Harness = "claude"
	cfg.Defaults.Model = "opus"
	cfg.Defaults.Mode = "bypassPermissions"
	cfg.Defaults.Effort = "high"

	got, err := Normalize("/tmp/worksauce", cfg)
	if err != nil {
		t.Fatal(err)
	}
	profile := got.Defaults.Harnesses["claude"]
	if profile.Model != "opus" || profile.Mode != "bypassPermissions" || profile.Effort != "high" {
		t.Fatalf("migrated profile = %+v", profile)
	}
	if got.Defaults.Model != "" || got.Defaults.Mode != "" || got.Defaults.Effort != "" {
		t.Fatalf("legacy fields survived normalization: %+v", got.Defaults)
	}
}

func TestNormalizeDoesNotOverwriteAnExistingHarnessProfile(t *testing.T) {
	cfg := DefaultConfig("/tmp/worksauce")
	cfg.Defaults.Harness = "codex"
	cfg.Defaults.Model = "legacy-model"
	cfg.Defaults.Harnesses = map[string]HarnessDefaults{
		"codex": {Model: "chosen-model", Mode: "full-access"},
	}

	got, err := Normalize("/tmp/worksauce", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if profile := got.Defaults.Harnesses["codex"]; profile.Model != "chosen-model" || profile.Mode != "full-access" {
		t.Fatalf("existing profile overwritten: %+v", profile)
	}
}
