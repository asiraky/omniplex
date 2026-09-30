package userconfig

import (
	"os"
	"testing"
)

func TestExpandHomeResolvesTildeAndRefusesRelativePaths(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	for in, want := range map[string]string{"~": "/home/someone", "~/code/app": "/home/someone/code/app", "/srv/x/../y": "/srv/y"} {
		if got, err := ExpandHome(in); err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := ExpandHome("code/app"); err == nil {
		t.Errorf("relative path accepted as %q", got)
	}
}

func TestSavingRefusesSettingsItCouldNotUse(t *testing.T) {
	t.Setenv("OMNIPLEX_CONFIG", t.TempDir()+"/config.json")
	for name, cfg := range map[string]Config{
		"relative projects folder": {ProjectsDir: "code/projects"},
		"unknown level":            {DefaultLevel: "yolo"},
		"relative skills library":  {Skills: SkillsConfig{Library: "skills"}},
		"project library escaping": {Skills: SkillsConfig{ProjectLibrary: "../skills"}},
		"absolute project library": {Skills: SkillsConfig{ProjectLibrary: "/srv/skills"}},
		"cli version with a space": {Skills: SkillsConfig{CLIVersion: "1.7.0 --evil"}},
		"cli version as a flag":    {Skills: SkillsConfig{CLIVersion: "-y"}},
	} {
		if _, err := Save(cfg); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	saved, err := Save(Config{ProjectsDir: " ~/work ", DefaultLevel: "edits", Skills: SkillsConfig{Library: " ~/dot/skills ", ProjectLibrary: "tools/skills", CLIVersion: "1.8.0-beta.2"}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Skills.Library != "~/dot/skills" {
		t.Errorf("skills library kept as %q", saved.Skills.Library)
	}
	if saved.ProjectsDir != "~/work" {
		t.Errorf("projects folder kept as %q", saved.ProjectsDir)
	}
}

func TestABadValueOnDiskStillLoads(t *testing.T) {
	path := t.TempDir() + "/config.json"
	t.Setenv("OMNIPLEX_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{"version":1,"projectsDir":"relative","defaultLevel":"yolo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	// And the settings screen can put it right.
	if _, err := Update(func(c *Config) error { c.ProjectsDir, c.DefaultLevel = "", ""; return nil }); err != nil {
		t.Fatalf("fix: %v", err)
	}
}

func TestNormalizeReplacesAnOldFunctionBranchFormatButKeepsATemplate(t *testing.T) {
	for in, want := range map[string]string{
		"":                         DefaultBranchFormat,
		"  ":                       DefaultBranchFormat,
		"(i) => `fix/${i.number}`": DefaultBranchFormat,
		"fix/{number}":             "fix/{number}",
		"{title}-{number}":         "{title}-{number}",
	} {
		got, err := Normalize(Config{BranchFormat: in})
		if err != nil {
			t.Fatal(err)
		}
		if got.BranchFormat != want {
			t.Errorf("Normalize(%q).BranchFormat = %q, want %q", in, got.BranchFormat, want)
		}
	}
}

func TestCheckBranchFormat(t *testing.T) {
	for format, ok := range map[string]bool{
		"":                       true,
		"issue/{number}-{title}": true,
		"fix/{title}":            true,
		"no-placeholders":        true,
		"fix/{foo}-{number}":     false,
		"issue/{}":               false,
		"issue/{number}/{Title}": false,
		"{constructor}":          false,
	} {
		if err := CheckBranchFormat(format); (err == nil) != ok {
			t.Errorf("%q: err %v, want ok=%v", format, err, ok)
		}
	}
}
