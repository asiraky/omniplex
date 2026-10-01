package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// stageLocal stages a folder of skills laid out as name/file.
func stageLocal(t *testing.T, r Roots, files map[string]string) (Staged, string) {
	t.Helper()
	src := filepath.Join(r.Home, "src")
	writeAll(t, src, files)
	got, err := Fetcher{}.Stage(context.Background(), r, src)
	if err != nil {
		t.Fatal(err)
	}
	return got, src
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

var twoSkills = map[string]string{
	"one/SKILL.md":       skillMD("one", "The first"),
	"one/notes.md":       "fetched",
	"one/scripts/run.sh": "#!/bin/sh\n",
	"two/SKILL.md":       skillMD("two", "The second"),
}

func TestInstallStaged(t *testing.T) {
	tmp := staging(t)
	r := machine(t)
	got, src := stageLocal(t, r, twoSkills)

	placed, err := InstallStaged(r, got.ID, []string{"one", "one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 {
		t.Fatalf("placed %d skills", len(placed))
	}
	s := placed[0]
	dir := filepath.Join(r.Library, "one")
	if s.Dir != dir || s.Name != "one" || s.Scope != ScopeUser || !s.Editable || s.Mode != ModeOn {
		t.Errorf("skill = %+v", s)
	}
	if read(t, filepath.Join(dir, "scripts", "run.sh")) != "#!/bin/sh\n" || isSymlink(dir) {
		t.Error("the skill was not copied in as plain files")
	}
	if !reflect.DeepEqual(entries(t, r.Library), []string{RecordFile, "one"}) {
		t.Errorf("library holds %v", entries(t, r.Library))
	}

	want, err := HashDir(filepath.Join(src, "one"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := LoadRecord(r.Library)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := rec.Get("one")
	if !ok || e.Method != MethodLocal || e.Repo != src || e.Path != "one" || e.Hash != want {
		t.Errorf("record = %+v, want the source and the hash %s", e, want)
	}
	if e.InstalledAt == "" || e.UpdatedAt != e.InstalledAt {
		t.Errorf("record dates = %q and %q", e.InstalledAt, e.UpdatedAt)
	}
	if s.Source == nil || !s.Source.Managed || s.Source.Repo != src {
		t.Errorf("source = %+v", s.Source)
	}
	// Installing is the end of a fetch.
	if left := stagingDirs(t, tmp); len(left) != 0 {
		t.Errorf("staging left behind: %v", left)
	}
	if _, err := InstallStaged(r, got.ID, []string{"two"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("installing from a fetch already used: %v", err)
	}
}

func TestInstallRecordsWhatTheCLIResolved(t *testing.T) {
	staging(t)
	r := machine(t)
	x := &tools{npx: npxAdds(t, npxRepo)}
	got, err := x.fetcher().Stage(context.Background(), r, "humanlayer/skills")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallStaged(r, got.ID, []string{"show-me"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := LoadRecord(r.Library)
	e, _ := rec.Get("show-me")
	if e.Method != MethodNpx || e.Repo != "humanlayer/skills" || e.Ref != "main" || e.Path != "skills/show-me" {
		t.Errorf("record = %+v", e)
	}
}

func TestInstallReplacesASkillAlreadyThere(t *testing.T) {
	staging(t)
	r := machine(t)
	dir := filepath.Join(r.Library, "one")
	write(t, filepath.Join(dir, "SKILL.md"), skillMD("one", "Mine"))
	write(t, filepath.Join(dir, "mine.md"), "written here")
	got, _ := stageLocal(t, r, twoSkills)
	if one := got.Skills[0]; one.Name != "one" || !one.Installed || got.Skills[1].Installed {
		t.Fatalf("staged = %+v", got.Skills)
	}

	placed, err := InstallStaged(r, got.ID, []string{"two", "one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 2 {
		t.Errorf("placed %d", len(placed))
	}
	// Replaced, not merged: what only the old copy had is gone.
	if exists(filepath.Join(dir, "mine.md")) || read(t, filepath.Join(dir, "notes.md")) != "fetched" {
		t.Errorf("one holds %v", entries(t, dir))
	}
	if !reflect.DeepEqual(entries(t, r.Library), []string{RecordFile, "one", "two"}) {
		t.Errorf("library holds %v", entries(t, r.Library))
	}
	if d := byName(t, mustDiscover(t, r))["one"]; d.Description != "The first" {
		t.Errorf("listed as %+v", d)
	}
}

func TestInstallRefusals(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, r Roots)
		names   []string
	}{
		{name: "nothing picked"},
		{name: "a skill that was not fetched", names: []string{"one", "three"}},
		{name: "a path for a name", names: []string{"../one"}},
		{name: "a file where the skill would go", names: []string{"two", "one"},
			prepare: func(t *testing.T, r Roots) { write(t, filepath.Join(r.Library, "one"), "a file") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := staging(t)
			r := machine(t)
			if tt.prepare != nil {
				tt.prepare(t, r)
			}
			before := entries(t, r.Library)
			got, _ := stageLocal(t, r, twoSkills)
			if placed, err := InstallStaged(r, got.ID, tt.names); !errors.Is(err, ErrInvalid) {
				t.Fatalf("placed %+v, err %v", placed, err)
			}
			if after := entries(t, r.Library); !reflect.DeepEqual(after, before) {
				t.Errorf("library went from %v to %v", before, after)
			}
			if len(stagingDirs(t, tmp)) != 1 {
				t.Error("a refused install dropped the fetch")
			}
		})
	}
}

func TestCopySkillLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(base, "outside.md"), "not the skill's")
	tests := []struct {
		name   string
		at     string // the link, inside the skill
		target string
		ok     bool
	}{
		{"a file beside it", "alias.md", "notes.md", true},
		{"a file in a folder", "alias.md", "docs/guide.md", true},
		{"up and across, inside the skill", "docs/alias.md", "../notes.md", true},
		{"a folder of the skill", "more", "docs", true},
		{"out of the skill", "alias.md", "../outside.md", false},
		{"out and back in", "alias.md", "../skill/notes.md", false},
		{"an absolute path outside", "alias.md", filepath.Join(base, "outside.md"), false},
		{"an absolute path inside", "alias.md", filepath.Join(base, "skill", "notes.md"), false},
		{"nothing at all", "alias.md", "gone.md", false},
		{"a chain that ends outside", "alias.md", "docs/hop", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := filepath.Join(base, "skill")
			if err := os.RemoveAll(src); err != nil {
				t.Fatal(err)
			}
			writeAll(t, src, map[string]string{"SKILL.md": skillMD("skill", "s"), "notes.md": "notes", "docs/guide.md": "guide"})
			if tt.target == "docs/hop" {
				link(t, "../../outside.md", filepath.Join(src, "docs", "hop"))
			}
			link(t, tt.target, filepath.Join(src, filepath.FromSlash(tt.at)))
			dst := filepath.Join(t.TempDir(), "copy")
			err := copySkill(src, dst)
			if !tt.ok {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("err = %v, want the link refused", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			to := filepath.Join(dst, filepath.FromSlash(tt.at))
			if got, _ := os.Readlink(to); got != tt.target {
				t.Errorf("link copied as %q, want %q", got, tt.target)
			}
			if _, err := os.Stat(to); err != nil {
				t.Errorf("the copied link leads nowhere: %v", err)
			}
		})
	}
}

func TestCopySkillLeavesGitBehind(t *testing.T) {
	src, dst := filepath.Join(t.TempDir(), "skill"), filepath.Join(t.TempDir(), "copy")
	writeAll(t, src, map[string]string{
		"SKILL.md":      skillMD("skill", "s"),
		".git/HEAD":     "ref",
		"sub/.git":      "gitdir: elsewhere",
		"sub/kept.md":   "kept",
		".gitignore":    "kept too",
		"scripts/go.sh": "#!/bin/sh\n",
	})
	if err := os.Chmod(filepath.Join(src, "scripts", "go.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copySkill(src, dst); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, ".git")) || exists(filepath.Join(dst, "sub", ".git")) {
		t.Error(".git was copied")
	}
	if !exists(filepath.Join(dst, ".gitignore")) || read(t, filepath.Join(dst, "sub", "kept.md")) != "kept" {
		t.Errorf("copy holds %v", entries(t, dst))
	}
	if info, err := os.Stat(filepath.Join(dst, "scripts", "go.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Error("a script stopped being executable")
	}
}

func TestInstallReplacesALinkedSkill(t *testing.T) {
	staging(t)
	r := machine(t)
	elsewhere := filepath.Join(r.Home, "dev", "one")
	write(t, filepath.Join(elsewhere, "SKILL.md"), skillMD("one", "Mine"))
	link(t, elsewhere, filepath.Join(r.Library, "one"))
	got, _ := stageLocal(t, r, twoSkills)
	if !got.Skills[0].Installed {
		t.Fatalf("staged = %+v", got.Skills)
	}
	if _, err := InstallStaged(r, got.ID, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if isSymlink(filepath.Join(r.Library, "one")) || read(t, filepath.Join(r.Library, "one", "notes.md")) != "fetched" {
		t.Error("the link was not replaced by the fetched copy")
	}
	if read(t, filepath.Join(elsewhere, "SKILL.md")) != skillMD("one", "Mine") {
		t.Error("the folder the link pointed at was touched")
	}
}
