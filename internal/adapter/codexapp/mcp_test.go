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
	"time"

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
	args, env, refused, _ := mcpConfig([]adapter.MCPServer{{
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
	}}, nil)
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
	args, env, refused, _ := mcpConfig([]adapter.MCPServer{{
		Name:    "my.tool",
		Command: "/bin/sh",
		Args:    []string{"-c", `printf '%s|%s|%s|' "$GH_TOKEN" "$OTHER" "$1"; env`, "zero", "a b"},
		Env:     map[string]string{"GH_TOKEN": "gh-sekrit", "OTHER": "o'th\"er $x"},
	}, {
		Name:    "plain",
		Command: "plain-server",
		Args:    []string{"--flag"},
	}}, nil)
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
	args, _, refused, _ := mcpConfig([]adapter.MCPServer{
		{Name: "bad", Command: "srv", Env: map[string]string{"NOT-A-NAME": "bad-sekrit"}},
		{Name: "good", Command: "srv"},
	}, nil)
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

// Codex merges an override into config.toml's table key by key, so a server
// sharing a name with a native one would inherit its headers. The native one
// is turned off and ours goes in under a key nothing else uses.
func TestMCPConfigShadowsANativeServerOfTheSameName(t *testing.T) {
	args, _, _, keys := mcpConfig([]adapter.MCPServer{
		{Name: "x", URL: "https://new.example/mcp"},
		{Name: "y", URL: "https://y.example/mcp"},
	}, []string{"x", "x-omniplex", "z"})
	cfg := overrides(t, args)
	all := cfg["mcp_servers"].(map[string]any)
	if x := server(t, cfg, "x"); x["enabled"] != false || len(x) != 1 {
		t.Fatalf("native x = %v; want only enabled=false", x)
	}
	if got := server(t, cfg, "x-omniplex-2")["url"]; got != "https://new.example/mcp" {
		t.Fatalf("renamed x url = %v", got)
	}
	if got := server(t, cfg, "y")["url"]; got != "https://y.example/mcp" {
		t.Fatalf("y url = %v", got)
	}
	if _, ok := all["z"]; ok {
		t.Fatal("an unrelated native server was touched")
	}
	want := map[string]string{"x-omniplex-2": "x", "y": "y"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
}

func TestMCPStatusReportsARenamedServerUnderItsName(t *testing.T) {
	entry := func(name, status string) map[string]any {
		return map[string]any{"name": name, "authStatus": "unsupported", "runtimeStatus": status}
	}
	conn, _ := pairedConn(t, map[string]any{
		"mcpServerStatus/list": map[string]any{"data": []any{
			entry("x", "disabled"), entry("x-omniplex", "connected"), entry("y", "connected"), entry("z", "failed"),
		}},
	})
	s := &session{conn: conn, threadID: "t", mcpKeys: map[string]string{"x-omniplex": "x", "y": "y"}, mcpOurs: map[string]bool{"x": true, "y": true}}
	got, err := s.MCPStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, st := range got {
		names = append(names, st.Name+"="+st.Status)
	}
	if want := []string{"x=connected", "y=connected", "z=failed"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
}

// A session deep in a repo gets every .codex/config.toml from the repo root
// down: a deeper layer's server replaces a shallower one of the same name,
// a disabled one is left out, and nothing above the root is read.
func TestCodexProjectMCPServers(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(home, "work")
	repo := filepath.Join(outside, "repo")
	dir := filepath.Join(repo, "pkg", "svc")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(outside, ".codex"), "[mcp_servers.above]\ncommand = \"no\"\n")
	writeConfig(t, filepath.Join(repo, ".codex"), `
[mcp_servers.db]
command = "db"
args = ["--root"]

[mcp_servers.remote]
url = "https://r.example/mcp"
bearer_token_env_var = "R_TOKEN"

[mcp_servers.quiet]
command = "q"
`)
	writeConfig(t, filepath.Join(dir, ".codex"), `
[mcp_servers.db]
command = "db"
args = ["--svc"]

[mcp_servers.quiet]
command = "q"
enabled = false

[mcp_servers.remote.http_headers]
X-Team = "svc"
`)
	env := map[string]string{"HOME": home, "CODEX_HOME": "", "R_TOKEN": "tok"}

	got, err := New("").ProjectMCPServers(context.Background(), env, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []adapter.ConfiguredMCPServer{{
		MCPServer: adapter.MCPServer{Name: "db", Command: "db", Args: []string{"--svc"}},
		Origin:    ".codex/config.toml",
	}, {
		// The svc layer adds a header to the root's server, which keeps its
		// URL and token.
		MCPServer: adapter.MCPServer{Name: "remote", URL: "https://r.example/mcp", Headers: map[string]string{"Authorization": "Bearer tok", "X-Team": "svc"}},
		Origin:    ".codex/config.toml",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	writeConfig(t, filepath.Join(dir, ".codex"), "[mcp_servers.x\n")
	if _, err := New("").ProjectMCPServers(context.Background(), env, dir); err == nil {
		t.Fatal("a broken layer read as empty")
	}
}

// Without a .git above it, the folder is its own project root. The codex home
// is the user's config, not a project layer, even in the home folder.
func TestCodexProjectMCPServersWithoutARepo(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, filepath.Join(home, ".codex"), "[mcp_servers.user]\ncommand = \"u\"\n")
	env := map[string]string{"HOME": home, "CODEX_HOME": ""}
	if got, err := New("").ProjectMCPServers(context.Background(), env, home); err != nil || len(got) != 0 {
		t.Fatalf("home folder: %+v, %v", got, err)
	}
	dir := filepath.Join(home, "plain")
	writeConfig(t, filepath.Join(dir, ".codex"), "[mcp_servers.here]\ncommand = \"h\"\n")
	got, err := New("").ProjectMCPServers(context.Background(), env, dir)
	if err != nil || len(got) != 1 || got[0].Name != "here" {
		t.Fatalf("plain folder: %+v, %v", got, err)
	}
}

// A server in the repo's own .codex/config.toml is one codex already has, so
// ours of the same name goes in under another key rather than merging into it.
func TestCreateSessionShadowsARepoServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"" + home + "/argv\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	for _, d := range []string{".git", "sub"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, filepath.Join(repo, ".codex"), "[mcp_servers.linear]\nurl = \"https://repo.example/mcp\"\n")
	_, err := New(bin).CreateSession(context.Background(), &elicitHost{}, adapter.CreateOptions{
		Cwd:        filepath.Join(repo, "sub"),
		Env:        map[string]string{"HOME": home, "CODEX_HOME": ""},
		MCPServers: []adapter.MCPServer{{Name: "linear", URL: "https://ours.example/mcp"}},
	})
	if err == nil {
		t.Fatal("the stand-in exits, so the session cannot start")
	}
	argv, _ := os.ReadFile(filepath.Join(home, "argv"))
	if len(argv) == 0 {
		t.Fatal("stand-in codex did not run")
	}
	cfg := overrides(t, strings.Split(strings.TrimSpace(string(argv)), "\n"))
	if x := server(t, cfg, "linear"); x["enabled"] != false || len(x) != 1 {
		t.Fatalf("repo linear = %v; want only enabled=false", x)
	}
	if got := server(t, cfg, "linear-omniplex")["url"]; got != "https://ours.example/mcp" {
		t.Fatalf("our linear url = %v", got)
	}
}

// A server that waits on a person gets its own tool timeout, under whatever
// key it ends up with; the others keep codex's default.
func TestMCPConfigToolTimeout(t *testing.T) {
	args, _, _, keys := mcpConfig([]adapter.MCPServer{
		{Name: "omniplex", Command: "/bin/omniplex", Args: []string{"mcp"}, ToolTimeout: 30 * time.Minute},
		{Name: "linear", URL: "https://mcp.linear.app/mcp", ToolTimeout: 90 * time.Second},
		{Name: "plain", Command: "plain"},
	}, []string{"linear"})
	cfg := overrides(t, args)
	if v := server(t, cfg, "omniplex")["tool_timeout_sec"]; v != 1800.0 {
		t.Fatalf("omniplex tool_timeout_sec = %v (%T)", v, v)
	}
	var shadow string
	for key, name := range keys {
		if name == "linear" {
			shadow = key
		}
	}
	if shadow == "linear" {
		t.Fatalf("linear was not moved off the native key: %v", keys)
	}
	if v := server(t, cfg, shadow)["tool_timeout_sec"]; v != 90.0 {
		t.Fatalf("%s tool_timeout_sec = %v", shadow, v)
	}
	if _, ok := server(t, cfg, "plain")["tool_timeout_sec"]; ok {
		t.Fatal("a server without a timeout got one")
	}
}
