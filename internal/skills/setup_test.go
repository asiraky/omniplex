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

func TestLinkState(t *testing.T) {
	type states [3]string // claude, codex, pi
	tests := []struct {
		name  string
		setup func(t *testing.T, r *Roots)
		scope string
		want  states
	}{
		{"nothing on disk", func(t *testing.T, r *Roots) {}, ScopeUser, states{LinkNone, LinkNone, LinkNone}},
		{"the default library is Codex and pi's own dir", func(t *testing.T, r *Roots) {
			mkdir(t, r.Library)
		}, ScopeUser, states{LinkNone, LinkDirect, LinkDirect}},
		{"claude's dir is a symlink to the library", func(t *testing.T, r *Roots) {
			mkdir(t, r.Library)
			link(t, r.Library, filepath.Join(r.ClaudeConfigDir, "skills"))
		}, ScopeUser, states{LinkDirect, LinkDirect, LinkDirect}},
		{"claude has a real dir of its own", func(t *testing.T, r *Roots) {
			mkdir(t, r.Library)
			mkdir(t, filepath.Join(r.ClaudeConfigDir, "skills"))
		}, ScopeUser, states{LinkPerSkill, LinkDirect, LinkDirect}},
		{"everything symlinked into a repo, the way dotfiles do it", func(t *testing.T, r *Roots) {
			repo := filepath.Join(r.Home, "dotfiles", "agents")
			mkdir(t, filepath.Join(repo, "skills"))
			link(t, "dotfiles/agents", filepath.Join(r.Home, ".agents"))
			link(t, "../dotfiles/agents/skills", filepath.Join(r.ClaudeConfigDir, "skills"))
		}, ScopeUser, states{LinkDirect, LinkDirect, LinkDirect}},
		{"a library elsewhere that nothing points at", func(t *testing.T, r *Roots) {
			r.Library = filepath.Join(r.Home, "library")
			mkdir(t, r.Library)
			mkdir(t, filepath.Join(r.Home, ".agents", "skills"))
		}, ScopeUser, states{LinkNone, LinkPerSkill, LinkPerSkill}},
		{"a library elsewhere reached through Codex's and pi's own dirs", func(t *testing.T, r *Roots) {
			r.Library = filepath.Join(r.Home, "library")
			mkdir(t, r.Library)
			link(t, r.Library, filepath.Join(r.CodexHome, "skills"))
			link(t, r.Library, filepath.Join(r.PiAgentDir, "skills"))
		}, ScopeUser, states{LinkNone, LinkDirect, LinkDirect}},
		{"a configured library that does not exist yet", func(t *testing.T, r *Roots) {
			r.Library = filepath.Join(r.Home, "library")
			mkdir(t, filepath.Join(r.ClaudeConfigDir, "skills"))
		}, ScopeUser, states{LinkPerSkill, LinkNone, LinkNone}},
		{"a symlink to nowhere is not a skills dir", func(t *testing.T, r *Roots) {
			mkdir(t, r.Library)
			link(t, filepath.Join(r.Home, "gone"), filepath.Join(r.ClaudeConfigDir, "skills"))
		}, ScopeUser, states{LinkNone, LinkDirect, LinkDirect}},
		{"project: .claude/skills linked to the project library", func(t *testing.T, r *Roots) {
			mkdir(t, r.ProjectLibrary)
			link(t, "../.agents/skills", filepath.Join(r.ProjectRoot, ".claude", "skills"))
		}, ScopeProject, states{LinkDirect, LinkDirect, LinkDirect}},
		{"project: a library of its own", func(t *testing.T, r *Roots) {
			r.ProjectLibrary = filepath.Join(r.ProjectRoot, "tools", "skills")
			mkdir(t, r.ProjectLibrary)
			mkdir(t, filepath.Join(r.ProjectRoot, ".claude", "skills"))
		}, ScopeProject, states{LinkPerSkill, LinkNone, LinkNone}},
		{"project scope is not the personal library", func(t *testing.T, r *Roots) {
			mkdir(t, r.Library)
		}, ScopeProject, states{LinkNone, LinkNone, LinkNone}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := machine(t)
			tt.setup(t, &r)
			var got states
			for i, h := range AllHarnesses {
				l := LinkState(r, h, tt.scope)
				got[i] = l.State
				if l.Harness != h {
					t.Errorf("link for %s is labelled %s", h, l.Harness)
				}
				// Whatever the state, Dir is somewhere the harness reads.
				reads := false
				for _, rt := range r.ownRoots() {
					reads = reads || (rt.path == l.Dir && rt.scope == tt.scope && containsHarness(rt.harnesses, h))
				}
				if !reads {
					t.Errorf("%s: dir %q is not one of its %s roots", h, l.Dir, tt.scope)
				}
				if l.State == LinkDirect && resolve(l.Dir) != resolve(map[string]string{ScopeUser: r.Library, ScopeProject: r.ProjectLibrary}[tt.scope]) {
					t.Errorf("%s: direct through %q, which is not the library", h, l.Dir)
				}
			}
			if got != tt.want {
				t.Errorf("states = %v, want %v", got, tt.want)
			}
		})
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

func TestDetectSetup(t *testing.T) {
	t.Run("a library that does not exist yet", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(r.Home, "dot", "skills")
		s := DetectSetup(r)
		if s.Exists || s.Git != nil || s.LibraryDir != r.Library {
			t.Errorf("setup = %+v", s)
		}
		if s.Library != "~/dot/skills" {
			t.Errorf("library shown as %q", s.Library)
		}
		if len(s.Links) != len(AllHarnesses) {
			t.Errorf("links = %+v", s.Links)
		}
	})

	t.Run("a library outside the home keeps its full path", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(filepath.Dir(r.Home), "srv", "skills")
		if s := DetectSetup(r); s.Library != r.Library {
			t.Errorf("library shown as %q", s.Library)
		}
	})

	t.Run("a symlinked library is reported where it really is", func(t *testing.T) {
		r := fixture(t)
		isolateGit(t, r)
		s := DetectSetup(r)
		if want := filepath.Join(r.Home, "dotfiles", "agents", "skills"); !s.Exists || s.LibraryDir != want {
			t.Errorf("exists=%v dir=%q, want %q", s.Exists, s.LibraryDir, want)
		}
		if s.Git != nil {
			t.Errorf("git = %+v outside any repository", s.Git)
		}
		for _, l := range s.Links {
			if l.State != LinkDirect {
				t.Errorf("%s reaches the library %s, want direct", l.Harness, l.State)
			}
		}
	})

	t.Run("the project library is shown relative to the project", func(t *testing.T) {
		r := machine(t)
		r.ProjectLibrary = filepath.Join(r.ProjectRoot, "tools", "skills")
		if s := DetectSetup(r); s.ProjectLibrary != "tools/skills" {
			t.Errorf("project library = %q", s.ProjectLibrary)
		}
	})

	t.Run("a library inside a git work tree", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("no git")
		}
		r := fixture(t)
		isolateGit(t, r)
		repo := filepath.Join(r.Home, "dotfiles")
		git(t, repo, "init", "-q")
		git(t, repo, "symbolic-ref", "HEAD", "refs/heads/trunk")

		// No commit yet: there is a branch, but no HEAD to resolve.
		s := DetectSetup(r)
		if s.Git == nil || s.Git.Root != repo || s.Git.Branch != "trunk" {
			t.Fatalf("before the first commit git = %+v, want root %q on trunk", s.Git, repo)
		}
		git(t, repo, "add", "-A")
		git(t, repo, "commit", "-q", "-m", "skills")
		if s := DetectSetup(r); s.Git == nil || s.Git.Root != repo || s.Git.Branch != "trunk" {
			t.Errorf("after a commit git = %+v", s.Git)
		}
	})
}

