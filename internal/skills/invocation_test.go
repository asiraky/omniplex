package skills

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	manualLine = "disable-model-invocation: true\n"
	noImplicit = "policy:\n  allow_implicit_invocation: false\n"
)

// everywhere puts one skill where all three harnesses see it: the real
// directory in ~/.agents/skills, linked into Claude's dir.
func everywhere(t *testing.T, r Roots, name, frontmatter string) string {
	t.Helper()
	dir := filepath.Join(r.Home, ".agents", "skills", name)
	write(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+name+"\ndescription: d\n"+frontmatter+"---\nbody\n")
	link(t, dir, filepath.Join(r.ClaudeConfigDir, "skills", name))
	return dir
}

func overrides(value string) string {
	return `{"model": "x", "skillOverrides": {"s": "` + value + `", "plug": "off"}}`
}

func TestInvocation(t *testing.T) {
	auto, manual := HarnessState{Mode: ModeAuto}, HarnessState{Mode: ModeManual}
	settings := func(mode string) HarnessState { return HarnessState{Mode: mode, By: BySettings} }
	all := func(claude, codex, pi HarnessState) map[Harness]HarnessState {
		return map[Harness]HarnessState{Claude: claude, Codex: codex, Pi: pi}
	}

	tests := []struct {
		name        string
		frontmatter string
		setup       func(t *testing.T, r Roots, dir string)
		want        map[Harness]HarnessState
	}{
		{"nothing says otherwise", "", nil, all(auto, auto, auto)},
		{"frontmatter alone leaves Codex on auto", manualLine, nil, all(manual, auto, manual)},
		{"frontmatter false is auto", "disable-model-invocation: false\n", nil, all(auto, auto, auto)},
		{"openai.yaml alone leaves Claude and pi on auto", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
		}, all(auto, manual, auto)},
		{"both files agree", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), "interface:\n  display_name: S\n"+noImplicit)
		}, all(manual, manual, manual)},
		{"openai.yaml allowing it is auto", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), "policy:\n  allow_implicit_invocation: true\n")
		}, all(auto, auto, auto)},

		{"claude user settings turn it off", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
		}, all(settings(ModeOff), auto, auto)},
		{"claude settings beat the frontmatter", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
		}, all(settings(ModeOff), auto, manual)},
		{"user-invocable-only is manual by settings", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("user-invocable-only"))
		}, all(settings(ModeManual), auto, auto)},
		{"name-only", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("name-only"))
		}, all(settings(ModeNameOnly), auto, auto)},
		{"name-only cannot list a skill its frontmatter hides", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("name-only"))
		}, all(manual, auto, manual)},
		{"on leaves the frontmatter in charge", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("on"))
		}, all(manual, auto, manual)},
		{"project settings beat user settings", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.json"), overrides("on"))
		}, all(auto, auto, auto)},
		{"local settings beat project settings", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("on"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.json"), overrides("off"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.local.json"), overrides("name-only"))
		}, all(settings(ModeNameOnly), auto, auto)},
		{"a settings file that does not parse is skipped", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.json"), "{not json")
		}, all(settings(ModeOff), auto, auto)},
		{"a settings file naming another skill changes nothing", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides": {"other": "off"}}`)
		}, all(auto, auto, auto)},

		{"codex config disables it", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\npath = \""+dir+"/SKILL.md\"\nenabled = false\n")
		}, all(auto, settings(ModeOff), auto)},
		{"codex config beats openai.yaml", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\npath = \""+dir+"/SKILL.md\"\nenabled = false\n")
		}, all(manual, settings(ModeOff), manual)},
		{"codex config enabling it leaves openai.yaml in charge", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\npath = \""+dir+"/SKILL.md\"\nenabled = true\n")
		}, all(auto, manual, auto)},
		{"codex config naming the path through a symlink", "", func(t *testing.T, r Roots, dir string) {
			via := filepath.Join(r.ClaudeConfigDir, "skills", "s", "SKILL.md")
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\npath = '"+via+"'\nenabled = false\n")
		}, all(auto, settings(ModeOff), auto)},
		{"codex config for another skill", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\npath = \""+dir+"-other/SKILL.md\"\nenabled = false\n")
		}, all(auto, auto, auto)},
		{"the last codex entry for a path wins", "", func(t *testing.T, r Roots, dir string) {
			entry := "[[skills.config]]\npath = \"" + dir + "/SKILL.md\"\nenabled = "
			write(t, filepath.Join(r.CodexHome, "config.toml"), entry+"false\n"+entry+"true\n")
		}, all(auto, auto, auto)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := machine(t)
			dir := everywhere(t, r, "s", tt.frontmatter)
			if tt.setup != nil {
				tt.setup(t, r, dir)
			}
			got := byName(t, mustDiscover(t, r))["s"].Invocation
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("invocation = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInvocationOnlyCoversHarnessesThatSeeTheSkill(t *testing.T) {
	r := machine(t)
	write(t, filepath.Join(r.ClaudeConfigDir, "skills", "claude-only", "SKILL.md"), skillMD("claude-only", "d"))
	write(t, filepath.Join(r.CodexHome, "skills", "codex-only", "SKILL.md"), "---\nname: codex-only\ndescription: d\n"+manualLine+"---\n")
	got := byName(t, mustDiscover(t, r))

	if inv := got["claude-only"].Invocation; !reflect.DeepEqual(inv, map[Harness]HarnessState{Claude: {Mode: ModeAuto}}) {
		t.Errorf("claude-only = %+v", inv)
	}
	// Codex does not read the frontmatter key, so a Codex-only skill that
	// sets it is still the model's to invoke.
	if inv := got["codex-only"].Invocation; !reflect.DeepEqual(inv, map[Harness]HarnessState{Codex: {Mode: ModeAuto}}) {
		t.Errorf("codex-only = %+v", inv)
	}
}

func TestClaudeSettingsDoNotApplyToPluginSkills(t *testing.T) {
	r := fixture(t)
	write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides": {"plug-skill": "off", "shared": "off"}}`)
	got := byName(t, mustDiscover(t, r))
	if st := got["plug-skill"].Invocation[Claude]; st != (HarnessState{Mode: ModeAuto}) {
		t.Errorf("plugin skill = %+v, want auto and not by settings", st)
	}
	if st := got["shared"].Invocation[Claude]; st != (HarnessState{Mode: ModeOff, By: BySettings}) {
		t.Errorf("the same file should still switch off a personal skill, got %+v", st)
	}
}

func TestParseCodexSkills(t *testing.T) {
	config := `model = "gpt"
enabled = false

[[skills.config]] # first
path = "/a/SKILL.md" # trailing
enabled = false

[mcp_servers.x]
path = "/not/a/skill"
enabled = false

[[ skills.config ]]
path = '/b b/SKILL.md'

[[skills.config]]
enabled = false

[[skills.config]]
enabled = true # not false
path = "/c/\"q\"/SKILL.md"
`
	got := parseCodexSkills(strings.ReplaceAll(config, "\n", "\r\n"))
	want := []codexSkill{{"/a/SKILL.md", false}, {"/b b/SKILL.md", true}, {`/c/"q"/SKILL.md`, true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestEditFrontmatterManual(t *testing.T) {
	bom := string(rune(0xFEFF))
	folded := "---\nname: a\ndescription: >\n  mentions\n  disable-model-invocation: true\n  in passing\n---\nbody\n"
	tests := []struct {
		name   string
		in     string
		manual bool
		want   string
	}{
		{"absent, on: added as the last key", "---\nname: a\ndescription: d\n---\nbody\n", true, "---\nname: a\ndescription: d\ndisable-model-invocation: true\n---\nbody\n"},
		{"absent, off: untouched", "---\nname: a\n---\n", false, "---\nname: a\n---\n"},
		{"true, off: the line goes", "---\nname: a\ndisable-model-invocation: true\ndescription: d\n---\nbody\n", false, "---\nname: a\ndescription: d\n---\nbody\n"},
		{"true, on: untouched", "---\ndisable-model-invocation:   true # why\nname: a\n---\n", true, "---\ndisable-model-invocation:   true # why\nname: a\n---\n"},
		{"false, on: flipped where it stands", "---\ndisable-model-invocation:   false  # why\nname: a\n---\n", true, "---\ndisable-model-invocation:   true  # why\nname: a\n---\n"},
		{"false, off: untouched", "---\ndisable-model-invocation: false\nname: a\n---\n", false, "---\ndisable-model-invocation: false\nname: a\n---\n"},
		{"empty value, on", "---\ndisable-model-invocation:\nname: a\n---\n", true, "---\ndisable-model-invocation: true\nname: a\n---\n"},
		{"quoted key and value, off", "---\nname: a\n\"disable-model-invocation\": \"true\"\n---\n", false, "---\nname: a\n---\n"},
		{"quoted value, on", "---\ndisable-model-invocation: 'false'\n---\n", true, "---\ndisable-model-invocation: true\n---\n"},
		{"the text inside another key's value is not the key, off", folded, false, folded},
		{"the text inside another key's value is not the key, on", folded, true, strings.Replace(folded, "---\nbody", manualLine+"---\nbody", 1)},
		{"the same line in the body is not frontmatter", "---\nname: a\n---\ndisable-model-invocation: true\n", false, "---\nname: a\n---\ndisable-model-invocation: true\n"},
		{"other keys keep their quoting and comments", "---\nname: \"a\"  # quoted\nmetadata:\n  k: 'v'\nallowed-tools: [Read, \"Bash(git *)\"]\n---\n", true, "---\nname: \"a\"  # quoted\nmetadata:\n  k: 'v'\nallowed-tools: [Read, \"Bash(git *)\"]\ndisable-model-invocation: true\n---\n"},
		{"crlf, on", "---\r\nname: a\r\n---\r\nbody\r\n", true, "---\r\nname: a\r\ndisable-model-invocation: true\r\n---\r\nbody\r\n"},
		{"crlf, off", "---\r\nname: a\r\ndisable-model-invocation: true\r\ndescription: d\r\n---\r\n", false, "---\r\nname: a\r\ndescription: d\r\n---\r\n"},
		{"crlf, flipped", "---\r\ndisable-model-invocation: false\r\n---\r\n", true, "---\r\ndisable-model-invocation: true\r\n---\r\n"},
		{"byte order mark", bom + "---\nname: a\n---\n", true, bom + "---\nname: a\ndisable-model-invocation: true\n---\n"},
		{"closing line without a newline", "---\nname: a\n---", true, "---\nname: a\ndisable-model-invocation: true\n---"},
		{"no frontmatter, off: untouched", "# just a title\n", false, "# just a title\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := editFrontmatterManual(tt.in, tt.manual)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			if _, _, err := parseFrontmatter(tt.in); err == nil && frontmatterManual(got) != tt.manual {
				t.Errorf("reads back as manual=%v", !tt.manual)
			}
		})
	}
	for _, in := range []string{"# just a title\n", "---\nname: a\n", ""} {
		if got, err := editFrontmatterManual(in, true); err == nil {
			t.Errorf("turning manual on in %q gave %q, want an error", in, got)
		}
	}
}

func TestEditOpenAIManual(t *testing.T) {
	nested := "policy:\n  nested:\n    allow_implicit_invocation: false\n"
	elsewhere := "interface:\n  policy:\n    allow_implicit_invocation: false\n"
	tests := []struct {
		name   string
		in     string
		manual bool
		want   string
	}{
		{"other keys stay, on", "interface:\n  display_name: \"S\"  # shown\npolicy:\n  allow_implicit_invocation: true\n  products: [a]\ndependencies: {}\n", true, "interface:\n  display_name: \"S\"  # shown\npolicy:\n  allow_implicit_invocation: false\n  products: [a]\ndependencies: {}\n"},
		{"other keys stay, off", "policy:\n    allow_implicit_invocation:  false # keep out\n    products: [a]\n", false, "policy:\n    allow_implicit_invocation:  true # keep out\n    products: [a]\n"},
		{"already manual", noImplicit, true, noImplicit},
		{"already allowed", "policy:\n  allow_implicit_invocation: true\n", false, "policy:\n  allow_implicit_invocation: true\n"},
		{"policy without the key, on: added at the siblings' indent", "policy:\n    products: [a]\ninterface: {}\n", true, "policy:\n    allow_implicit_invocation: false\n    products: [a]\ninterface: {}\n"},
		{"policy without the key, off: untouched", "policy:\n  products: [a]\n", false, "policy:\n  products: [a]\n"},
		{"empty policy, on", "policy:\ninterface: {}\n", true, "policy:\n  allow_implicit_invocation: false\ninterface: {}\n"},
		{"no policy, on: appended", "interface:\n  display_name: S\n", true, "interface:\n  display_name: S\n" + noImplicit},
		{"no policy, off: untouched", "interface:\n  display_name: S\n", false, "interface:\n  display_name: S\n"},
		{"no trailing newline", "interface: {}", true, "interface: {}\n" + noImplicit},
		{"policy on the last line without a newline", "policy:", true, "policy:\n  allow_implicit_invocation: false\n"},
		{"empty file", "", true, noImplicit},
		{"flow style, on", "policy: {products: [a], allow_implicit_invocation: true}\n", true, "policy: {products: [a], allow_implicit_invocation: false}\n"},
		{"flow style, off", "policy: {allow_implicit_invocation: false, products: [a]}\n", false, "policy: {allow_implicit_invocation: true, products: [a]}\n"},
		{"flow style without the key", "policy: {products: [a]}\n", true, "policy: {allow_implicit_invocation: false, products: [a]}\n"},
		{"empty flow", "policy: {}\n", true, "policy: {allow_implicit_invocation: false}\n"},
		{"a deeper key of the same name is not the policy", nested, true, "policy:\n  allow_implicit_invocation: false\n  nested:\n    allow_implicit_invocation: false\n"},
		{"a policy under another key is not the policy", elsewhere, true, elsewhere + noImplicit},
		{"crlf", "interface: {}\r\npolicy:\r\n  allow_implicit_invocation: true\r\n", true, "interface: {}\r\npolicy:\r\n  allow_implicit_invocation: false\r\n"},
		{"a comment at column 0 inside policy", "policy:\n# why\n  allow_implicit_invocation: true\n", true, "policy:\n# why\n  allow_implicit_invocation: false\n"},
		{"crlf, appended", "interface: {}\r\n", true, "interface: {}\r\npolicy:\r\n  allow_implicit_invocation: false\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := editOpenAIManual(tt.in, tt.manual)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			if openaiManual(got) != tt.manual {
				t.Errorf("reads back as manual=%v", !tt.manual)
			}
		})
	}
	for _, in := range []string{nested, elsewhere} {
		if openaiManual(in) {
			t.Errorf("%q read as manual", in)
		}
	}
	if got, err := editOpenAIManual("policy: null\n", true); err == nil {
		t.Errorf("a policy that is not a map was edited to %q", got)
	}
}

