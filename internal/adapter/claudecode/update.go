package claudecode

import (
	"context"
	"errors"
	"os"

	"github.com/asiraky/omniplex/internal/adapter"
)

var (
	_ adapter.Updater    = (*Adapter)(nil)
	_ adapter.Executable = (*Adapter)(nil)
)

// Update runs `claude update`. A native install downloads the new version
// beside the old one and repoints ~/.local/bin/claude at it, so the version
// after is read from a fresh lookup: the path found before still names the
// old file. Running sessions keep the binary they started with. It runs under
// the same environment as the threads, so a server started from inside Claude
// Code does not hand the updater that session's variables.
func (a *Adapter) Update(ctx context.Context, env map[string]string) (adapter.UpdateResult, error) {
	bin, ok := a.findClaude()
	if !ok {
		return adapter.UpdateResult{}, errors.New("Claude Code is not installed on this machine")
	}
	cmdEnv := claudeEnv(os.Environ(), env)
	version := func(ctx context.Context) string {
		path, ok := a.findClaude()
		if !ok {
			return ""
		}
		return runBrieflyEnv(ctx, cmdEnv, path, "--version")
	}
	return adapter.RunUpdate(ctx, cmdEnv, "claude", bin, []string{"update"}, version)
}
