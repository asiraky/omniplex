package codexapp

import (
	"path/filepath"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
)

var _ adapter.SharedHome = (*Adapter)(nil)

// DefaultHome is ~/.codex: Codex's home with no CODEX_HOME.
func (a *Adapter) DefaultHome(userHome string) string {
	return filepath.Join(userHome, ".codex")
}

// SharedEntries are what a user writes into ~/.codex. Codex writes config.toml
// through symlinks, and "always allow" approvals land in rules/. The account's
// own are everything else: auth.json and secrets/ (the login),
// models_cache.json and cache/ (keyed by plan and ChatGPT user), sessions,
// history and the sqlite state behind them, worktrees, memories, and the
// app-server daemon's socket, which must not reach another account's daemon.
func (a *Adapter) SharedEntries() []accounthome.Entry {
	return []accounthome.Entry{
		{Name: "config.toml"},
		{Name: "AGENTS.md"},
		{Name: "AGENTS.override.md"},
		{Name: "hooks.json"},
		// Marks rules/ as already cleaned up; without it Codex reruns the
		// cleanup over the shared rules.
		{Name: ".sandbox_migration"},
		{Name: "skills", Dir: true},
		{Name: "agents", Dir: true},
		{Name: "rules", Dir: true},
		{Name: "themes", Dir: true},
		{Name: "plugins", Dir: true},
	}
}
