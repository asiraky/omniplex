package skills

import (
	"path/filepath"
	"testing"
)

func TestEditCodexSkill(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "real", "s")
	write(t, filepath.Join(dir, "SKILL.md"), skillMD("s", "d"))
	link(t, "real", filepath.Join(base, "via"))
	md := dir + "/SKILL.md"
	other := "[[skills.config]]\npath = \"/x/other/SKILL.md\"\nenabled = false\n"

	tests := []struct {
		name, in string
		off      bool
		want     string
	}{
		{"off adds a table to an empty file", "", true,
			"[[skills.config]]\npath = \"" + md + "\"\nenabled = false\n"},
		{"off adds a table after a blank line", "model = \"x\"", true,
			"model = \"x\"\n\n[[skills.config]]\npath = \"" + md + "\"\nenabled = false\n"},
		{"off keeps the file's line endings", "model = \"x\"\r\n", true,
			"model = \"x\"\r\n\r\n[[skills.config]]\r\npath = \"" + md + "\"\r\nenabled = false\r\n"},
		{"off sets enabled in the table that names it, comment kept", "[[skills.config]]\npath = '" + md + "'\nenabled = true # mine\n[x]\n", true,
			"[[skills.config]]\npath = '" + md + "'\nenabled = false # mine\n[x]\n"},
		{"off adds enabled to a table that has none", "[[skills.config]]\npath = \"" + dir + "\"\n\n[x]\n", true,
			"[[skills.config]]\npath = \"" + dir + "\"\nenabled = false\n\n[x]\n"},
		{"off finds the table through a symlinked path", "[[skills.config]]\npath = \"" + base + "/via/s/SKILL.md\"\nenabled = true\n", true,
			"[[skills.config]]\npath = \"" + base + "/via/s/SKILL.md\"\nenabled = false\n"},
		{"off leaves another skill's table", other, true,
			other + "\n[[skills.config]]\npath = \"" + md + "\"\nenabled = false\n"},
		{"on takes out only this skill's tables", other + "\n[[skills.config]]\npath = \"" + md + "\"\nenabled = false\n\n[[ skills.config ]]\npath = \"" + base + "/via/s\"\n", false,
			other},
		{"on with nothing about the skill changes nothing", other, false, other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := editCodexSkill(tt.in, dir, tt.off); got != tt.want {
				t.Fatalf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}

	t.Run("a path that needs quoting reads back", func(t *testing.T) {
		odd := filepath.Join(base, `we"ird\s`)
		got := parseCodexSkills(editCodexSkill("", odd, true))
		if len(got) != 1 || got[0] != (codexSkill{odd + "/SKILL.md", false}) {
			t.Fatalf("read back %+v", got)
		}
		if editCodexSkill(editCodexSkill("", odd, true), odd, false) != "" {
			t.Fatal("on did not find the table off wrote")
		}
	})
}

func TestCodexBundled(t *testing.T) {
	for in, want := range map[string]bool{
		"":                                    true,
		"model = \"x\"\n":                     true,
		"[skills.bundled]\nenabled = false\n": false,
		"[skills.bundled]\nenabled = false # no\n": false,
		"[skills.bundled]\nenabled = true\n":       true,
		"[skills.bundled]\nenabled = \"false\"\n":  true,
		"[skills]\nenabled = false\n":              true,
		"[ skills.bundled ]\nenabled=false\n":      false,
	} {
		if got := codexBundled(in); got != want {
			t.Errorf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestEditCodexBundled(t *testing.T) {
	tests := []struct {
		name, in, off string
	}{
		{"an empty file", "", "[skills.bundled]\nenabled = false\n"},
		{"other content", "model = \"x\"\n\n[mcp]\na = 1 # c\n", "model = \"x\"\n\n[mcp]\na = 1 # c\n\n[skills.bundled]\nenabled = false\n"},
		{"a table with other keys", "[skills.bundled]\nfoo = 1\n\n[x]\n", "[skills.bundled]\nenabled = false\nfoo = 1\n\n[x]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off := editCodexBundled(tt.in, false)
			if off != tt.off {
				t.Fatalf("off:\n%q\nwant\n%q", off, tt.off)
			}
			if codexBundled(off) {
				t.Fatal("still on after off")
			}
			if again := editCodexBundled(off, false); again != off {
				t.Fatalf("off twice changed the file: %q", again)
			}
			if on := editCodexBundled(off, true); on != tt.in {
				t.Fatalf("on again:\n%q\nwant\n%q", on, tt.in)
			}
		})
	}

	t.Run("off over enabled = true flips the value", func(t *testing.T) {
		in := "[skills.bundled]\nenabled = true # x\n"
		if got := editCodexBundled(in, false); got != "[skills.bundled]\nenabled = false # x\n" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("on leaves a table that does not say false", func(t *testing.T) {
		in := "[skills.bundled]\nenabled = true\n"
		if got := editCodexBundled(in, true); got != in {
			t.Fatalf("got %q", got)
		}
	})
}

func TestSetCodexBundledWritesThroughALink(t *testing.T) {
	r := machine(t)
	real := filepath.Join(r.Home, "dotfiles", "codex.toml")
	write(t, real, codexConfig)
	link(t, real, filepath.Join(r.CodexHome, "config.toml"))
	if err := SetCodexBundled(r, false); err != nil {
		t.Fatal(err)
	}
	if !isSymlink(filepath.Join(r.CodexHome, "config.toml")) || CodexBundled(r) {
		t.Fatal("the link was replaced, or the switch did not take")
	}
	if err := SetCodexBundled(r, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, real); got != codexConfig {
		t.Fatalf("after the round trip:\n%q", got)
	}
}

func TestSetCodexBundledMakesNoFileToSayOn(t *testing.T) {
	r := machine(t)
	if err := SetCodexBundled(r, true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(r.CodexHome, "config.toml")) {
		t.Fatal("turning on created config.toml")
	}
}
