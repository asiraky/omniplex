package codexapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/asiraky/omniplex/internal/adapter"
)

// overrides reads -c key=value pairs back the way codex does: each value is
// TOML under a dotted key.
func overrides(t *testing.T, args []string) map[string]any {
	t.Helper()
	var doc strings.Builder
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) {
			doc.WriteString(args[i+1] + "\n")
			i++
		}
	}
	out := map[string]any{}
	if _, err := toml.Decode(doc.String(), &out); err != nil {
		t.Fatalf("overrides are not TOML: %v\n%s", err, doc.String())
	}
	return out
}

func server(t *testing.T, cfg map[string]any, name string) map[string]any {
	t.Helper()
	all, _ := cfg["mcp_servers"].(map[string]any)
	s, ok := all[name].(map[string]any)
	if !ok {
		t.Fatalf("no server %q in %v", name, cfg)
	}
	return s
}

func noSecretOn(t *testing.T, argv []string, secrets ...string) {
	t.Helper()
	joined := strings.Join(argv, "\x00")
	for _, s := range secrets {
		if strings.Contains(joined, s) {
			t.Fatalf("argv carries %q: %q", s, argv)
		}
	}
}

func TestMCPConfigKeepsHeaderValuesOffArgv(t *testing.T) {
	args, env, refused := mcpConfig([]adapter.MCPServer{{
		Name: "remote",
		URL:  "https://mcp.example/mcp",
		Headers: map[string]string{
			"Authorization": "Bearer tok-sekrit",
			"X-Api-Key":     "key-sekrit",
		},
	}, {
		Name: "basic",
		URL:  "https://other.example/mcp",
		// Not a bearer token: still a header, still off argv.
		Headers: map[string]string{"authorization": "Basic basic-sekrit"},
	}})
	if len(refused) != 0 {
		t.Fatalf("refused %v", refused)
	}
	noSecretOn(t, args, "tok-sekrit", "key-sekrit", "basic-sekrit")

	cfg := overrides(t, args)
	remote := server(t, cfg, "remote")
	if remote["url"] != "https://mcp.example/mcp" {
		t.Fatalf("url = %v", remote["url"])
	}
	if v := env[remote["bearer_token_env_var"].(string)]; v != "tok-sekrit" {
		t.Fatalf("bearer var carries %q", v)
	}
	headers := remote["env_http_headers"].(map[string]any)
	if v := env[headers["X-Api-Key"].(string)]; v != "key-sekrit" {
		t.Fatalf("header var carries %q", v)
	}
	if _, ok := headers["Authorization"]; ok {
		t.Fatal("the bearer header was passed twice")
	}

	basic := server(t, cfg, "basic")
	if _, ok := basic["bearer_token_env_var"]; ok {
		t.Fatal("a Basic header was taken for a bearer token")
	}
	if v := env[basic["env_http_headers"].(map[string]any)["authorization"].(string)]; v != "Basic basic-sekrit" {
		t.Fatalf("header var carries %q", v)
	}
	for name := range env {
		// The default shell environment policy hides *TOKEN* from the agent.
		if !strings.HasSuffix(name, "_TOKEN") {
			t.Fatalf("%s would reach the agent's shell", name)
		}
	}
}

// TestMCPConfigStdioServerSeesItsOwnEnv runs the generated stdio definition
// the way codex would, passing through only the env_vars it names, and checks
// the server gets its variables under their real names, its argv intact.
func TestMCPConfigStdioServerSeesItsOwnEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the env wrapper needs sh")
	}
	args, env, refused := mcpConfig([]adapter.MCPServer{{
		Name:    "my.tool",
		Command: "/bin/sh",
		Args:    []string{"-c", `printf '%s|%s|%s|' "$GH_TOKEN" "$OTHER" "$1"; env`, "zero", "a b"},
		Env:     map[string]string{"GH_TOKEN": "gh-sekrit", "OTHER": "o'th\"er $x"},
	}, {
		Name:    "plain",
		Command: "plain-server",
		Args:    []string{"--flag"},
	}})
	if len(refused) != 0 {
		t.Fatalf("refused %v", refused)
	}
	noSecretOn(t, args, "gh-sekrit", "o'th")

	cfg := overrides(t, args)
	tool := server(t, cfg, "my.tool")
	childEnv := []string{"PATH=" + os.Getenv("PATH")}
	for _, n := range tool["env_vars"].([]any) {
		v, ok := env[n.(string)]
		if !ok {
			t.Fatalf("env_vars names %v, which codex's env does not have", n)
		}
		childEnv = append(childEnv, n.(string)+"="+v)
	}
	var argv []string
	for _, a := range tool["args"].([]any) {
		argv = append(argv, a.(string))
	}
	cmd := exec.Command(tool["command"].(string), argv...)
	cmd.Env = childEnv
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := string(out)
	if !strings.HasPrefix(got, `gh-sekrit|o'th"er $x|a b|`) {
		t.Fatalf("server saw %q", got)
	}
	if strings.Contains(got, "OMNIPLEX_MCP_") {
		t.Fatalf("generated names reached the server: %q", got)
	}

	plain := server(t, cfg, "plain")
	if plain["command"] != "plain-server" || !reflect.DeepEqual(plain["args"], []any{"--flag"}) {
		t.Fatalf("plain server = %v", plain)
	}
	if _, ok := plain["env_vars"]; ok {
		t.Fatal("a server without env got env_vars")
	}
}

