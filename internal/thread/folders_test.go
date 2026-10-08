package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/store"
)

// projectsIn points the projects folder at a temp dir for the test and
// returns a manager over an empty database.
func projectsIn(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"version":1,"projectsDir":"`+dir+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMNIPLEX_CONFIG", cfg)
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)
	return mgr, dir
}

// A plain folder added as a project is its home; a repo waits for one.
func TestAFolderOnDiskBecomesAProjectWhereItIs(t *testing.T) {
	mgr, _ := projectsIn(t)
	ctx := context.Background()
	plain := t.TempDir()
	repo, _, _ := gitRepo(t)
	pp, err := mgr.NewProject(ctx, NewProjectOptions{Path: plain})
	if err != nil {
		t.Fatal(err)
	}
	gp, err := mgr.NewProject(ctx, NewProjectOptions{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	if pp.Home != plain || pp.Folders[0].Path != plain || pp.Folders[0].Git {
		t.Fatalf("plain folder project: %+v", pp)
	}
	if gp.Home != "" || gp.Folders[0].Path != repo || !gp.Folders[0].Git || gp.Name != filepath.Base(repo) {
		t.Fatalf("repo project: %+v", gp)
	}
}

func TestANamedProjectGetsAHomeAndATakenNameIsNumbered(t *testing.T) {
	mgr, dir := projectsIn(t)
	ctx := context.Background()
	a, err := mgr.NewProject(ctx, NewProjectOptions{Name: "Bowerbird"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := mgr.NewProject(ctx, NewProjectOptions{Name: "Bowerbird"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Home != filepath.Join(dir, "bowerbird") || b.Home != filepath.Join(dir, "bowerbird-2") {
		t.Fatalf("homes %q and %q", a.Home, b.Home)
	}
	if b.Folders[0].Path != b.Home {
		t.Fatalf("a new project's folder is %q, not its home %q", b.Folders[0].Path, b.Home)
	}
	if info, err := os.Stat(b.Home); err != nil || !info.IsDir() {
		t.Fatalf("home not made: %v", err)
	}
}

// The home is never a repo: a clone goes in a folder of its own inside it.
func TestACloneLandsInsideTheNewHome(t *testing.T) {
	mgr, dir := projectsIn(t)
	src, _, _ := gitRepo(t)
	p, err := mgr.NewProject(context.Background(), NewProjectOptions{URL: src})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Base(src)
	want := filepath.Join(dir, repo, repo)
	if p.Home != filepath.Join(dir, repo) || p.Folders[0].Path != want || !p.Folders[0].Git {
		t.Fatalf("clone project: home %q folder %+v", p.Home, p.Folders[0])
	}
}

func TestAFailedCloneLeavesNoProjectAndNoHome(t *testing.T) {
	mgr, dir := projectsIn(t)
	_, err := mgr.NewProject(context.Background(), NewProjectOptions{URL: filepath.Join(t.TempDir(), "gone")})
	if err == nil {
		t.Fatal("cloning nothing made a project")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
	if ps, _ := mgr.Projects(context.Background()); len(ps) != 0 {
		t.Fatalf("left a project: %+v", ps)
	}
}

func TestNewFoldersAndClonesGoInTheHomeNumberedWhenTaken(t *testing.T) {
	mgr, _ := projectsIn(t)
	ctx := context.Background()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Name: "Recipes"})
	if err != nil {
		t.Fatal(err)
	}
	if p, err = mgr.AddFolder(ctx, p.ID, AddFolderOptions{Name: "notes"}); err != nil {
		t.Fatal(err)
	}
	if p, err = mgr.AddFolder(ctx, p.ID, AddFolderOptions{Name: "notes"}); err != nil {
		t.Fatal(err)
	}
	src, _, _ := gitRepo(t)
	if p, err = mgr.AddFolder(ctx, p.ID, AddFolderOptions{URL: src}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range p.Folders[1:] {
		got = append(got, strings.TrimPrefix(f.Path, p.Home+"/"))
	}
	if want := []string{"notes", "notes-2", filepath.Base(src)}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("folders %v, want %v", got, want)
	}
	if !p.Folders[3].Git {
		t.Fatal("the clone is not a git folder")
	}
	if _, err := mgr.AddFolder(ctx, p.ID, AddFolderOptions{Name: "../out"}); err == nil {
		t.Fatal("a folder name climbed out of the home")
	}
}

func TestAFolderInsideAGitFolderOfTheProjectIsRefusedByName(t *testing.T) {
	mgr, _ := projectsIn(t)
	ctx := context.Background()
	repo, _, _ := gitRepo(t)
	inside := filepath.Join(repo, "docs")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := mgr.NewProject(ctx, NewProjectOptions{Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mgr.AddFolder(ctx, p.ID, AddFolderOptions{Path: inside})
	if err == nil || !strings.Contains(err.Error(), repo) {
		t.Fatalf("error %v does not name the git folder %s", err, repo)
	}
	if _, err := mgr.AddFolder(ctx, p.ID, AddFolderOptions{Path: repo}); err == nil {
		t.Fatal("the same folder was added twice")
	}
	// A plain folder holding a repo is harmless.
	plain := t.TempDir()
	q, err := mgr.NewProject(ctx, NewProjectOptions{Path: plain})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddFolder(ctx, q.ID, AddFolderOptions{Name: "sub"}); err != nil {
		t.Fatalf("a folder inside a plain folder was refused: %v", err)
	}
}

func TestRemovingAFolderLeavesItOnDiskAndKeepsTheLastOne(t *testing.T) {
	mgr, _ := projectsIn(t)
	ctx := context.Background()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Name: "Site"})
	if err != nil {
		t.Fatal(err)
	}
	if p, err = mgr.AddFolder(ctx, p.ID, AddFolderOptions{Name: "assets"}); err != nil {
		t.Fatal(err)
	}
	assets := p.Folders[1]
	if p, err = mgr.RemoveFolder(ctx, p.ID, assets.ID); err != nil {
		t.Fatal(err)
	}
	if len(p.Folders) != 1 {
		t.Fatalf("folders after removal: %+v", p.Folders)
	}
	if _, err := os.Stat(assets.Path); err != nil {
		t.Fatalf("removing the folder deleted it: %v", err)
	}
	if _, err := mgr.RemoveFolder(ctx, p.ID, p.Folders[0].ID); err == nil {
		t.Fatal("removed a project's last folder")
	}
	if _, err := mgr.RemoveFolder(ctx, p.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown folder: %v", err)
	}
}

// A thread across the whole project starts in the home folder and is handed
// every folder the home does not already hold, once. A thread in one folder
// gets only the home, not its sibling folders.
func TestAThreadAcrossTheProjectReachesEveryFolder(t *testing.T) {
	mgr, _ := projectsIn(t)
	ctx := context.Background()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Name: "Bowerbird"})
	if err != nil {
		t.Fatal(err)
	}
	home := p.Home
	repo, _, _ := gitRepo(t)
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, o := range []AddFolderOptions{{Path: repo}, {Name: "notes"}, {Path: other}, {Path: filepath.Join(other, "sub")}} {
		if p, err = mgr.AddFolder(ctx, p.ID, o); err != nil {
			t.Fatalf("add %+v: %v", o, err)
		}
	}

	all := harnessExtras(ctx, mgr.store, nil, store.ThreadMeta{ID: "t", ProjectID: p.ID}, home, t.Logf).extraDirs
	if want := []string{repo, other}; !slices.Equal(all, want) {
		t.Fatalf("whole-project thread reaches %v, want %v", all, want)
	}

	var repoID string
	for _, f := range p.Folders {
		if f.Path == repo {
			repoID = f.ID
		}
	}
	one := harnessExtras(ctx, mgr.store, nil, store.ThreadMeta{ID: "t", ProjectID: p.ID, FolderID: repoID}, repo, t.Logf).extraDirs
	if want := []string{home}; !slices.Equal(one, want) {
		t.Fatalf("one-folder thread reaches %v, want %v", one, want)
	}
}

// A project's private skills live in its home and reach a thread working
// anywhere else, in either of its repos or a worktree of one, with the home's
// .claude/skills already there for Claude to watch. A thread in the home
// itself, or with no project, gets no extra skills folder.
func TestPrivateProjectSkillsReachEveryThreadOutsideTheHome(t *testing.T) {
	mgr, _ := projectsIn(t)
	ctx := context.Background()
	p, err := mgr.NewProject(ctx, NewProjectOptions{Name: "Bowerbird"})
	if err != nil {
		t.Fatal(err)
	}
	home := p.Home
	web, webTree, _ := gitRepo(t)
	api, _, _ := gitRepo(t)
	for _, path := range []string{web, api} {
		if p, err = mgr.AddFolder(ctx, p.ID, AddFolderOptions{Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	folder := map[string]string{}
	for _, f := range p.Folders {
		folder[f.Path] = f.ID
	}

	want := []string{filepath.Join(home, ".agents", "skills")}
	for _, tc := range []struct{ name, cwd, folder string }{
		{"web", web, web},
		{"api", api, api},
		{"web worktree", webTree, web},
	} {
		got := harnessExtras(ctx, mgr.store, nil, store.ThreadMeta{ID: "t", ProjectID: p.ID, FolderID: folder[tc.folder]}, tc.cwd, t.Logf).skillDirs
		if !slices.Equal(got, want) {
			t.Errorf("%s: skill dirs %v, want %v", tc.name, got, want)
		}
	}
	for _, d := range []string{want[0], filepath.Join(home, ".claude", "skills")} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("%s not made before the session: %v", d, err)
		}
	}

	if got := harnessExtras(ctx, mgr.store, nil, store.ThreadMeta{ID: "t", ProjectID: p.ID}, home, t.Logf).skillDirs; got != nil {
		t.Errorf("whole-project thread in the home got %v", got)
	}

	plain := t.TempDir()
	pp, err := mgr.NewProject(ctx, NewProjectOptions{Path: plain})
	if err != nil {
		t.Fatal(err)
	}
	if got := harnessExtras(ctx, mgr.store, nil, store.ThreadMeta{ID: "t", ProjectID: pp.ID, FolderID: pp.Folders[0].ID}, plain, t.Logf).skillDirs; got != nil {
		t.Errorf("plain-folder thread in its home got %v", got)
	}
	if _, err := os.Stat(filepath.Join(plain, ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a thread in the home made .claude there: %v", err)
	}

	if got := harnessExtras(ctx, mgr.store, nil, store.ThreadMeta{ID: "t"}, t.TempDir(), t.Logf).skillDirs; got != nil {
		t.Errorf("thread with no project got %v", got)
	}
}
