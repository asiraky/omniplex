package claudecode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// resolved is everything this harness needs in order to start. Working out
// what goes in here is the adapter's entire responsibility; nothing outside
// this package knows any of it exists.
type resolved struct {
	// runtime runs the sidecar: either a bundled standalone binary (args empty)
	// or a JS runtime with the sidecar script as its first argument.
	runtime     string
	runtimeArgs []string
	runtimeKind string

	// claudePath is the Claude Code executable the SDK will drive. We never
	// ship one; this is always the user's own install.
	claudePath string

	sidecarDir string
}

// docsURL is where a user goes to install the harness we depend on.
const docsURL = "https://code.claude.com/docs"

// resolve locates a JS runtime and a Claude Code install, or explains what is
// missing in terms the user can act on.
func (a *Adapter) resolve(ctx context.Context) (resolved, adapter.Availability) {
	var r resolved

	dir, err := a.sidecarPath()
	if err != nil {
		return r, adapter.Unavailable(
			"The Claude bridge could not be unpacked: "+err.Error(),
			adapter.Remedy{Text: "Check that the cache directory is writable."},
		)
	}
	r.sidecarDir = dir

	// 1. A JS runtime to host Anthropic's SDK.
	switch {
	case a.bundledSidecar != "":
		r.runtime, r.runtimeKind = a.bundledSidecar, "bundled"
	default:
		script := filepath.Join(dir, "sidecar.mjs")
		if bun, err := exec.LookPath("bun"); err == nil {
			r.runtime, r.runtimeArgs, r.runtimeKind = bun, []string{script}, "bun"
		} else if node, err := exec.LookPath("node"); err == nil {
			if !nodeIsRecentEnough(ctx, node) {
				return r, adapter.Unavailable(
					"Node "+nodeVersion(ctx, node)+" is too old for the Claude Agent SDK, which needs 18 or newer.",
					adapter.Remedy{Text: "Upgrade Node to 18 or newer", URL: "https://nodejs.org"},
					adapter.Remedy{Text: "Or install Bun", Command: "curl -fsSL https://bun.sh/install | bash"},
				)
			}
			r.runtime, r.runtimeArgs, r.runtimeKind = node, []string{script}, "node"
		} else {
			return r, adapter.Unavailable(
				"Claude needs a JavaScript runtime to host Anthropic's Agent SDK, and neither Node nor Bun was found.",
				adapter.Remedy{Text: "Install Node 18 or newer", URL: "https://nodejs.org"},
				adapter.Remedy{Text: "Or install Bun", Command: "curl -fsSL https://bun.sh/install | bash"},
			)
		}
	}

	// 2. The SDK itself must be installed next to the script, unless the
	//    runtime is a bundle that already contains it.
	if r.runtimeKind != "bundled" {
		if !moduleInstalled(dir, "@anthropic-ai", "claude-agent-sdk") {
			return r, adapter.Unavailable(
				"Anthropic's Claude Agent SDK is not installed.",
				adapter.Remedy{
					Text:    "Install it once; it is cached afterwards",
					Command: "cd " + dir + " && npm install",
				},
			)
		}
	}

	// 3. Claude Code itself, which the SDK drives.
	claudePath, found := a.findClaude()
	if !found {
		return r, adapter.Unavailable(
			"Claude Code is not installed on this machine.",
			adapter.Remedy{Text: "Install Claude Code", URL: docsURL},
			adapter.Remedy{Text: "Or point Omniplex at an existing install with -claude-path"},
		)
	}
	r.claudePath = claudePath

	return r, adapter.Ready(map[string]string{
		"runtime":   r.runtimeKind,
		"claude":    claudePath,
		"sidecar":   dir,
		"claudeVer": claudeVersion(ctx, claudePath),
	})
}

// findClaude walks the places a Claude Code install can be, most explicit
// first. It never falls back to something we ship, because we ship none.
func (a *Adapter) findClaude() (string, bool) {
	if a.ClaudePath != "" {
		if isExecutable(a.ClaudePath) {
			return a.ClaudePath, true
		}
		return "", false // an explicit path that is wrong is an error, not a hint
	}
	if env := os.Getenv("OMNIPLEX_CLAUDE_PATH"); env != "" && isExecutable(env) {
		return env, true
	}
	if p, err := exec.LookPath("claude"); err == nil {
		if resolvedPath, err := filepath.EvalSymlinks(p); err == nil && isExecutable(resolvedPath) {
			return resolvedPath, true
		}
		return p, true
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	// Versioned installs: pick the newest.
	if matches, _ := filepath.Glob(filepath.Join(home, ".local", "share", "claude", "versions", "*")); len(matches) > 0 {
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		for _, m := range matches {
			if isExecutable(m) {
				return m, true
			}
		}
	}
	for _, candidate := range []string{
		filepath.Join(home, ".claude", "local", "claude"),
		filepath.Join(home, ".local", "bin", "claude"),
	} {
		if isExecutable(candidate) {
			if r, err := filepath.EvalSymlinks(candidate); err == nil {
				return r, true
			}
			return candidate, true
		}
	}
	return "", false
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func nodeVersion(ctx context.Context, node string) string {
	return strings.TrimSpace(runBriefly(ctx, node, "--version"))
}

func nodeIsRecentEnough(ctx context.Context, node string) bool {
	v := strings.TrimPrefix(nodeVersion(ctx, node), "v")
	major, _, _ := strings.Cut(v, ".")
	n := 0
	for _, c := range major {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n >= 18
}

func claudeVersion(ctx context.Context, path string) string {
	return strings.TrimSpace(runBriefly(ctx, path, "--version"))
}

// runBriefly runs a fast informational command with a hard deadline, so a
// wedged binary cannot stall a probe.
func runBriefly(ctx context.Context, name string, args ...string) string {
	return runBrieflyEnv(ctx, nil, name, args...)
}

// runBrieflyEnv is runBriefly under a given environment; nil inherits.
func runBrieflyEnv(ctx context.Context, env []string, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	out, err := cmd.Output()
	// A non-zero exit still answered: `claude auth status` exits 1 when
	// signed out, with the JSON that says so on stdout. Only a command that
	// could not run at all — or was cut off by the deadline — has nothing.
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || ctx.Err() != nil) {
		return ""
	}
	return string(out)
}
