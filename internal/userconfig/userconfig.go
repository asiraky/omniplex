// Package userconfig holds per-machine preferences that are not about any one
// project: the operator's own habits, and where new projects go. They live
// beside the database in ~/.omniplex.
package userconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// DefaultBranchFormat turns a `gh issue list` row into a branch name. It ships
// as the default so the suggestion list works before anyone opens settings.
const DefaultBranchFormat = "issue/{number}-{title}"

var placeholder = regexp.MustCompile(`\{([^{}]*)\}`)

// CheckBranchFormat refuses a template naming a placeholder the UI cannot
// fill. The UI would fall back to issue/{number} with an error on every
// suggestion, so the mistake belongs at save time, not in the picker.
func CheckBranchFormat(format string) error {
	for _, m := range placeholder.FindAllStringSubmatch(format, -1) {
		if m[1] != "number" && m[1] != "title" {
			return fmt.Errorf("branch name template: unknown placeholder {%s}; use {number} and {title}", m[1])
		}
	}
	return nil
}

type Config struct {
	Version int `json:"version"`
	// BranchFormat is a template with {number} and {title} placeholders,
	// filled in by the web UI to name a new worktree from an issue. Empty
	// means the default.
	BranchFormat string `json:"branchFormat,omitempty"`
	// SuggestIssues disables the `gh` lookup for people who do not use it.
	SuggestIssues *bool `json:"suggestIssues,omitempty"`
	// Providers declares provider instances — configured accounts for the
	// harness adapters. Entries are held raw and written back verbatim: an
	// entry naming a driver this build has never heard of must survive a
	// load/save cycle untouched, so a config written on another branch is
	// never destroyed. internal/provider parses them.
	Providers []json.RawMessage `json:"providers,omitempty"`
	// ProjectsDir is where a project's home folder is made. Empty means
	// ~/Omniplex.
	ProjectsDir string `json:"projectsDir,omitempty"`
	// DefaultInstance and DefaultModel are what a project's first thread runs
	// on, before the project has a habit of its own. Empty defers to whichever
	// account is ready.
	DefaultInstance string `json:"defaultInstance,omitempty"`
	DefaultModel    string `json:"defaultModel,omitempty"`
	// DefaultLevel is the permission level a project's first thread starts
	// on: "ask", "edits" or "all". Empty defers to each harness's default.
	DefaultLevel string `json:"defaultLevel,omitempty"`
	// Skills is where Omniplex keeps the skills it installs and creates. The
	// skills screen owns it, not the general settings screen.
	Skills SkillsConfig `json:"skills,omitempty"`
}

// SkillsConfig names the skill libraries. Empty fields mean the default.
type SkillsConfig struct {
	// Library is the personal library: a full path, or one starting with ~.
	Library string `json:"library,omitempty"`
	// ProjectLibrary is relative to a project's root.
	ProjectLibrary string `json:"projectLibrary,omitempty"`
	// CLIVersion pins the `skills` npm package a fetch runs.
	CLIVersion string `json:"cliVersion,omitempty"`
}

var cliVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.\-]*$`)

func (s SkillsConfig) validate() error {
	if s.Library != "" {
		if _, err := ExpandHome(s.Library); err != nil {
			return fmt.Errorf("skills library: %w", err)
		}
	}
	if s.ProjectLibrary != "" && !filepath.IsLocal(filepath.FromSlash(s.ProjectLibrary)) {
		return fmt.Errorf("project skills library must be a path inside the project: %s", s.ProjectLibrary)
	}
	if s.CLIVersion != "" && !cliVersionRe.MatchString(s.CLIVersion) {
		return fmt.Errorf("skills CLI version: %q is not a version", s.CLIVersion)
	}
	return nil
}

// ProjectsDirOrDefault is the folder project home folders go in.
func (c Config) ProjectsDirOrDefault() (string, error) {
	if c.ProjectsDir != "" {
		return ExpandHome(c.ProjectsDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Omniplex"), nil
}

// ExpandHome turns a leading ~ into the user's home folder. People type
// ~/code, not /home/them/code. A relative path is refused: it would resolve
// against wherever the server was started, which is nobody's intent.
func ExpandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("give the full path, starting with / or ~: %s", path)
	}
	return filepath.Clean(path), nil
}

func Default() Config {
	return Config{Version: 1, BranchFormat: DefaultBranchFormat}
}

// Path is ~/.omniplex/config.json, beside omniplex.db. OMNIPLEX_CONFIG
// overrides it, so a worktree's dev server (and tests) can manage provider
// instances without writing into the live server's configuration.
func Path() (string, error) {
	if p := os.Getenv("OMNIPLEX_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".omniplex", "config.json"), nil
}

func Normalize(cfg Config) (Config, error) {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return cfg, fmt.Errorf("unsupported user config version %d", cfg.Version)
	}
	// Configs written before templates hold a JavaScript arrow function, which
	// the UI no longer runs; any of those goes back to the default.
	if strings.TrimSpace(cfg.BranchFormat) == "" || strings.Contains(cfg.BranchFormat, "=>") {
		cfg.BranchFormat = DefaultBranchFormat
	}
	cfg.ProjectsDir = strings.TrimSpace(cfg.ProjectsDir)
	cfg.Skills.Library = strings.TrimSpace(cfg.Skills.Library)
	cfg.Skills.ProjectLibrary = strings.TrimSpace(cfg.Skills.ProjectLibrary)
	cfg.Skills.CLIVersion = strings.TrimSpace(cfg.Skills.CLIVersion)
	return cfg, nil
}

// updateMu serialises read-modify-write cycles on the config file. Two
// writers exist now — the settings commands and provider-instance management —
// and interleaving their Load/Save pairs would silently drop whichever half
// wrote first.
var updateMu sync.Mutex

// Update applies fn to the current config and persists the result, atomically
// with respect to every other Update call in this process. fn returning an
// error abandons the write.
func Update(fn func(*Config) error) (Config, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	cfg, err := Load()
	if err != nil {
		return cfg, err
	}
	if err := fn(&cfg); err != nil {
		return cfg, err
	}
	return Save(cfg)
}

// Load never fails on a missing file: an operator who has never opened settings
// still gets working suggestions.
func Load() (Config, error) {
	cfg := Default()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Default(), fmt.Errorf("parse %s: %w", path, err)
	}
	return Normalize(cfg)
}

// Save writes atomically, matching project.Save, so a crash mid-write cannot
// leave a half-parsed config that breaks every later thread.
func Save(cfg Config) (Config, error) {
	cfg, err := Normalize(cfg)
	if err != nil {
		return cfg, err
	}
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return cfg, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return cfg, err
	}
	tmp, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return cfg, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return cfg, err
	}
	if err := tmp.Close(); err != nil {
		return cfg, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return cfg, err
	}
	return cfg, os.Rename(name, path)
}

// validate refuses what a settings screen could get wrong. It runs on save,
// not load: a bad value already on disk must not lock out the screen that
// fixes it.
func validate(cfg Config) error {
	if cfg.ProjectsDir != "" {
		if _, err := ExpandHome(cfg.ProjectsDir); err != nil {
			return fmt.Errorf("projects folder: %w", err)
		}
	}
	if err := cfg.Skills.validate(); err != nil {
		return err
	}
	switch cfg.DefaultLevel {
	case "", "ask", "edits", "all":
		return nil
	}
	return fmt.Errorf("unknown permission level %q", cfg.DefaultLevel)
}
