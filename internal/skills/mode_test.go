package skills

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
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

func codexEntry(path, enabled string) string {
	return "[[skills.config]]\npath = \"" + path + "\"\nenabled = " + enabled + "\n"
}

func TestMode(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		setup       func(t *testing.T, r Roots, dir string)
		want        string
	}{
		{"nothing says otherwise", "", nil, ModeOn},
		{"the frontmatter alone leaves Codex free", manualLine, nil, ModeOn},
		{"openai.yaml alone leaves Claude and pi free", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
		}, ModeOn},
		{"both files say manual", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), "interface:\n  display_name: S\n"+noImplicit)
		}, ModeManual},
		{"frontmatter false is on", "disable-model-invocation: false\n", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
		}, ModeOn},

		{"claude settings turn it off", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
		}, ModeOff},
		{"off beats manual files", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
		}, ModeOff},
		{"user-invocable-only is manual", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("user-invocable-only"))
		}, ModeManual},
		{"name-only is on", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("name-only"))
		}, ModeOn},
		{"name-only leaves manual files manual", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("name-only"))
		}, ModeManual},
		{"an override by folder name", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides": {"s": "off"}}`)
		}, ModeOff},
		{"project settings beat user settings", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.json"), overrides("on"))
		}, ModeOn},
		{"local settings beat project settings", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.json"), overrides("off"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.local.json"), overrides("user-invocable-only"))
		}, ModeManual},
		{"a settings file that does not parse is skipped", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
			write(t, filepath.Join(r.ProjectRoot, ".claude", "settings.json"), "{not json")
		}, ModeOff},
		{"settings naming another skill change nothing", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides": {"other": "off"}}`)
		}, ModeOn},

		{"codex config turns it off", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(dir+"/SKILL.md", "false"))
		}, ModeOff},
		{"codex config naming the folder", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(dir, "false"))
		}, ModeOff},
		{"codex config naming the path through a symlink", "", func(t *testing.T, r Roots, dir string) {
			via := filepath.Join(r.ClaudeConfigDir, "skills", "s", "SKILL.md")
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\npath = '"+via+"'\nenabled = false\n")
		}, ModeOff},
		{"codex config enabling it leaves the files in charge", manualLine, func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(dir, "agents", "openai.yaml"), noImplicit)
			write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(dir+"/SKILL.md", "true"))
		}, ModeManual},
		{"codex config for another skill", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(dir+"-other/SKILL.md", "false"))
		}, ModeOn},
		{"the last codex entry for a path wins", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(dir+"/SKILL.md", "false")+codexEntry(dir+"/SKILL.md", "true"))
		}, ModeOn},
		{"bundled skills off says nothing about this one", "", func(t *testing.T, r Roots, dir string) {
			write(t, filepath.Join(r.CodexHome, "config.toml"), "[skills.bundled]\nenabled = false\n")
		}, ModeOn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := machine(t)
			dir := everywhere(t, r, "s", tt.frontmatter)
			if tt.setup != nil {
				tt.setup(t, r, dir)
			}
			if got := byName(t, mustDiscover(t, r))["s"].Mode; got != tt.want {
				t.Errorf("mode = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestModeCountsOnlyTheHarnessesThatSeeTheSkill(t *testing.T) {
	r := machine(t)
	claudeOnly := filepath.Join(r.ClaudeConfigDir, "skills", "claude-only")
	codexOnly := filepath.Join(r.CodexHome, "skills", "codex-only")
	write(t, filepath.Join(claudeOnly, "SKILL.md"), "---\nname: claude-only\ndescription: d\n"+manualLine+"---\n")
	write(t, filepath.Join(codexOnly, "SKILL.md"), "---\nname: codex-only\ndescription: d\n"+manualLine+"---\n")
	write(t, filepath.Join(r.CodexHome, "skills", "codex-off", "SKILL.md"), skillMD("codex-off", "d"))
	write(t, filepath.Join(r.ClaudeConfigDir, "skills", "claude-off", "SKILL.md"), skillMD("claude-off", "d"))
	write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides": {"codex-off": "off"}}`)
	write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(filepath.Join(r.ClaudeConfigDir, "skills", "claude-off", "SKILL.md"), "false"))
	got := byName(t, mustDiscover(t, r))

	// Claude reads the frontmatter, so that is all a Claude-only skill needs.
	if m := got["claude-only"].Mode; m != ModeManual {
		t.Errorf("claude-only = %s", m)
	}
	// Codex does not read the frontmatter key.
	if m := got["codex-only"].Mode; m != ModeOn {
		t.Errorf("codex-only = %s", m)
	}
	// Each harness's settings reach only the skills it sees.
	if m := got["codex-off"].Mode; m != ModeOn {
		t.Errorf("a claude override turned off a skill only codex sees: %s", m)
	}
	if m := got["claude-off"].Mode; m != ModeOn {
		t.Errorf("a codex entry turned off a skill only claude sees: %s", m)
	}
}

func TestClaudeSettingsDoNotApplyToPluginSkills(t *testing.T) {
	r := fixture(t)
	write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides": {"plug-skill": "off", "shared": "off"}}`)
	got := byName(t, mustDiscover(t, r))
	if m := got["plug-skill"].Mode; m != ModeOn {
		t.Errorf("plugin skill = %s, want on", m)
	}
	if m := got["shared"].Mode; m != ModeOff {
		t.Errorf("the same file should still switch off a personal skill, got %s", m)
	}
}

func TestCodexBundledSkillsFollowTheSwitch(t *testing.T) {
	r := fixture(t)
	if m := byName(t, mustDiscover(t, r))["sys-skill"].Mode; m != ModeOn || !CodexBundled(r) {
		t.Fatalf("with no setting: mode %s, bundled %v", m, CodexBundled(r))
	}
	if err := SetCodexBundled(r, false); err != nil {
		t.Fatal(err)
	}
	got := byName(t, mustDiscover(t, r))
	if m := got["sys-skill"].Mode; m != ModeOff || CodexBundled(r) {
		t.Errorf("switched off: mode %s, bundled %v", m, CodexBundled(r))
	}
	if m := got["codex-only"].Mode; m != ModeOn {
		t.Errorf("a user's codex skill went off with the bundled ones: %s", m)
	}
	if err := SetCodexBundled(r, true); err != nil {
		t.Fatal(err)
	}
	if m := byName(t, mustDiscover(t, r))["sys-skill"].Mode; m != ModeOn || !CodexBundled(r) {
		t.Errorf("switched on again: mode %s, bundled %v", m, CodexBundled(r))
	}
}

// linkedConfigs moves Claude's settings.json and Codex's config.toml into a
// dotfiles folder and links them back, the way a synced setup has them.
func linkedConfigs(t *testing.T, r Roots, settings, config string) (settingsFile, configFile string) {
	t.Helper()
	settingsFile = filepath.Join(r.Home, "dotfiles", "claude-settings.json")
	configFile = filepath.Join(r.Home, "dotfiles", "codex.toml")
	write(t, settingsFile, settings)
	write(t, configFile, config)
	link(t, "../dotfiles/claude-settings.json", filepath.Join(r.ClaudeConfigDir, "settings.json"))
	link(t, configFile, filepath.Join(r.CodexHome, "config.toml"))
	return settingsFile, configFile
}

const codexConfig = `# my codex config
model = "gpt-5"

[[skills.config]] # keep this one
path = "/elsewhere/other/SKILL.md"
enabled = false

[mcp_servers.docs]
command = "npx"
args = ["-y", "docs"]   # trailing comment
`

func TestSetModeRoundTrip(t *testing.T) {
	for name, settings := range map[string]string{
		"settings with other overrides": "{\n  \"model\": \"opus\",\n  \"skillOverrides\": {\n    \"other\": \"user-invocable-only\"\n  },\n  \"syncClaudeAiSkills\": false\n}\n",
		"settings without overrides":    "{\n\t\"model\": \"opus\",\n\t\"env\": {\"A\": \"1\"}\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := machine(t)
			dir := everywhere(t, r, "s", "")
			skillFile := filepath.Join(dir, "SKILL.md")
			settingsFile, configFile := linkedConfigs(t, r, settings, codexConfig)
			before := map[string]string{skillFile: read(t, skillFile), settingsFile: settings, configFile: codexConfig}

			set := func(mode string) Skill {
				t.Helper()
				s, err := SetMode(r, dir, mode)
				if err != nil {
					t.Fatal(err)
				}
				if s.Mode != mode {
					t.Fatalf("set %s, skill says %s", mode, s.Mode)
				}
				if listed := byName(t, mustDiscover(t, r))["s"].Mode; listed != mode {
					t.Fatalf("set %s, listed as %s", mode, listed)
				}
				for _, l := range []string{filepath.Join(r.ClaudeConfigDir, "settings.json"), filepath.Join(r.CodexHome, "config.toml")} {
					if !isSymlink(l) {
						t.Fatalf("%s was replaced by a file", l)
					}
				}
				return s
			}

			set(ModeOn)
			for path, want := range before {
				if got := read(t, path); got != want {
					t.Errorf("on when already on changed %s:\n%q", path, got)
				}
			}

			set(ModeManual)
			if fm, oy := FileManual(dir); !fm || !oy {
				t.Errorf("manual: files say frontmatter=%v openai=%v", fm, oy)
			}
			if read(t, settingsFile) != settings || read(t, configFile) != codexConfig {
				t.Error("manual touched the harness settings")
			}

			set(ModeOff)
			var parsed struct {
				SkillOverrides map[string]string `json:"skillOverrides"`
				Model          string            `json:"model"`
			}
			if err := json.Unmarshal([]byte(read(t, settingsFile)), &parsed); err != nil {
				t.Fatalf("settings no longer parse: %v", err)
			}
			if parsed.SkillOverrides["s"] != "off" || parsed.Model != "opus" {
				t.Errorf("settings after off = %+v", parsed)
			}
			entries := parseCodexSkills(read(t, configFile))
			if len(entries) != 2 || entries[1] != (codexSkill{dir + "/SKILL.md", false}) {
				t.Errorf("codex entries after off = %+v", entries)
			}
			// pi has no off: it is kept from the model as manual.
			if fm, _ := FileManual(dir); !fm {
				t.Error("off left the frontmatter free for pi")
			}

			set(ModeOn)
			for path, want := range before {
				if got := read(t, path); got != want {
					t.Errorf("%s after the round trip:\n%q\nwant\n%q", path, got, want)
				}
			}
			if exists(filepath.Join(dir, "agents")) {
				t.Error("the openai.yaml made for manual is still there")
			}
		})
	}
}

