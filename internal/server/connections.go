package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/thread"
)

// connectionCommands are the MCP server and sign-in commands. All of them
// skip the command ledger (see ephemeralCommand) and run through
// connectionsCommand; one list keeps the two in step.
var connectionCommands = map[string]bool{
	"list_connections":           true,
	"parse_mcp_server":           true,
	"save_mcp_server":            true,
	"remove_mcp_server":          true,
	"add_found_server":           true,
	"check_mcp_server":           true,
	"sign_out_mcp_server":        true,
	"set_mcp_server_off":         true,
	"set_mcp_server_project_off": true,
	"save_cli":                   true,
	"remove_cli":                 true,
	"add_cli_account":            true,
	"remove_cli_account":         true,
	"check_cli":                  true,
	"thread_mcp_status":          true,
	"thread_mcp_reconnect":       true,
}

var errNoConnections = errors.New("MCP servers and sign-ins are not set up on this server")

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func (s *Server) connectionsCommand(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if name == "thread_mcp_status" || name == "thread_mcp_reconnect" {
		return s.threadMCPCommand(ctx, name, raw)
	}
	if name == "parse_mcp_server" {
		a, err := decode[parseMCPServerArgs](raw)
		if err != nil {
			return nil, err
		}
		d, err := mcp.Parse(a.Text)
		if err != nil {
			return nil, err
		}
		return map[string]any{"draft": d}, nil
	}
	conns := s.conns
	if conns == nil {
		return nil, errNoConnections
	}
	server := func(v mcp.ServerView, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		return map[string]any{"server": v}, nil
	}
	cli := func(v mcp.CLIView, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		return map[string]any{"cli": v}, nil
	}
	ok := func(err error) (any, error) {
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}

	switch name {
	case "list_connections":
		a, err := decode[listConnectionsArgs](raw)
		if err != nil {
			return nil, err
		}
		return conns.List(ctx, a.ProjectID)

	case "save_mcp_server":
		a, err := decode[saveMCPServerArgs](raw)
		if err != nil {
			return nil, err
		}
		return server(conns.Save(ctx, a.Server, a.PreviousName))

	case "remove_mcp_server", "check_mcp_server", "sign_out_mcp_server":
		a, err := decode[mcpServerArgs](raw)
		if err != nil {
			return nil, err
		}
		switch name {
		case "remove_mcp_server":
			return ok(conns.Remove(a.Name, a.Project))
		case "check_mcp_server":
			return server(conns.Check(ctx, a.Name, a.Project))
		default:
			return server(conns.SignOut(ctx, a.Name, a.Project))
		}

	case "set_mcp_server_off":
		a, err := decode[setMCPServerOffArgs](raw)
		if err != nil {
			return nil, err
		}
		return server(conns.SetOff(a.Name, a.Project, a.Off))

	case "set_mcp_server_project_off":
		a, err := decode[setMCPServerProjectOffArgs](raw)
		if err != nil {
			return nil, err
		}
		return server(conns.SetProjectOff(ctx, a.Name, a.ProjectID, a.Off))

	case "add_found_server":
		a, err := decode[addFoundServerArgs](raw)
		if err != nil {
			return nil, err
		}
		return server(conns.AddFound(ctx, a.Harness, a.Name, a.Where, a.Project))

	case "save_cli":
		a, err := decode[saveCLIArgs](raw)
		if err != nil {
			return nil, err
		}
		return cli(conns.SaveCLI(a.CLI, a.PreviousID))

	case "remove_cli", "add_cli_account", "remove_cli_account", "check_cli":
		a, err := decode[cliArgs](raw)
		if err != nil {
			return nil, err
		}
		switch name {
		case "remove_cli":
			return ok(conns.RemoveCLI(a.ID))
		case "add_cli_account":
			return cli(conns.AddAccount(a.ID, a.Account))
		case "remove_cli_account":
			return cli(conns.RemoveAccount(a.ID, a.Account))
		default:
			return cli(conns.CheckCLI(ctx, a.ID))
		}
	}
	return nil, fmt.Errorf("unknown command %q", name)
}

func (s *Server) threadMCPCommand(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	a, err := decode[threadMCPArgs](raw)
	if err != nil {
		return nil, err
	}
	if name == "thread_mcp_reconnect" {
		if err := s.mgr.ReconnectMCP(ctx, a.ThreadID, a.Name); err != nil {
			return nil, err
		}
	}
	live, servers, err := s.mgr.MCPStatus(ctx, a.ThreadID)
	if err != nil {
		return nil, err
	}
	if servers == nil {
		servers = []adapter.MCPServerStatus{}
	}
	return map[string]any{"live": live, "servers": servers}, nil
}

// beginFlow starts whichever sign-in auth_begin names under the flow engine.
func (c *conn) beginFlow(a authBeginArgs) (string, <-chan thread.AuthFlowEvent, error) {
	if a.MCPServer == "" && a.CLI == "" {
		return c.srv.mgr.BeginAuthFlow(a.InstanceID, a.MethodID)
	}
	conns := c.srv.conns
	if conns == nil {
		return "", nil, errNoConnections
	}
	var (
		run func(context.Context, adapter.AuthInteraction) error
		err error
	)
	if a.MCPServer != "" {
		run, err = conns.SignIn(a.MCPServer, a.MCPProject, a.Origin)
	} else {
		run, err = conns.SignInAccount(a.CLI, a.Account)
	}
	if err != nil {
		return "", nil, err
	}
	return c.srv.mgr.BeginFlow(run)
}

// handleOAuthCallback is where an authorization server sends the browser
// back. It is public: it only completes a sign-in some paired device began,
// matched by its state.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.conns == nil {
		http.NotFound(w, r)
		return
	}
	s.conns.OAuth().HandleCallback(w, r)
}

// handleMCPProxy forwards a harness's request to one of the user's remote MCP
// servers. The proxy checks its own key.
func (s *Server) handleMCPProxy(w http.ResponseWriter, r *http.Request) {
	if s.conns == nil {
		http.NotFound(w, r)
		return
	}
	s.conns.ServeProxy(w, r, r.PathValue("id"))
}
