package skills

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// isolateGit keeps the developer's git config, and any repository the temp
// dir happens to sit inside, out of what git reports.
func isolateGit(t *testing.T, r Roots) {
	t.Helper()
	t.Setenv("HOME", r.Home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(r.Home))
}

var allHarnesses = []Harness{Claude, Codex, Pi}

// add puts a skill into the library one of the two ways there are.
type add func(t *testing.T, r Roots, name string) Skill

func addBoth(t *testing.T) map[string]add {
	return map[string]add{
		"create": func(t *testing.T, r Roots, name string) Skill {
			t.Helper()
			s, err := Create(r, name, "A new skill")
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
		"install": func(t *testing.T, r Roots, name string) Skill {
			t.Helper()
			staging(t)
			got, _ := stageLocal(t, r, map[string]string{name + "/SKILL.md": skillMD(name, "Fetched")})
			placed, err := InstallStaged(r, got.ID, []string{name})
			if err != nil {
				t.Fatal(err)
			}
			return placed[0]
		},
	}
}

func TestANewSkillReachesEveryAgent(t *testing.T) {
	for how, add := range addBoth(t) {
		t.Run(how+" into an empty home", func(t *testing.T) {
			r := machine(t)
			s := add(t, r, "fresh")
			if !reflect.DeepEqual(s.harnesses, allHarnesses) {
				t.Errorf("seen by %v", s.harnesses)
			}
			// Claude had no skills dir, so it is given the whole library.
			claude := filepath.Join(r.ClaudeConfigDir, "skills")
			if real, _ := filepath.EvalSymlinks(claude); !isSymlink(claude) || real != r.Library {
				t.Errorf("claude's skills dir resolves to %q", real)
			}
			// Codex and pi read the library already: nothing is made for them.
			for _, dir := range []string{filepath.Join(r.CodexHome, "skills"), filepath.Join(r.PiAgentDir, "skills")} {
				if exists(dir) {
					t.Errorf("%s was made", dir)
				}
			}
			// The next one needs nothing more.
			if next := add(t, r, "second"); !reflect.DeepEqual(next.harnesses, allHarnesses) || isSymlink(filepath.Join(r.Library, "second")) {
				t.Errorf("second: seen by %v", next.harnesses)
			}
		})

		t.Run(how+" next to agents with skills dirs of their own", func(t *testing.T) {
			r := machine(t)
			// A library Codex and pi do not read directly, so each needs a way in.
			r.Library = filepath.Join(r.Home, "library")
			write(t, filepath.Join(r.ClaudeConfigDir, "skills", "own", "SKILL.md"), skillMD("own", "Claude's own"))
			write(t, filepath.Join(r.PiAgentDir, "skills", "pi-own", "SKILL.md"), skillMD("pi-own", "Pi's own"))
			s := add(t, r, "fresh")
			if !reflect.DeepEqual(s.harnesses, allHarnesses) {
				t.Errorf("seen by %v", s.harnesses)
			}
			for _, at := range []string{filepath.Join(r.ClaudeConfigDir, "skills", "fresh"), filepath.Join(r.PiAgentDir, "skills", "fresh")} {
				target, err := os.Readlink(at)
				if err != nil || filepath.IsAbs(target) {
					t.Errorf("%s: link %q, %v; want a relative symlink", at, target, err)
				}
			}
			if real, _ := filepath.EvalSymlinks(filepath.Join(r.CodexHome, "skills")); real != r.Library {
				t.Errorf("codex's missing skills dir became %q", real)
			}
			own := byName(t, mustDiscover(t, r))
			if _, ok := own["own"]; !ok {
				t.Error("claude's own skill went missing")
			}
			if _, ok := own["pi-own"]; !ok {
				t.Error("pi's own skill went missing")
			}
		})
	}
}

func TestInstallLeavesAnAgentsOwnSkillOfTheSameName(t *testing.T) {
	staging(t)
	r := machine(t)
	own := filepath.Join(r.ClaudeConfigDir, "skills", "one", "SKILL.md")
	write(t, own, skillMD("one", "Claude's own"))
	// A link an earlier copy left behind, pointing nowhere now.
	link(t, filepath.Join(r.Home, "gone", "two"), filepath.Join(r.ClaudeConfigDir, "skills", "two"))
	got, _ := stageLocal(t, r, twoSkills)
	if _, err := InstallStaged(r, got.ID, []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if isSymlink(filepath.Dir(own)) || read(t, own) != skillMD("one", "Claude's own") {
		t.Error("claude's own skill was replaced")
	}
	at := filepath.Join(r.ClaudeConfigDir, "skills", "two")
	if real, _ := filepath.EvalSymlinks(at); real != filepath.Join(r.Library, "two") {
		t.Errorf("the dangling link now resolves to %q", real)
	}
}

func TestALinkThatCannotBeMadeDoesNotFailTheInstall(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only dir")
	}
	staging(t)
	r := machine(t)
	claude := filepath.Join(r.ClaudeConfigDir, "skills")
	write(t, filepath.Join(claude, "own", "SKILL.md"), skillMD("own", "Claude's own"))
	if err := os.Chmod(claude, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(claude, 0o755) })
	got, _ := stageLocal(t, r, twoSkills)
	placed, err := InstallStaged(r, got.ID, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	if s := placed[0]; s.Dir != filepath.Join(r.Library, "one") || !reflect.DeepEqual(s.harnesses, []Harness{Codex, Pi}) {
		t.Errorf("placed %+v seen by %v", s, s.harnesses)
	}
}

func TestRemove(t *testing.T) {
	entry := RecordEntry{Method: MethodNpx, Repo: "owner/repo"}

	t.Run("takes the per-skill links and the record entry with it", func(t *testing.T) {
		r := machine(t)
		write(t, filepath.Join(r.ClaudeConfigDir, "skills", "claude-own", "SKILL.md"), skillMD("claude-own", "Claude's"))
		s, err := Create(r, "lib-skill", "In the library")
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(r.Library, "keep", "SKILL.md"), skillMD("keep", "Stays"))
		if err := SaveRecord(r.Library, Record{Skills: map[string]RecordEntry{"lib-skill": entry, "keep": entry}}); err != nil {
			t.Fatal(err)
		}
		if !isSymlink(filepath.Join(r.ClaudeConfigDir, "skills", "lib-skill")) {
			t.Fatal("create made no link for claude")
		}
		if err := Remove(r, s.Dir); err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{s.Dir, filepath.Join(r.ClaudeConfigDir, "skills", "lib-skill")} {
			if exists(gone) {
				t.Errorf("%s is still there", gone)
			}
		}
		left := byName(t, mustDiscover(t, r))
		if _, ok := left["lib-skill"]; ok {
			t.Error("still discovered")
		}
		for _, name := range []string{"keep", "claude-own"} {
			if _, ok := left[name]; !ok {
				t.Errorf("%s went with it", name)
			}
		}
		rec, err := LoadRecord(r.Library)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := rec.Get("lib-skill"); ok {
			t.Error("the record still names the removed skill")
		}
		if _, ok := rec.Get("keep"); !ok {
			t.Error("the record lost another skill's entry")
		}
	})

	t.Run("a skill reached through a symlinked dir leaves that dir's link alone", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		if err := Remove(r, shared.Dir); err != nil {
			t.Fatal(err)
		}
		if exists(shared.Dir) {
			t.Error("the skill is still there")
		}
		for _, parent := range []string{filepath.Join(r.Home, ".agents"), filepath.Join(r.ClaudeConfigDir, "skills")} {
			if !isSymlink(parent) {
				t.Errorf("%s is no longer a symlink", parent)
			}
		}
		if exists(filepath.Join(r.Library, RecordFile)) {
			t.Error("removing an unrecorded skill wrote a record")
		}
	})

	t.Run("a project skill's entry leaves the project library's record", func(t *testing.T) {
		r := fixture(t)
		for _, lib := range []string{r.Library, r.ProjectLibrary} {
			if err := SaveRecord(lib, Record{Skills: map[string]RecordEntry{"dev": entry}}); err != nil {
				t.Fatal(err)
			}
		}
		dev := byName(t, mustDiscover(t, r))["dev"]
		if err := Remove(r, dev.Dir); err != nil {
			t.Fatal(err)
		}
		if rec, _ := LoadRecord(r.ProjectLibrary); len(rec.Skills) != 0 {
			t.Errorf("project record = %+v", rec.Skills)
		}
		// The personal library's entry of the same name is another skill's.
		if rec, _ := LoadRecord(r.Library); len(rec.Skills) != 1 {
			t.Errorf("personal record = %+v", rec.Skills)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		r := fixture(t)
		got := byName(t, mustDiscover(t, r))
		for _, name := range []string{"plug-skill", "sys-skill", "cloud-one"} {
			if err := Remove(r, got[name].Dir); !errors.Is(err, ErrNotEditable) {
				t.Errorf("%s: err = %v, want ErrNotEditable", name, err)
			}
			if !exists(filepath.Join(got[name].Dir, "SKILL.md")) {
				t.Errorf("%s was removed", name)
			}
		}
		for _, dir := range []string{"", r.Home, filepath.Join(r.Home, "dotfiles", "agents", "skills"), filepath.Join(r.Home, "nowhere")} {
			if err := Remove(r, dir); !errors.Is(err, ErrNotFound) {
				t.Errorf("Remove(%q) err = %v, want ErrNotFound", dir, err)
			}
		}
		if !exists(filepath.Join(r.Home, "dotfiles", "agents", "skills")) {
			t.Error("the library itself was removed")
		}
	})
}
