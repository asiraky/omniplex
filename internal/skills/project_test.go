package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// project is a machine with a project: its home folder and one repo, the
// project root, checked out in place.
func project(t *testing.T) Roots {
	t.Helper()
	r := machine(t)
	r.ProjectHome = filepath.Join(filepath.Dir(r.Home), "projects", "demo")
	mkdir(t, r.ProjectHome)
	r.Repos = []Repo{{Dir: r.ProjectRoot, Name: "project", Main: true}}
	return r
}

func lockOf(t *testing.T, folder string) map[string]any {
	t.Helper()
	var lock map[string]any
	if err := json.Unmarshal([]byte(read(t, ProjectCLILock(folder))), &lock); err != nil {
		t.Fatal(err)
	}
	return lock
}

func lockEntry(t *testing.T, folder, name string) map[string]any {
	t.Helper()
	e, _ := lockOf(t, folder)["skills"].(map[string]any)[name].(map[string]any)
	return e
}

func TestDestinations(t *testing.T) {
	r := machine(t)
	if got := r.Destinations(); !reflect.DeepEqual(got, []Destination{{Kind: DestPersonal, Label: "Personal"}}) || r.DefaultDestination() != "" {
		t.Errorf("without a project: %+v, default %q", got, r.DefaultDestination())
	}
	r = project(t)
	other := filepath.Join(filepath.Dir(r.Home), "other")
	// A plain-folder project's home is its folder: offered once, as the project.
	r.Repos = append(r.Repos, Repo{Dir: r.ProjectHome, Name: "demo", Main: true}, Repo{Dir: other, Name: "other"})
	want := []Destination{
		{Kind: DestProject, Folder: r.ProjectHome, Label: "This project"},
		{Kind: DestRepo, Folder: r.ProjectRoot, Label: "project repo", Main: true},
		{Kind: DestRepo, Folder: other, Label: "other repo"},
		{Kind: DestPersonal, Label: "Personal"},
	}
	if got := r.Destinations(); !reflect.DeepEqual(got, want) {
		t.Errorf("destinations = %+v", got)
	}
	if r.DefaultDestination() != r.ProjectHome {
		t.Errorf("default = %q", r.DefaultDestination())
	}
}

func TestInstallIntoEachDestination(t *testing.T) {
	tests := []struct {
		name    string
		folder  func(r Roots) string
		pi      bool
		private bool
	}{
		{name: "the project's home", folder: func(r Roots) string { return r.ProjectHome }, private: true},
		{name: "the project's home, with pi", folder: func(r Roots) string { return r.ProjectHome }, pi: true, private: true},
		{name: "a repo", folder: func(r Roots) string { return r.ProjectRoot }},
		{name: "a repo, with pi", folder: func(r Roots) string { return r.ProjectRoot }, pi: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staging(t)
			r := project(t)
			folder := tt.folder(r)
			if tt.pi {
				mkdir(t, filepath.Join(folder, ".pi"))
			}
			got, src := stageLocal(t, r, twoSkills)
			placed, err := InstallStaged(r, got.ID, []string{"one"}, folder)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(folder, ".agents", "skills", "one")
			if s := placed[0]; s.Dir != dir || s.Folder != folder || s.Private != tt.private || s.Scope != ScopeProject {
				t.Errorf("skill = %+v", s)
			}
			if read(t, filepath.Join(dir, "notes.md")) != "fetched" || isSymlink(dir) {
				t.Error("the skill was not copied into the folder's library")
			}
			// Linked as the skills CLI links, relative, so a clone or a
			// worktree of the folder has a link that works there too.
			links := map[string]bool{".claude": true, ".pi": tt.pi}
			for agent, want := range links {
				at := filepath.Join(folder, agent, "skills", "one")
				target, err := os.Readlink(at)
				if !want {
					if err == nil || exists(at) {
						t.Errorf("%s has a link, with no %s in the folder", agent, agent)
					}
					continue
				}
				if target != filepath.FromSlash("../../.agents/skills/one") {
					t.Errorf("%s link = %q, %v", agent, target, err)
				}
				if resolve(at) != dir {
					t.Errorf("%s link leads to %s", agent, resolve(at))
				}
			}

			want, err := cliHash(filepath.Join(src, "one"))
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(folder, src)
			e := lockEntry(t, folder, "one")
			wantEntry := map[string]any{"source": filepath.ToSlash(rel), "sourceType": "local", "skillPath": "one/SKILL.md", "computedHash": want}
			if !reflect.DeepEqual(e, wantEntry) {
				t.Errorf("lock entry = %v, want %v", e, wantEntry)
			}
			// Read back as the folder it names, so an update finds it.
			if s := byName(t, mustDiscover(t, r))["one"]; s.Source == nil || s.Source.Repo != src || s.Source.Method != MethodLocal {
				t.Errorf("source = %+v", s.Source)
			}
			// Nothing anywhere else: not the personal library, no other folder.
			if entries(t, r.Library) != nil && len(entries(t, r.Library)) != 0 {
				t.Errorf("personal library holds %v", entries(t, r.Library))
			}
			for _, other := range []string{r.ProjectHome, r.ProjectRoot} {
				if other != folder && (exists(ProjectCLILock(other)) || exists(filepath.Join(other, ".agents"))) {
					t.Errorf("%s was written to", other)
				}
			}
		})
	}
}

