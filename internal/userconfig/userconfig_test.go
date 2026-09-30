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
	} {
		if _, err := Save(cfg); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	saved, err := Save(Config{ProjectsDir: " ~/work ", DefaultLevel: "edits"})
	if err != nil {
		t.Fatal(err)
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
