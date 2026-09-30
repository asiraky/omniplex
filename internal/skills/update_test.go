package skills

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// upstream is a repo of skills that a test changes between fetches. Its npx
// leaves what `skills add` would: the skills and a lock naming their folders.
type upstream struct {
	tools
	t     *testing.T
	files map[string]string // <skill>/<file>
	// folder is where a skill lives in the repo, when not skills/<skill>.
	folder map[string]string
}

func newUpstream(t *testing.T, files map[string]string) *upstream {
	u := &upstream{t: t, files: maps.Clone(files), folder: map[string]string{}}
	u.npx = func(c Command) ([]byte, error) {
		lock := map[string]any{}
		for rel, content := range u.files {
			name, _, _ := strings.Cut(rel, "/")
			write(t, filepath.Join(c.Dir, ".agents", "skills", filepath.FromSlash(rel)), content)
			folder := u.folder[name]
			if folder == "" {
				folder = "skills/" + name
			}
			lock[name] = map[string]string{"source": "owner/repo", "sourceType": "github", "skillPath": folder + "/SKILL.md"}
		}
		data, _ := json.Marshal(map[string]any{"version": 1, "skills": lock})
		write(t, filepath.Join(c.Dir, "skills-lock.json"), string(data))
		return nil, nil
	}
	return u
}

func (u *upstream) drop(skill string) {
	maps.DeleteFunc(u.files, func(rel, _ string) bool { return strings.HasPrefix(rel, skill+"/") })
}

// install fetches source and puts the named skills in the library of a scope.
func (u *upstream) install(r Roots, source, scope string, names ...string) {
	u.t.Helper()
	got, err := u.fetcher().Stage(context.Background(), r, source)
	if err != nil {
		u.t.Fatal(err)
	}
	if _, err := InstallStaged(r, got.ID, names, scope, nil, false); err != nil {
		u.t.Fatal(err)
	}
}

func (u *upstream) stageUpdate(r Roots, dir string) (UpdateStage, map[string]UpdateSkill) {
	u.t.Helper()
	got, err := u.fetcher().StageUpdate(context.Background(), r, dir)
	if err != nil {
		u.t.Fatal(err)
	}
	by := map[string]UpdateSkill{}
	for _, s := range got.Skills {
		by[s.Name] = s
	}
	if len(by) != len(got.Skills) {
		u.t.Fatalf("a skill is listed twice: %+v", got.Skills)
	}
	return got, by
}

var v1 = map[string]string{
	"show-me/SKILL.md":       skillMD("show-me", "Shows things"),
	"show-me/notes.md":       "v1",
	"show-me/scripts/run.sh": "#!/bin/sh\n",
	"quiet/SKILL.md":         manualMD,
	"quiet/notes.md":         "v1",
}

// installed is a machine with v1 of both skills installed from owner/repo.
func installed(t *testing.T) (Roots, *upstream) {
	t.Helper()
	staging(t)
	r := machine(t)
	u := newUpstream(t, v1)
	u.install(r, "owner/repo", ScopeUser, "show-me", "quiet")
	return r, u
}

func TestStageUpdate(t *testing.T) {
	r, u := installed(t)
	show, quiet := filepath.Join(r.Library, "show-me"), filepath.Join(r.Library, "quiet")

	got, by := u.stageUpdate(r, show)
	if got.Repo != "owner/repo" || len(by) != 2 {
		t.Fatalf("update = %+v", got)
	}
	for name, s := range by {
		if s.Changed || s.Gone || s.Files == nil || len(s.Files) != 0 {
			t.Errorf("%s with nothing new upstream = %+v", name, s)
		}
	}
	if by["show-me"].Dir != show || by["quiet"].Dir != quiet {
		t.Errorf("dirs = %q and %q", by["show-me"].Dir, by["quiet"].Dir)
	}

	// The user's own edits are not an update.
	write(t, filepath.Join(show, "notes.md"), "v1, and my own notes")
	write(t, filepath.Join(show, "mine.md"), "written here")
	if _, by = u.stageUpdate(r, quiet); by["show-me"].Changed {
		t.Errorf("local edits were reported as an update: %+v", by["show-me"])
	}

	u.files["show-me/notes.md"] = "v2"
	u.files["show-me/new.md"] = "new"
	delete(u.files, "show-me/scripts/run.sh")
	u.drop("quiet")
	_, by = u.stageUpdate(r, show)
	// What applying would do to the installed copy, the user's edits included.
	want := []FileChange{
		{Path: "mine.md", Status: ChangeRemoved},
		{Path: "new.md", Status: ChangeAdded},
		{Path: "notes.md", Status: ChangeModified},
		{Path: "scripts/run.sh", Status: ChangeRemoved},
	}
	if s := by["show-me"]; !s.Changed || s.Gone || !reflect.DeepEqual(s.Files, want) {
		t.Errorf("show-me = %+v\nwant %+v", s, want)
	}
	if s := by["quiet"]; !s.Gone || s.Changed || len(s.Files) != 0 {
		t.Errorf("a skill the source dropped = %+v", s)
	}
	// Looking changed nothing.
	if read(t, filepath.Join(show, "notes.md")) != "v1, and my own notes" || !exists(filepath.Join(quiet, "SKILL.md")) {
		t.Error("staging an update touched the installed skills")
	}
}

