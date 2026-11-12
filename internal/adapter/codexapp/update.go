package codexapp

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

var (
	_ adapter.Updater    = (*Adapter)(nil)
	_ adapter.Executable = (*Adapter)(nil)
)

// Executable is the Codex CLI a session would start, found on the PATH as a
// session finds it.
func (a *Adapter) Executable() (string, bool) {
	path, err := exec.LookPath(a.Bin)
	return path, err == nil
}

// Update runs `codex update` under the instance's environment, so a
// per-account CODEX_HOME is the one it reads. The standalone install keeps
// each release in its own directory and repoints the link on the PATH, so
// the version after is read from a fresh lookup.
func (a *Adapter) Update(ctx context.Context, env map[string]string) (adapter.UpdateResult, error) {
	bin, ok := a.Executable()
	if !ok {
		return adapter.UpdateResult{}, errors.New("the Codex CLI was not found on this machine")
	}
	cmdEnv := codexEnv(env)
	version := func(ctx context.Context) string {
		path, ok := a.Executable()
		if !ok {
			return ""
		}
		return versionOutput(ctx, path, cmdEnv)
	}
	return adapter.RunUpdate(ctx, cmdEnv, "codex", bin, []string{"update"}, version)
}

// versionOutput is what `codex --version` prints, or nothing if it did not
// run within a probe's deadline.
func versionOutput(ctx context.Context, path string, env []string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}
