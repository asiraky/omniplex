// Package skills finds the Agent Skills each harness can see on disk, and
// reads, edits and creates them.
//
// Every harness has its own roots (see the table in Discover). One physical
// skill is often reachable through several of them via symlinks, so a skill's
// identity is its symlink-resolved directory, and the harnesses that can see
// it are the union over every path that reaches it.
package skills

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Harness string

const (
	Claude Harness = "claude"
	Codex  Harness = "codex"
	Pi     Harness = "pi"
)

const (
	ScopeProject = "project"
	ScopeUser    = "user"
	ScopePlugin  = "plugin"
	ScopeSystem  = "system"
)

var (
	ErrNotFound    = errors.New("skill not found")
	ErrNotEditable = errors.New("skill is not editable")
	ErrInvalid     = errors.New("invalid skill")
)

// Roots are the directories discovery starts from. Build them with
// DefaultRoots unless a test needs something odd.
type Roots struct {
	Home            string
	ClaudeConfigDir string // CLAUDE_CONFIG_DIR, else ~/.claude
	CodexHome       string // CODEX_HOME, else ~/.codex
	PiAgentDir      string // PI_CODING_AGENT_DIR, else ~/.pi/agent
	ProjectRoot     string // "" when there is no project

	// Library is the one personal directory Omniplex writes skills into, and
	// ProjectLibrary the one inside the project ("" when there is no project).
	// Both absolute. A harness reaches a library through its own roots or
	// through symlinks; nothing is ever installed into a harness dir.
	Library        string
	ProjectLibrary string
	// CLIVersion is the `skills` npm package version a fetch runs.
	CLIVersion string
	// CLILock is the skills CLI's own global lock file, read for the
	// provenance of skills installed from a terminal.
	CLILock string
}

// What a machine with no skills settings gets. The library is where the
// skills CLI and two of the three harnesses already look.
const (
	DefaultLibrary        = ".agents/skills" // under the home folder
	DefaultProjectLibrary = ".agents/skills" // under the project root
	DefaultCLIVersion     = "1.7.0"
)

// DefaultRoots resolves the per-harness config dirs the way the harnesses do.
// env is an overlay (a provider instance's env): a key present there wins,
// even when empty; otherwise the process environment is consulted.
func DefaultRoots(home string, env map[string]string, projectRoot string) Roots {
	dir := func(key string, fallback ...string) string {
		v, ok := env[key]
		if !ok {
			v = os.Getenv(key)
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return filepath.Join(append([]string{home}, fallback...)...)
		}
		if v == "~" {
			return home
		}
		if strings.HasPrefix(v, "~/") {
			return filepath.Join(home, v[2:])
		}
		if !filepath.IsAbs(v) {
			return filepath.Join(home, v)
		}
		return filepath.Clean(v)
	}
	r := Roots{
		Home:            home,
		ClaudeConfigDir: dir("CLAUDE_CONFIG_DIR", ".claude"),
		CodexHome:       dir("CODEX_HOME", ".codex"),
		PiAgentDir:      dir("PI_CODING_AGENT_DIR", ".pi", "agent"),
		Library:         filepath.Join(home, filepath.FromSlash(DefaultLibrary)),
		CLIVersion:      DefaultCLIVersion,
		CLILock:         filepath.Join(home, ".agents", ".skill-lock.json"),
	}
	// The skills CLI runs from the user's own shell, so the overlay does not
	// apply: its lock is wherever the process environment puts it.
	if state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); filepath.IsAbs(state) {
		r.CLILock = filepath.Join(state, "skills", ".skill-lock.json")
	}
	if projectRoot != "" {
		r.ProjectRoot = filepath.Clean(projectRoot)
		r.ProjectLibrary = filepath.Join(r.ProjectRoot, filepath.FromSlash(DefaultProjectLibrary))
	}
	return r
}

// HarnessState is how one harness that can see a skill will use it.
type HarnessState struct {
	Mode string `json:"mode"`         // auto | name-only | manual | off
	By   string `json:"by,omitempty"` // "settings" when harness config, not the skill's files, decides
}

