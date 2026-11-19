package piapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// launched starts a session against the fake pi and returns its argv and the
// OMNIPLEX_PI_MCP it was given.
func launched(t *testing.T, servers []adapter.MCPServer) (args []string, mcp string) {
	t.Helper()
	dir := t.TempDir()
	s := startSession(t, dir, adapter.CreateOptions{ThreadID: "sess-1", MCPServers: servers})
	drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.ThreadConfigChanged) })
	raw, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "mcp"))
	return strings.Split(strings.TrimSpace(string(raw)), "\n"), string(env)
}

func TestStdioServersReachPiThroughTheBridge(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	args, mcp := launched(t, []adapter.MCPServer{
		{Name: "omniplex", Command: "/bin/omniplex", Args: []string{"mcp"}, Env: map[string]string{"OMNIPLEX_AGENT_TOKEN": "secret-tok"}},
		{Name: "remote", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer h"}},
	})

	i := slices.Index(args, "--extension")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("argv has no --extension; got %q", args)
	}
	if info, err := os.Stat(args[i+1]); err != nil || info.Size() == 0 {
		t.Fatalf("extension %q was not written: %v", args[i+1], err)
	}
	for _, a := range args {
		if strings.Contains(a, "secret-tok") {
			t.Fatalf("a server's env value reached argv: %q", args)
		}
	}

	var got []bridgedServer
	if err := json.Unmarshal([]byte(mcp), &got); err != nil {
		t.Fatalf("OMNIPLEX_PI_MCP = %q: %v", mcp, err)
	}
	if len(got) != 1 || got[0].Name != "omniplex" || got[0].Command != "/bin/omniplex" ||
		!slices.Equal(got[0].Args, []string{"mcp"}) || got[0].Env["OMNIPLEX_AGENT_TOKEN"] != "secret-tok" {
		t.Fatalf("bridged servers = %+v, want only the stdio one, whole", got)
	}
}

func TestNoBridgeWithoutStdioServers(t *testing.T) {
	// A value the server process inherited is nobody's session's.
	t.Setenv(mcpBridgeEnv, `[{"name":"stale","command":"x"}]`)
	for name, servers := range map[string][]adapter.MCPServer{
		"none":     nil,
		"url only": {{Name: "remote", URL: "https://example.com/mcp"}},
	} {
		args, mcp := launched(t, servers)
		if slices.Contains(args, "--extension") {
			t.Errorf("%s: argv loads the bridge: %q", name, args)
		}
		if mcp != "" {
			t.Errorf("%s: pi got %s=%q", name, mcpBridgeEnv, mcp)
		}
	}
}

func TestBridgedToolTitle(t *testing.T) {
	path := json.RawMessage(`{"path":"a.md"}`)
	if got := toolTitle("mcp__omniplex__show_file", path); got != "omniplex/show_file" {
		t.Errorf("bridged tool title = %q, want omniplex/show_file", got)
	}
	// Not a bridged name: titled like any other tool.
	if got := toolTitle("mcp__show_file", path); got != "mcp__show_file a.md" {
		t.Errorf("toolTitle(mcp__show_file) = %q", got)
	}
}
