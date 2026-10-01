package codexapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/skills"
)

// mcpConfig adds the session's MCP servers to this app-server run as config
// overrides, leaving the user's config.toml alone. Only names go on argv:
// every header and env value travels in codex's own environment under a
// generated name, and the config says which name to read.
//
//   - A URL server's "Authorization: Bearer" becomes bearer_token_env_var and
//     any other header env_http_headers.
//   - A stdio server's env reaches it through env_vars, which passes a variable
//     of codex's environment through under the same name. The generated name is
//     not the one the server expects, so the server is started by a tiny sh
//     wrapper that renames each one and then execs the real command.
//
// The generated names end in _TOKEN, which codex's default shell environment
// policy keeps out of the agent's shell. A server whose values cannot be kept
// off argv is refused, with the reason in refused, rather than passed.
//
// Codex merges a -c table into config.toml's key by key, so an override can
// never drop a field. A server whose name config.toml already uses would keep
// that entry's headers, env or transport, and send its credentials wherever
// the new URL points. So that entry is turned off and the server goes in under
// another key; keys maps each key used back to the server's name.
func mcpConfig(servers []adapter.MCPServer, native []string) (args []string, env map[string]string, refused []string, keys map[string]string) {
	env = map[string]string{}
	keys = map[string]string{}
	taken := map[string]bool{}
	for _, n := range native {
		taken[n] = true
	}
	for _, m := range servers {
		taken[m.Name] = true
	}
	shadowed := map[string]bool{}
	for _, n := range native {
		shadowed[n] = true
	}
	for si, m := range servers {
		name := m.Name
		if shadowed[m.Name] {
			args = append(args, "-c", "mcp_servers."+tomlKey(m.Name)+".enabled=false")
			name = m.Name + "-omniplex"
			for i := 2; taken[name]; i++ {
				name = m.Name + "-omniplex-" + strconv.Itoa(i)
			}
			taken[name] = true
		}
		keys[name] = m.Name
		key := "mcp_servers." + tomlKey(name)
		secret := func(kind string, j int, value string) string {
			name := fmt.Sprintf("OMNIPLEX_MCP_%d_%s_%s%d_TOKEN", si, envSafe(m.Name), kind, j)
			env[name] = value
			return name
		}
		if m.URL != "" {
			args = append(args, "-c", key+".url="+strconv.Quote(m.URL))
			names := sortedKeys(m.Headers)
			var headers []string
			for j, h := range names {
				v := m.Headers[h]
				if token, ok := bearer(h, v); ok {
					args = append(args, "-c", key+".bearer_token_env_var="+strconv.Quote(secret("B", j, token)))
					continue
				}
				headers = append(headers, strconv.Quote(h)+"="+strconv.Quote(secret("H", j, v)))
			}
			if len(headers) > 0 {
				args = append(args, "-c", key+".env_http_headers={"+strings.Join(headers, ",")+"}")
			}
			continue
		}

		if len(m.Env) == 0 {
			args = append(args, "-c", key+".command="+strconv.Quote(m.Command), "-c", key+".args="+tomlStrings(m.Args))
			continue
		}
		if runtime.GOOS == "windows" {
			refused = append(refused, m.Name+": passing its env without a command line needs sh")
			continue
		}
		names := sortedKeys(m.Env)
		bad := ""
		for _, n := range names {
			if !shellName.MatchString(n) {
				bad = n
				break
			}
		}
		if bad != "" {
			refused = append(refused, fmt.Sprintf("%s: env name %q cannot be passed without a command line", m.Name, bad))
			continue
		}
		var exports, generated []string
		for j, n := range names {
			g := secret("E", j, m.Env[n])
			exports = append(exports, n+`="$`+g+`"`)
			generated = append(generated, g)
		}
		// sh -c script cmd args...: inside the script $0 is cmd, "$@" its args.
		script := "export " + strings.Join(exports, " ") + "; unset " + strings.Join(generated, " ") + `; exec "$0" "$@"`
		args = append(args,
			"-c", key+".command="+strconv.Quote("/bin/sh"),
			"-c", key+".args="+tomlStrings(append([]string{"-c", script, m.Command}, m.Args...)),
			"-c", key+".env_vars="+tomlStrings(generated),
		)
	}
	return args, env, refused, keys
}

var (
	shellName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	bareKey   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	notAlnum  = regexp.MustCompile(`[^A-Z0-9]+`)
)

