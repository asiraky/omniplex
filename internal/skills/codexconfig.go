package skills

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// Codex's config.toml carries two skill switches: a [[skills.config]] table
// per skill path with enabled = false, and [skills.bundled] enabled = false for
// the skills Codex ships. Both are read and written as text. Nothing else in
// the file is understood, which saves a TOML dependency and leaves every other
// byte of a hand-written file where it was.
const (
	skillTable   = "[[skills.config]]"
	bundledTable = "[skills.bundled]"
)

func codexConfigPath(r Roots) string {
	return filepath.Join(r.CodexHome, "config.toml")
}

// tomlTable is one table of a TOML file, by line index: at is its header,
// end the line after its last key. Comments and blank lines after the last key
// are left to whatever follows.
type tomlTable struct {
	header string // with spaces and quotes taken out: "[[skills.config]]"
	at     int
	end    int
	keys   []tomlKey
}

type tomlKey struct {
	name  string
	value string // as written, comment and all
	at    int
}

func (t tomlTable) key(name string) (tomlKey, bool) {
	for _, k := range t.keys {
		if k.name == name {
			return k, true
		}
	}
	return tomlKey{}, false
}

// boolValue reads a key's value as TOML would: only the bare word false is
// false here, which is what both switches need.
func (k tomlKey) isFalse() bool {
	bare, _, _ := strings.Cut(k.value, "#")
	return strings.TrimSpace(bare) == "false"
}

// tomlLines splits a file into lines that keep their endings.
func tomlLines(content string) []string {
	return strings.SplitAfter(content, "\n")
}

func scanTOML(lines []string) []tomlTable {
	var out []tomlTable
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			header, _, _ := strings.Cut(line, "#")
			header = strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(header)
			out = append(out, tomlTable{header: header, at: i, end: i + 1})
			continue
		}
		if len(out) == 0 {
			continue // a top-level key
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		t := &out[len(out)-1]
		t.keys = append(t.keys, tomlKey{name: unquote(key), value: strings.TrimSpace(value), at: i})
		t.end = i + 1
	}
	return out
}

func tablesNamed(lines []string, header string) []tomlTable {
	var out []tomlTable
	for _, t := range scanTOML(lines) {
		if t.header == header {
			out = append(out, t)
		}
	}
	return out
}

type codexSkill struct {
	path    string
	enabled bool
}

// parseCodexSkills reads the [[skills.config]] tables of Codex's config.toml:
// a path and an enabled flag each.
func parseCodexSkills(content string) []codexSkill {
	var out []codexSkill
	for _, t := range tablesNamed(tomlLines(content), skillTable) {
		path, ok := t.key("path")
		if !ok {
			continue
		}
		e := codexSkill{path: tomlString(path.value), enabled: true}
		if enabled, ok := t.key("enabled"); ok {
			e.enabled = !enabled.isFalse()
		}
		if e.path != "" {
			out = append(out, e)
		}
	}
	return out
}

// tomlString reads a basic or literal string value, ignoring what follows it.
func tomlString(v string) string {
	switch {
	case strings.HasPrefix(v, "'"):
		if end := strings.Index(v[1:], "'"); end >= 0 {
			return v[1 : end+1]
		}
	case strings.HasPrefix(v, `"`):
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == '"' {
				if s, err := strconv.Unquote(v[:i+1]); err == nil {
					return s
				}
				return v[1:i]
			}
		}
	}
	return ""
}

func tomlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// setTOMLValue swaps the value on a `key = value` line, keeping the rest of
// the line (spacing, a trailing comment, the line ending).
func setTOMLValue(line, value string) string {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line
	}
	rest := line[eq+1:]
	gap := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
	old := strings.TrimLeft(rest, " \t")
	end := strings.IndexAny(old, " \t#\r\n")
	if end < 0 {
		end = len(old)
	}
	if gap == "" {
		gap = " "
	}
	return line[:eq+1] + gap + value + old[end:]
}

// insertLine puts line in at index at, making sure the line before it ends.
func insertLine(lines []string, at int, line, eol string) []string {
	if at > 0 && !strings.HasSuffix(lines[at-1], "\n") {
		lines[at-1] += eol
	}
	return append(lines[:at], append([]string{line + eol}, lines[at:]...)...)
}

