package skills

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func builtinModes(list []ClaudeBuiltin) map[string]string {
	out := map[string]string{}
	for _, b := range list {
		out[b.Name] = b.Mode
	}
	return out
}

func TestClaudeBuiltinsIsTheCLIsListTheExtrasAndTheOffsOfNoSkillOfOurs(t *testing.T) {
	r := fixture(t)
	write(t, claudeSettingsPath(r), `{"skillOverrides": {"simplify": "off", "loop": "user-invocable-only", "gone-now": "off", "shared": "off"}}`)
	got := ClaudeBuiltins(r, []string{"simplify", "loop", "schedule"}, mustDiscover(t, r))
	want := map[string]string{
		"gone-now":         ModeOff,
		"keybindings-help": ModeOn,
		"loop":             ModeManual,
		"schedule":         ModeOn,
		"security-review":  ModeOn,
		"simplify":         ModeOff,
	}
	if !reflect.DeepEqual(builtinModes(got), want) {
		t.Fatalf("built-ins = %v, want %v", builtinModes(got), want)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Name > got[i].Name {
			t.Fatalf("not sorted by name: %v", got)
		}
	}
}

func TestTheGroupSwitchTurnsEveryBuiltinOffAndBackToWhatEachWas(t *testing.T) {
	r := machine(t)
	if _, err := SetClaudeBuiltin(r, "simplify", false); err != nil {
		t.Fatal(err)
	}
	if err := SetClaudeBundled(r, false); err != nil {
		t.Fatal(err)
	}
	if ClaudeBundled(r) {
		t.Fatal("the group is still on")
	}
	for name, mode := range builtinModes(ClaudeBuiltins(r, []string{"simplify", "loop"}, nil)) {
		if mode != ModeOff {
			t.Errorf("%s is %s with the group off", name, mode)
		}
	}
	if err := SetClaudeBundled(r, true); err != nil {
		t.Fatal(err)
	}
	modes := builtinModes(ClaudeBuiltins(r, []string{"simplify", "loop"}, nil))
	if modes["simplify"] != ModeOff || modes["loop"] != ModeOn {
		t.Fatalf("after the group came back: %v", modes)
	}
	if got, want := read(t, claudeSettingsPath(r)), "{\n  \"skillOverrides\": {\n    \"simplify\": \"off\"\n  }\n}\n"; got != want {
		t.Fatalf("settings:\n%s\nwant:\n%s", got, want)
	}
}

func TestABuiltinSwitchedOffAndOnLeavesTheSettingsAsTheyWere(t *testing.T) {
	r := machine(t)
	in := "{\n  \"model\": \"opus\",\n  \"skillOverrides\": {\n    \"mine\": \"name-only\"\n  }\n}\n"
	write(t, claudeSettingsPath(r), in)
	b, err := SetClaudeBuiltin(r, "security-review", false)
	if err != nil || b.Mode != ModeOff {
		t.Fatalf("off: %v %v", b, err)
	}
	b, err = SetClaudeBuiltin(r, "security-review", true)
	if err != nil || b.Mode != ModeOn {
		t.Fatalf("on: %v %v", b, err)
	}
	if got := read(t, claudeSettingsPath(r)); got != in {
		t.Fatalf("settings:\n%s\nwant:\n%s", got, in)
	}
	if _, err := SetClaudeBuiltin(r, " ", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank name err = %v", err)
	}
}

func TestACodexBuiltinIsSwitchedInConfigTomlAlone(t *testing.T) {
	r := fixture(t)
	sys := byName(t, mustDiscover(t, r))["sys-skill"]
	skillFile := filepath.Join(sys.Dir, "SKILL.md")
	before := read(t, skillFile)
	config := filepath.Join(r.CodexHome, "config.toml")
	write(t, config, "model = \"gpt\"\n")

	s, err := SetMode(r, sys.Dir, ModeOff)
	if err != nil || s.Mode != ModeOff {
		t.Fatalf("off: %v %v", s.Mode, err)
	}
	if got, want := read(t, config), "model = \"gpt\"\n\n"+codexEntry(skillFile, "false"); got != want {
		t.Fatalf("config.toml:\n%s\nwant:\n%s", got, want)
	}
	if _, err := SetMode(r, sys.Dir, ModeManual); !errors.Is(err, ErrInvalid) {
		t.Fatalf("manual err = %v", err)
	}
	s, err = SetMode(r, sys.Dir, ModeOn)
	if err != nil || s.Mode != ModeOn {
		t.Fatalf("on: %v %v", s.Mode, err)
	}
	if got := read(t, config); got != "model = \"gpt\"\n" {
		t.Fatalf("config.toml after on:\n%s", got)
	}
	if read(t, skillFile) != before || exists(filepath.Join(sys.Dir, "agents")) || exists(claudeSettingsPath(r)) {
		t.Fatal("switching a built-in wrote outside config.toml")
	}
}

type fakeCLI struct {
	version string
	answer  []string
	err     error
	asked   int
}

func (f *fakeCLI) probe(cache string) *BundledProbe {
	p := NewBundledProbe(cache, func() (string, bool) { return "/bin/claude", true })
	p.version = func(context.Context, string) (string, error) { return f.version, nil }
	p.query = func(context.Context, string) ([]string, error) {
		f.asked++
		return f.answer, f.err
	}
	return p
}

func TestTheProbeAsksOncePerVersionAndKeepsItsLastAnswer(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "data", "claude-bundled-skills.json")
	cli := &fakeCLI{version: "2.1.283", answer: []string{"simplify", "loop"}}
	p := cli.probe(cache)
	if p.Names() != nil {
		t.Fatal("names before any answer")
	}
	for range 2 {
		if err := p.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if cli.asked != 1 || !reflect.DeepEqual(p.Names(), []string{"simplify", "loop"}) {
		t.Fatalf("asked %d times, names %v", cli.asked, p.Names())
	}

	// A new server reads the answer back without asking.
	again := cli.probe(cache)
	if err := again.Refresh(context.Background()); err != nil || cli.asked != 1 {
		t.Fatalf("a restart asked again: %d %v", cli.asked, err)
	}

	// A new version that cannot be asked keeps the old answer, and is asked
	// again next time.
	cli.version, cli.err = "2.1.284", errors.New("not signed in")
	if err := again.Refresh(context.Background()); err == nil {
		t.Fatal("a failed query was not reported")
	}
	if !reflect.DeepEqual(again.Names(), []string{"simplify", "loop"}) {
		t.Fatalf("names after a failure: %v", again.Names())
	}
	cli.answer, cli.err = []string{"simplify"}, nil
	if err := again.Refresh(context.Background()); err != nil || cli.asked != 3 {
		t.Fatalf("asked %d, err %v", cli.asked, err)
	}
	if !reflect.DeepEqual(again.Names(), []string{"simplify"}) {
		t.Fatalf("names after the new version: %v", again.Names())
	}
}

func TestTheProbeDoesNothingWithoutAClaude(t *testing.T) {
	p := NewBundledProbe(filepath.Join(t.TempDir(), "c.json"), func() (string, bool) { return "", false })
	p.version = func(context.Context, string) (string, error) { t.Fatal("ran a CLI that is not there"); return "", nil }
	if err := p.Refresh(context.Background()); err != nil || p.Names() != nil {
		t.Fatalf("%v %v", err, p.Names())
	}
	var none *BundledProbe
	if none.Names() != nil {
		t.Fatal("a nil probe has names")
	}
}