// bearer reports whether a header is "Authorization: Bearer <token>".
func bearer(name, value string) (string, bool) {
	if !strings.EqualFold(name, "Authorization") {
		return "", false
	}
	scheme, token, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func tomlKey(name string) string {
	if bareKey.MatchString(name) {
		return name
	}
	return strconv.Quote(name)
}

func tomlStrings(list []string) string {
	quoted := make([]string, len(list))
	for i, s := range list {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func envSafe(name string) string {
	return strings.Trim(notAlnum.ReplaceAllString(strings.ToUpper(name), "_"), "_")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MCPTransports: codex runs local servers and streamable-HTTP ones.
func (a *Adapter) MCPTransports() []string { return []string{"stdio", "http"} }

// codexServerTOML is one [mcp_servers.<name>] table of config.toml.
type codexServerTOML struct {
	Command           string            `toml:"command"`
	Args              []string          `toml:"args"`
	Env               map[string]string `toml:"env"`
	EnvVars           []any             `toml:"env_vars"`
	URL               string            `toml:"url"`
	BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
	HTTPHeaders       map[string]string `toml:"http_headers"`
	EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`
	Enabled           *bool             `toml:"enabled"`
}

// ConfiguredMCPServers lists the servers in the instance's config.toml
// ($CODEX_HOME, or ~/.codex). Values codex would read from its environment
// (bearer_token_env_var, env_http_headers, env_vars) are resolved the way
// codex would resolve them: the instance's overlay over the ambient env.
// Disabled servers are left out.
func (a *Adapter) ConfiguredMCPServers(ctx context.Context, env map[string]string) ([]adapter.ConfiguredMCPServer, error) {
	all, err := nativeServers(env)
	if err != nil {
		return nil, err
	}
	var out []adapter.ConfiguredMCPServer
	for _, name := range sortedNames(all) {
		s := all[name]
		if s.Enabled != nil && !*s.Enabled {
			continue
		}
		m := adapter.MCPServer{Name: name}
		switch {
		case s.URL != "":
			m.URL = s.URL
			headers := map[string]string{}
			for k, v := range s.HTTPHeaders {
				headers[k] = v
			}
			for k, v := range s.EnvHTTPHeaders {
				if val, ok := lookupEnv(env, v); ok && val != "" {
					headers[k] = val
				}
			}
			if s.BearerTokenEnvVar != "" {
				if val, ok := lookupEnv(env, s.BearerTokenEnvVar); ok && val != "" {
					headers["Authorization"] = "Bearer " + val
				}
			}
			if len(headers) > 0 {
				m.Headers = headers
			}
		case s.Command != "":
			m.Command, m.Args = s.Command, s.Args
			vars := map[string]string{}
			for _, v := range s.EnvVars {
				n := envVarName(v)
				if val, ok := lookupEnv(env, n); ok && n != "" {
					vars[n] = val
				}
			}
			for k, v := range s.Env {
				vars[k] = v
			}
			if len(vars) > 0 {
				m.Env = vars
			}
		default:
			continue
		}
		out = append(out, adapter.ConfiguredMCPServer{MCPServer: m, Origin: "config.toml"})
	}
	return out, nil
}

// nativeServers reads every [mcp_servers.<name>] table of the instance's
// config.toml ($CODEX_HOME, or ~/.codex), disabled ones included.
func nativeServers(env map[string]string) (map[string]codexServerTOML, error) {
	path := filepath.Join(skills.DefaultRoots(envHome(env), env, "").CodexHome, "config.toml")
	var doc struct {
		MCPServers map[string]codexServerTOML `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return doc.MCPServers, nil
}

func sortedNames(m map[string]codexServerTOML) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// envVarName reads one env_vars entry: a name, or {name = ..., source = ...}.
func envVarName(v any) string {
	switch e := v.(type) {
	case string:
		return e
	case map[string]any:
		n, _ := e["name"].(string)
		return n
	}
	return ""
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

// codexMCPStatus is the part of codex's McpServerStatus omniplex reads.
type codexMCPStatus struct {
	Name          string  `json:"name"`
	AuthStatus    string  `json:"authStatus"`
	RuntimeStatus *string `json:"runtimeStatus"`
	ToolsError    *string `json:"toolsError"`
}

// MCPStatus is codex's report on each server this thread has, read through
// the thread's own connections rather than fresh ones.
func (s *session) MCPStatus(ctx context.Context) ([]adapter.MCPServerStatus, error) {
	var out []adapter.MCPServerStatus
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{"threadId": s.threadID, "detail": "toolsAndAuthOnly"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []codexMCPStatus `json:"data"`
			NextCursor *string          `json:"nextCursor"`
		}
		if err := s.conn.Call(ctx, "mcpServerStatus/list", params, &res); err != nil {
			return nil, err
		}
		for _, st := range res.Data {
			// A config.toml entry that gave its name to one of omniplex's is
			// off for this session, and the renamed one reports as itself.
			name, ok := s.mcpKeys[st.Name]
			if !ok && s.mcpOurs[st.Name] {
				continue
			}
			if ok {
				st.Name = name
			}
			out = append(out, codexStatus(st))
		}
		if res.NextCursor == nil || *res.NextCursor == "" || *res.NextCursor == cursor {
			break
		}
		cursor = *res.NextCursor
	}
	return out, nil
}

// codexStatus maps one of codex's statuses onto omniplex's. A server codex has
// not yet tried reads as pending; a state codex adds later reads as failed.
func codexStatus(st codexMCPStatus) adapter.MCPServerStatus {
	out := adapter.MCPServerStatus{Name: st.Name}
	if st.ToolsError != nil {
		out.Error = *st.ToolsError
	}
	rs := ""
	if st.RuntimeStatus != nil {
		rs = *st.RuntimeStatus
	}
	switch rs {
	case "connected":
		out.Status = "connected"
	case "authenticationRequired":
		out.Status = "needs_auth"
	case "", "notStarted", "starting":
		out.Status = "pending"
		if st.AuthStatus == "notLoggedIn" {
			out.Status = "needs_auth"
		}
	case "failed", "cancelled":
		out.Status = "failed"
		if st.AuthStatus == "notLoggedIn" {
			out.Status = "needs_auth"
		}
	case "disabled":
		out.Status = "disabled"
	default:
		out.Status = "failed"
		if out.Error == "" {
			out.Error = "unknown status " + rs
		}
	}
	return out
}

// ReconnectMCP is not supported. A token reaches codex as an environment
// variable fixed when app-server starts, and the only ways to change a server's
// definition while it runs write the user's config.toml. A fresh token takes
// effect in the thread's next session.
func (s *session) ReconnectMCP(ctx context.Context, server adapter.MCPServer) error {
	return adapter.ErrMCPUnsupported
}

var (
	_ adapter.MCPHost    = (*Adapter)(nil)
	_ adapter.MCPControl = (*session)(nil)
)