func TestUpdateFetchesTheRefThatWasInstalled(t *testing.T) {
	staging(t)
	r := machine(t)
	u := newUpstream(t, v1)
	u.install(r, "owner/repo#v2", ScopeUser, "show-me")
	u.stageUpdate(r, filepath.Join(r.Library, "show-me"))
	calls := u.ran("npx")
	if last := calls[len(calls)-1]; !slices.Contains(last.Args, "owner/repo#v2") {
		t.Errorf("npx args = %v", last.Args)
	}
}

func TestReadUpdateFile(t *testing.T) {
	r, u := installed(t)
	show, quiet := filepath.Join(r.Library, "show-me"), filepath.Join(r.Library, "quiet")
	write(t, filepath.Join(r.Library, "mine", "SKILL.md"), skillMD("mine", "Written here"))
	write(t, filepath.Join(show, "logo.png"), "was text")
	u.files["show-me/notes.md"] = "v2"
	u.files["show-me/new.md"] = "new"
	u.files["show-me/logo.png"] = "\x89PNG\x00"
	delete(u.files, "show-me/scripts/run.sh")
	u.drop("quiet")
	got, _ := u.stageUpdate(r, show)

	tests := []struct {
		rel, before, after string
		binary             bool
	}{
		{"notes.md", "v1", "v2", false},
		{"new.md", "", "new", false},
		{"scripts/run.sh", "#!/bin/sh\n", "", false},
		{"logo.png", "", "", true},
	}
	for _, tt := range tests {
		before, after, binary, err := ReadUpdateFile(r, got.ID, show, tt.rel)
		if err != nil || before != tt.before || after != tt.after || binary != tt.binary {
			t.Errorf("%s = %q, %q, %v, %v", tt.rel, before, after, binary, err)
		}
	}

	if _, _, _, err := ReadUpdateFile(r, got.ID, show, "nowhere.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a file on neither side: %v", err)
	}
	for _, rel := range []string{"../quiet/SKILL.md", "../../skills-lock.json", filepath.Join(r.Library, "quiet", "SKILL.md"), ""} {
		if before, after, _, err := ReadUpdateFile(r, got.ID, show, rel); err == nil {
			t.Errorf("read %q as %q and %q", rel, before, after)
		}
	}
	// Only the skills the update was staged for, and only with something to
	// replace them.
	for name, dir := range map[string]string{"a skill from nowhere": filepath.Join(r.Library, "mine"), "a skill the source dropped": quiet} {
		if _, _, _, err := ReadUpdateFile(r, got.ID, dir, "SKILL.md"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := ApplyUpdate(r, got.ID, []string{dir}); !errors.Is(err, ErrInvalid) {
			t.Errorf("applying to %s: %v", name, err)
		}
	}
	if !exists(filepath.Join(quiet, "SKILL.md")) {
		t.Error("a skill the source dropped was removed")
	}
}

