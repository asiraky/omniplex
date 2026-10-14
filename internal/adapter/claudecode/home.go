package claudecode

import (
	"path/filepath"

	"github.com/asiraky/omniplex/internal/accounthome"
	"github.com/asiraky/omniplex/internal/adapter"
)

var _ adapter.SharedHome = (*Adapter)(nil)

// DefaultHome is ~/.claude: Claude Code's home with no CLAUDE_CONFIG_DIR.
func (a *Adapter) DefaultHome(userHome string) string {
	return filepath.Join(userHome, ".claude")
}

// SharedEntries are what a user writes into ~/.claude. The account's own are
// everything else: .credentials.json, and .claude.json, which carries the
// signed-in identity (oauthAccount) Claude Code keys synced skills, plugins
// and org features by, so it cannot be shared; projects/ and sessions/,
// which a thread's switch of account moves between homes; and the caches.
func (a *Adapter) SharedEntries() []accounthome.Entry {
	return []accounthome.Entry{
		{Name: "settings.json"},
		{Name: "CLAUDE.md"},
		{Name: "keybindings.json"},
		{Name: "loop.md"},
		{Name: "skills", Dir: true},
		{Name: "agents", Dir: true},
		{Name: "commands", Dir: true},
		{Name: "output-styles", Dir: true},
		{Name: "rules", Dir: true},
		{Name: "hooks", Dir: true},
		{Name: "workflows", Dir: true},
		{Name: "routines", Dir: true},
		{Name: "themes", Dir: true},
		{Name: "agent-memory", Dir: true},
		{Name: "dev-mods", Dir: true},
		// Claude Code buckets what it syncs here by organisation and account
		// (plugins/synced/<org>_<account>), so one folder serves every login.
		{Name: "plugins", Dir: true},
	}
}
