package userconfig

import "testing"

// projectsDirectory is a plain preference: it must survive a save/load like
// any other field, and must not acquire an invented default on the way.
func TestProjectsDirectorySurvivesASaveAndHasNoDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectsDirectory != "" {
		t.Fatalf("a fresh config has projectsDirectory %q, want it unset", cfg.ProjectsDirectory)
	}

	cfg.ProjectsDirectory = "/home/t/code"
	saved, err := Save(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProjectsDirectory != "/home/t/code" {
		t.Fatalf("Save returned projectsDirectory %q", saved.ProjectsDirectory)
	}
	back, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if back.ProjectsDirectory != "/home/t/code" {
		t.Fatalf("projectsDirectory came back as %q, want it preserved", back.ProjectsDirectory)
	}
}
