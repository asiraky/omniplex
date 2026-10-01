package thread

import (
	"context"
	"testing"
)

func TestSkillRootsNameTheProject(t *testing.T) {
	mgr, _ := projectsIn(t)
	t.Setenv("HOME", t.TempDir())
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
	ctx := context.Background()
	folder := t.TempDir()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Path: folder, Name: "Bowerbird"})
	if err != nil {
		t.Fatal(err)
	}
	roots, name, err := mgr.SkillRoots(ctx, "", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if roots.ProjectRoot != folder || name != "Bowerbird" {
		t.Errorf("project root %q named %q", roots.ProjectRoot, name)
	}

	roots, name, err = mgr.SkillRoots(ctx, "", "")
	if err != nil || roots.ProjectRoot != "" || name != "" {
		t.Errorf("with no project: root %q, name %q, err %v", roots.ProjectRoot, name, err)
	}
}