func TestMCPConfigRefusesWhatCannotStayOffArgv(t *testing.T) {
	args, _, refused := mcpConfig([]adapter.MCPServer{
		{Name: "bad", Command: "srv", Env: map[string]string{"NOT-A-NAME": "bad-sekrit"}},
		{Name: "good", Command: "srv"},
	})
	if len(refused) != 1 || !strings.HasPrefix(refused[0], "bad:") {
		t.Fatalf("refused = %v", refused)
	}
	noSecretOn(t, args, "bad-sekrit")
	all := overrides(t, args)["mcp_servers"].(map[string]any)
	if _, ok := all["bad"]; ok {
		t.Fatal("a refused server was still passed")
	}
	if _, ok := all["good"]; !ok {
		t.Fatal("refusing one server dropped another")
	}
}

// TestCreateSessionPutsMCPValuesInEnv starts a stand-in codex that records
// what it was given and exits.
func TestCreateSessionPutsMCPValuesInEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"" + dir + "/argv\"\nenv > \"" + dir + "/env\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := New(bin).CreateSession(context.Background(), &elicitHost{}, adapter.CreateOptions{
		Cwd: dir,
		Env: map[string]string{"INSTANCE": "kept"},
		MCPServers: []adapter.MCPServer{
			{Name: "omniplex", Command: "omniplex", Args: []string{"mcp"}, Env: map[string]string{"OMNIPLEX_AGENT_TOKEN": "agent-sekrit"}},
			{Name: "remote", URL: "https://mcp.example/mcp", Headers: map[string]string{"Authorization": "Bearer tok-sekrit"}},
		},
	})
	if err == nil {
		t.Fatal("the stand-in exits, so the session cannot start")
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if len(argv) == 0 || len(env) == 0 {
		t.Fatal("stand-in codex did not run")
	}
	noSecretOn(t, strings.Split(string(argv), "\n"), "agent-sekrit", "tok-sekrit")
	for _, want := range []string{"=agent-sekrit\n", "=tok-sekrit\n", "INSTANCE=kept\n"} {
		if !strings.Contains(string(env), want) {
			t.Fatalf("codex env lacks %q", want)
		}
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const codexFixture = `
model = "gpt-5"

[mcp_servers.remote]
url = "https://mcp.example/mcp"
bearer_token_env_var = "REMOTE_TOKEN"
http_headers = { "X-Team" = "eng" }
env_http_headers = { "X-Api-Key" = "REMOTE_KEY" }

[mcp_servers.local]
command = "npx"
args = ["-y", "local-server"]
env = { "MODE" = "fast" }
env_vars = ["PASS_ME", { name = "PASS_TOO", source = "local" }, "UNSET_VAR"]

[mcp_servers.off]
command = "off-server"
enabled = false

[mcp_servers.empty]
startup_timeout_sec = 5
`

func TestCodexConfiguredMCPServers(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, filepath.Join(home, ".codex"), codexFixture)
	env := map[string]string{
		"HOME": home, "CODEX_HOME": "",
		"REMOTE_TOKEN": "tok", "REMOTE_KEY": "key", "PASS_ME": "p1", "PASS_TOO": "p2",
	}
	t.Setenv("UNSET_VAR", "")
	os.Unsetenv("UNSET_VAR")

	got, err := New("").ConfiguredMCPServers(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.ConfiguredMCPServer{{
		MCPServer: adapter.MCPServer{
			Name: "local", Command: "npx", Args: []string{"-y", "local-server"},
			Env: map[string]string{"MODE": "fast", "PASS_ME": "p1", "PASS_TOO": "p2"},
		},
		Origin: "config.toml",
	}, {
		MCPServer: adapter.MCPServer{
			Name: "remote", URL: "https://mcp.example/mcp",
			Headers: map[string]string{"Authorization": "Bearer tok", "X-Team": "eng", "X-Api-Key": "key"},
		},
		Origin: "config.toml",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestCodexConfiguredMCPServersCodexHomeOverride(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, filepath.Join(home, ".codex"), "[mcp_servers.fromhome]\ncommand = \"a\"\n")
	other := t.TempDir()
	writeConfig(t, other, "[mcp_servers.fromoverride]\ncommand = \"b\"\n")

	got, err := New("").ConfiguredMCPServers(context.Background(), map[string]string{"HOME": home, "CODEX_HOME": other})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "fromoverride" {
		t.Fatalf("got %+v", got)
	}
}

func TestCodexConfiguredMCPServersMissingAndBroken(t *testing.T) {
	home := t.TempDir()
	got, err := New("").ConfiguredMCPServers(context.Background(), map[string]string{"HOME": home, "CODEX_HOME": ""})
	if err != nil || len(got) != 0 {
		t.Fatalf("missing config: %+v, %v", got, err)
	}
	writeConfig(t, filepath.Join(home, ".codex"), "[mcp_servers.x\n")
	if _, err := New("").ConfiguredMCPServers(context.Background(), map[string]string{"HOME": home, "CODEX_HOME": ""}); err == nil {
		t.Fatal("a broken config.toml read as empty")
	}
}

func TestCodexStatusMapping(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		in         codexMCPStatus
		status     string
		errorMatch string
	}{
		{codexMCPStatus{RuntimeStatus: str("connected"), AuthStatus: "bearerToken"}, "connected", ""},
		{codexMCPStatus{RuntimeStatus: str("authenticationRequired")}, "needs_auth", ""},
		{codexMCPStatus{RuntimeStatus: str("starting")}, "pending", ""},
		{codexMCPStatus{RuntimeStatus: str("notStarted"), AuthStatus: "notLoggedIn"}, "needs_auth", ""},
		{codexMCPStatus{}, "pending", ""},
		{codexMCPStatus{RuntimeStatus: str("failed"), ToolsError: str("boom")}, "failed", "boom"},
		{codexMCPStatus{RuntimeStatus: str("failed"), AuthStatus: "notLoggedIn"}, "needs_auth", ""},
		{codexMCPStatus{RuntimeStatus: str("cancelled")}, "failed", ""},
		{codexMCPStatus{RuntimeStatus: str("disabled")}, "disabled", ""},
		{codexMCPStatus{RuntimeStatus: str("sideways")}, "failed", "sideways"},
	}
	for _, c := range cases {
		got := codexStatus(c.in)
		if got.Status != c.status || (c.errorMatch == "") != (got.Error == "") || !strings.Contains(got.Error, c.errorMatch) {
			t.Errorf("%+v -> %+v, want %s/%q", c.in, got, c.status, c.errorMatch)
		}
	}
}

func TestMCPStatusAsksAboutThisThread(t *testing.T) {
	conn, rec := pairedConn(t, map[string]any{
		"mcpServerStatus/list": map[string]any{"data": []any{
			map[string]any{"name": "omniplex", "authStatus": "unsupported", "runtimeStatus": "connected", "tools": map[string]any{}, "resources": []any{}, "resourceTemplates": []any{}},
		}},
	})
	s := &session{conn: conn, threadID: "thread-1"}
	got, err := s.MCPStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (adapter.MCPServerStatus{Name: "omniplex", Status: "connected"}) {
		t.Fatalf("got %+v", got)
	}
	method, params := rec.last()
	if method != "mcpServerStatus/list" || !strings.Contains(string(params), `"threadId":"thread-1"`) {
		t.Fatalf("asked %s %s", method, params)
	}
	if err := s.ReconnectMCP(context.Background(), adapter.MCPServer{Name: "omniplex"}); !errors.Is(err, adapter.ErrMCPUnsupported) {
		t.Fatalf("reconnect = %v", err)
	}
}
