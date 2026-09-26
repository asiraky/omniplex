package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Subagent is a custom agent definition a harness loads: a Claude .md file or
// a Codex .toml file. pi has none.
type Subagent struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Path        string  `json:"path"`
	Scope       string  `json:"scope"`
	Harness     Harness `json:"harness"`
}

// DiscoverSubagents lists Claude agents (<cfg>/agents and
// <project>/.claude/agents, recursive) and Codex agents (<codex>/agents and
// <project>/.codex/agents). Project first, then user; by name.
func DiscoverSubagents(r Roots) ([]Subagent, error) {
	type agentRoot struct {
		path    string
		scope   string
		harness Harness
	}
	var roots []agentRoot
	if r.ProjectRoot != "" {
		roots = append(roots,
			agentRoot{filepath.Join(r.ProjectRoot, ".claude", "agents"), ScopeProject, Claude},
			agentRoot{filepath.Join(r.ProjectRoot, ".codex", "agents"), ScopeProject, Codex},
		)
	}
	if r.ClaudeConfigDir != "" {
		roots = append(roots, agentRoot{filepath.Join(r.ClaudeConfigDir, "agents"), ScopeUser, Claude})
	}
	if r.CodexHome != "" {
		roots = append(roots, agentRoot{filepath.Join(r.CodexHome, "agents"), ScopeUser, Codex})
	}

	seen := map[string]bool{}
	out := []Subagent{}
	for _, rt := range roots {
		// WalkDir does not follow a symlinked root, and agents dirs are
		// commonly symlinks into dotfiles.
		real, err := filepath.EvalSymlinks(rt.path)
		if err != nil {
			continue
		}
		_ = filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != real && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				// Codex reads only the top level.
				if path != real && rt.harness == Codex {
					return fs.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if (rt.harness == Claude && ext != ".md") || (rt.harness == Codex && ext != ".toml") {
				return nil
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || seen[resolved] {
				return nil
			}
			seen[resolved] = true
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(real, path)
			a := Subagent{Path: filepath.Join(rt.path, rel), Scope: rt.scope, Harness: rt.harness}
			var fields map[string]string
			if rt.harness == Claude {
				fields, _, _ = parseFrontmatter(string(data))
			} else {
				fields = parseTOMLTop(string(data))
			}
			a.Name = strings.TrimSpace(fields["name"])
			if a.Name == "" {
				a.Name = strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			}
			a.Description = strings.TrimSpace(fields["description"])
			out = append(out, a)
			return nil
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := scopeRank(out[i].Scope), scopeRank(out[j].Scope); a != b {
			return a < b
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// parseTOMLTop reads the top-level string keys of a TOML file, up to the first
// table header: basic and literal strings, single- or multi-line. Anything
// else is skipped.
func parseTOMLTop(content string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if strings.HasPrefix(line, "[") {
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		value = strings.TrimSpace(value)
		for _, delim := range []string{`"""`, `'''`} {
			if !strings.HasPrefix(value, delim) {
				continue
			}
			rest := strings.TrimPrefix(value, delim)
			var b strings.Builder
			for {
				if end := strings.Index(rest, delim); end >= 0 {
					b.WriteString(rest[:end])
					break
				}
				b.WriteString(rest)
				i++
				if i >= len(lines) {
					break
				}
				b.WriteString("\n")
				rest = strings.TrimRight(lines[i], "\r")
			}
			v := strings.TrimPrefix(b.String(), "\n")
			if delim == `"""` {
				if u, err := strconv.Unquote(`"` + strings.ReplaceAll(strings.ReplaceAll(v, `"`, `\"`), "\n", `\n`) + `"`); err == nil {
					v = u
				}
			}
			out[key] = v
			value = ""
			break
		}
		switch {
		case value == "":
		case strings.HasPrefix(value, `"`):
			out[key] = doubleQuoted(value)
		case strings.HasPrefix(value, "'"):
			out[key] = singleQuoted(value)
		}
	}
	return out
}
