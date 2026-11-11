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
// old file. Running sessions keep the binary they started with.
func (a *Adapter) Update(ctx context.Context, env map[string]string) (adapter.UpdateResult, error) {
	bin, ok := a.findClaude()
	if !ok {
		return adapter.UpdateResult{}, errors.New("Claude Code is not installed on this machine")
	}
	cmdEnv := adapter.MergeEnv(os.Environ(), env)
	version := func(ctx context.Context) string {
		path, ok := a.findClaude()
		if !ok {
			return ""
		}
		return runBrieflyEnv(ctx, cmdEnv, path, "--version")
	}
	return adapter.RunUpdate(ctx, cmdEnv, bin, []string{"update"}, version)
}
