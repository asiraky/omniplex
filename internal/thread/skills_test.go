package thread

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/userconfig"
)

func TestSkillsConfigMovesTheLibraries(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	defaults := skills.Roots{Home: "/home/someone", ProjectRoot: "/p", Library: "/default/lib", ProjectLibrary: "/p/default", CLIVersion: "1"}
	noProject := defaults
	noProject.ProjectRoot, noProject.ProjectLibrary = "", ""

	tests := []struct {
		name string
		in   skills.Roots
		cfg  userconfig.SkillsConfig
		want [3]string // library, project library, CLI version
	}{
		{"nothing configured keeps the defaults", defaults, userconfig.SkillsConfig{}, [3]string{"/default/lib", "/p/default", "1"}},
		{"a library under the home", defaults, userconfig.SkillsConfig{Library: "~/dot/skills"}, [3]string{"/home/someone/dot/skills", "/p/default", "1"}},
		{"a library by full path", defaults, userconfig.SkillsConfig{Library: "/srv/skills/"}, [3]string{"/srv/skills", "/p/default", "1"}},
		{"a project library sits under the project", defaults, userconfig.SkillsConfig{ProjectLibrary: "tools/skills"}, [3]string{"/default/lib", "/p/tools/skills", "1"}},
		{"a project library without a project is nowhere", noProject, userconfig.SkillsConfig{ProjectLibrary: "tools/skills"}, [3]string{"/default/lib", "", "1"}},
		{"a pinned CLI version", defaults, userconfig.SkillsConfig{CLIVersion: "2.0.0"}, [3]string{"/default/lib", "/p/default", "2.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := withSkillsConfig(tt.in, tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := [3]string{r.Library, r.ProjectLibrary, r.CLIVersion}; got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
	// A hand-edited config can hold what the settings screen would refuse.
	if r, err := withSkillsConfig(defaults, userconfig.SkillsConfig{Library: "skills"}); err == nil {
		t.Errorf("a relative library resolved to %q", r.Library)
	}
}

func TestSkillRootsReadTheLibrariesFromTheUserConfig(t *testing.T) {
	mgr, _ := projectsIn(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
	ctx := context.Background()
	folder := t.TempDir()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Path: folder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := userconfig.Update(func(c *userconfig.Config) error {
		c.Skills = userconfig.SkillsConfig{Library: "~/dot/skills", ProjectLibrary: "tools/skills"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	r, err := mgr.SkillRoots(ctx, "", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Library != filepath.Join(home, "dot", "skills") || r.ProjectLibrary != filepath.Join(folder, "tools", "skills") {
		t.Errorf("library %q, project library %q", r.Library, r.ProjectLibrary)
	}

	// A config that does not parse must not quietly send writes to the
	// default library.
	if err := os.WriteFile(os.Getenv("OMNIPLEX_CONFIG"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := mgr.SkillRoots(ctx, "", p.ID); err == nil {
		t.Errorf("roots from a broken config: %+v", r)
	}
}