func TestSetModeOffAndOnAgain(t *testing.T) {
	t.Run("off replaces an entry the config already has", func(t *testing.T) {
		r := machine(t)
		dir := everywhere(t, r, "s", "")
		via := filepath.Join(r.ClaudeConfigDir, "skills", "s", "SKILL.md")
		write(t, filepath.Join(r.CodexHome, "config.toml"), "[[skills.config]]\nenabled = true # mine\npath = \""+via+"\"\n")
		if _, err := SetMode(r, dir, ModeOff); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(r.CodexHome, "config.toml")); got != "[[skills.config]]\nenabled = false # mine\npath = \""+via+"\"\n" {
			t.Errorf("config = %q", got)
		}
		// On takes the entry away, whichever path it names the skill by.
		if _, err := SetMode(r, dir, ModeOn); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(r.CodexHome, "config.toml")); got != "" {
			t.Errorf("config after on = %q", got)
		}
	})

	t.Run("on takes away claude's off and manual, and nothing else", func(t *testing.T) {
		for value, kept := range map[string]bool{"off": false, "user-invocable-only": false, "name-only": true, "on": true} {
			r := machine(t)
			dir := everywhere(t, r, "s", "")
			path := filepath.Join(r.ClaudeConfigDir, "settings.json")
			write(t, path, overrides(value))
			if _, err := SetMode(r, dir, ModeOn); err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				SkillOverrides map[string]string `json:"skillOverrides"`
			}
			if err := json.Unmarshal([]byte(read(t, path)), &parsed); err != nil {
				t.Fatal(err)
			}
			if _, ok := parsed.SkillOverrides["s"]; ok != kept {
				t.Errorf("%s: kept = %v, want %v", value, ok, kept)
			}
			if parsed.SkillOverrides["plug"] != "off" {
				t.Errorf("%s: another skill's override went too", value)
			}
		}
	})

	t.Run("manual takes away off", func(t *testing.T) {
		r := machine(t)
		dir := everywhere(t, r, "s", "")
		write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), overrides("off"))
		write(t, filepath.Join(r.CodexHome, "config.toml"), codexEntry(dir, "false"))
		if s, err := SetMode(r, dir, ModeManual); err != nil || s.Mode != ModeManual {
			t.Fatalf("mode %s, err %v", s.Mode, err)
		}
	})

	t.Run("off with no settings files makes them", func(t *testing.T) {
		r := machine(t)
		dir := everywhere(t, r, "s", "")
		if _, err := SetMode(r, dir, ModeOff); err != nil {
			t.Fatal(err)
		}
		if !exists(filepath.Join(r.ClaudeConfigDir, "settings.json")) || !exists(filepath.Join(r.CodexHome, "config.toml")) {
			t.Error("a settings file is missing")
		}
		if s := byName(t, mustDiscover(t, r))["s"]; s.Mode != ModeOff {
			t.Errorf("mode = %s", s.Mode)
		}
	})

	t.Run("on with no settings files makes none", func(t *testing.T) {
		r := machine(t)
		dir := everywhere(t, r, "s", "")
		if _, err := SetMode(r, dir, ModeOn); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{filepath.Join(r.ClaudeConfigDir, "settings.json"), filepath.Join(r.CodexHome, "config.toml"), filepath.Join(dir, "agents")} {
			if exists(path) {
				t.Errorf("%s was made to say nothing", path)
			}
		}
	})
}

