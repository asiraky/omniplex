package thread

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/store"
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

func TestSkillRootsOfferTheProjectsFolders(t *testing.T) {
	mgr, projects := projectsIn(t)
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	a, worktree, _ := gitRepo(t)
	b, _, _ := gitRepo(t)
	plain := t.TempDir()
	folders := []project.Folder{project.NewFolder("fa", a), project.NewFolder("fb", b), project.NewFolder("fc", plain)}
	folders[0].Git, folders[1].Git = true, true
	now := proto.NowMillis()
	p := project.Project{ID: "p1", Name: "Kiosk", Defaults: project.NormalizeDefaults(project.Defaults{}), Folders: folders, CreatedAt: now, UpdatedAt: now}
	if err := putProject(ctx, mgr.store, p); err != nil {
		t.Fatal(err)
	}
	thread := func(id, folderID, cwd string) string {
		t.Helper()
		meta := store.ThreadMeta{ID: id, Cwd: cwd, Harness: "fake", ProjectID: p.ID, FolderID: folderID, Phase: "idle", CreatedAt: now, UpdatedAt: now}
		if err := mgr.store.CreateThread(ctx, meta); err != nil {
			t.Fatal(err)
		}
		return id
	}
	both := []skills.Repo{{Dir: a, Name: filepath.Base(a), Main: true}, {Dir: b, Name: filepath.Base(b), Main: true}}
	tests := []struct {
		name              string
		threadID, project string
		repos             []skills.Repo
	}{
		{name: "the project", project: p.ID, repos: both},
		{name: "a thread on the whole project", threadID: thread("whole", "", plain), repos: both},
		{name: "a thread in a folder's main checkout", threadID: thread("main", "fa", a),
			repos: []skills.Repo{{Dir: a, Name: filepath.Base(a), Main: true}}},
		{name: "a thread in a worktree", threadID: thread("side", "fa", worktree),
			repos: []skills.Repo{{Dir: worktree, Name: filepath.Base(a)}}},
		{name: "a thread in a plain folder", threadID: thread("plain", "fc", plain)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots, _, err := mgr.SkillRoots(ctx, tt.threadID, tt.project)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(roots.ProjectHome, projects) {
				t.Errorf("home = %q, want it in the projects folder", roots.ProjectHome)
			}
			if !reflect.DeepEqual(roots.Repos, tt.repos) {
				t.Errorf("repos = %+v, want %+v", roots.Repos, tt.repos)
			}
		})
	}

	roots, _, err := mgr.SkillRoots(ctx, "", "")
	if err != nil || roots.ProjectHome != "" || roots.Repos != nil {
		t.Errorf("with no project: %+v, %v", roots, err)
	}
}
