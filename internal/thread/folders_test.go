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

	_, all := harnessExtras(ctx, mgr.store, store.ThreadMeta{ID: "t", ProjectID: p.ID}, home, t.Logf)
	if want := []string{repo, other}; !slices.Equal(all, want) {
		t.Fatalf("whole-project thread reaches %v, want %v", all, want)
	}

	var repoID string
	for _, f := range p.Folders {
		if f.Path == repo {
			repoID = f.ID
		}
	}
	_, one := harnessExtras(ctx, mgr.store, store.ThreadMeta{ID: "t", ProjectID: p.ID, FolderID: repoID}, repo, t.Logf)
	if want := []string{home}; !slices.Equal(one, want) {
		t.Fatalf("one-folder thread reaches %v, want %v", one, want)
	}
}
