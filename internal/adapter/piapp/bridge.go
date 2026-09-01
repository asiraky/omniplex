package piapp

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/asiraky/omniplex/internal/adapter"
)

// The bridge script rides inside the binary and is extracted on first use.
// Unlike claude's sidecar it bundles no runtime and no SDK: it is a page of
// glue that imports ModelRuntime from the user's own Pi install, so Pi's
// providers, flows, and credential storage are always exactly the installed
// version's.
//
//go:embed authbridge.mjs
var bridgeScript []byte

var (
	bridgeOnce sync.Once
	bridgeAt   string
)

// bridgeScriptPath extracts the embedded script once per content version and
// returns its path. The hash is in the filename so an upgraded omniplex never
// runs a stale extraction.
func bridgeScriptPath() (string, error) {
	bridgeOnce.Do(func() {
		sum := sha256.Sum256(bridgeScript)
		name := "pi-authbridge-" + hex.EncodeToString(sum[:8]) + ".mjs"

		base, err := os.UserCacheDir()
		if err != nil {
			return
		}
		dir := filepath.Join(base, "omniplex", "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		path := filepath.Join(dir, name)

		if info, err := os.Stat(path); err == nil && info.Size() == int64(len(bridgeScript)) {
			bridgeAt = path
			return
		}
		// Write to a temp name and rename, so a torn write is never executed.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, bridgeScript, 0o644); err != nil {
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return
		}
		bridgeAt = path
	})
	if bridgeAt == "" {
		return "", errors.New("could not extract the pi auth bridge script")
	}
	return bridgeAt, nil
}

// packageRootMarker is where an npm install of pi keeps the importable package.
const packageRootMarker = "node_modules/@earendil-works/pi-coding-agent"

// resolveBridge finds the node binary and the pi package root the bridge
// imports from. The pi command on PATH is normally npm's symlink into the
// install prefix — following it lands inside the package — so the package
// root falls out of the binary's real location, and the node that installed
// pi lives beside the symlink. No configuration, no second install.
func (a *Adapter) resolveBridge() (node, pkgRoot string, err error) {
	piPath, found := a.findPi()
	if !found {
		return "", "", errors.New("the Pi CLI was not found on this machine")
	}
	real, err := filepath.EvalSymlinks(piPath)
	if err != nil {
		real = piPath
	}

	// An npm-installed pi resolves to a file inside the package; take the
	// path up to and including the package directory.
	if i := strings.Index(real, packageRootMarker); i >= 0 {
		pkgRoot = real[:i+len(packageRootMarker)]
	} else {
		// A pi that is not a symlink into node_modules (a compiled binary, a
		// wrapper script): try the conventional prefix layout next to it.
		pkgRoot = filepath.Join(filepath.Dir(piPath), "..", "lib", packageRootMarker)
	}
	if _, statErr := os.Stat(filepath.Join(pkgRoot, "dist", "index.js")); statErr != nil {
		return "", "", fmt.Errorf("pi's package sources were not found near %s; structured sign-in needs the npm install (npm i -g @earendil-works/pi-coding-agent)", piPath)
	}

	// Prefer the node that pi itself was installed with — it is guaranteed
	// new enough — and fall back to whatever PATH offers.
	node = filepath.Join(filepath.Dir(piPath), "node")
	if info, statErr := os.Stat(node); statErr != nil || info.IsDir() {
		node, err = exec.LookPath("node")
		if err != nil {
			return "", "", errors.New("node was not found; pi's sign-in flows need the Node.js runtime pi was installed with")
		}
	}
	return node, pkgRoot, nil
}

// bridgeArgv builds the command line for one bridge run. Provider and auth
// type ride on argv because they are not secrets; prompt answers — which can
// be — go over stdin only.
func (a *Adapter) bridgeArgv(cmd string, extra ...string) ([]string, error) {
	if len(a.bridgeOverride) > 0 {
		return append(append([]string{}, a.bridgeOverride...), append([]string{cmd}, extra...)...), nil
	}
	node, pkgRoot, err := a.resolveBridge()
	if err != nil {
		return nil, err
	}
	script, err := bridgeScriptPath()
	if err != nil {
		return nil, err
	}
	return append([]string{node, script, pkgRoot, cmd}, extra...), nil
}