func TestApplyUpdate(t *testing.T) {
	r, u := installed(t)
	show, quiet := filepath.Join(r.Library, "show-me"), filepath.Join(r.Library, "quiet")
	if _, err := LinkSkill(r, show, Claude); err != nil {
		t.Fatal(err)
	}
	const long = "2020-01-02T03:04:05Z"
	err := UpdateRecord(r.Library, func(rec *Record) error {
		e, _ := rec.Get("show-me")
		e.InstalledAt, e.UpdatedAt = long, long
		rec.Set("show-me", e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(show, "mine.md"), "written here")
	u.files["show-me/notes.md"] = "v2"
	u.files["show-me/new.md"] = "new"
	delete(u.files, "show-me/scripts/run.sh")
	u.files["quiet/notes.md"] = "v2"
	got, _ := u.stageUpdate(r, show)

	if _, err := ApplyUpdate(r, got.ID, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("applying to nothing: %v", err)
	}
	updated, err := ApplyUpdate(r, got.ID, []string{show, show})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].Dir != show || !containsHarness(updated[0].Harnesses, Claude) {
		t.Errorf("updated = %+v", updated)
	}
	if !reflect.DeepEqual(entries(t, show), []string{"SKILL.md", "new.md", "notes.md"}) || read(t, filepath.Join(show, "notes.md")) != "v2" {
		t.Errorf("show-me holds %v", entries(t, show))
	}
	if read(t, filepath.Join(quiet, "notes.md")) != "v1" {
		t.Error("a skill that was not picked was updated")
	}
	if !reflect.DeepEqual(entries(t, r.Library), []string{RecordFile, "quiet", "show-me"}) {
		t.Errorf("library holds %v", entries(t, r.Library))
	}

	rec, _ := LoadRecord(r.Library)
	e, _ := rec.Get("show-me")
	now, _ := HashDir(show)
	if e.Hash != now || e.InstalledAt != long || e.UpdatedAt == long || e.Repo != "owner/repo" || e.Path != "skills/show-me" {
		t.Errorf("record = %+v, want the new hash %s and the old install date", e, now)
	}

	// The rest of the same fetch can be applied afterwards.
	if _, err := ApplyUpdate(r, got.ID, []string{quiet}); err != nil {
		t.Fatalf("applying the second skill: %v", err)
	}
	if read(t, filepath.Join(quiet, "notes.md")) != "v2" {
		t.Error("quiet was not updated")
	}
	_, by := u.stageUpdate(r, show)
	if by["show-me"].Changed || by["quiet"].Changed {
		t.Errorf("still changed after applying: %+v", by)
	}
}

func TestAnUpdateKeepsTheInvocationChoice(t *testing.T) {
	const manualOpenAI = "interface:\n  display_name: Show\n" + noImplicit
	manualShow := "---\nname: show-me\ndescription: Shows things\n" + manualLine + "---\n\nnew body\n"
	type choice struct{ frontmatter, openai bool }
	tests := []struct {
		name  string
		skill string
		// here lays the choice on the installed copy; next is what upstream
		// says about it in the new version.
		here func(t *testing.T, dir string)
		next map[string]string
		want choice
	}{
		{name: "manual here, auto upstream", skill: "show-me",
			here: func(t *testing.T, dir string) { setManual(t, dir, true) },
			want: choice{true, true}},
		{name: "auto here, manual upstream", skill: "quiet",
			here: func(t *testing.T, dir string) { setManual(t, dir, false) },
			want: choice{false, false}},
		{name: "auto here, upstream turns manual", skill: "show-me",
			next: map[string]string{"show-me/SKILL.md": manualShow, "show-me/agents/openai.yaml": manualOpenAI},
			want: choice{false, false}},
		{name: "manual here, upstream turns manual its own way", skill: "show-me",
			here: func(t *testing.T, dir string) { setManual(t, dir, true) },
			next: map[string]string{"show-me/SKILL.md": manualShow},
			want: choice{true, true}},
		{name: "manual for Claude and pi only", skill: "show-me",
			here: func(t *testing.T, dir string) { write(t, filepath.Join(dir, "SKILL.md"), manualShow) },
			want: choice{true, false}},
		{name: "manual for Codex only", skill: "show-me",
			here: func(t *testing.T, dir string) { write(t, filepath.Join(dir, "agents", "openai.yaml"), manualOpenAI) },
			next: map[string]string{"show-me/SKILL.md": manualShow},
			want: choice{false, true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, u := installed(t)
			dir := filepath.Join(r.Library, tt.skill)
			if tt.here != nil {
				tt.here(t, dir)
			}
			if f, o := FileManual(dir); (choice{f, o}) != tt.want {
				t.Fatalf("the installed copy says %v, %v before the update", f, o)
			}
			// A choice alone is not something to update.
			if _, by := u.stageUpdate(r, dir); by[tt.skill].Changed {
				t.Errorf("reported as changed with nothing new upstream: %+v", by[tt.skill])
			}

			u.files[tt.skill+"/notes.md"] = "v2"
			maps.Copy(u.files, tt.next)
			got, by := u.stageUpdate(r, dir)
			if !by[tt.skill].Changed {
				t.Fatalf("not reported as changed: %+v", by[tt.skill])
			}
			// The diff is of what will be written: the choice is already in it.
			_, after, _, err := ReadUpdateFile(r, got.ID, dir, "SKILL.md")
			if err != nil || frontmatterManual(after) != tt.want.frontmatter {
				t.Errorf("the new SKILL.md shown does not carry the choice (%v):\n%s", err, after)
			}

			if _, err := ApplyUpdate(r, got.ID, []string{dir}); err != nil {
				t.Fatal(err)
			}
			if f, o := FileManual(dir); (choice{f, o}) != tt.want {
				t.Errorf("after the update the files say %v, %v, want %+v", f, o, tt.want)
			}
			if read(t, filepath.Join(dir, "notes.md")) != "v2" {
				t.Error("the update did not land")
			}
			if tt.next != nil && !strings.Contains(read(t, filepath.Join(dir, "SKILL.md")), "new body") {
				t.Error("the new SKILL.md did not land")
			}
			// The record remembers upstream's version, not ours with the choice
			// written over it, so the next check is quiet.
			if _, by := u.stageUpdate(r, dir); by[tt.skill].Changed {
				t.Errorf("reported as changed straight after updating: %+v", by[tt.skill])
			}
		})
	}
}

func setManual(t *testing.T, dir string, manual bool) {
	t.Helper()
	if err := SetManual(dir, manual); err != nil {
		t.Fatal(err)
	}
}

func TestAnUpdateThatCannotKeepTheChoiceIsRefused(t *testing.T) {
	r, u := installed(t)
	show := filepath.Join(r.Library, "show-me")
	setManual(t, show, true)
	before := read(t, filepath.Join(show, "SKILL.md"))
	// Frontmatter that never closes: there is nowhere to write the key.
	u.files["show-me/SKILL.md"] = "---\nname: show-me\ndescription: broken upstream\n"
	u.files["show-me/notes.md"] = "v2"
	got, by := u.stageUpdate(r, show)
	if !by["show-me"].Changed {
		t.Fatalf("show-me = %+v", by["show-me"])
	}
	if _, err := ApplyUpdate(r, got.ID, []string{show}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if read(t, filepath.Join(show, "SKILL.md")) != before || read(t, filepath.Join(show, "notes.md")) != "v1" {
		t.Error("the skill was replaced by a copy that lost the choice")
	}
	if !reflect.DeepEqual(entries(t, r.Library), []string{RecordFile, "quiet", "show-me"}) {
		t.Errorf("library holds %v", entries(t, r.Library))
	}
}

func TestUpdateAdoptsASkillTheCLIInstalled(t *testing.T) {
	staging(t)
	r := machine(t)
	u := newUpstream(t, v1)
	show := filepath.Join(r.Library, "show-me")
	// As `npx skills add -g` from a terminal leaves it: the files and a line
	// in the CLI's own lock, nothing in our record.
	for rel, content := range v1 {
		if name, file, _ := strings.Cut(rel, "/"); name == "show-me" {
			write(t, filepath.Join(show, filepath.FromSlash(file)), content)
		}
	}
	const when = "2026-01-02T03:04:05.000Z"
	lock := `{"version":3,"skills":{"show-me":{"source":"owner/repo","sourceType":"github","sourceUrl":"https://github.com/owner/repo.git","skillPath":"skills/show-me/SKILL.md","installedAt":"` + when + `","updatedAt":"` + when + `"}}}`
	write(t, r.CLILock, lock)
	setManual(t, show, true)

	// No record, so nothing says what was fetched: the files are compared,
	// and the choice made here is not a difference.
	if _, by := u.stageUpdate(r, show); by["show-me"].Changed || by["show-me"].Gone {
		t.Errorf("with nothing new upstream: %+v", by["show-me"])
	}
	u.files["show-me/notes.md"] = "v2"
	got, by := u.stageUpdate(r, show)
	if s := by["show-me"]; !s.Changed || !reflect.DeepEqual(s.Files, []FileChange{{Path: "notes.md", Status: ChangeModified}}) {
		t.Errorf("show-me = %+v", s)
	}

	updated, err := ApplyUpdate(r, got.ID, []string{show})
	if err != nil {
		t.Fatal(err)
	}
	src := updated[0].Source
	if src == nil || !src.Managed || src.Method != MethodNpx || src.Repo != "owner/repo" || src.Path != "skills/show-me" {
		t.Errorf("source = %+v, want it in our record", src)
	}
	if src.InstalledAt != when || src.UpdatedAt == when {
		t.Errorf("dates = %q and %q, want the CLI's install date kept", src.InstalledAt, src.UpdatedAt)
	}
	if f, o := FileManual(show); !f || !o {
		t.Errorf("the choice was lost: %v, %v", f, o)
	}
	if read(t, r.CLILock) != lock {
		t.Error("the CLI's lock was written")
	}
	// Adopted: from here a change is told by the record.
	write(t, filepath.Join(show, "notes.md"), "v2, and my own notes")
	if _, by := u.stageUpdate(r, show); by["show-me"].Changed {
		t.Errorf("still changed after adopting: %+v", by["show-me"])
	}
}

func TestAnUpdateCoversOnlySiblings(t *testing.T) {
	r, u := installed(t)
	show := filepath.Join(r.Library, "show-me")
	// Same repo in the project's library, same repo at another ref, another
	// repo, and a skill from nowhere.
	u.install(r, "owner/repo", ScopeProject, "show-me")
	for _, name := range []string{"pinned", "stranger", "mine"} {
		write(t, filepath.Join(r.Library, name, "SKILL.md"), skillMD(name, "d"))
		write(t, filepath.Join(r.Library, name, "notes.md"), "v1")
	}
	err := UpdateRecord(r.Library, func(rec *Record) error {
		rec.Set("pinned", RecordEntry{Method: MethodNpx, Repo: "owner/repo", Ref: "v1", Path: "skills/show-me"})
		rec.Set("stranger", RecordEntry{Method: MethodNpx, Repo: "someone/else", Path: "skills/show-me"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	u.files["show-me/notes.md"] = "v2"
	u.files["quiet/notes.md"] = "v2"

	got, by := u.stageUpdate(r, show)
	names := slices.Sorted(maps.Keys(by))
	if !reflect.DeepEqual(names, []string{"quiet", "show-me"}) || by["show-me"].Dir != show {
		t.Fatalf("update covers %v: %+v", names, got.Skills)
	}
	others := map[string]string{
		"the project's copy": filepath.Join(r.ProjectLibrary, "show-me"),
		"another ref":        filepath.Join(r.Library, "pinned"),
		"another repo":       filepath.Join(r.Library, "stranger"),
		"no source":          filepath.Join(r.Library, "mine"),
	}
	for name, dir := range others {
		// One bad pick refuses the lot, before anything is replaced.
		if _, err := ApplyUpdate(r, got.ID, []string{show, dir}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
		if read(t, filepath.Join(dir, "notes.md")) != "v1" {
			t.Errorf("%s was updated", name)
		}
	}
	if read(t, filepath.Join(show, "notes.md")) != "v1" {
		t.Error("a refused update replaced a skill")
	}
	if _, err := ApplyUpdate(r, got.ID, []string{filepath.Join(r.Library, "nothing")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a folder that is no skill: %v", err)
	}
}

func TestUpdateFollowsASkillRenamedUpstream(t *testing.T) {
	r, u := installed(t)
	show := filepath.Join(r.Library, "show-me")
	// Same folder in the repo, a new name in its frontmatter.
	u.drop("show-me")
	u.files["shown/SKILL.md"] = skillMD("shown", "Shows things, renamed")
	u.folder["shown"] = "skills/show-me"

	got, by := u.stageUpdate(r, show)
	s := by["show-me"]
	if s.Gone || !s.Changed || s.Dir != show {
		t.Fatalf("show-me = %+v", s)
	}
	if _, err := ApplyUpdate(r, got.ID, []string{show}); err != nil {
		t.Fatal(err)
	}
	// It stays where it is installed: links and settings name that folder.
	if read(t, filepath.Join(show, "SKILL.md")) != u.files["shown/SKILL.md"] || exists(filepath.Join(r.Library, "shown")) {
		t.Errorf("library holds %v", entries(t, r.Library))
	}
}

func TestUpdateFromALocalFolder(t *testing.T) {
	staging(t)
	r := machine(t)
	got, src := stageLocal(t, r, twoSkills)
	if _, err := InstallStaged(r, got.ID, []string{"one"}, ScopeUser, nil, false); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(r.Library, "one")
	write(t, filepath.Join(src, "one", "notes.md"), "edited in the folder")

	// No npx, no git: a folder is read where it is.
	u := &upstream{t: t}
	up, by := u.stageUpdate(r, one)
	if s := by["one"]; !s.Changed || !reflect.DeepEqual(s.Files, []FileChange{{Path: "notes.md", Status: ChangeModified}}) {
		t.Fatalf("one = %+v", s)
	}
	if len(by) != 1 || len(u.calls) != 0 {
		t.Errorf("update covers %v and ran %v", by, u.calls)
	}
	updated, err := ApplyUpdate(r, up.ID, []string{one})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(one, "notes.md")) != "edited in the folder" || updated[0].Source.Method != MethodLocal || updated[0].Source.Repo != src {
		t.Errorf("updated = %+v", updated[0].Source)
	}

	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if _, err := u.fetcher().StageUpdate(context.Background(), r, one); err == nil {
		t.Error("staged an update from a folder that is gone")
	}
}

func TestStageUpdateRefusals(t *testing.T) {
	t.Run("a skill that is not ours to change", func(t *testing.T) {
		staging(t)
		r := fixture(t)
		u := newUpstream(t, v1)
		plug := byName(t, mustDiscover(t, r))["plug-skill"]
		if _, err := u.fetcher().StageUpdate(context.Background(), r, plug.Dir); !errors.Is(err, ErrNotEditable) {
			t.Errorf("err = %v", err)
		}
		if len(u.calls) != 0 {
			t.Errorf("ran %v", u.calls)
		}
	})

	r, u := installed(t)
	tmp := os.Getenv("TMPDIR")
	for _, name := range []string{"mine", "flag", "helper", "ref", "relative"} {
		write(t, filepath.Join(r.Library, name, "SKILL.md"), skillMD(name, "d"))
	}
	err := UpdateRecord(r.Library, func(rec *Record) error {
		// A record is a file in a repo the user may have pulled from anywhere.
		rec.Set("flag", RecordEntry{Method: MethodGit, Repo: "--upload-pack=touch pwned"})
		rec.Set("helper", RecordEntry{Method: MethodGit, Repo: "ext::sh -c touch% pwned"})
		rec.Set("ref", RecordEntry{Method: MethodNpx, Repo: "owner/repo", Ref: "--force"})
		rec.Set("relative", RecordEntry{Method: MethodLocal, Repo: "../somewhere"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(u.calls)
	for _, name := range []string{"mine", "flag", "helper", "ref", "relative"} {
		if got, err := u.fetcher().StageUpdate(context.Background(), r, filepath.Join(r.Library, name)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: staged %+v, err %v", name, got, err)
		}
	}
	if _, err := u.fetcher().StageUpdate(context.Background(), r, filepath.Join(r.Library, "nothing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("a folder that is no skill: %v", err)
	}
	if len(u.calls) != before {
		t.Errorf("a refused update ran %v", u.calls[before:])
	}

	t.Run("a fetch that fails", func(t *testing.T) {
		u.npx = func(Command) ([]byte, error) { return nil, errors.New("offline") }
		u.git = func(Command) error { return errors.New("offline too") }
		if _, err := u.fetcher().StageUpdate(context.Background(), r, filepath.Join(r.Library, "show-me")); err == nil {
			t.Error("staged an update from nothing")
		}
		if left := stagingDirs(t, tmp); len(left) != 0 {
			t.Errorf("left %v", left)
		}
	})
}

func TestAFetchIsForInstallingOrForUpdating(t *testing.T) {
	r, u := installed(t)
	show := filepath.Join(r.Library, "show-me")
	u.files["show-me/notes.md"] = "v2"
	update, _ := u.stageUpdate(r, show)
	install, err := u.fetcher().Stage(context.Background(), r, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}

	// An update's fetch carries the choice written over upstream's files; it
	// is not what install would record as fetched.
	if _, err := InstallStaged(r, update.ID, []string{"show-me"}, ScopeUser, nil, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("installing from an update's fetch: %v", err)
	}
	if _, err := ApplyUpdate(r, install.ID, []string{show}); !errors.Is(err, ErrInvalid) {
		t.Errorf("updating from an install's fetch: %v", err)
	}
	if _, _, _, err := ReadUpdateFile(r, install.ID, show, "notes.md"); !errors.Is(err, ErrInvalid) {
		t.Errorf("reading an update out of an install's fetch: %v", err)
	}
	if read(t, filepath.Join(show, "notes.md")) != "v1" {
		t.Error("the skill was replaced")
	}
	// A staged file is readable either way.
	if content, _, err := ReadStagedFile(update.ID, "show-me", "notes.md"); err != nil || content != "v2" {
		t.Errorf("staged notes.md = %q, %v", content, err)
	}
}