func TestLinkLibrary(t *testing.T) {
	t.Run("points a harness with no skills dir at the library", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(r.Home, "library")
		write(t, filepath.Join(r.Library, "lib-skill", "SKILL.md"), skillMD("lib-skill", "In the library"))
		if s := byName(t, mustDiscover(t, r))["lib-skill"]; len(s.Harnesses) != 0 {
			t.Fatalf("before linking harnesses = %v", s.Harnesses)
		}
		// Codex and pi share a link dir, so linking one links the other.
		for _, h := range []Harness{Claude, Codex} {
			if err := LinkLibrary(r, h); err != nil {
				t.Fatalf("%s: %v", h, err)
			}
		}
		for _, h := range AllHarnesses {
			if l := LinkState(r, h, ScopeUser); l.State != LinkDirect {
				t.Errorf("%s is %s after linking", h, l.State)
			}
		}
		if s := byName(t, mustDiscover(t, r))["lib-skill"]; !reflect.DeepEqual(s.Harnesses, AllHarnesses) {
			t.Errorf("after linking harnesses = %v", s.Harnesses)
		}
		target, err := os.Readlink(filepath.Join(r.ClaudeConfigDir, "skills"))
		if err != nil || filepath.IsAbs(target) {
			t.Errorf("claude link = %q, %v; want a relative symlink", target, err)
		}
	})

	t.Run("makes a default library that is not there yet", func(t *testing.T) {
		r := machine(t)
		if err := LinkLibrary(r, Codex); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Lstat(r.Library); err != nil || !info.IsDir() {
			t.Errorf("library is not a real dir: %v", err)
		}
		if l := LinkState(r, Pi, ScopeUser); l.State != LinkDirect {
			t.Errorf("pi shares the dir but is %s", l.State)
		}
	})

	t.Run("refuses a dir the harness already has", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(r.Home, "library")
		mine := filepath.Join(r.ClaudeConfigDir, "skills", "mine", "SKILL.md")
		write(t, mine, skillMD("mine", "Already here"))
		if err := LinkLibrary(r, Claude); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if isSymlink(filepath.Join(r.ClaudeConfigDir, "skills")) || !exists(mine) {
			t.Error("the harness's own dir was replaced")
		}
		if exists(r.Library) {
			t.Error("a refused link still made the library")
		}
	})

	t.Run("refuses a dangling symlink rather than replacing it", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(r.Home, "library")
		dangling := filepath.Join(r.ClaudeConfigDir, "skills")
		link(t, filepath.Join(r.Home, "gone"), dangling)
		if err := LinkLibrary(r, Claude); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if target, _ := os.Readlink(dangling); target != filepath.Join(r.Home, "gone") {
			t.Errorf("the symlink now points at %q", target)
		}
	})

	t.Run("refuses a harness that already reads the library", func(t *testing.T) {
		r := fixture(t)
		if err := LinkLibrary(r, Claude); !errors.Is(err, ErrInvalid) {
			t.Errorf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("refuses a harness it does not know", func(t *testing.T) {
		if err := LinkLibrary(machine(t), Harness("cursor")); !errors.Is(err, ErrInvalid) {
			t.Errorf("err = %v, want ErrInvalid", err)
		}
	})
}

// apart is a machine whose library no harness reads, holding one skill, and
// whose harnesses each have a skills dir of their own.
func apart(t *testing.T) (Roots, Skill) {
	t.Helper()
	r := machine(t)
	r.Library = filepath.Join(r.Home, "library")
	r.ProjectLibrary = filepath.Join(r.ProjectRoot, "tools", "skills")
	write(t, filepath.Join(r.Library, "lib-skill", "SKILL.md"), skillMD("lib-skill", "In the library"))
	write(t, filepath.Join(r.ProjectLibrary, "proj-skill", "SKILL.md"), skillMD("proj-skill", "In the project library"))
	write(t, filepath.Join(r.ClaudeConfigDir, "skills", "claude-own", "SKILL.md"), skillMD("claude-own", "Claude's"))
	return r, byName(t, mustDiscover(t, r))["lib-skill"]
}

func TestALibraryNoHarnessReadsStillListsItsSkills(t *testing.T) {
	r, s := apart(t)
	if s.Dir == "" || s.Scope != ScopeUser || !s.Editable || len(s.Harnesses) != 0 || len(s.Invocation) != 0 {
		t.Errorf("library skill = %+v", s)
	}
	if s.Harnesses == nil || s.Invocation == nil {
		t.Error("harnesses and invocation must be empty, not null, on the wire")
	}
	if p := byName(t, mustDiscover(t, r))["proj-skill"]; p.Scope != ScopeProject || len(p.Harnesses) != 0 {
		t.Errorf("project library skill = %+v", p)
	}
}

func TestLinkSkill(t *testing.T) {
	t.Run("one harness at a time", func(t *testing.T) {
		r, s := apart(t)
		linked, err := LinkSkill(r, s.Dir, Claude)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(linked.Harnesses, []Harness{Claude}) || linked.Invocation[Claude].Mode != ModeAuto {
			t.Errorf("after linking for claude: harnesses %v invocation %v", linked.Harnesses, linked.Invocation)
		}
		at := filepath.Join(r.ClaudeConfigDir, "skills", "lib-skill")
		if target, err := os.Readlink(at); err != nil || filepath.IsAbs(target) {
			t.Errorf("link = %q, %v; want a relative symlink", target, err)
		}
		if real, _ := filepath.EvalSymlinks(at); real != s.Dir {
			t.Errorf("link resolves to %q, want %q", real, s.Dir)
		}

		// Codex and pi share a dir, so one link serves both.
		linked, err = LinkSkill(r, s.Dir, Codex)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(linked.Harnesses, AllHarnesses) {
			t.Errorf("after linking for codex: harnesses %v", linked.Harnesses)
		}
		if _, err := LinkSkill(r, s.Dir, Pi); !errors.Is(err, ErrInvalid) {
			t.Errorf("linking for a harness that already sees it: err = %v", err)
		}
	})

	t.Run("a project skill is linked inside the project", func(t *testing.T) {
		r, _ := apart(t)
		proj := byName(t, mustDiscover(t, r))["proj-skill"]
		linked, err := LinkSkill(r, proj.Dir, Claude)
		if err != nil {
			t.Fatal(err)
		}
		if !isSymlink(filepath.Join(r.ProjectRoot, ".claude", "skills", "proj-skill")) || exists(filepath.Join(r.ClaudeConfigDir, "skills", "proj-skill")) {
			t.Error("the link should be in the project's .claude/skills and nowhere else")
		}
		if linked.Scope != ScopeProject || !containsHarness(linked.Harnesses, Claude) {
			t.Errorf("linked = %+v", linked)
		}
	})

	t.Run("refuses a name the harness dir already has", func(t *testing.T) {
		r, s := apart(t)
		taken := filepath.Join(r.ClaudeConfigDir, "skills", "lib-skill", "SKILL.md")
		write(t, taken, skillMD("lib-skill", "Claude's own of the same name"))
		if _, err := LinkSkill(r, s.Dir, Claude); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if isSymlink(filepath.Dir(taken)) || !exists(taken) {
			t.Error("the existing entry was replaced")
		}
	})

	t.Run("refuses a skill that is not editable", func(t *testing.T) {
		r := fixture(t)
		plug := byName(t, mustDiscover(t, r))["plug-skill"]
		if _, err := LinkSkill(r, plug.Dir, Codex); !errors.Is(err, ErrNotEditable) {
			t.Errorf("err = %v, want ErrNotEditable", err)
		}
		if exists(filepath.Join(r.Home, ".agents", "skills", "plug-skill")) {
			t.Error("a refused link was still made")
		}
	})

	t.Run("refuses an unknown harness and an undiscovered dir", func(t *testing.T) {
		r, s := apart(t)
		if _, err := LinkSkill(r, s.Dir, Harness("cursor")); !errors.Is(err, ErrInvalid) {
			t.Errorf("unknown harness err = %v", err)
		}
		if _, err := LinkSkill(r, filepath.Join(r.Home, "nowhere"), Claude); !errors.Is(err, ErrNotFound) {
			t.Errorf("undiscovered dir err = %v", err)
		}
	})
}

func TestRemove(t *testing.T) {
	entry := RecordEntry{Method: MethodNpx, Repo: "owner/repo"}

	t.Run("takes the per-skill links and the record entry with it", func(t *testing.T) {
		r, s := apart(t)
		write(t, filepath.Join(r.Library, "keep", "SKILL.md"), skillMD("keep", "Stays"))
		if err := SaveRecord(r.Library, Record{Skills: map[string]RecordEntry{"lib-skill": entry, "keep": entry}}); err != nil {
			t.Fatal(err)
		}
		for _, h := range []Harness{Claude, Codex} {
			if _, err := LinkSkill(r, s.Dir, h); err != nil {
				t.Fatal(err)
			}
		}
		if err := Remove(r, s.Dir); err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{s.Dir, filepath.Join(r.ClaudeConfigDir, "skills", "lib-skill"), filepath.Join(r.Home, ".agents", "skills", "lib-skill")} {
			if exists(gone) {
				t.Errorf("%s is still there", gone)
			}
		}
		left := byName(t, mustDiscover(t, r))
		if _, ok := left["lib-skill"]; ok {
			t.Error("still discovered")
		}
		for _, name := range []string{"keep", "claude-own", "proj-skill"} {
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

func TestCreateLandsInTheConfiguredLibrary(t *testing.T) {
	t.Run("personal", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(r.Home, "library")
		s, err := Create(r, ScopeUser, "fresh", "A new skill")
		if err != nil {
			t.Fatal(err)
		}
		if s.Dir != filepath.Join(r.Library, "fresh") || s.Scope != ScopeUser {
			t.Errorf("created %+v", s)
		}
		// Claude is linked; Codex and pi do not read this library, and nothing
		// was written into the dir they do read.
		if !reflect.DeepEqual(s.Harnesses, []Harness{Claude}) {
			t.Errorf("harnesses = %v", s.Harnesses)
		}
		if exists(filepath.Join(r.Home, ".agents")) {
			t.Error("wrote into ~/.agents although the library is elsewhere")
		}
	})

	t.Run("personal, claude already reading the library", func(t *testing.T) {
		r := machine(t)
		r.Library = filepath.Join(r.Home, "library")
		if err := LinkLibrary(r, Claude); err != nil {
			t.Fatal(err)
		}
		s, err := Create(r, ScopeUser, "fresh", "A new skill")
		if err != nil {
			t.Fatal(err)
		}
		if isSymlink(filepath.Join(r.Library, "fresh")) || len(s.Paths) != 2 {
			t.Errorf("want one real dir seen at two paths, got paths %v", s.Paths)
		}
	})

	t.Run("project", func(t *testing.T) {
		r := machine(t)
		r.ProjectLibrary = filepath.Join(r.ProjectRoot, "tools", "skills")
		s, err := Create(r, ScopeProject, "fresh", "A new skill")
		if err != nil {
			t.Fatal(err)
		}
		if s.Dir != filepath.Join(r.ProjectLibrary, "fresh") || s.Scope != ScopeProject {
			t.Errorf("created %+v", s)
		}
		if !isSymlink(filepath.Join(r.ProjectRoot, ".claude", "skills", "fresh")) || exists(filepath.Join(r.ProjectRoot, ".agents")) {
			t.Error("want a claude link and nothing in .agents")
		}
	})

	t.Run("refuses a name the library already has", func(t *testing.T) {
		r, _ := apart(t)
		if _, err := Create(r, ScopeUser, "lib-skill", "Again"); !errors.Is(err, ErrInvalid) {
			t.Errorf("err = %v, want ErrInvalid", err)
		}
	})
}