func TestSetInvocation(t *testing.T) {
	t.Run("on then off leaves SKILL.md as it started", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		skillFile := filepath.Join(shared.Dir, "SKILL.md")
		yamlFile := filepath.Join(shared.Dir, "agents", "openai.yaml")
		before := read(t, skillFile)

		s, err := SetInvocation(r, shared.Dir, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range AllHarnesses {
			if s.Invocation[h] != (HarnessState{Mode: ModeManual}) {
				t.Errorf("%s = %+v after turning manual on", h, s.Invocation[h])
			}
		}
		if fm, oy := FileManual(shared.Dir); !fm || !oy {
			t.Errorf("files say frontmatter=%v openai=%v", fm, oy)
		}

		s, err = SetInvocation(r, shared.Dir, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range AllHarnesses {
			if s.Invocation[h] != (HarnessState{Mode: ModeAuto}) {
				t.Errorf("%s = %+v after turning manual off", h, s.Invocation[h])
			}
		}
		if after := read(t, skillFile); after != before {
			t.Errorf("SKILL.md = %q, want the original %q", after, before)
		}
		// The file made for Codex goes again.
		if exists(yamlFile) {
			t.Errorf("openai.yaml after turning off: %q", read(t, yamlFile))
		}
	})

	t.Run("off does not create openai.yaml", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		if _, err := SetInvocation(r, shared.Dir, false); err != nil {
			t.Fatal(err)
		}
		if exists(filepath.Join(shared.Dir, "agents")) {
			t.Error("agents/ was created to say nothing")
		}
	})

	t.Run("an existing openai.yaml keeps its other keys", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		yamlFile := filepath.Join(shared.Dir, "agents", "openai.yaml")
		write(t, yamlFile, "interface:\n  display_name: Shared\n")
		if _, err := SetInvocation(r, shared.Dir, true); err != nil {
			t.Fatal(err)
		}
		if got := read(t, yamlFile); got != "interface:\n  display_name: Shared\n"+noImplicit {
			t.Errorf("openai.yaml = %q", got)
		}
	})

	t.Run("writes through a symlinked SKILL.md", func(t *testing.T) {
		r := fixture(t)
		dev := byName(t, mustDiscover(t, r))["dev"]
		real := filepath.Join(dev.Dir, "SKILL.md")
		moved := filepath.Join(r.ProjectRoot, "canonical.md")
		if err := os.Rename(real, moved); err != nil {
			t.Fatal(err)
		}
		link(t, moved, real)
		if _, err := SetInvocation(r, dev.Dir, true); err != nil {
			t.Fatal(err)
		}
		if !isSymlink(real) || !frontmatterManual(read(t, moved)) {
			t.Errorf("symlink kept=%v, target = %q", isSymlink(real), read(t, moved))
		}
	})

	t.Run("a skill whose frontmatter cannot take the key gets neither file written", func(t *testing.T) {
		r := fixture(t)
		unclosed := byName(t, mustDiscover(t, r))["unclosed"]
		before := read(t, filepath.Join(unclosed.Dir, "SKILL.md"))
		if _, err := SetInvocation(r, unclosed.Dir, true); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if read(t, filepath.Join(unclosed.Dir, "SKILL.md")) != before || exists(filepath.Join(unclosed.Dir, "agents")) {
			t.Error("a refused toggle still wrote something")
		}
	})

	t.Run("an openai.yaml that cannot be edited leaves SKILL.md alone", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		write(t, filepath.Join(shared.Dir, "agents", "openai.yaml"), "policy: null\n")
		before := read(t, filepath.Join(shared.Dir, "SKILL.md"))
		if _, err := SetInvocation(r, shared.Dir, true); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if read(t, filepath.Join(shared.Dir, "SKILL.md")) != before {
			t.Error("SKILL.md was edited although the toggle failed")
		}
	})

	t.Run("refusals", func(t *testing.T) {
		r := fixture(t)
		got := byName(t, mustDiscover(t, r))
		for _, name := range []string{"plug-skill", "sys-skill", "cloud-one"} {
			s := got[name]
			before := read(t, filepath.Join(s.Dir, "SKILL.md"))
			if _, err := SetInvocation(r, s.Dir, true); !errors.Is(err, ErrNotEditable) {
				t.Errorf("%s: err = %v, want ErrNotEditable", name, err)
			}
			if read(t, filepath.Join(s.Dir, "SKILL.md")) != before || exists(filepath.Join(s.Dir, "agents")) {
				t.Errorf("%s: a refused toggle still wrote something", name)
			}
		}
		if _, err := SetInvocation(r, filepath.Join(r.Home, "nowhere"), true); !errors.Is(err, ErrNotFound) {
			t.Errorf("undiscovered dir err = %v", err)
		}
	})
}
