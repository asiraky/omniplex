// Package userconfig holds per-machine preferences that deliberately do not
// belong in a repo. Project settings live in .omniplex/project.json and are shared
// with whoever clones the project; the things here are the operator's own
// habits, so they live beside the database in ~/.omniplex instead.
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

// DefaultSummaryPrompt is the system prompt the summariser runs under when the
// operator has not written one. It is deliberately shaped around the question
// somebody actually has when they reopen a session they have forgotten: what
// did I ask for, what happened, and is anything still owed. The transcript
// arrives as a user turn, so this says nothing about how to parse it.
const DefaultSummaryPrompt = `You are summarising a coding-agent session for the person who started it. They have forgotten what it was about and do not want to reread it.

Write three short sections, using these exact headings:

**Request** — what the user originally asked for, in one or two plain sentences. Their opening message may be long and rambling; state the actual intent, not their wording.

**What happened** — what the agent did, and whether it changed any files. Name the files or areas it touched. Say plainly if it changed nothing, got stuck, or was interrupted.

**Follow-ups** — anything still outstanding: unanswered questions, failing checks, work the agent said it would do but did not. Write "None." if there is nothing.

Be brief and concrete — under 200 words in total. Report only what the transcript shows; never guess at intent or invent work that is not there. Address the user as "you". Do not add a preamble, a title, or a closing remark.`

type Config struct {
	Version int `json:"version"`
	// BranchFormat is a JavaScript arrow function, object in and string out,
	// evaluated by the web UI to name a new worktree. Empty means the default.
	BranchFormat string `json:"branchFormat,omitempty"`
	// SuggestIssues disables the `gh` lookup for people who do not use it.
	SuggestIssues *bool `json:"suggestIssues,omitempty"`
	// SummaryPrompt is the system prompt the session summariser runs under.
	// Empty means DefaultSummaryPrompt, so an operator who has never opened
	// settings still gets a usable summary — and clearing the box is how you
	// go back to the default rather than a separate stored flag.
	SummaryPrompt string `json:"summaryPrompt,omitempty"`
	// Providers declares provider instances — configured accounts for the
	// harness adapters. Entries are held raw and written back verbatim: an
	// entry naming a driver this build has never heard of must survive a
	// load/save cycle untouched, so a config written on another branch is
	// never destroyed. internal/provider parses them.
	Providers []json.RawMessage `json:"providers,omitempty"`
}

func Default() Config {
	return Config{Version: 1, BranchFormat: DefaultBranchFormat, SummaryPrompt: DefaultSummaryPrompt}
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
	if strings.TrimSpace(cfg.SummaryPrompt) == "" {
		cfg.SummaryPrompt = DefaultSummaryPrompt
	}
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
// leave a half-parsed config that breaks every later session.
func Save(cfg Config) (Config, error) {
	cfg, err := Normalize(cfg)
	if err != nil {
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
