package claudecode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A project's dotenv files must never reach Claude Code: Bun loads them from
// its cwd, and a session that carries them hands them to everything it spawns.
// This runs the real bridge, under each runtime present, in a project that has
// dotenv files, with a stand-in for Claude Code that records the environment
// it was started with. The stand-in comes in both shapes an install does: a
// native executable, and a script the SDK runs under the bridge's own runtime —
// which under Bun is a second process that would load the files again.
func TestProjectDotenvDoesNotReachClaudeCode(t *testing.T) {
	a := &Adapter{}
	sidecarDir, err := a.sidecarPath()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(sidecarDir, "sidecar.mjs")

	runtimes := map[string]func() (resolved, bool){
		"bun": func() (resolved, bool) {
			bun, err := exec.LookPath("bun")
			return resolved{runtime: bun, runtimeArgs: bunArgs(script)}, err == nil && sdkInstalled(sidecarDir)
		},
		"node": func() (resolved, bool) {
			node, err := exec.LookPath("node")
			return resolved{runtime: node, runtimeArgs: []string{script}}, err == nil && sdkInstalled(sidecarDir)
		},
		"bundled": func() (resolved, bool) {
			path := bundledSidecarPath()
			return resolved{runtime: path}, path != ""
		},
	}

	// Each stand-in renames its recording into place, so a file that exists is
	// complete.
	stubs := map[string]func(seen string) string{
		"claude": func(seen string) string {
			return "#!/bin/sh\nenv > '" + seen + ".tmp' && mv '" + seen + ".tmp' '" + seen + "'\n"
		},
		"claude.js": func(seen string) string {
			return `import { writeFileSync, renameSync } from "node:fs";
const seen = ` + strconv.Quote(seen) + `;
writeFileSync(seen + ".tmp", Object.entries(process.env).map(([k, v]) => k + "=" + v).join("\n"));
renameSync(seen + ".tmp", seen);
`
		},
	}

	for name, find := range runtimes {
		for stub, body := range stubs {
			t.Run(name+"/"+stub, func(t *testing.T) {
				r, ok := find()
				if !ok {
					t.Skip("runtime not available")
				}

				project := t.TempDir()
				write := func(name, content string) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(project, name), []byte(content), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				write(".env", "OMNIPLEX_LEAK_PROBE=1\nOMNIPLEX_GIVEN=from-file\n")
				write(".env.local", "OMNIPLEX_LEAK_PROBE_LOCAL=1\n")

				seen := filepath.Join(t.TempDir(), "env")
				write(stub, body(seen))

				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd, err := r.command(ctx,
					sidecarConfig{Op: "models", Cwd: project, ClaudePath: filepath.Join(project, stub)},
					map[string]string{"OMNIPLEX_GIVEN": "from-host", "OMNIPLEX_OVERLAY": "yes"},
				)
				if err != nil {
					t.Fatal(err)
				}
				cmd.Dir = project
				// The stand-in answers nothing, so the listing fails; only the
				// environment it was handed matters here.
				_ = cmd.Run()

				data, err := os.ReadFile(seen)
				if err != nil {
					t.Fatalf("the bridge never started Claude Code: %v", err)
				}
				got := map[string]string{}
				for _, line := range strings.Split(string(data), "\n") {
					if k, v, ok := strings.Cut(line, "="); ok {
						got[k] = v
					}
				}

				for _, leaked := range []string{"OMNIPLEX_LEAK_PROBE", "OMNIPLEX_LEAK_PROBE_LOCAL"} {
					if v, ok := got[leaked]; ok {
						t.Errorf("%s=%q reached Claude Code from the project's dotenv files", leaked, v)
					}
				}
				for k, want := range map[string]string{
					"OMNIPLEX_GIVEN":         "from-host",
					"OMNIPLEX_OVERLAY":       "yes",
					"CLAUDE_CODE_ENTRYPOINT": "sdk-ts",
				} {
					if got[k] != want {
						t.Errorf("%s = %q, want %q", k, got[k], want)
					}
				}
				if got["PATH"] == "" {
					t.Error("the ambient environment did not reach Claude Code")
				}
			})
		}
	}
}

func sdkInstalled(sidecarDir string) bool {
	return moduleInstalled(sidecarDir, "@anthropic-ai", "claude-agent-sdk")
}
