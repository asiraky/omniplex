package piapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

var (
	_ adapter.Updater    = (*Adapter)(nil)
	_ adapter.Executable = (*Adapter)(nil)
)

// Executable is the pi binary a session would start, found the same way.
func (a *Adapter) Executable() (string, bool) {
	return a.findPi()
}

// Update runs `pi update --self`, which updates pi and nothing else: the
// extensions an operator installed are theirs to move. Pi updates itself with
// the npm that installed it, which sits beside its node, so the binary's own
// directory goes on the PATH exactly as it does for a session.
func (a *Adapter) Update(ctx context.Context, env map[string]string) (adapter.UpdateResult, error) {
	bin, ok := a.findPi()
	if !ok {
		return adapter.UpdateResult{}, errors.New("the Pi CLI was not found on this machine")
	}
	merged := adapter.MergeEnv(os.Environ(), env)
	version := func(ctx context.Context) string {
		path, ok := a.findPi()
		if !ok {
			return ""
		}
		return versionOutput(ctx, path, withBinDir(path, merged))
	}
	return adapter.RunUpdate(ctx, withBinDir(bin, merged), bin, []string{"update", "--self"}, version)
}

// versionOutput is what `pi --version` prints, or nothing if it did not run
// within a probe's deadline.
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