const (
	ModeAuto     = "auto"
	ModeNameOnly = "name-only"
	ModeManual   = "manual"
	ModeOff      = "off"

	BySettings = "settings"
)

// Source is where a skill was installed from.
type Source struct {
	Method      string `json:"method"` // npx | git | local
	Repo        string `json:"repo"`   // "owner/repo", a URL, or a local path
	Ref         string `json:"ref,omitempty"`
	Path        string `json:"path,omitempty"` // skill folder inside the repo, slash-separated
	Managed     bool   `json:"managed"`        // true: from our record. false: from the skills CLI lock
	InstalledAt string `json:"installedAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Dir         string    `json:"dir"`
	Scope       string    `json:"scope"`
	Plugin      string    `json:"plugin,omitempty"`
	Paths       []string  `json:"paths"`
	Harnesses   []Harness `json:"harnesses"`
	Editable    bool      `json:"editable"`
	Problem     string    `json:"problem,omitempty"`
	// Synced marks a claude.ai-synced skill (synced/<account>/<skill>).
	Synced bool `json:"synced,omitempty"`
	// Invocation has one entry per harness in Harnesses.
	Invocation map[Harness]HarnessState `json:"invocation"`
	Source     *Source                  `json:"source,omitempty"`
	// Manual is what the skill's own files say, both of them: the manual-only
	// switch's state when no harness reads the files to say it for them.
	Manual bool `json:"manual,omitempty"`
}

type File struct {
	Path string `json:"path"` // relative to the skill dir, slash-separated
	Size int64  `json:"size"`
}

type Detail struct {
	Skill
	Content string `json:"content"` // SKILL.md
	Files   []File `json:"files"`
}

// root is one discovery directory and what finding a skill there means.
type root struct {
	path      string
	scope     string
	plugin    string
	harnesses []Harness
	readonly  bool
	synced    bool // Claude's skills dir: also look in synced/<account>/<skill>
}

// roots lists every directory a harness reads skills from, per the research
// table: project roots first so that a skill reachable from both a project
// and a user root is labelled with the narrower scope.
func (r Roots) roots() []root {
	out := r.ownRoots()
	out = append(out, r.claudePluginRoots()...)
	out = append(out, r.codexPluginRoots()...)
	if r.CodexHome != "" {
		out = append(out, root{path: filepath.Join(r.CodexHome, "skills", ".system"), scope: ScopeSystem, harnesses: []Harness{Codex}, readonly: true})
	}
	return out
}

// ownRoots are the project and personal roots: the ones a user's own skills
// live in, as opposed to what plugins and the harnesses ship.
func (r Roots) ownRoots() []root {
	var out []root
	if r.ProjectRoot != "" {
		p := r.ProjectRoot
		out = append(out,
			root{path: filepath.Join(p, ".claude", "skills"), scope: ScopeProject, harnesses: []Harness{Claude}},
			root{path: filepath.Join(p, ".agents", "skills"), scope: ScopeProject, harnesses: []Harness{Codex, Pi}},
			root{path: filepath.Join(p, ".codex", "skills"), scope: ScopeProject, harnesses: []Harness{Codex}},
			root{path: filepath.Join(p, ".pi", "skills"), scope: ScopeProject, harnesses: []Harness{Pi}},
		)
		out = appendLibrary(out, r.ProjectLibrary, ScopeProject)
	}
	if r.ClaudeConfigDir != "" {
		out = append(out, root{path: filepath.Join(r.ClaudeConfigDir, "skills"), scope: ScopeUser, harnesses: []Harness{Claude}, synced: true})
	}
	if r.Home != "" {
		out = append(out, root{path: filepath.Join(r.Home, ".agents", "skills"), scope: ScopeUser, harnesses: []Harness{Codex, Pi}})
	}
	if r.CodexHome != "" {
		out = append(out, root{path: filepath.Join(r.CodexHome, "skills"), scope: ScopeUser, harnesses: []Harness{Codex}})
	}
	if r.PiAgentDir != "" {
		out = append(out, root{path: filepath.Join(r.PiAgentDir, "skills"), scope: ScopeUser, harnesses: []Harness{Pi}})
	}
	return appendLibrary(out, r.Library, ScopeUser)
}

// appendLibrary lists a library as a root of its own, read by no harness, so
// a library nothing links to still shows its skills. It adds nothing when a
// harness root is already that path.
func appendLibrary(out []root, path, scope string) []root {
	if path == "" {
		return out
	}
	for _, rt := range out {
		if rt.path == path {
			return out
		}
	}
	return append(out, root{path: path, scope: scope})
}

// claudePluginRoots reads Claude's installed_plugins.json: a user-scoped
// install applies everywhere, a project/local one only under its project.
func (r Roots) claudePluginRoots() []root {
	if r.ClaudeConfigDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(r.ClaudeConfigDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil {
		return nil
	}
	keys := make([]string, 0, len(installed.Plugins))
	for k := range installed.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []root
	seen := map[string]bool{}
	for _, key := range keys {
		name, _, _ := strings.Cut(key, "@")
		for _, p := range installed.Plugins[key] {
			if !pluginApplies(p.Scope, p.ProjectPath, r.ProjectRoot) {
				continue
			}
			install := filepath.Clean(p.InstallPath)
			if install == "." || seen[install] {
				continue
			}
			seen[install] = true
			out = append(out, root{path: filepath.Join(install, "skills"), scope: ScopePlugin, plugin: name, harnesses: []Harness{Claude}, readonly: true})
		}
	}
	return out
}

func pluginApplies(scope, projectPath, projectRoot string) bool {
	if scope == "user" {
		return true
	}
	if scope != "project" && scope != "local" {
		return false
	}
	if projectPath == "" || projectRoot == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(projectPath), filepath.Clean(projectRoot))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// codexPluginRoots finds $CODEX_HOME/plugins/cache/<marketplace>/<plugin>/<version>/skills,
// taking the most recently written version of each plugin.
func (r Roots) codexPluginRoots() []root {
	if r.CodexHome == "" {
		return nil
	}
	cache := filepath.Join(r.CodexHome, "plugins", "cache")
	var out []root
	for _, market := range readDirs(cache) {
		for _, plugin := range readDirs(filepath.Join(cache, market)) {
			base := filepath.Join(cache, market, plugin)
			var best string
			var bestMod int64
			for _, version := range readDirs(base) {
				skillsDir := filepath.Join(base, version, "skills")
				info, err := os.Stat(skillsDir)
				if err != nil || !info.IsDir() {
					continue
				}
				if mod := info.ModTime().UnixNano(); best == "" || mod > bestMod || (mod == bestMod && version > filepath.Base(filepath.Dir(best))) {
					best, bestMod = skillsDir, mod
				}
			}
			if best != "" {
				out = append(out, root{path: best, scope: ScopePlugin, plugin: plugin, harnesses: []Harness{Codex}, readonly: true})
			}
		}
	}
	return out
}

// readDirs lists the non-dot directories under dir, following symlinks.
func readDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && info.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func hasSkillFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && info.Mode().IsRegular()
}

// Discover lists every skill reachable from r, one entry per real directory.
// Sorted project, user, plugin, system; by name within each.
func Discover(r Roots) ([]Skill, error) {
	byDir := map[string]*Skill{}
	manual := map[string]bool{} // real dir -> frontmatter says manual-only
	var order []string
	add := func(rt root, path string, synced bool) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return
		}
		if s, ok := byDir[real]; ok {
			if !contains(s.Paths, path) {
				s.Paths = append(s.Paths, path)
			}
			for _, h := range rt.harnesses {
				if !containsHarness(s.Harnesses, h) {
					s.Harnesses = append(s.Harnesses, h)
				}
			}
			return
		}
		s := &Skill{
			Dir:       real,
			Scope:     rt.scope,
			Plugin:    rt.plugin,
			Paths:     []string{path},
			Harnesses: append([]Harness{}, rt.harnesses...),
			Editable:  !rt.readonly && !synced,
			Synced:    synced,
		}
		manual[real] = fillMeta(s, filepath.Base(path))
		byDir[real] = s
		order = append(order, real)
	}
	for _, rt := range r.roots() {
		for _, name := range readDirs(rt.path) {
			dir := filepath.Join(rt.path, name)
			if hasSkillFile(dir) {
				add(rt, dir, false)
				continue
			}
			// claude.ai-synced skills sit two levels down, under
			// synced/<account-id>/<skill>. They are rewritten by the sync, so
			// they are shown but not editable here.
			if rt.synced && name == "synced" {
				for _, account := range readDirs(dir) {
					for _, skill := range readDirs(filepath.Join(dir, account)) {
						if d := filepath.Join(dir, account, skill); hasSkillFile(d) {
							add(rt, d, true)
						}
					}
				}
			}
		}
	}
	policy := readPolicy(r)
	sources := newSourceIndex(r)
	out := make([]Skill, 0, len(order))
	for _, dir := range order {
		s := byDir[dir]
		sort.Slice(s.Harnesses, func(i, j int) bool { return harnessRank(s.Harnesses[i]) < harnessRank(s.Harnesses[j]) })
		s.Invocation = policy.invocation(s, manual[dir])
		if s.Editable {
			frontmatter, openai := FileManual(dir)
			s.Manual = frontmatter && openai
		}
		s.Source = sources.lookup(s)
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := scopeRank(out[i].Scope), scopeRank(out[j].Scope); a != b {
			return a < b
		}
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateName applies the Agent Skills spec's name rule.
func ValidateName(name string) error {
	if name == "" || len(name) > 64 {
		return errors.New("name must be 1-64 characters")
	}
	if !nameRe.MatchString(name) {
		return errors.New("name must be lowercase letters, digits and single hyphens, not starting or ending with a hyphen")
	}
	return nil
}

func validateDescription(desc string) error {
	if n := len([]rune(strings.TrimSpace(desc))); n == 0 || n > 1024 {
		return errors.New("description must be 1-1024 characters")
	}
	return nil
}

// fillMeta reads SKILL.md's frontmatter into the skill and records anything
// a harness would warn about or skip over. It reports whether the frontmatter
// keeps the skill out of the model's hands.
func fillMeta(s *Skill, dirName string) (manual bool) {
	s.Name = dirName
	data, err := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
	if err != nil {
		s.Problem = "SKILL.md is unreadable: " + err.Error()
		return false
	}
	fields, _, err := parseFrontmatter(string(data))
	if err != nil {
		s.Problem = err.Error()
		return false
	}
	manual = frontmatterManual(string(data))
	var problems []string
	if name := strings.TrimSpace(fields["name"]); name != "" {
		s.Name = name
		if err := ValidateName(name); err != nil {
			problems = append(problems, err.Error())
		} else if name != dirName {
			problems = append(problems, "name "+name+" does not match directory "+dirName)
		}
	} else {
		problems = append(problems, "missing name")
	}
	s.Description = strings.TrimSpace(fields["description"])
	if s.Description == "" {
		problems = append(problems, "missing description")
	} else if len([]rune(s.Description)) > 1024 {
		problems = append(problems, "description is over 1024 characters")
	}
	s.Problem = strings.Join(problems, "; ")
	return manual
}

func scopeRank(scope string) int {
	switch scope {
	case ScopeProject:
		return 0
	case ScopeUser:
		return 1
	case ScopePlugin:
		return 2
	default:
		return 3
	}
}

func harnessRank(h Harness) int {
	switch h {
	case Claude:
		return 0
	case Codex:
		return 1
	case Pi:
		return 2
	default:
		return 3
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsHarness(list []Harness, v Harness) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
