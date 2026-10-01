package skills

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func skillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n\nbody of " + name + "\n"
}

// fixture builds a machine shaped like the real one: ~/.agents and
// ~/.claude/skills both point into a dotfiles repo, a project keeps its
// canonical skills in .agents/skills with .claude/skills linked to it.
func fixture(t *testing.T) Roots {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	project := filepath.Join(base, "project")
	dot := filepath.Join(home, "dotfiles", "agents")

	write(t, filepath.Join(dot, "skills", "shared", "SKILL.md"), skillMD("shared", "Shared by everyone"))
	write(t, filepath.Join(dot, "skills", "shared", "scripts", "run.sh"), "#!/bin/sh\necho hi\n")
	write(t, filepath.Join(dot, "skills", "shared", "blob.bin"), "a\x00b")
	write(t, filepath.Join(dot, "skills", "synced", "acct1", "cloud-one", "SKILL.md"), skillMD("cloud-one", "From claude.ai"))
	link(t, "dotfiles/agents", filepath.Join(home, ".agents"))
	link(t, "../dotfiles/agents/skills", filepath.Join(home, ".claude", "skills"))

	write(t, filepath.Join(home, ".codex", "skills", ".system", "sys-skill", "SKILL.md"), skillMD("sys-skill", "Bundled"))
	write(t, filepath.Join(home, ".codex", "skills", "codex-only", "SKILL.md"), skillMD("codex-only", "Codex user skill"))
	write(t, filepath.Join(home, ".pi", "agent", "skills", "pi-only", "SKILL.md"), skillMD("pi-only", "Pi user skill"))

	plugin := filepath.Join(home, "plugcache", "tools", "1.0.0")
	other := filepath.Join(home, "plugcache", "elsewhere", "1.0.0")
	write(t, filepath.Join(plugin, "skills", "plug-skill", "SKILL.md"), skillMD("plug-skill", "From a plugin"))
	write(t, filepath.Join(other, "skills", "other-plug", "SKILL.md"), skillMD("other-plug", "Other project's plugin"))
	installed, _ := json.Marshal(map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"tools@market":     []map[string]string{{"scope": "user", "installPath": plugin}},
			"elsewhere@market": []map[string]string{{"scope": "project", "installPath": other, "projectPath": filepath.Join(base, "nope")}},
		},
	})
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), string(installed))

	write(t, filepath.Join(project, ".agents", "skills", "dev", "SKILL.md"), skillMD("dev", "Run the dev server"))
	write(t, filepath.Join(project, ".agents", "skills", "no-desc", "SKILL.md"), "---\nname: no-desc\n---\nbody\n")
	write(t, filepath.Join(project, ".agents", "skills", "mismatch", "SKILL.md"), skillMD("other-name", "Wrong name"))
	write(t, filepath.Join(project, ".agents", "skills", "unclosed", "SKILL.md"), "---\nname: unclosed\n")
	write(t, filepath.Join(project, ".agents", "skills", "not-a-skill", "README.md"), "no SKILL.md here")
	link(t, "../.agents/skills", filepath.Join(project, ".claude", "skills"))

	clearEnv(t)
	return DefaultRoots(home, nil, project)
}

// clearEnv keeps the developer's own harness and XDG settings out of the
// roots a test builds.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
}

