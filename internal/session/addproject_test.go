package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/store"
)

// emptyManager is a manager with nothing registered, which is what adding a
// project actually starts from.
func emptyManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "workspaces.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	return mgr, st
}

func TestAddProjectRegistersADirectory(t *testing.T) {
	mgr, st := emptyManager(t)
	root, _, _ := gitRepo(t)

	p, err := mgr.AddProject(context.Background(), root, "")
	if err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if p.Root != root {
		t.Fatalf("project root is %q, want %q", p.Root, root)
	}
	if p.ID == "" {
		t.Fatal("project has no id")
	}
	got, err := st.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != p.ID {
		t.Fatalf("store holds %+v, want just the new project", got)
	}
}

// filepath.Abs alone was happy to register a path that is not there, and the
// operator only found out when every later session failed.
func TestAddProjectRefusesAPathThatIsNotThere(t *testing.T) {
	mgr, _ := emptyManager(t)
	missing := filepath.Join(t.TempDir(), "nope")

	_, err := mgr.AddProject(context.Background(), missing, "")
	if err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("error is %v, want one about a missing directory", err)
	}
}

func TestAddProjectRefusesAFile(t *testing.T) {
	mgr, _ := emptyManager(t)
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.AddProject(context.Background(), file, "")
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error is %v, want one about the path not being a directory", err)
	}
}

func TestAddProjectRefusesAnEmptyRoot(t *testing.T) {
	mgr, _ := emptyManager(t)
	if _, err := mgr.AddProject(context.Background(), "  ", ""); err == nil {
		t.Fatal("adding an empty root succeeded")
	}
}

// The UNIQUE constraint on projects.root used to reach the UI as
// "UNIQUE constraint failed: projects.root", which means nothing to anybody.
func TestAddProjectRefusesADuplicateReadably(t *testing.T) {
	mgr, _ := emptyManager(t)
	root, _, _ := gitRepo(t)
	if _, err := mgr.AddProject(context.Background(), root, ""); err != nil {
		t.Fatal(err)
	}

	// The same directory named a second way must still be caught.
	_, err := mgr.AddProject(context.Background(), root+string(filepath.Separator)+".", "")
	if err == nil {
		t.Fatal("adding the same directory twice succeeded")
	}
	if !strings.Contains(err.Error(), "already a project") {
		t.Fatalf("error is %q, want a readable duplicate message", err)
	}
	if strings.Contains(err.Error(), "UNIQUE constraint") {
		t.Fatalf("error %q leaks the SQLite constraint", err)
	}
}

// Adding by URL clones and then registers the clone, config and all — the
// project file lives in the repo, so it arrives with it.
func TestAddProjectClonesAURL(t *testing.T) {
	mgr, st := emptyManager(t)
	src, _, _ := gitRepo(t)

	cfg := project.DefaultConfig(src)
	cfg.Workspace.Provision = "scripts/omniplex-provision.mjs"
	cfg.Defaults.BaseBranch = "main"
	if err := os.MkdirAll(filepath.Join(src, ".omniplex"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, project.ConfigPath), b, 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, src, "add project config")

	dest := filepath.Join(t.TempDir(), "cloned")
	p, err := mgr.AddProject(context.Background(), dest, src)
	if err != nil {
		t.Fatalf("AddProject by url: %v", err)
	}
	if p.Root != dest {
		t.Fatalf("project root is %q, want the clone destination %q", p.Root, dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Fatalf("clone destination has no checkout: %v", err)
	}
	if p.Config.Workspace.Provision != "scripts/omniplex-provision.mjs" {
		t.Fatalf("provision hook is %q, want the one from the cloned repo", p.Config.Workspace.Provision)
	}
	if p.Config.Defaults.BaseBranch != "main" {
		t.Fatalf("base branch is %q, want the one from the cloned repo", p.Config.Defaults.BaseBranch)
	}
	got, err := st.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Root != dest {
		t.Fatalf("store holds %+v, want the clone registered", got)
	}
}

// A clone that cannot happen registers nothing: a half-added project is worse
// than no project.
func TestAddProjectRegistersNothingWhenTheCloneFails(t *testing.T) {
	mgr, st := emptyManager(t)
	dest := filepath.Join(t.TempDir(), "cloned")

	_, err := mgr.AddProject(context.Background(), dest, filepath.Join(t.TempDir(), "not-a-repo"))
	if err == nil {
		t.Fatal("cloning a path that is not a repo succeeded")
	}
	got, err := st.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("store holds %+v after a failed clone, want nothing", got)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a failed clone left %s behind", dest)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", msg}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
