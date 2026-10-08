package skills

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// omniplex's bundled skill is listed for every harness, read-only, and
// nothing here moves, links, changes or removes it.
func TestBundledSkillIsListedReadOnly(t *testing.T) {
	r := machine(t)
	r.Bundled = filepath.Join(filepath.Dir(r.Home), "data", "plugin", "skills")
	write(t, filepath.Join(r.Bundled, "omniplex", "SKILL.md"), skillMD("omniplex", "Manage MCP servers through Omniplex"))
	write(t, filepath.Join(r.Library, "mine", "SKILL.md"), skillMD("mine", "Mine"))
	// A user override for a skill of the same name in Claude's settings
	// does not reach a plugin's skill.
	write(t, filepath.Join(r.ClaudeConfigDir, "settings.json"), `{"skillOverrides":{"omniplex":"off"}}`)

	list := mustDiscover(t, r)
	s, ok := byName(t, list)["omniplex"]
	if !ok {
		t.Fatalf("bundled skill not listed: %v", list)
	}
	if s.Scope != ScopeOmniplex || s.Editable || s.Mode != ModeOn {
		t.Fatalf("scope/editable/mode = %q/%v/%q", s.Scope, s.Editable, s.Mode)
	}
	if !reflect.DeepEqual(s.Harnesses, []Harness{Claude, Codex, Pi}) {
		t.Fatalf("harnesses = %v", s.Harnesses)
	}
	if list[len(list)-1].Name != "omniplex" {
		t.Fatalf("bundled skill sorts before the user's own: %v", list)
	}

	before := read(t, filepath.Join(s.Dir, "SKILL.md"))
	for _, mode := range []string{ModeOff, ModeManual} {
		if _, err := SetMode(r, s.Dir, mode); !errors.Is(err, ErrNotEditable) {
			t.Fatalf("SetMode %s = %v, want ErrNotEditable", mode, err)
		}
	}
	if err := Remove(r, s.Dir); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("Remove = %v, want ErrNotEditable", err)
	}
	if err := Save(r, s.Dir, skillMD("omniplex", "changed")); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("Save = %v, want ErrNotEditable", err)
	}
	if read(t, filepath.Join(s.Dir, "SKILL.md")) != before {
		t.Fatal("the bundled skill was changed")
	}
	// Creating a skill links the library into each harness; the bundled
	// skill never comes along.
	if _, err := Create(r, "another", "Another", ""); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{r.Library, filepath.Join(r.ClaudeConfigDir, "skills"), filepath.Join(r.CodexHome, "skills"), filepath.Join(r.PiAgentDir, "skills")} {
		if _, err := os.Lstat(filepath.Join(dir, "omniplex")); err == nil {
			t.Fatalf("bundled skill reached %s", dir)
		}
	}
}
