package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/skills"
)

// mcpSecretsEnv carries every MCP server's header and env values from here to
// the bridge, as JSON {name: {headers, env}}. The bridge reads it, removes it
// from its own environment, and never passes it on to Claude Code.
const mcpSecretsEnv = "OMNIPLEX_MCP_SECRETS"

// sdkMCPServer is one server as the bridge receives it on argv: where it is,
// never what it authenticates with.
type sdkMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
}

type mcpSecret struct {
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// sidecarMCP splits the session's servers into what may go on argv and the
// JSON of what may not ("" when there is nothing secret).
func sidecarMCP(servers []adapter.MCPServer) (map[string]sdkMCPServer, string, error) {
	if len(servers) == 0 {
		return nil, "", nil
	}
	out := make(map[string]sdkMCPServer, len(servers))
	secrets := map[string]mcpSecret{}
	for _, m := range servers {
		if m.URL != "" {
			out[m.Name] = sdkMCPServer{Type: "http", URL: m.URL}
			if len(m.Headers) > 0 {
				secrets[m.Name] = mcpSecret{Headers: m.Headers}
			}
			continue
		}
		out[m.Name] = sdkMCPServer{Type: "stdio", Command: m.Command, Args: m.Args}
		if len(m.Env) > 0 {
			secrets[m.Name] = mcpSecret{Env: m.Env}
		}
	}
	if len(secrets) == 0 {
		return out, "", nil
	}
	blob, err := json.Marshal(secrets)
	if err != nil {
		return nil, "", err
	}
	return out, string(blob), nil
}

// mcpAllowedTools pre-approves the tools each server declares.
func mcpAllowedTools(servers []adapter.MCPServer) []string {
	var out []string
	for _, m := range servers {
		for _, t := range m.Tools {
			out = append(out, "mcp__"+m.Name+"__"+t)
		}
	}
	return out
}

// mcpToolTimeoutEnv is how long Claude Code waits on any MCP tool call, in
// milliseconds. It is one setting for every server.
const mcpToolTimeoutEnv = "MCP_TOOL_TIMEOUT"

// mcpToolTimeout is the MCP_TOOL_TIMEOUT=ms assignment that lets the slowest
// server's calls finish, or "" when no server asks for more than Claude
// Code's default or the user already set it at least that high.
func mcpToolTimeout(servers []adapter.MCPServer, overlay map[string]string) string {
	var longest time.Duration
	for _, m := range servers {
		longest = max(longest, m.ToolTimeout)
	}
	if longest <= 0 {
		return ""
	}
	have, ok := overlay[mcpToolTimeoutEnv]
	if !ok {
		have = os.Getenv(mcpToolTimeoutEnv)
	}
	if ms, err := strconv.ParseInt(strings.TrimSpace(have), 10, 64); err == nil && ms >= longest.Milliseconds() {
		return ""
	}
	return mcpToolTimeoutEnv + "=" + strconv.FormatInt(longest.Milliseconds(), 10)
}

// sdkServerConfig is a server's whole definition as the SDK takes it, values
// included. It only ever crosses the bridge's stdin.
func sdkServerConfig(m adapter.MCPServer) map[string]any {
	if m.URL != "" {
		cfg := map[string]any{"type": "http", "url": m.URL}
		if len(m.Headers) > 0 {
			cfg["headers"] = m.Headers
		}
		return cfg
	}
	cfg := map[string]any{"type": "stdio", "command": m.Command}
	if len(m.Args) > 0 {
		cfg["args"] = m.Args
	}
	if len(m.Env) > 0 {
		cfg["env"] = m.Env
	}
	return cfg
}

// MCPTransports: Claude Code runs local servers and streamable-HTTP ones.
func (a *Adapter) MCPTransports() []string { return []string{"stdio", "http"} }

// ConfiguredMCPServers lists the servers Claude Code itself is configured
// with for every project: the user's own (top-level mcpServers in
// .claude.json) and those of user-scoped plugins that are enabled. Servers
// scoped to one folder are ProjectMCPServers.
func (a *Adapter) ConfiguredMCPServers(ctx context.Context, env map[string]string) ([]adapter.ConfiguredMCPServer, error) {
	configDir := skills.DefaultRoots(envHome(env), env, "").ClaudeConfigDir
	var out []adapter.ConfiguredMCPServer
	userFile := claudeJSON(env)
	if data, err := os.ReadFile(userFile); err == nil {
		var doc struct {
			MCPServers map[string]claudeServerJSON `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("read %s: %w", userFile, err)
		}
		out = append(out, configured(doc.MCPServers, "User settings", "")...)
	}
	out = append(out, pluginServers(configDir)...)
	return out, nil
}

// ProjectMCPServers lists the servers Claude Code adds for a session in dir:
// the folder's .mcp.json, and its local scope in .claude.json
// (projects.<path>.mcpServers). Claude keys the local scope by the main
// checkout's git root, so a worktree shares its repo's entry; an entry under
// dir itself is read too, for the names the root's entry lacks.
func (a *Adapter) ProjectMCPServers(ctx context.Context, env map[string]string, dir string) ([]adapter.ConfiguredMCPServer, error) {
	if dir == "" {
		return nil, nil
	}
	// The local scope comes first: Claude prefers it to .mcp.json, so where
	// both define a server the local one is what a session gets, and what
	// adding it should copy.
	var out []adapter.ConfiguredMCPServer
	userFile := claudeJSON(env)
	if data, err := os.ReadFile(userFile); err == nil {
		var doc struct {
			Projects map[string]struct {
				MCPServers map[string]claudeServerJSON `json:"mcpServers"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("read %s: %w", userFile, err)
		}
		local := map[string]claudeServerJSON{}
		for _, key := range []string{filepath.Clean(dir), gitRoot(ctx, dir)} {
			for name, s := range doc.Projects[key].MCPServers {
				local[name] = s
			}
		}
		out = append(out, configured(local, "Local settings", "")...)
	}

	repoFile := filepath.Join(dir, ".mcp.json")
	if data, err := os.ReadFile(repoFile); err == nil {
		var doc struct {
			MCPServers map[string]claudeServerJSON `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("read %s: %w", repoFile, err)
		}
		out = append(out, configured(doc.MCPServers, ".mcp.json", "")...)
	}
	return out, nil
}

// claudeJSON is the user's .claude.json: in the config dir when
// CLAUDE_CONFIG_DIR moves it, and in the home dir otherwise.
func claudeJSON(env map[string]string) string {
	home := envHome(env)
	if v, ok := lookupEnv(env, "CLAUDE_CONFIG_DIR"); ok && strings.TrimSpace(v) != "" {
		return filepath.Join(skills.DefaultRoots(home, env, "").ClaudeConfigDir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// gitRoot is the root of the main checkout dir belongs to, a worktree's
// included, or dir itself outside a git repo.
func gitRoot(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	common := filepath.Clean(strings.TrimSpace(string(out)))
	if err != nil || filepath.Base(common) != ".git" {
		return filepath.Clean(dir)
	}
	return filepath.Dir(common)
}

// claudeServerJSON is one server in Claude's config files.
type claudeServerJSON struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// configured turns a file's servers into the found list, sorted by name.
// pluginRoot, when set, stands in for ${CLAUDE_PLUGIN_ROOT}.
func configured(servers map[string]claudeServerJSON, origin, pluginRoot string) []adapter.ConfiguredMCPServer {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]adapter.ConfiguredMCPServer, 0, len(names))
	expand := func(s string) string {
		if pluginRoot == "" {
			return s
		}
		return strings.ReplaceAll(s, "${CLAUDE_PLUGIN_ROOT}", pluginRoot)
	}
	for _, name := range names {
		s := servers[name]
		m := adapter.MCPServer{Name: name}
		if s.URL != "" {
			m.URL, m.Headers = s.URL, s.Headers
		} else if s.Command != "" {
			m.Command = expand(s.Command)
			for _, arg := range s.Args {
				m.Args = append(m.Args, expand(arg))
			}
			if len(s.Env) > 0 {
				m.Env = make(map[string]string, len(s.Env))
				for k, v := range s.Env {
					m.Env[k] = expand(v)
				}
			}
		} else {
			continue
		}
		out = append(out, adapter.ConfiguredMCPServer{MCPServer: m, Origin: origin})
	}
	return out
}

// pluginServers reads the MCP servers of each user-scoped plugin that the
// config dir's settings.json enables. A plugin names its servers in
// .claude-plugin/plugin.json ("mcpServers": a file, a list of files, or the
// servers inline) or, by default, in .mcp.json at its root.
func pluginServers(configDir string) []adapter.ConfiguredMCPServer {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if data, err := os.ReadFile(filepath.Join(configDir, "settings.json")); err != nil || json.Unmarshal(data, &settings) != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil {
		return nil
	}
	keys := make([]string, 0, len(installed.Plugins))
	for key := range installed.Plugins {
		if settings.EnabledPlugins[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var out []adapter.ConfiguredMCPServer
	for _, key := range keys {
		name, _, _ := strings.Cut(key, "@")
		for _, install := range installed.Plugins[key] {
			if install.Scope != "user" || install.InstallPath == "" {
				continue
			}
			out = append(out, configured(pluginManifestServers(install.InstallPath), "Plugin "+name, install.InstallPath)...)
			break
		}
	}
	return out
}

func pluginManifestServers(root string) map[string]claudeServerJSON {
	var manifest struct {
		MCPServers json.RawMessage `json:"mcpServers"`
	}
	if data, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json")); err == nil {
		_ = json.Unmarshal(data, &manifest)
	}
	out := map[string]claudeServerJSON{}
	var add func(raw json.RawMessage)
	add = func(raw json.RawMessage) {
		var path string
		if json.Unmarshal(raw, &path) == nil {
			if data, err := os.ReadFile(filepath.Join(root, path)); err == nil {
				for name, s := range mcpFile(data) {
					out[name] = s
				}
			}
			return
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			for _, item := range list {
				add(item)
			}
			return
		}
		for name, s := range mcpFile(raw) {
			out[name] = s
		}
	}
	if len(manifest.MCPServers) == 0 || string(manifest.MCPServers) == "null" {
		add(json.RawMessage(`".mcp.json"`))
	} else {
		add(manifest.MCPServers)
	}
	return out
}

// mcpFile reads an .mcp.json: {"mcpServers": {...}}, or the servers bare.
func mcpFile(data []byte) map[string]claudeServerJSON {
	var wrapped struct {
		MCPServers map[string]claudeServerJSON `json:"mcpServers"`
	}
	if json.Unmarshal(data, &wrapped) == nil && wrapped.MCPServers != nil {
		return wrapped.MCPServers
	}
	var bare map[string]claudeServerJSON
	if json.Unmarshal(data, &bare) != nil {
		return nil
	}
	return bare
}

// lookupEnv reads a variable the way the harness would see it: the instance's
// overlay wins, even when empty, over the process environment.
func lookupEnv(env map[string]string, key string) (string, bool) {
	if v, ok := env[key]; ok {
		return v, true
	}
	return os.LookupEnv(key)
}

func envHome(env map[string]string) string {
	if v, ok := lookupEnv(env, "HOME"); ok && strings.TrimSpace(v) != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return home
}

// MCPStatus is the harness's own report on each server it has.
func (s *session) MCPStatus(ctx context.Context) ([]adapter.MCPServerStatus, error) {
	var res struct {
		Servers []adapter.MCPServerStatus `json:"servers"`
	}
	if err := s.conn.Call(ctx, "mcpStatus", map[string]any{}, &res); err != nil {
		return nil, err
	}
	return mcpStatuses(res.Servers), nil
}

// mcpStatuses maps the SDK's statuses onto omniplex's. Anything the SDK adds
// later reads as failed rather than as a word the client does not know.
func mcpStatuses(in []adapter.MCPServerStatus) []adapter.MCPServerStatus {
	out := make([]adapter.MCPServerStatus, 0, len(in))
	for _, st := range in {
		switch st.Status {
		case "connected", "failed", "pending", "disabled":
		case "needs-auth":
			st.Status = "needs_auth"
		default:
			if st.Error == "" {
				st.Error = "unknown status " + st.Status
			}
			st.Status = "failed"
		}
		out = append(out, st)
	}
	return out
}

// ReconnectMCP hands the session a server's current definition, a fresh token
// included, and has it reconnect. The definition crosses the bridge's stdin,
// never a command line.
func (s *session) ReconnectMCP(ctx context.Context, server adapter.MCPServer) error {
	return s.conn.Call(ctx, "reconnectMcp", map[string]any{
		"name":   server.Name,
		"server": sdkServerConfig(server),
	}, nil)
}

var (
	_ adapter.MCPHost    = (*Adapter)(nil)
	_ adapter.MCPControl = (*session)(nil)
)
