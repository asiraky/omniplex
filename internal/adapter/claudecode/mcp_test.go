package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
)

const (
	headerSecret = "Bearer sekrit-header-value"
	envSecret    = "sekrit-env-value"
)

func secretServers() []adapter.MCPServer {
	return []adapter.MCPServer{
		{Name: "omniplex", Command: "/bin/omniplex", Args: []string{"mcp"}, Env: map[string]string{"OMNIPLEX_AGENT_TOKEN": envSecret}, Tools: []string{"show_file"}},
		{Name: "cloud", URL: "https://mcp.example.test/mcp", Headers: map[string]string{"Authorization": headerSecret}},
		{Name: "open", URL: "https://open.example.test/mcp"},
	}
}

// Header and env values never reach the bridge's command line; they ride in
// one variable that the bridge is not told to pass on.
func TestSidecarMCPKeepsValuesOffArgv(t *testing.T) {
	servers, secrets, err := sidecarMCP(secretServers())
	if err != nil {
		t.Fatal(err)
	}
	r := resolved{runtime: "/bin/true"}
	cmd, err := r.command(context.Background(), sidecarConfig{MCPServers: servers}, nil, mcpSecretsEnv+"="+secrets)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(cmd.Args, " ")
	for _, s := range []string{headerSecret, envSecret, "sekrit"} {
		if strings.Contains(argv, s) {
			t.Fatalf("argv carries %q: %s", s, argv)
		}
	}
	for _, name := range []string{"omniplex", "cloud", "open", "https://mcp.example.test/mcp"} {
		if !strings.Contains(argv, name) {
			t.Errorf("argv lost %q: %s", name, argv)
		}
	}

	var got map[string]mcpSecret
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, mcpSecretsEnv+"="); ok {
			if err := json.Unmarshal([]byte(v), &got); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got["cloud"].Headers["Authorization"] != headerSecret || got["omniplex"].Env["OMNIPLEX_AGENT_TOKEN"] != envSecret {
		t.Fatalf("secrets env = %+v", got)
	}
	if _, ok := got["open"]; ok {
		t.Error("a server with nothing secret got a secrets entry")
	}

	var cfg sidecarConfig
	if err := json.Unmarshal([]byte(cmd.Args[len(cmd.Args)-1]), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, k := range cfg.EnvKeys {
		if k == mcpSecretsEnv {
			t.Error("the bridge is told to pass the secrets variable on to Claude Code")
		}
	}
}

func TestSidecarMCPWithoutSecretsSetsNoVariable(t *testing.T) {
	_, secrets, err := sidecarMCP([]adapter.MCPServer{{Name: "open", URL: "https://open.example.test/mcp"}, {Name: "local", Command: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if secrets != "" {
		t.Fatalf("secrets = %q", secrets)
	}
}

func TestMCPStatusesMapping(t *testing.T) {
	got := mcpStatuses([]adapter.MCPServerStatus{
		{Name: "a", Status: "connected"},
		{Name: "b", Status: "needs-auth"},
		{Name: "c", Status: "failed", Error: "boom"},
		{Name: "d", Status: "pending"},
		{Name: "e", Status: "disabled"},
		{Name: "f", Status: "sleeping"},
	})
	want := []string{"connected", "needs_auth", "failed", "pending", "disabled", "failed"}
	for i, st := range got {
		if st.Status != want[i] {
			t.Errorf("%s: %q, want %q", st.Name, st.Status, want[i])
		}
	}
	if got[2].Error != "boom" || got[5].Error == "" {
		t.Errorf("errors = %q, %q", got[2].Error, got[5].Error)
	}
}

// The real bridge, with the real SDK, driving a stand-in Claude Code that
// records its argv and environment and answers control requests. The servers'
// values must reach the CLI only over its stdin, and status and reconnect
// must work through the bridge.
func TestBridgeHandsMCPSecretsOverStdin(t *testing.T) {
	a := &Adapter{}
	sidecarDir, err := a.sidecarPath()
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil || !sdkInstalled(sidecarDir) {
		t.Skip("node or the SDK is not installed")
	}

	dir := t.TempDir()
	record := filepath.Join(dir, "record.jsonl")
	stub := filepath.Join(dir, "claude.js")
	if err := os.WriteFile(stub, []byte(`import { appendFileSync } from "node:fs";
import { createInterface } from "node:readline";
const record = `+strconv.Quote(record)+`;
const log = (entry) => appendFileSync(record, JSON.stringify(entry) + "\n");
log({ argv: process.argv, env: process.env });
const reply = (request_id, response) =>
  process.stdout.write(JSON.stringify({ type: "control_response", response: { subtype: "success", request_id, response } }) + "\n");
createInterface({ input: process.stdin }).on("line", (line) => {
  let f;
  try { f = JSON.parse(line); } catch { return; }
  if (f.type !== "control_request") return;
  const req = f.request;
  log({ request: req });
  switch (req.subtype) {
    case "initialize": return reply(f.request_id, { commands: [], models: [], agents: [], account: {} });
    case "mcp_set_servers": return reply(f.request_id, { added: [], removed: [], errors: {} });
    case "mcp_status": return reply(f.request_id, { mcpServers: [
      { name: "cloud", status: "needs-auth", config: { type: "http", url: "u", headers: { Authorization: "x" } } },
      { name: "omniplex", status: "connected" },
    ] });
    default: return reply(f.request_id, {});
  }
});
`), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, secrets, err := sidecarMCP(secretServers())
	if err != nil {
		t.Fatal(err)
	}
	r := resolved{runtime: node, runtimeArgs: []string{filepath.Join(sidecarDir, "sidecar.mjs")}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, err := r.command(ctx, sidecarConfig{Cwd: dir, ClaudePath: stub, MCPServers: servers, AllowedTools: mcpAllowedTools(secretServers()), SessionID: "00000000-0000-4000-8000-000000000001"}, nil, mcpSecretsEnv+"="+secrets)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = dir
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })

	s := &session{}
	s.conn = jsonrpc.NewConn(stdout, stdin, func(context.Context, string, json.RawMessage) (any, error) { return nil, nil }, func(string, json.RawMessage) {})

	requests := func(subtype string) []map[string]any {
		data, _ := os.ReadFile(record)
		var out []map[string]any
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var e struct {
				Request map[string]any `json:"request"`
			}
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Request != nil && e.Request["subtype"] == subtype {
				out = append(out, e.Request)
			}
		}
		return out
	}
	waitFor := func(subtype string, n int) []map[string]any {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if got := requests(subtype); len(got) >= n {
				return got
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("no %d× %s reached Claude Code", n, subtype)
		return nil
	}

	// At start: the full set, values merged, over stdin.
	set := waitFor("mcp_set_servers", 1)[0]["servers"].(map[string]any)
	cloud := set["cloud"].(map[string]any)
	if cloud["headers"].(map[string]any)["Authorization"] != headerSecret || cloud["type"] != "http" {
		t.Errorf("cloud = %v", cloud)
	}
	if set["omniplex"].(map[string]any)["env"].(map[string]any)["OMNIPLEX_AGENT_TOKEN"] != envSecret {
		t.Errorf("omniplex = %v", set["omniplex"])
	}
	if _, ok := set["open"]; !ok {
		t.Error("a server without values was dropped")
	}

	// Never on Claude Code's argv or in its environment.
	data, _ := os.ReadFile(record)
	var first struct {
		Argv []string          `json:"argv"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &first); err != nil {
		t.Fatal(err)
	}
	for _, arg := range first.Argv {
		if strings.Contains(arg, "sekrit") {
			t.Errorf("Claude Code's argv carries a secret: %s", arg)
		}
	}
	for k, v := range first.Env {
		if k == mcpSecretsEnv || strings.Contains(v, "sekrit") {
			t.Errorf("Claude Code's environment carries %s", k)
		}
	}
	if !strings.Contains(strings.Join(first.Argv, " "), "mcp__omniplex__show_file") {
		t.Error("omniplex's tool is no longer pre-approved")
	}

	// Status comes back mapped, without the config the SDK attaches.
	st, err := s.MCPStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []adapter.MCPServerStatus{{Name: "cloud", Status: "needs_auth"}, {Name: "omniplex", Status: "connected"}}; !reflect.DeepEqual(st, want) {
		t.Errorf("status = %+v", st)
	}

	// An unchanged definition is reconnected in place.
	if err := s.ReconnectMCP(ctx, secretServers()[1]); err != nil {
		t.Fatal(err)
	}
	if got := waitFor("mcp_reconnect", 1); got[0]["serverName"] != "cloud" {
		t.Errorf("reconnect = %v", got[0])
	}
	if n := len(requests("mcp_set_servers")); n != 1 {
		t.Errorf("an unchanged definition was handed over again (%d sets)", n)
	}

	// A fresh token swaps the definition in and keeps every other server.
	fresh := secretServers()[1]
	fresh.Headers = map[string]string{"Authorization": "Bearer fresh"}
	if err := s.ReconnectMCP(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	set = waitFor("mcp_set_servers", 2)[1]["servers"].(map[string]any)
	if set["cloud"].(map[string]any)["headers"].(map[string]any)["Authorization"] != "Bearer fresh" {
		t.Errorf("cloud = %v", set["cloud"])
	}
	if set["omniplex"].(map[string]any)["env"].(map[string]any)["OMNIPLEX_AGENT_TOKEN"] != envSecret || set["open"] == nil {
		t.Errorf("other servers changed: %v", set)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeFixture lays out a config dir with user servers, one enabled plugin
// (manifest pointing at a file), one enabled plugin with the default
// .mcp.json, and one installed but disabled.
func claudeFixture(t *testing.T, claudeJSON, configDir string) {
	t.Helper()
	writeJSON(t, claudeJSON, map[string]any{
		"mcpServers": map[string]any{
			"remote": map[string]any{"type": "http", "url": "https://r.example.test/mcp", "headers": map[string]string{"X-Key": "v"}},
			"local":  map[string]any{"command": "npx", "args": []string{"srv"}, "env": map[string]string{"K": "V"}},
		},
		"projects": map[string]any{"/p": map[string]any{"mcpServers": map[string]any{"projonly": map[string]any{"command": "x"}}}},
	})
	plugins := filepath.Join(configDir, "plugins", "cache")
	cf := filepath.Join(plugins, "cloudflare", "1.0.0")
	writeJSON(t, filepath.Join(cf, ".claude-plugin", "plugin.json"), map[string]any{"name": "cloudflare", "mcpServers": "./servers.json"})
	writeJSON(t, filepath.Join(cf, "servers.json"), map[string]any{"mcpServers": map[string]any{"cloudflare": map[string]any{"type": "http", "url": "https://mcp.cf.test/mcp"}}})
	tool := filepath.Join(plugins, "tool", "2.0.0")
	writeJSON(t, filepath.Join(tool, ".mcp.json"), map[string]any{"tooly": map[string]any{"command": "${CLAUDE_PLUGIN_ROOT}/bin/tooly", "args": []string{"${CLAUDE_PLUGIN_ROOT}/cfg"}, "env": map[string]string{"CONFIG_DIR": "${CLAUDE_PLUGIN_ROOT}/config"}}})
	off := filepath.Join(plugins, "off", "1.0.0")
	writeJSON(t, filepath.Join(off, ".mcp.json"), map[string]any{"offserver": map[string]any{"command": "x"}})
	writeJSON(t, filepath.Join(configDir, "plugins", "installed_plugins.json"), map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"cloudflare@cloudflare": []any{map[string]any{"scope": "user", "installPath": cf}},
			"tool@market":           []any{map[string]any{"scope": "project", "projectPath": "/p", "installPath": "/nowhere"}, map[string]any{"scope": "user", "installPath": tool}},
			"off@market":            []any{map[string]any{"scope": "user", "installPath": off}},
		},
	})
	writeJSON(t, filepath.Join(configDir, "settings.json"), map[string]any{"enabledPlugins": map[string]bool{"cloudflare@cloudflare": true, "tool@market": true, "off@market": false}})
}

func foundByName(found []adapter.ConfiguredMCPServer) map[string]adapter.ConfiguredMCPServer {
	out := map[string]adapter.ConfiguredMCPServer{}
	for _, f := range found {
		out[f.Name] = f
	}
	return out
}

func TestConfiguredMCPServersUnderHome(t *testing.T) {
	home := t.TempDir()
	claudeFixture(t, filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude"))

	found, err := (&Adapter{}).ConfiguredMCPServers(context.Background(), map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": ""})
	if err != nil {
		t.Fatal(err)
	}
	got := foundByName(found)
	if len(got) != 4 {
		t.Fatalf("found %v", found)
	}
	if r := got["remote"]; r.URL != "https://r.example.test/mcp" || r.Headers["X-Key"] != "v" || r.Origin != "User settings" {
		t.Errorf("remote = %+v", r)
	}
	if l := got["local"]; l.Command != "npx" || l.Env["K"] != "V" || !reflect.DeepEqual(l.Args, []string{"srv"}) {
		t.Errorf("local = %+v", l)
	}
	if c := got["cloudflare"]; c.URL != "https://mcp.cf.test/mcp" || c.Origin != "Plugin cloudflare" {
		t.Errorf("cloudflare = %+v", c)
	}
	root := filepath.Join(home, ".claude", "plugins", "cache", "tool", "2.0.0")
	if tl := got["tooly"]; tl.Command != root+"/bin/tooly" || tl.Args[0] != root+"/cfg" || tl.Env["CONFIG_DIR"] != root+"/config" || tl.Origin != "Plugin tool" {
		t.Errorf("tooly = %+v", tl)
	}
	for _, absent := range []string{"projonly", "offserver"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s listed", absent)
		}
	}
}

// CLAUDE_CONFIG_DIR moves .claude.json into the config dir with everything
// else, and the home dir's own files are not read.
func TestConfiguredMCPServersConfigDirOverride(t *testing.T) {
	home := t.TempDir()
	claudeFixture(t, filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude"))
	other := t.TempDir()
	writeJSON(t, filepath.Join(other, ".claude.json"), map[string]any{"mcpServers": map[string]any{"elsewhere": map[string]any{"url": "https://e.test"}}})

	found, err := (&Adapter{}).ConfiguredMCPServers(context.Background(), map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": other})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "elsewhere" {
		t.Fatalf("found %+v", found)
	}
}

func TestConfiguredMCPServersMissingFiles(t *testing.T) {
	found, err := (&Adapter{}).ConfiguredMCPServers(context.Background(), map[string]string{"HOME": t.TempDir(), "CLAUDE_CONFIG_DIR": ""})
	if err != nil || len(found) != 0 {
		t.Fatalf("found %v, %v", found, err)
	}
}
