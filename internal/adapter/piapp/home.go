package piapp

import (
	"path/filepath"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
)

var _ adapter.SharedHome = (*Adapter)(nil)

// DefaultHome is ~/.pi/agent: Pi's agent directory with no
// PI_CODING_AGENT_DIR.
func (a *Adapter) DefaultHome(userHome string) string {
	return filepath.Join(userHome, ".pi", "agent")
}

// SharedEntries are what a user writes into ~/.pi/agent. The account's own
// are auth.json (every provider's keys and OAuth tokens), models-store.json
// (the catalog fetched with those credentials), sessions/, logs and tmp/.
func (a *Adapter) SharedEntries() []accounthome.Entry {
	return []accounthome.Entry{
		{Name: "settings.json"},
		{Name: "keybindings.json"},
		{Name: "models.json"},
		{Name: "trust.json"},
		{Name: "AGENTS.md"},
		{Name: "AGENTS.override.md"},
		{Name: "SYSTEM.md"},
		{Name: "APPEND_SYSTEM.md"},
		{Name: "skills", Dir: true},
		{Name: "prompts", Dir: true},
		{Name: "themes", Dir: true},
		{Name: "extensions", Dir: true},
		// Packages `pi install` puts where settings.json's packages list
		// points, and the fd/rg binaries Pi downloads.
		{Name: "npm", Dir: true},
		{Name: "git", Dir: true},
		{Name: "bin", Dir: true},
	}
}
