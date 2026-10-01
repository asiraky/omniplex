package thread

import (
	"context"
	"errors"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// MCPSource is the user's own MCP servers, as sessions get them: values
// filled in and OAuth tokens fresh. Only adapters implementing
// adapter.MCPHost are asked about, with the kinds of server they run.
type MCPSource interface {
	// Servers is everything a session of the harness gets.
	Servers(ctx context.Context, harness string, transports []string) []adapter.MCPServer
	// Server is one of them, for reconnecting it.
	Server(ctx context.Context, harness string, transports []string, name string) (adapter.MCPServer, error)
}

// UserMCP supplies the user's MCP servers. Set once at startup, before any
// thread starts; nil means none. A package variable for the same reason as
// ToolServers.
var UserMCP MCPSource

// mcpTimeout bounds one status read or reconnect through a live session.
const mcpTimeout = 30 * time.Second

func userMCPServers(ctx context.Context, ad adapter.Adapter) []adapter.MCPServer {
	if UserMCP == nil || ad == nil {
		return nil
	}
	host, ok := ad.(adapter.MCPHost)
	if !ok {
		return nil
	}
	return UserMCP.Servers(ctx, ad.ID(), host.MCPTransports())
}

// MCPHarness is a harness whose adapter takes MCP servers, with the env
// overlay of each of its enabled provider instances.
type MCPHarness struct {
	ID   string
	Name string
	Host adapter.MCPHost
	Envs []map[string]string
}

// MCPHosts lists the harnesses that take MCP servers, in registration order.
// An instance whose env cannot be built (a missing secret) is left out.
func (m *Manager) MCPHosts() []MCPHarness {
	var out []MCPHarness
	instances := m.orderedInstances()
	for _, id := range m.driverOrder {
		ad := m.drivers[id]
		host, ok := ad.(adapter.MCPHost)
		if !ok {
			continue
		}
		h := MCPHarness{ID: id, Name: ad.Meta().Name, Host: host}
		for _, reg := range instances {
			if reg.inst.Driver != id || !reg.inst.Enabled {
				continue
			}
			env, err := m.envFor(reg.inst)
			if err != nil {
				m.logf("mcp servers for %s: %v", reg.inst.ID, err)
				continue
			}
			h.Envs = append(h.Envs, env)
		}
		out = append(out, h)
	}
	return out
}

// MCPStatus is the live session's own report on its MCP servers. live is
// false when the thread has no running session.
func (m *Manager) MCPStatus(ctx context.Context, threadID string) (live bool, servers []adapter.MCPServerStatus, err error) {
	a, ok := m.Peek(threadID)
	if !ok {
		return false, nil, nil
	}
	v, err := a.call(ctx, command{kind: cmdMCP, mcp: func(ctx context.Context, ctl adapter.MCPControl) (any, error) {
		return ctl.MCPStatus(ctx)
	}})
	if errors.Is(err, ErrNotReady) || errors.Is(err, ErrClosed) {
		return false, nil, nil
	}
	if err != nil {
		return true, nil, err
	}
	servers, _ = v.([]adapter.MCPServerStatus)
	return true, servers, nil
}

// ReconnectMCP hands the live session the server's current definition, with
// a fresh token, and has it reconnect. Only the user's own servers can be
// reconnected from here: omniplex has no definition for the others.
func (m *Manager) ReconnectMCP(ctx context.Context, threadID, name string) error {
	a, ok := m.Peek(threadID)
	if !ok {
		return errors.New("this thread has no running session")
	}
	if UserMCP == nil {
		return errors.New("no MCP servers are set up here")
	}
	host, ok := m.drivers[a.Harness].(adapter.MCPHost)
	if !ok {
		return adapter.ErrMCPUnsupported
	}
	def, err := UserMCP.Server(ctx, a.Harness, host.MCPTransports(), name)
	if err != nil {
		return err
	}
	_, err = a.call(ctx, command{kind: cmdMCP, mcp: func(ctx context.Context, ctl adapter.MCPControl) (any, error) {
		return nil, ctl.ReconnectMCP(ctx, def)
	}})
	if errors.Is(err, ErrNotReady) || errors.Is(err, ErrClosed) {
		return errors.New("this thread has no running session")
	}
	return err
}
