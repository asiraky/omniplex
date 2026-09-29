// Package userconfig holds per-machine preferences that are not about any one
// project: the operator's own habits, and where new projects go. They live
// beside the database in ~/.omniplex.
package userconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultBranchFormat turns a `gh issue list` row into a branch name. It ships
// as the default so the suggestion list works before anyone opens settings; it
// is a string rather than Go code because the presenter is what evaluates it.
const DefaultBranchFormat = "(issue) => `issue/${issue.number}-${issue.title.toLowerCase().replace(/[^a-z0-9]+/g, \"-\").replace(/^-+|-+$/g, \"\").slice(0, 40).replace(/-+$/, \"\")}`"

type Config struct {
	Version int `json:"version"`
	// BranchFormat is a JavaScript arrow function, object in and string out,
	// evaluated by the web UI to name a new worktree. Empty means the default.
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
	if strings.TrimSpace(cfg.BranchFormat) == "" {
		cfg.BranchFormat = DefaultBranchFormat
	}
	cfg.ProjectsDir = strings.TrimSpace(cfg.ProjectsDir)
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
	switch cfg.DefaultLevel {
	case "", "ask", "edits", "all":
		return nil
	}
	return fmt.Errorf("unknown permission level %q", cfg.DefaultLevel)
}