// dropTable takes a table out, header to last key, with one blank line beside
// it: the one appendTable put before it, so adding a table and taking it away
// again leaves the file as it was.
func dropTable(lines []string, t tomlTable) []string {
	lines = append(lines[:t.at], lines[t.end:]...)
	blank := func(i int) bool { return i >= 0 && i < len(lines) && strings.TrimSpace(lines[i]) == "" }
	switch {
	case blank(t.at - 1):
		lines = append(lines[:t.at-1], lines[t.at:]...)
	case blank(t.at):
		lines = append(lines[:t.at], lines[t.at+1:]...)
	}
	return lines
}

// appendTable adds a table at the end of the file, after a blank line.
func appendTable(content string, lines ...string) string {
	eol := eolOf(content)
	if content != "" {
		if !strings.HasSuffix(content, "\n") {
			content += eol
		}
		content += eol
	}
	return content + strings.Join(lines, eol) + eol
}

// codexSkillMatches reports whether a [[skills.config]] path names the skill
// in dir: its SKILL.md or the folder, through any symlinks.
func codexSkillMatches(path, dir string) bool {
	if path == "" {
		return false
	}
	p := resolve(path)
	return p == dir || p == resolve(filepath.Join(dir, "SKILL.md"))
}

// editCodexSkill turns a skill off in config.toml, or takes away whatever
// the file says about it. Off sets enabled = false in a table that already
// names the skill rather than adding a second.
func editCodexSkill(content, dir string, off bool) string {
	lines := tomlLines(content)
	var mine []tomlTable
	for _, t := range tablesNamed(lines, skillTable) {
		if path, ok := t.key("path"); ok && codexSkillMatches(tomlString(path.value), dir) {
			mine = append(mine, t)
		}
	}
	if off && len(mine) == 0 {
		return appendTable(content, skillTable, "path = "+tomlQuote(filepath.Join(dir, "SKILL.md")), "enabled = false")
	}
	// Last first, so the line numbers of the ones before stay good.
	for i := len(mine) - 1; i >= 0; i-- {
		t := mine[i]
		switch enabled, ok := t.key("enabled"); {
		case !off:
			lines = dropTable(lines, t)
		case ok:
			lines[enabled.at] = setTOMLValue(lines[enabled.at], "false")
		default:
			lines = insertLine(lines, t.end, "enabled = false", eolOf(content))
		}
	}
	return strings.Join(lines, "")
}

// CodexBundled reports whether Codex loads the skills it ships with: on
// unless [skills.bundled] says enabled = false.
func CodexBundled(r Roots) bool {
	if r.CodexHome == "" {
		return true
	}
	content, _, err := readThrough(codexConfigPath(r))
	if err != nil {
		return true
	}
	return codexBundled(content)
}

func codexBundled(content string) bool {
	on := true
	for _, t := range tablesNamed(tomlLines(content), bundledTable) {
		if enabled, ok := t.key("enabled"); ok {
			on = !enabled.isFalse()
		}
	}
	return on
}

// editCodexBundled writes enabled = false under [skills.bundled], adding the
// table when there is none, or takes that line out again, and the table with
// it when nothing else is left in it.
func editCodexBundled(content string, on bool) string {
	lines := tomlLines(content)
	tables := tablesNamed(lines, bundledTable)
	if !on && len(tables) == 0 {
		return appendTable(content, bundledTable, "enabled = false")
	}
	for i := len(tables) - 1; i >= 0; i-- {
		t := tables[i]
		enabled, ok := t.key("enabled")
		switch {
		case !on && ok:
			lines[enabled.at] = setTOMLValue(lines[enabled.at], "false")
		case !on:
			lines = insertLine(lines, t.at+1, "enabled = false", eolOf(content))
		case !ok || !enabled.isFalse():
		case len(t.keys) == 1:
			lines = dropTable(lines, t)
		default:
			lines = append(lines[:enabled.at], lines[enabled.at+1:]...)
		}
		if !on {
			break // one table to say it is enough
		}
	}
	return strings.Join(lines, "")
}

// SetCodexBundled turns the skills Codex ships with on or off.
func SetCodexBundled(r Roots, on bool) error {
	if r.CodexHome == "" {
		return errors.New("no Codex home")
	}
	return editThrough(codexConfigPath(r), func(content string, _ bool) (string, error) {
		return editCodexBundled(content, on), nil
	})
}
