package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Two files in a skill say "only the user invokes this": Claude and pi read a
// frontmatter key in SKILL.md, Codex reads agents/openai.yaml. Manual writes
// both so the harnesses agree.
const (
	manualKey   = "disable-model-invocation" // SKILL.md frontmatter; true means manual
	policyKey   = "policy"                   // agents/openai.yaml
	implicitKey = "allow_implicit_invocation"
	openaiYAML  = "agents/openai.yaml"
)

// findTopKey finds an unindented `key:` line in lines[from:to]. at is -1 when
// there is none; next is the line after whatever is indented under it; value
// is the text after the colon, comment and quotes removed.
func findTopKey(lines []string, from, to int, key string) (at, next int, value string) {
	for i := from; i < to; i++ {
		line := strings.TrimRight(lines[i], "\r\n")
		if strings.TrimSpace(line) == "" || isIndented(line) || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || unquote(k) != key {
			continue
		}
		next = i + 1
		for j := i + 1; j < to; j++ {
			l := strings.TrimRight(lines[j], "\r\n")
			// A comment's indentation means nothing in YAML: one at column 0
			// does not end the block.
			if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if !isIndented(l) {
				break
			}
			next = j + 1
		}
		return i, next, unquote(stripComment(" " + v))
	}
	return -1, -1, ""
}

func unquote(v string) string {
	return strings.Trim(strings.TrimSpace(v), `"'`)
}

func indentOf(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// eolOf is the line ending the file uses, so an added line matches it.
func eolOf(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// frontmatterLines splits SKILL.md into lines that keep their endings and
// finds the closing --- of the frontmatter.
func frontmatterLines(content string) (lines []string, end int, err error) {
	lines = strings.SplitAfter(content, "\n")
	if strings.TrimRight(strings.TrimPrefix(lines[0], "\uFEFF"), " \t\r\n") != "---" {
		return nil, 0, errNoFrontmatter
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r\n") == "---" {
			return lines, i, nil
		}
	}
	return nil, 0, errUnclosedFrontmatter
}

// frontmatterManual reports whether SKILL.md keeps the skill out of the
// model's hands. A quoted "true" counts: the harnesses coerce it.
func frontmatterManual(content string) bool {
	lines, end, err := frontmatterLines(content)
	if err != nil {
		return false
	}
	at, _, value := findTopKey(lines, 1, end, manualKey)
	return at >= 0 && strings.EqualFold(value, "true")
}

// setValue swaps the value on a `key: value` line and leaves the rest of the
// line (indentation, spacing, a trailing comment, the line ending) alone.
func setValue(line, key, value string) string {
	re := regexp.MustCompile(`(?s)(["']?` + regexp.QuoteMeta(key) + `["']?\s*:)([ \t]*)([^\s#,}]*)(.*)$`)
	m := re.FindStringSubmatchIndex(line)
	if m == nil {
		return line
	}
	gap := line[m[4]:m[5]]
	if gap == "" {
		gap = " "
	}
	return line[:m[3]] + gap + value + line[m[8]:]
}

// editFrontmatterManual adds or removes the one frontmatter line. Every other
// byte of the file is kept: a skill's frontmatter is hand-written, and
// re-serialising it would reflow descriptions and drop comments.
func editFrontmatterManual(content string, manual bool) (string, error) {
	lines, end, err := frontmatterLines(content)
	if err != nil {
		if !manual {
			return content, nil // nothing there to remove
		}
		return "", err
	}
	at, next, value := findTopKey(lines, 1, end, manualKey)
	isManual := at >= 0 && strings.EqualFold(value, "true")
	switch {
	case isManual == manual:
		return content, nil
	case at < 0:
		line := manualKey + ": true" + eolOf(content)
		lines = append(lines[:end], append([]string{line}, lines[end:]...)...)
	case manual:
		lines[at] = setValue(lines[at], manualKey, "true")
		lines = append(lines[:at+1], lines[next:]...)
	default:
		lines = append(lines[:at], lines[next:]...)
	}
	return strings.Join(lines, ""), nil
}

var flowImplicitRe = regexp.MustCompile(`["']?` + implicitKey + `["']?\s*:\s*([A-Za-z]+)`)

// openaiPolicy locates policy.allow_implicit_invocation in agents/openai.yaml.
// policyAt and keyAt are line indexes, -1 when absent. A flow-style
// `policy: {…}` has both on one line.
type openaiPolicy struct {
	lines    []string
	policyAt int
	keyAt    int
	flow     bool   // policy: { ... } on one line
	inline   bool   // policy has some other inline value we cannot edit
	indent   string // of the keys under policy:
	value    string
}

func scanOpenAI(content string) openaiPolicy {
	p := openaiPolicy{lines: strings.SplitAfter(content, "\n"), policyAt: -1, keyAt: -1}
	at, next, value := findTopKey(p.lines, 0, len(p.lines), policyKey)
	if at < 0 {
		return p
	}
	p.policyAt = at
	_, after, _ := strings.Cut(p.lines[at], ":")
	if strings.HasPrefix(strings.TrimSpace(after), "{") {
		p.flow = true
		if m := flowImplicitRe.FindStringSubmatch(after); m != nil {
			p.keyAt, p.value = at, m[1]
		}
		return p
	}
	if value != "" {
		p.inline = true
		return p
	}
	for i := at + 1; i < next; i++ {
		line := strings.TrimRight(p.lines[i], "\r\n")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if p.indent == "" {
			p.indent = indentOf(line)
		}
		// Only a direct child of policy: counts, not a deeper key of the same name.
		if indentOf(line) != p.indent {
			continue
		}
		if k, v, ok := strings.Cut(trimmed, ":"); ok && unquote(k) == implicitKey {
			p.keyAt, p.value = i, unquote(stripComment(" "+v))
		}
	}
	return p
}

// openaiManual reports whether agents/openai.yaml tells Codex not to pick the
// skill up on its own.
func openaiManual(content string) bool {
	p := scanOpenAI(content)
	return p.keyAt >= 0 && strings.EqualFold(p.value, "false")
}

// editOpenAIManual sets policy.allow_implicit_invocation, in place where the
// key exists. Turning manual off only ever flips an existing false: absent
// means implicit invocation is allowed, so there is nothing to add.
func editOpenAIManual(content string, manual bool) (string, error) {
	p := scanOpenAI(content)
	isManual := p.keyAt >= 0 && strings.EqualFold(p.value, "false")
	if isManual == manual {
		return content, nil
	}
	eol := eolOf(content)
	want := strconv.FormatBool(!manual)
	switch {
	case p.keyAt >= 0:
		p.lines[p.keyAt] = setValue(p.lines[p.keyAt], implicitKey, want)
	case !manual:
		return content, nil
	case p.inline:
		return "", fmt.Errorf("%s has a policy this cannot edit", openaiYAML)
	case p.flow:
		line := p.lines[p.policyAt]
		open := strings.Index(line, "{")
		sep := ", "
		if strings.HasPrefix(strings.TrimSpace(line[open+1:]), "}") {
			sep = ""
		}
		p.lines[p.policyAt] = line[:open+1] + implicitKey + ": " + want + sep + line[open+1:]
	case p.policyAt >= 0:
		indent := p.indent
		if indent == "" {
			indent = "  "
		}
		if !strings.HasSuffix(p.lines[p.policyAt], "\n") {
			p.lines[p.policyAt] += eol
		}
		child := indent + implicitKey + ": " + want + eol
		at := p.policyAt + 1
		p.lines = append(p.lines[:at], append([]string{child}, p.lines[at:]...)...)
	default:
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += eol
		}
		return content + policyKey + ":" + eol + "  " + implicitKey + ": " + want + eol, nil
	}
	return strings.Join(p.lines, ""), nil
}