// machine is an empty home and project with nothing linked, for a test that
// lays out its own skills.
func machine(t *testing.T) Roots {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, project := filepath.Join(base, "home"), filepath.Join(base, "project")
	for _, dir := range []string{home, project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	clearEnv(t)
	return DefaultRoots(home, nil, project)
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func byName(t *testing.T, list []Skill) map[string]Skill {
	t.Helper()
	out := map[string]Skill{}
	for _, s := range list {
		if _, dup := out[s.Name]; dup {
			t.Fatalf("skill %q listed twice", s.Name)
		}
		out[s.Name] = s
	}
	return out
}

func TestDiscover(t *testing.T) {
	r := fixture(t)
	list, err := Discover(r)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(t, list)

	tests := []struct {
		name      string
		scope     string
		plugin    string
		harnesses []Harness
		editable  bool
		paths     int
		problem   string
	}{
		{"shared", ScopeUser, "", []Harness{Claude, Codex, Pi}, true, 2, ""},
		{"cloud-one", ScopeUser, "", []Harness{Claude}, false, 1, ""},
		{"codex-only", ScopeUser, "", []Harness{Codex}, true, 1, ""},
		{"pi-only", ScopeUser, "", []Harness{Pi}, true, 1, ""},
		{"sys-skill", ScopeSystem, "", []Harness{Codex}, false, 1, ""},
		{"plug-skill", ScopePlugin, "tools", []Harness{Claude}, false, 1, ""},
		{"dev", ScopeProject, "", []Harness{Claude, Codex, Pi}, true, 2, ""},
		{"no-desc", ScopeProject, "", []Harness{Claude, Codex, Pi}, true, 2, "missing description"},
		{"other-name", ScopeProject, "", []Harness{Claude, Codex, Pi}, true, 2, "does not match directory"},
		{"unclosed", ScopeProject, "", []Harness{Claude, Codex, Pi}, true, 2, "not closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := got[tt.name]
			if !ok {
				t.Fatalf("not discovered; got %v", keys(got))
			}
			if s.Scope != tt.scope || s.Plugin != tt.plugin || s.Editable != tt.editable {
				t.Errorf("scope/plugin/editable = %q/%q/%v, want %q/%q/%v", s.Scope, s.Plugin, s.Editable, tt.scope, tt.plugin, tt.editable)
			}
			if !reflect.DeepEqual(s.harnesses, tt.harnesses) {
				t.Errorf("harnesses = %v, want %v", s.harnesses, tt.harnesses)
			}
			if len(s.paths) != tt.paths {
				t.Errorf("paths = %v, want %d", s.paths, tt.paths)
			}
			if tt.problem == "" && s.Problem != "" || !strings.Contains(s.Problem, tt.problem) {
				t.Errorf("problem = %q, want containing %q", s.Problem, tt.problem)
			}
			real, _ := filepath.EvalSymlinks(s.Dir)
			if real != s.Dir {
				t.Errorf("dir %q is not symlink-resolved (%q)", s.Dir, real)
			}
		})
	}
	for _, absent := range []string{"other-plug", "not-a-skill", "synced"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s should not be discovered", absent)
		}
	}
	if len(list) != len(tests) {
		t.Errorf("got %d skills %v, want %d", len(list), keys(got), len(tests))
	}

	// Project first, then user, then plugin, then system.
	last := -1
	for _, s := range list {
		if rank := scopeRank(s.Scope); rank < last {
			t.Fatalf("%s (%s) sorted after a later scope", s.Name, s.Scope)
		} else {
			last = rank
		}
	}
}

func TestDiscoverWithoutProject(t *testing.T) {
	r := fixture(t)
	r.ProjectRoot = ""
	list, _ := Discover(r)
	for _, s := range list {
		if s.Scope == ScopeProject {
			t.Errorf("project skill %s discovered with no project", s.Name)
		}
	}
}

func TestProjectPluginAppliesUnderItsProject(t *testing.T) {
	r := fixture(t)
	r.ProjectRoot = filepath.Join(filepath.Dir(r.Home), "nope", "sub")
	got := byName(t, mustDiscover(t, r))
	if s, ok := got["other-plug"]; !ok || s.Plugin != "elsewhere" {
		t.Errorf("project-scoped plugin not applied under its project: %+v", s)
	}
}

func TestDefaultRoots(t *testing.T) {
	t.Setenv("CODEX_HOME", "/from/process")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "/process/claude")
	t.Setenv("XDG_STATE_HOME", "")
	r := DefaultRoots("/h", map[string]string{"CLAUDE_CONFIG_DIR": "~/inst/a"}, "/p/")
	got := [5]string{r.Home, r.ClaudeConfigDir, r.CodexHome, r.PiAgentDir, r.ProjectRoot}
	want := [5]string{"/h", "/h/inst/a", "/from/process", "/h/.pi/agent", "/p"}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if !strings.HasPrefix(r.Library, "/h/") || !strings.HasPrefix(r.ProjectLibrary, "/p/") || !strings.HasPrefix(r.CLILock, "/h/") {
		t.Errorf("library %q, project library %q and lock %q should sit under the home and the project", r.Library, r.ProjectLibrary, r.CLILock)
	}
	if none := DefaultRoots("/h", nil, ""); none.ProjectLibrary != "" {
		t.Errorf("project library %q with no project", none.ProjectLibrary)
	}
	// The skills CLI keeps its lock under XDG_STATE_HOME when that is set.
	t.Setenv("XDG_STATE_HOME", "/state")
	if lock := DefaultRoots("/h", nil, "").CLILock; !strings.HasPrefix(lock, "/state/") {
		t.Errorf("lock = %q, want it under XDG_STATE_HOME", lock)
	}
}

