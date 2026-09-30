package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// commandConn is a connection that can execute commands against a server
// over an empty database, with the home folder and user config in temp dirs.
func commandConn(t *testing.T) (*conn, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)
	return &conn{srv: New(Options{Manager: mgr, Store: st})}, home
}

func run(t *testing.T, c *conn, command string, args any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return c.execute(context.Background(), clientFrame{Command: command, Args: raw})
}

func TestTheGeneralSettingsScreenCannotChangeTheSkillsSetup(t *testing.T) {
	c, _ := commandConn(t)
	onDisk := userconfig.SkillsConfig{Library: "~/dot/skills", ProjectLibrary: "tools/skills", CLIVersion: "2.0.0"}
	if _, err := userconfig.Update(func(cfg *userconfig.Config) error { cfg.Skills = onDisk; return nil }); err != nil {
		t.Fatal(err)
	}
	for name, sent := range map[string]userconfig.SkillsConfig{
		"a client that does not know about skills": {},
		"a client echoing a stale setup":           {Library: "~/elsewhere"},
	} {
		if _, err := run(t, c, "save_user_config", saveUserConfigArgs{Config: userconfig.Config{DefaultLevel: "edits", Skills: sent}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg, err := userconfig.Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Skills != onDisk || cfg.DefaultLevel != "edits" {
			t.Errorf("%s: skills = %+v, level = %q", name, cfg.Skills, cfg.DefaultLevel)
		}
	}
}

func TestSaveSkillsSetup(t *testing.T) {
	c, home := commandConn(t)
	if _, err := userconfig.Update(func(cfg *userconfig.Config) error { cfg.DefaultLevel = "edits"; return nil }); err != nil {
		t.Fatal(err)
	}

	for name, a := range map[string]skillArgs{
		"a relative library":                 {Library: "skills"},
		"a project library outside the repo": {ProjectLibrary: "../skills"},
		"an absolute project library":        {ProjectLibrary: "/srv/skills"},
		"a version that is really a flag":    {CLIVersion: "--registry=evil"},
	} {
		if _, err := run(t, c, "save_skills_setup", a); err == nil {
			t.Errorf("%s: saved", name)
		}
		if cfg, _ := userconfig.Load(); cfg.Skills != (userconfig.SkillsConfig{}) {
			t.Fatalf("%s: a refused save still wrote %+v", name, cfg.Skills)
		}
	}

	result, err := run(t, c, "save_skills_setup", skillArgs{Library: "~/dot/skills", ProjectLibrary: "tools/skills", CLIVersion: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	setup := result.(map[string]any)["setup"].(skills.Setup)
	if setup.LibraryDir != filepath.Join(home, "dot", "skills") || setup.ProjectLibrary != "tools/skills" || setup.CLIVersion != "2.0.0" {
		t.Errorf("setup after saving = %+v", setup)
	}
	cfg, err := userconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultLevel != "edits" {
		t.Errorf("saving the skills setup changed the rest of the config: %+v", cfg)
	}

	// The next read is of the library that was just chosen.
	if _, err := run(t, c, "link_library", skillArgs{Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, c, "create_skill", skillArgs{Scope: skills.ScopeUser, Name: "fresh", Description: "A new skill"}); err != nil {
		t.Fatal(err)
	}
	result, err = run(t, c, "list_skills", skillArgs{})
	if err != nil {
		t.Fatal(err)
	}
	listed := result.(map[string]any)
	found := listed["skills"].([]skills.Skill)
	if len(found) != 1 || found[0].Dir != filepath.Join(home, "dot", "skills", "fresh") {
		t.Errorf("skills = %+v", found)
	}
	if setup := listed["setup"].(skills.Setup); !setup.Exists || setup.Links[0].State != skills.LinkDirect {
		t.Errorf("setup = %+v", setup)
	}

	// Emptying the fields goes back to the defaults.
	if _, err := run(t, c, "save_skills_setup", skillArgs{}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := userconfig.Load(); cfg.Skills != (userconfig.SkillsConfig{}) {
		t.Errorf("skills config after clearing = %+v", cfg.Skills)
	}
}