// FileManual reports what the skill's own files say, one answer per
// mechanism: the SKILL.md frontmatter key (Claude and pi) and
// agents/openai.yaml (Codex). dir need not be a discovered skill, so a staged
// copy can be asked too.
func FileManual(dir string) (frontmatter, openai bool) {
	if data, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err == nil {
		frontmatter = frontmatterManual(string(data))
	}
	if data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(openaiYAML))); err == nil {
		openai = openaiManual(string(data))
	}
	return frontmatter, openai
}

// createdOpenAI is agents/openai.yaml as SetManual creates it.
const createdOpenAI = policyKey + ":\n  " + implicitKey + ": false\n"

// SetManual writes the manual-only choice into both of the skill's files.
// agents/openai.yaml is created only to turn manual on. Both edits are worked
// out before either file is written, so a refusal leaves the skill as it was.
func SetManual(dir string, manual bool) error {
	skillPath, err := filepath.EvalSymlinks(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return err
	}
	skill, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}
	nextSkill, err := editFrontmatterManual(string(skill), manual)
	if err != nil {
		return fmt.Errorf("%w: SKILL.md: %v", ErrInvalid, err)
	}

	yamlPath := filepath.Join(dir, filepath.FromSlash(openaiYAML))
	var yaml, nextYAML string
	drop := false
	data, err := os.ReadFile(yamlPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if manual {
			nextYAML = createdOpenAI
		}
	case err != nil:
		return err
	case !manual && string(data) == createdOpenAI:
		// The file as turning manual on made it: turning manual off takes
		// it away again, so the skill goes back to what it was.
		yaml, drop = string(data), true
	default:
		yaml = string(data)
		if nextYAML, err = editOpenAIManual(yaml, manual); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if yamlPath, err = filepath.EvalSymlinks(yamlPath); err != nil {
			return err
		}
	}

	if nextSkill != string(skill) {
		if err := writeAtomic(skillPath, []byte(nextSkill)); err != nil {
			return err
		}
	}
	if drop {
		if err := os.Remove(yamlPath); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(yamlPath)) // agents/, when nothing else is in it
		return nil
	}
	if nextYAML != yaml {
		if err := os.MkdirAll(filepath.Dir(yamlPath), 0o755); err != nil {
			return err
		}
		return writeAtomic(yamlPath, []byte(nextYAML))
	}
	return nil
}