func mustDiscover(t *testing.T, r Roots) []Skill {
	t.Helper()
	list, err := Discover(r)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func keys(m map[string]Skill) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRead(t *testing.T) {
	r := fixture(t)
	shared := byName(t, mustDiscover(t, r))["shared"]
	d, err := Read(r, shared.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Content, "body of shared") {
		t.Errorf("content = %q", d.Content)
	}
	want := []File{{Path: "blob.bin", Size: 3}, {Path: "scripts/run.sh", Size: 18}}
	if !reflect.DeepEqual(d.Files, want) {
		t.Errorf("files = %+v, want %+v", d.Files, want)
	}
	for _, dir := range []string{"", "relative", filepath.Join(r.Home, "dotfiles"), shared.paths[0] + "/.."} {
		if _, err := Read(r, dir); !errors.Is(err, ErrNotFound) {
			t.Errorf("Read(%q) err = %v, want ErrNotFound", dir, err)
		}
	}
}

func TestSave(t *testing.T) {
	r := fixture(t)
	got := byName(t, mustDiscover(t, r))

	t.Run("writes through the symlinked SKILL.md", func(t *testing.T) {
		dev := got["dev"]
		real := filepath.Join(dev.Dir, "SKILL.md")
		moved := filepath.Join(r.ProjectRoot, "canonical.md")
		if err := os.Rename(real, moved); err != nil {
			t.Fatal(err)
		}
		link(t, moved, real)
		next := skillMD("dev", "Updated description")
		if err := Save(r, dev.Dir, next); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Lstat(real); fi.Mode()&os.ModeSymlink == 0 {
			t.Error("SKILL.md symlink was replaced by a file")
		}
		if data, _ := os.ReadFile(moved); string(data) != next {
			t.Errorf("target = %q", data)
		}
		if s := byName(t, mustDiscover(t, r))["dev"]; s.Description != "Updated description" {
			t.Errorf("description after save = %q", s.Description)
		}
	})

	tests := []struct {
		name    string
		skill   string
		content string
		want    error
	}{
		{"plugin skill", "plug-skill", skillMD("plug-skill", "x"), ErrNotEditable},
		{"system skill", "sys-skill", skillMD("sys-skill", "x"), ErrNotEditable},
		{"synced skill", "cloud-one", skillMD("cloud-one", "x"), ErrNotEditable},
		{"no frontmatter", "shared", "just text", ErrInvalid},
		{"no description", "shared", "---\nname: shared\n---\n", ErrInvalid},
		{"no name", "shared", "---\ndescription: d\n---\n", ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := got[tt.skill]
			before, _ := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
			if err := Save(r, s.Dir, tt.content); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			after, _ := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
			if string(before) != string(after) {
				t.Error("refused save still changed the file")
			}
		})
	}
	if err := Save(r, filepath.Join(r.Home, "nowhere"), skillMD("x", "y")); !errors.Is(err, ErrNotFound) {
		t.Errorf("save to undiscovered dir err = %v", err)
	}
}

func TestReadFile(t *testing.T) {
	r := fixture(t)
	shared := byName(t, mustDiscover(t, r))["shared"]
	write(t, filepath.Join(r.Home, "secret.txt"), "secret")
	link(t, filepath.Join(r.Home, "secret.txt"), filepath.Join(shared.Dir, "escape.txt"))
	write(t, filepath.Join(shared.Dir, "big.txt"), strings.Repeat("x", maxFileBytes+1))

	content, binary, err := ReadFile(r, shared.Dir, "scripts/run.sh")
	if err != nil || binary || !strings.Contains(content, "echo hi") {
		t.Errorf("text file: %q %v %v", content, binary, err)
	}
	if _, binary, err := ReadFile(r, shared.Dir, "blob.bin"); err != nil || !binary {
		t.Errorf("binary file: %v %v", binary, err)
	}
	for _, rel := range []string{"../../secret.txt", "/etc/passwd", "scripts/../../x", "escape.txt", "big.txt", "scripts", ""} {
		if _, _, err := ReadFile(r, shared.Dir, rel); err == nil {
			t.Errorf("ReadFile(%q) should fail", rel)
		}
	}
	if _, _, err := ReadFile(r, shared.Dir, "missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing file err = %v", err)
	}
}

