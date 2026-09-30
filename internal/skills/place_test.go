package skills

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	// The default library is the dir Codex and pi read; Claude needs a link.
	placed, err := InstallStaged(r, got.ID, []string{"one", "one"}, ScopeUser, []Harness{Claude, Codex}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 1 {
		t.Fatalf("placed %d skills", len(placed))
	}
	s := placed[0]
	dir := filepath.Join(r.Library, "one")
	if s.Dir != dir || s.Name != "one" || s.Scope != ScopeUser || !s.Editable {
		t.Errorf("skill = %+v", s)
	}
	for _, h := range []Harness{Claude, Codex, Pi} {
		if !containsHarness(s.Harnesses, h) {
			t.Errorf("%s cannot see the skill: %v", h, s.Harnesses)
		}
	}
	if !isSymlink(filepath.Join(r.ClaudeConfigDir, "skills", "one")) {
		t.Error("no link was made for Claude")
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
	if _, err := InstallStaged(r, got.ID, []string{"two"}, ScopeUser, nil, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("installing from a fetch already used: %v", err)
	}
}

func TestInstallIntoAProject(t *testing.T) {
	staging(t)
	r := machine(t)
	got, _ := stageLocal(t, r, twoSkills)
	placed, err := InstallStaged(r, got.ID, []string{"two", "one"}, ScopeProject, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(placed) != 2 || placed[0].Name != "two" || placed[0].Scope != ScopeProject || placed[1].Dir != filepath.Join(r.ProjectLibrary, "one") {
		t.Errorf("placed = %+v", placed)
	}
	rec, _ := LoadRecord(r.ProjectLibrary)
	if _, ok := rec.Get("two"); !ok || len(rec.Skills) != 2 {
		t.Errorf("project record = %+v", rec)
	}
	if exists(r.Library) {
		t.Error("a project install wrote to the personal library")
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
	if _, err := InstallStaged(r, got.ID, []string{"show-me"}, ScopeUser, nil, false); err != nil {
		t.Fatal(err)
	}
	rec, _ := LoadRecord(r.Library)
	e, _ := rec.Get("show-me")
	if e.Method != MethodNpx || e.Repo != "humanlayer/skills" || e.Ref != "main" || e.Path != "skills/show-me" {
		t.Errorf("record = %+v", e)
	}
}

func TestInstallOverASkillAlreadyThere(t *testing.T) {
	staging(t)
	r := machine(t)
	dir := filepath.Join(r.Library, "one")
	write(t, filepath.Join(dir, "SKILL.md"), skillMD("one", "Mine"))
	write(t, filepath.Join(dir, "mine.md"), "written here")
	got, _ := stageLocal(t, r, twoSkills)

	_, err := InstallStaged(r, got.ID, []string{"two", "one"}, ScopeUser, nil, false)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "one") {
		t.Fatalf("err = %v, want a refusal naming the skill", err)
	}
	// Nothing of the batch landed, and the fetch is still there to retry.
	if !reflect.DeepEqual(entries(t, r.Library), []string{"one"}) || read(t, filepath.Join(dir, "mine.md")) != "written here" {
		t.Errorf("library holds %v after a refusal", entries(t, r.Library))
	}

	placed, err := InstallStaged(r, got.ID, []string{"two", "one"}, ScopeUser, nil, true)
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
}

func TestInstallRefusals(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, r Roots)
		names   []string
		scope   string
		link    []Harness
		replace bool
	}{
		{name: "nothing picked", scope: ScopeUser},
		{name: "a skill that was not fetched", names: []string{"one", "three"}, scope: ScopeUser},
		{name: "a path for a name", names: []string{"../one"}, scope: ScopeUser},
		{name: "a harness nobody has heard of", names: []string{"one"}, scope: ScopeUser, link: []Harness{"cursor"}},
		{name: "a scope that is not a library", names: []string{"one"}, scope: ScopePlugin},
		{name: "a file where the skill would go", names: []string{"one"}, scope: ScopeUser, replace: true,
			prepare: func(t *testing.T, r Roots) { write(t, filepath.Join(r.Library, "one"), "a file") }},
		{name: "a harness with its own skill of that name", names: []string{"two", "one"}, scope: ScopeUser, link: []Harness{Claude},
			prepare: func(t *testing.T, r Roots) {
				write(t, filepath.Join(r.ClaudeConfigDir, "skills", "one", "SKILL.md"), skillMD("one", "Claude's own"))
			}},
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
			if placed, err := InstallStaged(r, got.ID, tt.names, tt.scope, tt.link, tt.replace); !errors.Is(err, ErrInvalid) {
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

func TestInstallLinksOnlyWhereALinkIsNeeded(t *testing.T) {
	staging(t)
	r := machine(t)
	r.Library = filepath.Join(r.Home, "library")
	claude := filepath.Join(r.ClaudeConfigDir, "skills")
	// A link an earlier copy of the skill left behind is reused, not a clash.
	mkdir(t, filepath.Join(r.Library, "two"))
	link(t, filepath.Join(r.Library, "two"), filepath.Join(claude, "two"))
	if err := os.Remove(filepath.Join(r.Library, "two")); err != nil {
		t.Fatal(err)
	}
	got, _ := stageLocal(t, r, twoSkills)

	placed, err := InstallStaged(r, got.ID, []string{"one", "two"}, ScopeUser, []Harness{Claude}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range placed {
		if !reflect.DeepEqual(s.Harnesses, []Harness{Claude}) {
			t.Errorf("%s is seen by %v, want only the harness asked for", s.Name, s.Harnesses)
		}
	}
	if !isSymlink(filepath.Join(claude, "one")) || read(t, filepath.Join(claude, "two", "SKILL.md")) != twoSkills["two/SKILL.md"] {
		t.Errorf("claude's dir holds %v", entries(t, claude))
	}
	if exists(filepath.Join(r.Home, ".agents", "skills")) {
		t.Error("a link was made for a harness that was not asked for")
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
