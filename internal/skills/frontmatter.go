package skills

import (
	"errors"
	"strconv"
	"strings"
)

var (
	errNoFrontmatter       = errors.New("no frontmatter")
	errUnclosedFrontmatter = errors.New("frontmatter is not closed with ---")
)

// parseFrontmatter reads the YAML subset SKILL.md files use in practice:
// top-level `key: value` pairs, values plain, single- or double-quoted, or a
// `>` / `|` block scalar on the following indented lines. Nested maps
// (metadata:, hooks:) are skipped rather than parsed; only scalar top-level
// keys come back. The body is everything after the closing ---.
func parseFrontmatter(content string) (map[string]string, string, error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\r") != "---" {
		return nil, content, errNoFrontmatter
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, content, errUnclosedFrontmatter
	}
	body := strings.Join(lines[end+1:], "\n")
	fields := make(map[string]string)
	block := lines[1:end]
	for i := 0; i < len(block); i++ {
		line := strings.TrimRight(block[i], "\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") || isIndented(line) {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Everything indented under this key belongs to it.
		j := i + 1
		for j < len(block) && (strings.TrimSpace(block[j]) == "" || isIndented(block[j])) {
			j++
		}
		cont := block[i+1 : j]
		i = j - 1
		switch {
		case strings.HasPrefix(value, ">") || strings.HasPrefix(value, "|"):
			fields[key] = blockScalar(value, cont)
		case strings.HasPrefix(value, `"`):
			fields[key] = doubleQuoted(joinFolded(value, cont))
		case strings.HasPrefix(value, "'"):
			fields[key] = singleQuoted(joinFolded(value, cont))
		case value == "":
			// A nested map or a list: not a scalar this package reads.
		default:
			fields[key] = stripComment(joinFolded(value, cont))
		}
	}
	return fields, body, nil
}

func isIndented(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

// joinFolded folds a flow scalar's continuation lines the YAML way: a single
// newline becomes a space.
func joinFolded(first string, cont []string) string {
	parts := []string{first}
	for _, line := range cont {
		if t := strings.TrimSpace(line); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func blockScalar(indicator string, cont []string) string {
	literal := strings.HasPrefix(indicator, "|")
	keep := strings.Contains(indicator, "+")
	strip := strings.Contains(indicator, "-")
	indent := -1
	var lines []string
	for _, raw := range cont {
		raw = strings.TrimRight(raw, "\r")
		if strings.TrimSpace(raw) == "" {
			lines = append(lines, "")
			continue
		}
		n := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
		lines = append(lines, raw)
	}
	for i, line := range lines {
		if len(line) >= indent && indent > 0 {
			lines[i] = line[indent:]
		}
	}
	// Trailing blanks belong to chomping, not to the text.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out string
	if literal {
		out = strings.Join(lines, "\n")
	} else {
		var b strings.Builder
		for i, line := range lines {
			// A blank line is a newline; a break between two text lines folds
			// to a space.
			switch {
			case line == "":
				b.WriteString("\n")
			case i > 0 && lines[i-1] != "":
				b.WriteString(" ")
			}
			b.WriteString(line)
		}
		out = b.String()
	}
	if !strip && out != "" && keep {
		out += "\n"
	}
	return out
}

func doubleQuoted(v string) string {
	if end := strings.LastIndex(v, `"`); end > 0 {
		v = v[:end+1]
	}
	if s, err := strconv.Unquote(v); err == nil {
		return s
	}
	return strings.Trim(v, `"`)
}

func singleQuoted(v string) string {
	if end := strings.LastIndex(v, "'"); end > 0 {
		v = v[1:end]
	} else {
		v = strings.TrimPrefix(v, "'")
	}
	return strings.ReplaceAll(v, "''", "'")
}

func stripComment(v string) string {
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// yamlScalar renders a value for a frontmatter line: plain when YAML would
// read it back unchanged, double-quoted otherwise.
func yamlScalar(v string) string {
	plain := v != "" &&
		!strings.ContainsAny(v[:1], `!&*-?|>'"%@`+"`#[]{},") &&
		!strings.Contains(v, ": ") &&
		!strings.Contains(v, " #") &&
		!strings.ContainsAny(v, "\n\r\t") &&
		strings.TrimSpace(v) == v &&
		!strings.HasSuffix(v, ":")
	if plain {
		return v
	}
	return strconv.Quote(v)
}