func TestSetModeFiles(t *testing.T) {
	t.Run("an existing openai.yaml keeps its other keys", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		yamlFile := filepath.Join(shared.Dir, "agents", "openai.yaml")
		write(t, yamlFile, "interface:\n  display_name: Shared\n")
		if _, err := SetMode(r, shared.Dir, ModeManual); err != nil {
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
		if _, err := SetMode(r, dev.Dir, ModeManual); err != nil {
			t.Fatal(err)
		}
		if !isSymlink(real) || !frontmatterManual(read(t, moved)) {
			t.Errorf("symlink kept=%v, target = %q", isSymlink(real), read(t, moved))
		}
	})

	t.Run("a skill whose frontmatter cannot take the key gets nothing written", func(t *testing.T) {
		r := fixture(t)
		unclosed := byName(t, mustDiscover(t, r))["unclosed"]
		before := read(t, filepath.Join(unclosed.Dir, "SKILL.md"))
		for _, mode := range []string{ModeManual, ModeOff} {
			if _, err := SetMode(r, unclosed.Dir, mode); !errors.Is(err, ErrInvalid) {
				t.Fatalf("%s: err = %v, want ErrInvalid", mode, err)
			}
		}
		if read(t, filepath.Join(unclosed.Dir, "SKILL.md")) != before || exists(filepath.Join(unclosed.Dir, "agents")) {
			t.Error("a refused switch still wrote into the skill")
		}
		if exists(filepath.Join(r.ClaudeConfigDir, "settings.json")) || exists(filepath.Join(r.CodexHome, "config.toml")) {
			t.Error("a refused switch still wrote settings")
		}
	})

	t.Run("an openai.yaml that cannot be edited leaves SKILL.md alone", func(t *testing.T) {
		r := fixture(t)
		shared := byName(t, mustDiscover(t, r))["shared"]
		write(t, filepath.Join(shared.Dir, "agents", "openai.yaml"), "policy: null\n")
		before := read(t, filepath.Join(shared.Dir, "SKILL.md"))
		if _, err := SetMode(r, shared.Dir, ModeManual); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if read(t, filepath.Join(shared.Dir, "SKILL.md")) != before {
			t.Error("SKILL.md was edited although the switch failed")
		}
	})

	t.Run("refusals", func(t *testing.T) {
		r := fixture(t)
		got := byName(t, mustDiscover(t, r))
		for _, name := range []string{"plug-skill", "cloud-one"} {
			s := got[name]
			before := read(t, filepath.Join(s.Dir, "SKILL.md"))
			if _, err := SetMode(r, s.Dir, ModeOff); !errors.Is(err, ErrNotEditable) {
				t.Errorf("%s: err = %v, want ErrNotEditable", name, err)
			}
			if read(t, filepath.Join(s.Dir, "SKILL.md")) != before || exists(filepath.Join(s.Dir, "agents")) {
				t.Errorf("%s: a refused switch still wrote something", name)
			}
		}
		if _, err := SetMode(r, filepath.Join(r.Home, "nowhere"), ModeOn); !errors.Is(err, ErrNotFound) {
			t.Errorf("undiscovered dir err = %v", err)
		}
		if _, err := SetMode(r, got["shared"].Dir, "name-only"); !errors.Is(err, ErrInvalid) {
			t.Errorf("unknown mode err = %v", err)
		}
		if exists(filepath.Join(r.ClaudeConfigDir, "settings.json")) || exists(filepath.Join(r.CodexHome, "config.toml")) {
			t.Error("a refused switch wrote settings")
		}
	})
}

func TestSetModeOffRefusesAConfigItCannotEditBeforeWritingAnything(t *testing.T) {
	r := machine(t)
	dir := everywhere(t, r, "s", "")
	config := filepath.Join(r.CodexHome, "config.toml")
	in := "[skills]\nconfig = []\n"
	write(t, config, in)
	before := read(t, filepath.Join(dir, "SKILL.md"))
	if _, err := SetMode(r, dir, ModeOff); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err %v, want a refusal", err)
	}
	if read(t, config) != in || read(t, filepath.Join(dir, "SKILL.md")) != before || exists(filepath.Join(r.ClaudeConfigDir, "settings.json")) {
		t.Fatal("a refused off still wrote something")
	}
}