func TestInstallIntoAProjectLeavesItsOwnFolders(t *testing.T) {
	staging(t)
	r := project(t)
	folder := r.ProjectRoot
	elsewhere := filepath.Join(r.Home, "dev", "one")
	write(t, filepath.Join(elsewhere, "SKILL.md"), skillMD("one", "Mine"))
	link(t, elsewhere, filepath.Join(folder, ".claude", "skills", "one"))
	write(t, filepath.Join(folder, ".claude", "skills", "two", "SKILL.md"), skillMD("two", "Claude's own"))

	got, _ := stageLocal(t, r, twoSkills)
	if _, err := InstallStaged(r, got.ID, []string{"one", "two"}, folder); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(folder, ".claude", "skills", "one")); target != filepath.FromSlash("../../.agents/skills/one") {
		t.Errorf("the old link points at %q", target)
	}
	if read(t, filepath.Join(elsewhere, "SKILL.md")) != skillMD("one", "Mine") {
		t.Error("what the old link pointed at was touched")
	}
	two := filepath.Join(folder, ".claude", "skills", "two")
	if isSymlink(two) || read(t, filepath.Join(two, "SKILL.md")) != skillMD("two", "Claude's own") {
		t.Error("a real folder was replaced by a link")
	}
}

func TestProjectLockKeepsWhatOmniplexDidNotWrite(t *testing.T) {
	staging(t)
	r := project(t)
	folder := r.ProjectHome
	write(t, ProjectCLILock(folder), `{"version":1,"generator":{"by":"someone"},"skills":{`+
		`"zeta":{"source":"z/z","sourceType":"github","computedHash":"z","pinned":true},`+
		`"one":{"source":"old/one","sourceUrl":"https://old.example/one.git","sourceType":"git","computedHash":"old","note":"keep me"}}}`)
	got, _ := stageLocal(t, r, twoSkills)
	if _, err := InstallStaged(r, got.ID, []string{"one"}, folder); err != nil {
		t.Fatal(err)
	}
	lock := lockOf(t, folder)
	if lock["version"] != 1.0 || !reflect.DeepEqual(lock["generator"], map[string]any{"by": "someone"}) {
		t.Errorf("lock = %v", lock)
	}
	skills := lock["skills"].(map[string]any)
	if !reflect.DeepEqual(skills["zeta"], map[string]any{"source": "z/z", "sourceType": "github", "computedHash": "z", "pinned": true}) {
		t.Errorf("another skill's entry became %v", skills["zeta"])
	}
	one := skills["one"].(map[string]any)
	// The old source's fields go; the field Omniplex does not write stays.
	if one["note"] != "keep me" || one["sourceUrl"] != nil || one["sourceType"] != "local" {
		t.Errorf("one = %v", one)
	}
	// Sorted by name, as the CLI writes it, so the two take turns on it.
	text := read(t, ProjectCLILock(folder))
	if strings.Index(text, `"one"`) > strings.Index(text, `"zeta"`) || !strings.HasSuffix(text, "}\n") {
		t.Errorf("lock written as\n%s", text)
	}
}