func TestCreate(t *testing.T) {
	t.Run("claude dir already shares the library", func(t *testing.T) {
		r := fixture(t)
		s, err := Create(r, "fresh", "A new skill: with a colon")
		if err != nil {
			t.Fatal(err)
		}
		if s.Name != "fresh" || s.Description != "A new skill: with a colon" || s.Scope != ScopeUser || !s.Editable || s.Problem != "" || s.Mode != ModeOn {
			t.Errorf("created %+v", s)
		}
		if s.Dir != filepath.Join(r.Home, "dotfiles", "agents", "skills", "fresh") {
			t.Errorf("dir = %s", s.Dir)
		}
		if !reflect.DeepEqual(s.harnesses, []Harness{Claude, Codex, Pi}) {
			t.Errorf("harnesses = %v", s.harnesses)
		}
		// ~/.claude/skills already resolves to the library: no extra link.
		entries, _ := os.ReadDir(filepath.Join(r.Home, "dotfiles", "agents", "skills"))
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				t.Errorf("unexpected symlink %s", e.Name())
			}
		}
	})

	t.Run("a separate claude dir gets a relative link", func(t *testing.T) {
		r := fixture(t)
		r.ClaudeConfigDir = filepath.Join(r.Home, "instances", "a")
		write(t, filepath.Join(r.ClaudeConfigDir, "skills", "own", "SKILL.md"), skillMD("own", "Claude's own"))
		s, err := Create(r, "linked", "Linked for Claude")
		if err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(r.ClaudeConfigDir, "skills", "linked")
		target, err := os.Readlink(linkPath)
		if err != nil || filepath.IsAbs(target) {
			t.Fatalf("link = %q, %v; want a relative symlink", target, err)
		}
		if real, _ := filepath.EvalSymlinks(linkPath); real != s.Dir {
			t.Errorf("link resolves to %q, want %q", real, s.Dir)
		}
		if !reflect.DeepEqual(s.harnesses, []Harness{Claude, Codex, Pi}) || len(s.paths) != 2 {
			t.Errorf("harnesses %v paths %v", s.harnesses, s.paths)
		}
	})

	refusals := []struct {
		name, skill, desc string
	}{
		{"existing in the library", "shared", "d"},
		{"existing only in claude dir", "claude-only", "d"},
		{"existing only in codex dir", "codex-only", "d"},
		{"uppercase", "Bad", "d"},
		{"leading hyphen", "-bad", "d"},
		{"trailing hyphen", "bad-", "d"},
		{"double hyphen", "a--b", "d"},
		{"too long", strings.Repeat("a", 65), "d"},
		{"empty description", "ok", "  "},
		{"long description", "ok", strings.Repeat("d", 1025)},
	}
	for _, tt := range refusals {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			r := fixture(t)
			r.ClaudeConfigDir = filepath.Join(r.Home, "instances", "b")
			write(t, filepath.Join(r.ClaudeConfigDir, "skills", "claude-only", "SKILL.md"), skillMD("claude-only", "x"))
			before := len(mustDiscover(t, r))
			if _, err := Create(r, tt.skill, tt.desc); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if after := len(mustDiscover(t, r)); after != before {
				t.Errorf("refused create changed the skill count %d -> %d", before, after)
			}
		})
	}
}

func TestFrontmatter(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
		err  error
	}{
		{"plain", "---\nname: a\ndescription: plain text # comment\n---\n", map[string]string{"name": "a", "description": "plain text"}, nil},
		{"double quoted", "---\ndescription: \"say \\\"hi\\\": now\"\n---\n", map[string]string{"description": `say "hi": now`}, nil},
		{"single quoted", "---\ndescription: 'it''s'\n---\n", map[string]string{"description": "it's"}, nil},
		{"folded", "---\ndescription: >\n  one\n  two\n\n  three\nname: x\n---\n", map[string]string{"description": "one two\nthree", "name": "x"}, nil},
		{"literal", "---\ndescription: |-\n  one\n  two\n---\n", map[string]string{"description": "one\ntwo"}, nil},
		{"plain continuation", "---\ndescription: one\n  two\n---\n", map[string]string{"description": "one two"}, nil},
		{"nested map skipped", "---\nmetadata:\n  name: inner\nname: outer\n---\n", map[string]string{"name": "outer"}, nil},
		{"crlf", "---\r\nname: a\r\n---\r\n", map[string]string{"name": "a"}, nil},
		{"none", "# title\n", nil, errNoFrontmatter},
		{"unclosed", "---\nname: a\n", nil, errUnclosedFrontmatter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := parseFrontmatter(tt.in)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if tt.err == nil && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestYAMLScalarRoundTrips(t *testing.T) {
	for _, v := range []string{"plain", "has: colon", "- dash", "quote \"inside\"", "#hash", "trailing:", "tab\there", "ünïcode"} {
		got, _, err := parseFrontmatter("---\ndescription: " + yamlScalar(v) + "\n---\n")
		if err != nil || got["description"] != v {
			t.Errorf("%q round-tripped to %q (%v)", v, got["description"], err)
		}
	}
}
