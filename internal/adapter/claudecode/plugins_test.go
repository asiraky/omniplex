package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// startStandIn starts a session on a stand-in bridge that records its config
// argument and the MCP_TOOL_TIMEOUT it was started with, then exits.
func startStandIn(t *testing.T, o adapter.CreateOptions) (cfg sidecarConfig, timeout string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stand-in bridge is a shell script")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("HOME", dir)
	claude := filepath.Join(dir, "claude")
	bridge := filepath.Join(dir, "bridge")
	for path, body := range map[string]string{
		claude: "#!/bin/sh\necho 2.0.0\n",
		bridge: "#!/bin/sh\nfor a; do :; done\nprintf '%s' \"$MCP_TOOL_TIMEOUT\" > \"" + dir + "/timeout\"\nprintf '%s' \"$a\" > \"" + dir + "/config\"\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	o.ThreadID, o.Cwd = "t", dir
	a := &Adapter{ClaudePath: claude, bundledSidecar: bridge}
	s, err := a.CreateSession(context.Background(), &fakeHost{}, o)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-s.Events():
		case <-deadline:
			t.Fatal("stand-in bridge never exited")
		}
	}
	_ = s.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "timeout"))
	return cfg, string(got)
}

// The bundled plugin reaches the SDK as a session-only local plugin.
func TestPluginsReachTheBridge(t *testing.T) {
	cfg, _ := startStandIn(t, adapter.CreateOptions{Plugins: []string{"/data/plugin"}})
	if len(cfg.Plugins) != 1 || cfg.Plugins[0] != (sdkPlugin{Type: "local", Path: "/data/plugin"}) {
		t.Fatalf("plugins = %+v", cfg.Plugins)
	}
}

// MCP_TOOL_TIMEOUT is global in Claude Code, so it is the longest any server
// asks for, and Claude Code reads it from the bridge's environment.
func TestToolTimeoutReachesClaudeEnv(t *testing.T) {
	t.Setenv("MCP_TOOL_TIMEOUT", "")
	_, timeout := startStandIn(t, adapter.CreateOptions{MCPServers: []adapter.MCPServer{
		{Name: "a", Command: "a", ToolTimeout: time.Minute},
		{Name: "omniplex", Command: "omniplex", ToolTimeout: 30 * time.Minute},
		{Name: "b", Command: "b"},
	}})
	if timeout != "1800000" {
		t.Fatalf("MCP_TOOL_TIMEOUT = %q, want 1800000", timeout)
	}
}

func TestMCPToolTimeout(t *testing.T) {
	servers := []adapter.MCPServer{{Name: "omniplex", ToolTimeout: 30 * time.Minute}}
	for _, tc := range []struct {
		name    string
		servers []adapter.MCPServer
		ambient string
		overlay map[string]string
		want    string
	}{
		{"none asks", []adapter.MCPServer{{Name: "x"}}, "", nil, ""},
		{"set", servers, "", nil, "MCP_TOOL_TIMEOUT=1800000"},
		// The user's own longer setting stands; a shorter one would cut
		// omniplex's calls off.
		{"user longer", servers, "3600000", nil, ""},
		{"user shorter", servers, "60000", nil, "MCP_TOOL_TIMEOUT=1800000"},
		{"instance longer", servers, "60000", map[string]string{"MCP_TOOL_TIMEOUT": "3600000"}, ""},
		{"garbage", servers, "soon", nil, "MCP_TOOL_TIMEOUT=1800000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_TOOL_TIMEOUT", tc.ambient)
			if got := mcpToolTimeout(tc.servers, tc.overlay); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
