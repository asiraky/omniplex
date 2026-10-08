package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// A skill's mode is the one switch the screen shows for it. Each harness says
// it its own way, and a skill's mode is what the harnesses that see it say
// between them: off if any has it off, else manual if its files or Claude's
// settings say so, else on.
const (
	ModeOn     = "on"
	ModeManual = "manual"
	ModeOff    = "off"
)

// Claude Code's per-skill switch in settings.json. "name-only" still lets
// the model pick the skill up, so here it counts as on.
const (
	overridesKey       = "skillOverrides"
	overrideOff        = "off"
	overrideManualOnly = "user-invocable-only"
)

// policy is what the harnesses' own settings say about skills, on top of the
// skills' files.
type policy struct {
	claude  map[string]string // skill name -> skillOverrides value
	codex   map[string]bool   // SKILL.md or skill dir, symlink-resolved -> enabled
	bundled bool              // Codex loads the skills it ships with
}

func readPolicy(r Roots) policy {
	p := policy{claude: map[string]string{}, codex: map[string]bool{}, bundled: true}
	// Claude merges user, project and local settings; the later file wins.
	var files []string
	if r.ClaudeConfigDir != "" {
		files = append(files, claudeSettingsPath(r))
	}
	if r.ProjectRoot != "" {
		files = append(files,
			filepath.Join(r.ProjectRoot, ".claude", "settings.json"),
			filepath.Join(r.ProjectRoot, ".claude", "settings.local.json"))
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var settings struct {
			SkillOverrides map[string]string `json:"skillOverrides"`
		}
		if json.Unmarshal(data, &settings) != nil {
			continue
		}
		for name, v := range settings.SkillOverrides {
			p.claude[name] = v
		}
	}
	if r.CodexHome != "" {
		if data, err := os.ReadFile(codexConfigPath(r)); err == nil {
			for _, e := range parseCodexSkills(string(data)) {
				p.codex[resolve(e.path)] = e.enabled
			}
			p.bundled = codexBundled(string(data))
		}
	}
	return p
}

// resolve follows symlinks where the path exists and cleans it where not.
func resolve(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

func (p policy) mode(s *Skill) string {
	if s.Scope == ScopeSystem && !p.bundled {
		return ModeOff
	}
	override := p.claudeOverride(s)
	if override == overrideOff || p.codexOff(s) {
		return ModeOff
	}
	if override == overrideManualOnly || filesManual(s) {
		return ModeManual
	}
	return ModeOn
}

// claudeOverride is Claude's skillOverrides value for a skill Claude sees.
// Claude does not apply skillOverrides to a plugin's skills.
func (p policy) claudeOverride(s *Skill) string {
	if !containsHarness(s.Harnesses, Claude) || s.Scope == ScopePlugin {
		return ""
	}
	if v, ok := p.claude[s.Name]; ok {
		return v
	}
	return p.claude[filepath.Base(s.Dir)]
}

func (p policy) codexOff(s *Skill) bool {
	if !containsHarness(s.Harnesses, Codex) {
		return false
	}
	for _, key := range []string{resolve(filepath.Join(s.Dir, "SKILL.md")), s.Dir} {
		if enabled, ok := p.codex[key]; ok && !enabled {
			return true
		}
	}
	return false
}

// filesManual reports whether the skill's own files keep it out of the
// model's hands for every harness that sees it: Claude and pi read the
// SKILL.md frontmatter, Codex reads agents/openai.yaml. Seen by none, both
// files have to say so.
func filesManual(s *Skill) bool {
	frontmatter, openai := FileManual(s.Dir)
	if len(s.Harnesses) == 0 {
		return frontmatter && openai
	}
	for _, h := range s.Harnesses {
		if h == Codex && !openai || h != Codex && !frontmatter {
			return false
		}
	}
	return true
}

// SetMode switches an editable skill on, to manual-only or off for every
// harness. On and manual are the skill's own files, and take away any off the
// harnesses' settings hold for it; off is written into Claude's settings.json
// and Codex's config.toml, and into the files as manual, since pi has no off.
// A skill Codex ships is on or off in config.toml alone: see setBuiltinMode.
func SetMode(r Roots, dir, mode string) (Skill, error) {
	if mode != ModeOn && mode != ModeManual && mode != ModeOff {
		return Skill{}, fmt.Errorf("%w: mode must be on, manual or off", ErrInvalid)
	}
	s, err := find(r, dir)
	if err != nil {
		return Skill{}, err
	}
	if s.Scope == ScopeSystem {
		return setBuiltinMode(r, s, mode)
	}
	if !s.Editable {
		return Skill{}, ErrNotEditable
	}
	off := mode == ModeOff
	// Turning off adds a table to config.toml; refuse before anything is
	// written rather than leave the skill half off.
	if off && r.CodexHome != "" {
		if content, _, err := readThrough(codexConfigPath(r)); err == nil {
			if err := codexSkillsElsewhere(content); err != nil {
				return Skill{}, err
			}
		}
	}
	if err := SetManual(s.Dir, mode != ModeOn); err != nil {
		return Skill{}, err
	}
	if r.ClaudeConfigDir != "" {
		if err := editSettings(claudeSettingsPath(r), func(content string) (string, error) {
			return setClaudeOff(content, []string{s.Name, filepath.Base(s.Dir)}, off)
		}); err != nil {
			return Skill{}, err
		}
	}
	if r.CodexHome != "" {
		if err := editThrough(codexConfigPath(r), func(content string, _ bool) (string, error) {
			return editCodexSkill(content, s.Dir, off), nil
		}); err != nil {
			return Skill{}, err
		}
	}
	return find(r, s.Dir)
}

// setBuiltinMode switches one of the skills Codex ships on or off. Its files
// are Codex's own and read-only, and config.toml has no manual for a skill, so
// on and off are all there is, written as a [[skills.config]] table and
// nothing else.
func setBuiltinMode(r Roots, s Skill, mode string) (Skill, error) {
	if mode == ModeManual {
		return Skill{}, fmt.Errorf("%w: a built-in skill is on or off", ErrInvalid)
	}
	if r.CodexHome == "" {
		return Skill{}, errors.New("no Codex home")
	}
	off := mode == ModeOff
	if err := editThrough(codexConfigPath(r), func(content string, _ bool) (string, error) {
		if off {
			if err := codexSkillsElsewhere(content); err != nil {
				return "", err
			}
		}
		return editCodexSkill(content, s.Dir, off), nil
	}); err != nil {
		return Skill{}, err
	}
	return find(r, s.Dir)
}

// setClaudeOff writes skillOverrides[names[0]] = "off", or takes away an
// entry under any of names that turns the skill off or makes it manual. Any
// other value is the user's own and stays.
func setClaudeOff(content string, names []string, off bool) (string, error) {
	if off {
		v := jsonString(overrideOff)
		return setNestedKey(content, overridesKey, names[0], &v)
	}
	var settings struct {
		SkillOverrides map[string]json.RawMessage `json:"skillOverrides"`
	}
	if err := json.Unmarshal([]byte(content), &settings); err != nil {
		return "", err
	}
	for _, name := range names {
		var v string
		if json.Unmarshal(settings.SkillOverrides[name], &v) != nil || (v != overrideOff && v != overrideManualOnly) {
			continue
		}
		var err error
		if content, err = setNestedKey(content, overridesKey, name, nil); err != nil {
			return "", err
		}
	}
	return content, nil
}