// bridgeLine is one NDJSON frame from the script.
type bridgeLine struct {
	Type    string          `json:"type"`
	ID      int             `json:"id"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Event   json.RawMessage `json:"event"`
	Prompt  json.RawMessage `json:"prompt"`
}

// runBridge executes one bridge command under the instance's env overlay,
// relaying notify/prompt traffic through ia (nil for non-interactive
// commands), and decodes the result frame into out. It returns only when the
// flow has finished or failed.
func (a *Adapter) runBridge(ctx context.Context, env map[string]string, ia adapter.AuthInteraction, out any, cmd string, extra ...string) error {
	argv, err := a.bridgeArgv(cmd, extra...)
	if err != nil {
		return err
	}
	proc := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// The overlay is what points ModelRuntime at this instance's
	// PI_CODING_AGENT_DIR; without it every account would sign into the
	// default directory.
	proc.Env = adapter.MergeEnv(os.Environ(), env)

	stdin, err := proc.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		return err
	}
	proc.Stderr = nil
	if err := proc.Start(); err != nil {
		return fmt.Errorf("start pi auth bridge: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}()

	var writeMu sync.Mutex
	answer := func(v map[string]any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = stdin.Write(append(b, '\n'))
		return err
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var line bridgeLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		switch line.Type {
		case "notify":
			if ia != nil {
				ia.Notify(mapAuthEvent(line.Event))
			}
		case "prompt":
			if ia == nil {
				// A prompt with nobody to answer it: cancel so the flow
				// unwinds instead of hanging.
				_ = answer(map[string]any{"type": "cancel", "id": line.ID})
				continue
			}
			value, err := ia.Prompt(ctx, mapAuthPrompt(line.Prompt))
			if err != nil {
				_ = answer(map[string]any{"type": "cancel", "id": line.ID})
				continue
			}
			// The value may be a secret; it goes to the bridge and nowhere
			// else — never logged, never stored.
			if err := answer(map[string]any{"type": "answer", "id": line.ID, "value": value}); err != nil {
				return err
			}
		case "result":
			if out != nil && len(line.Data) > 0 {
				return json.Unmarshal(line.Data, out)
			}
			return nil
		case "error":
			return errors.New(line.Message)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return err
	}
	return errors.New("the pi auth bridge exited without a result")
}

// mapAuthEvent translates pi's AuthEvent into the adapter vocabulary. The
// types and field names line up one-to-one by design (the adapter vocabulary
// was drawn from what pi's flows emit), so this is a re-tag, not a transform.
func mapAuthEvent(raw json.RawMessage) adapter.AuthEvent {
	var ev struct {
		Type            string `json:"type"`
		Message         string `json:"message"`
		URL             string `json:"url"`
		Instructions    string `json:"instructions"`
		UserCode        string `json:"userCode"`
		VerificationURI string `json:"verificationUri"`
	}
	_ = json.Unmarshal(raw, &ev)
	return adapter.AuthEvent{
		Type:            ev.Type,
		Message:         ev.Message,
		URL:             ev.URL,
		Instructions:    ev.Instructions,
		UserCode:        ev.UserCode,
		VerificationURI: ev.VerificationURI,
	}
}

// mapAuthPrompt translates pi's AuthPrompt. "secret" and "manual_code" both
// carry values that may grant access (an API key, an OAuth code), so both use
// the non-persisted secret transport.
func mapAuthPrompt(raw json.RawMessage) adapter.AuthPrompt {
	var p struct {
		Type        string `json:"type"`
		Message     string `json:"message"`
		Placeholder string `json:"placeholder"`
		Options     []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"options"`
	}
	_ = json.Unmarshal(raw, &p)
	out := adapter.AuthPrompt{
		Message:     p.Message,
		Placeholder: p.Placeholder,
		Secret:      p.Type == "secret" || p.Type == "manual_code",
	}
	for _, o := range p.Options {
		out.Options = append(out.Options, adapter.AuthPromptOption{ID: o.ID, Label: o.Label})
	}
	return out
}