func TestInstallRefusesALockItCannotRead(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":      "{nope",
		"a new version": `{"version":2,"skills":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			staging(t)
			r := project(t)
			write(t, ProjectCLILock(r.ProjectRoot), content)
			got, _ := stageLocal(t, r, twoSkills)
			if _, err := InstallStaged(r, got.ID, []string{"one"}, r.ProjectRoot); err == nil {
				t.Fatal("installed over a lock it could not read")
			}
			if exists(filepath.Join(r.ProjectRoot, ".agents", "skills", "one")) || read(t, ProjectCLILock(r.ProjectRoot)) != content {
				t.Error("the folder was touched")
			}
		})
	}
}

func TestProjectWritesStayInTheFolder(t *testing.T) {
	for _, rel := range []string{".agents", ".agents/skills", ".claude/skills", LockFile} {
		t.Run(rel, func(t *testing.T) {
			staging(t)
			r := project(t)
			outside := filepath.Join(t.TempDir(), "elsewhere")
			mkdir(t, outside)
			at := filepath.Join(r.ProjectRoot, filepath.FromSlash(rel))
			mkdir(t, filepath.Dir(at))
			if err := os.Symlink(outside, at); err != nil {
				t.Fatal(err)
			}
			got, _ := stageLocal(t, r, twoSkills)
			if _, err := InstallStaged(r, got.ID, []string{"one"}, r.ProjectRoot); err == nil {
				t.Error("installed through a link out of the folder")
			}
			if _, err := Create(r, "fresh", "New", r.ProjectRoot); err == nil {
				t.Error("created through a link out of the folder")
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("wrote outside the folder: %v", entries)
			}
		})
	}
}

func TestADestinationNotOfferedIsRefused(t *testing.T) {
	staging(t)
	r := project(t)
	stranger := filepath.Join(filepath.Dir(r.Home), "stranger")
	mkdir(t, stranger)
	noProject := machine(t)
	cases := map[string]struct {
		r      Roots
		folder string
	}{
		"a folder of no project":             {r, stranger},
		"inside a destination":               {r, filepath.Join(r.ProjectRoot, "sub")},
		"a project folder with no project":   {noProject, noProject.ProjectRoot},
		"the personal library named by path": {r, r.Library},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, _ := stageLocal(t, c.r, twoSkills)
			if _, err := InstallStaged(c.r, got.ID, []string{"one"}, c.folder); !errors.Is(err, ErrInvalid) {
				t.Errorf("install: %v", err)
			}
			if _, err := Create(c.r, "fresh", "New", c.folder); !errors.Is(err, ErrInvalid) {
				t.Errorf("create: %v", err)
			}
			if exists(filepath.Join(c.folder, ".agents")) || exists(filepath.Join(c.folder, ".claude")) || exists(ProjectCLILock(c.folder)) {
				t.Error("written to anyway")
			}
		})
	}
}

func TestCreateInAProject(t *testing.T) {
	r := project(t)
	folder := r.ProjectHome
	mkdir(t, filepath.Join(folder, ".pi"))
	s, err := Create(r, "fresh", "A new skill", folder)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(folder, ".agents", "skills", "fresh")
	if s.Dir != dir || !s.Private || !s.Editable || s.Folder != folder {
		t.Errorf("skill = %+v", s)
	}
	for _, agent := range []string{".claude", ".pi"} {
		if target, _ := os.Readlink(filepath.Join(folder, agent, "skills", "fresh")); target != filepath.FromSlash("../../.agents/skills/fresh") {
			t.Errorf("%s link = %q", agent, target)
		}
	}
	if exists(ProjectCLILock(folder)) {
		t.Error("a skill made here was given a source")
	}
	if !reflect.DeepEqual(s.Harnesses, allHarnesses) {
		t.Errorf("reaches %v", s.Harnesses)
	}

	for _, at := range []string{".claude/skills/claude-own", ".pi/skills/pi-own"} {
		write(t, filepath.Join(folder, filepath.FromSlash(at), "SKILL.md"), skillMD(filepath.Base(at), "d"))
	}
	for _, name := range []string{"fresh", "claude-own", "pi-own"} {
		if _, err := Create(r, name, "Again", folder); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The repo has none of these: the same name is free there.
	if _, err := Create(r, "claude-own", "In the repo", r.ProjectRoot); err != nil {
		t.Error(err)
	}
}

func TestEditAndSwitchAHomeSkill(t *testing.T) {
	r := project(t)
	s, err := Create(r, "home-skill", "Private", r.ProjectHome)
	if err != nil {
		t.Fatal(err)
	}
	content := skillMD("home-skill", "Edited")
	if err := Save(r, s.Dir, content); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(s.Dir, "SKILL.md")) != content {
		t.Error("the edit did not land")
	}
	got, err := SetMode(r, s.Dir, ModeManual)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeManual {
		t.Errorf("mode = %s", got.Mode)
	}
}

func TestUpdateAndRemoveUseTheirFolderLock(t *testing.T) {
	for _, where := range []string{"home", "repo"} {
		t.Run(where, func(t *testing.T) {
			staging(t)
			r := project(t)
			folder, other := r.ProjectHome, r.ProjectRoot
			if where == "repo" {
				folder, other = other, folder
			}
			got, src := stageLocal(t, r, twoSkills)
			if _, err := InstallStaged(r, got.ID, []string{"one", "two"}, folder); err != nil {
				t.Fatal(err)
			}
			// The same skill in the other folder, from the same source, is
			// another install with its own lock.
			again, _ := Fetcher{}.Stage(context.Background(), r, src)
			if _, err := InstallStaged(r, again.ID, []string{"one"}, other); err != nil {
				t.Fatal(err)
			}
			otherLock := read(t, ProjectCLILock(other))
			one := filepath.Join(folder, ".agents", "skills", "one")

			// Edited here only: changed, and the lock's hash says it is the
			// edit, not upstream.
			write(t, filepath.Join(one, "notes.md"), "my edit")
			x := &upstream{t: t}
			if _, by := x.stageUpdate(r, one); !by["one"].Changed || !by["one"].Local {
				t.Errorf("one = %+v", by["one"])
			}

			write(t, filepath.Join(src, "one", "notes.md"), "upstream moved")
			stage, by := x.stageUpdate(r, one)
			if !by["one"].Changed || by["one"].Local || len(by) != 2 {
				t.Fatalf("update = %+v", by)
			}
			if _, err := ApplyUpdate(r, stage.ID, []string{one}); err != nil {
				t.Fatal(err)
			}
			if read(t, filepath.Join(one, "notes.md")) != "upstream moved" {
				t.Error("not updated")
			}
			want, _ := cliHash(filepath.Join(src, "one"))
			if e := lockEntry(t, folder, "one"); e["computedHash"] != want || e["sourceType"] != "local" {
				t.Errorf("lock entry after the update = %v", e)
			}
			if read(t, ProjectCLILock(other)) != otherLock {
				t.Error("the other folder's lock was written")
			}

			if err := Remove(r, one); err != nil {
				t.Fatal(err)
			}
			skills := lockOf(t, folder)["skills"].(map[string]any)
			if _, ok := skills["one"]; ok || skills["two"] == nil {
				t.Errorf("lock after the remove = %v", skills)
			}
			if exists(one) || isSymlink(filepath.Join(folder, ".claude", "skills", "one")) {
				t.Error("the skill or its link is still there")
			}
			if read(t, ProjectCLILock(other)) != otherLock {
				t.Error("the other folder's lock was written")
			}
		})
	}
}

func TestDiscoverProjectFolders(t *testing.T) {
	r := project(t)
	write(t, filepath.Join(r.ProjectHome, ".agents", "skills", "home-skill", "SKILL.md"), skillMD("home-skill", "Private"))
	write(t, filepath.Join(r.ProjectRoot, ".agents", "skills", "repo-skill", "SKILL.md"), skillMD("repo-skill", "Shared"))
	got := byName(t, mustDiscover(t, r))
	if s := got["home-skill"]; !s.Private || s.Folder != r.ProjectHome || s.Scope != ScopeProject {
		t.Errorf("home skill = %+v", s)
	}
	if s := got["repo-skill"]; s.Private || s.Folder != r.ProjectRoot || s.Scope != ScopeProject {
		t.Errorf("repo skill = %+v", s)
	}

	// A plain folder is its own home: its skills are found once, and private.
	r.ProjectHome, r.Repos = r.ProjectRoot, nil
	if s := byName(t, mustDiscover(t, r))["repo-skill"]; !s.Private || s.Folder != r.ProjectRoot {
		t.Errorf("a home that is the root = %+v", s)
	}
}

func TestMarkUncommitted(t *testing.T) {
	staging(t)
	r := project(t)
	isolateGit(t, r)
	git(t, r.ProjectRoot, "init", "-q")
	write(t, filepath.Join(r.ProjectRoot, "README.md"), "hi")
	git(t, r.ProjectRoot, "add", ".")
	git(t, r.ProjectRoot, "commit", "-qm", "init")

	got, _ := stageLocal(t, r, twoSkills)
	if _, err := InstallStaged(r, got.ID, []string{"one", "two"}, r.ProjectRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(r, "home-skill", "Private", r.ProjectHome); err != nil {
		t.Fatal(err)
	}
	marked := func(r Roots) map[string]bool {
		t.Helper()
		list := mustDiscover(t, r)
		MarkUncommitted(context.Background(), r, list)
		out := map[string]bool{}
		for _, s := range list {
			out[s.Name] = s.Uncommitted
		}
		return out
	}
	if m := marked(r); !m["one"] || !m["two"] || m["home-skill"] {
		t.Errorf("before a commit: %v", m)
	}

	git(t, r.ProjectRoot, "add", ".agents/skills/one", ".claude/skills/one")
	git(t, r.ProjectRoot, "commit", "-qm", "one")
	if m := marked(r); m["one"] || !m["two"] {
		t.Errorf("after committing one: %v", m)
	}
	// Committed but for its link: a new worktree's Claude would not see it.
	git(t, r.ProjectRoot, "add", ".agents/skills/two")
	git(t, r.ProjectRoot, "commit", "-qm", "two")
	if m := marked(r); !m["two"] {
		t.Errorf("two's link is not committed: %v", m)
	}

	// Ignored is not committed either: a new worktree gets none of it.
	write(t, filepath.Join(r.ProjectRoot, ".gitignore"), ".agents/skills/home-made/\n.claude/skills/home-made\n")
	git(t, r.ProjectRoot, "add", ".")
	git(t, r.ProjectRoot, "commit", "-qm", "ignore")
	if _, err := Create(r, "home-made", "Ignored", r.ProjectRoot); err != nil {
		t.Fatal(err)
	}
	if m := marked(r); !m["home-made"] {
		t.Errorf("an ignored skill is not marked: %v", m)
	}

	// A worktree's checkout is not the main one: nothing to warn about.
	worktree := r
	worktree.Repos = []Repo{{Dir: r.ProjectRoot, Name: "project"}}
	if m := marked(worktree); m["two"] {
		t.Errorf("marked outside a main checkout: %v", m)
	}
	// Not a repository: left unmarked.
	if err := os.RemoveAll(filepath.Join(r.ProjectRoot, ".git")); err != nil {
		t.Fatal(err)
	}
	if m := marked(r); m["two"] {
		t.Errorf("marked with no repository: %v", m)
	}
}

func TestCLIHash(t *testing.T) {
	dir := t.TempDir()
	writeAll(t, dir, map[string]string{
		"a.md":               "a",
		"B.md":               "b",
		".git/HEAD":          "ignored",
		"node_modules/x/y":   "ignored",
		"scripts/run.sh":     "run",
		"scripts/.hidden.md": "h",
	})
	link(t, "a.md", filepath.Join(dir, "alias.md"))
	link(t, "scripts", filepath.Join(dir, "more"))
	h := sha256.New()
	// localeCompare order, not byte order: a before B, punctuation first.
	for _, f := range [][2]string{{"a.md", "a"}, {"B.md", "b"}, {"scripts/.hidden.md", "h"}, {"scripts/run.sh", "run"}} {
		h.Write([]byte(f[0] + f[1]))
	}
	want := hex.EncodeToString(h.Sum(nil))
	if got, err := cliHash(dir); err != nil || got != want {
		t.Errorf("hash = %s, %v; want %s", got, err, want)
	}
}
