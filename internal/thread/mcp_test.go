package thread

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
)

// fakeMCP is the user's servers: one per transport, and it records what it
// was asked.
type fakeMCP struct {
	mu    sync.Mutex
	asked []string
}

func (f *fakeMCP) Servers(_ context.Context, harness string, transports []string) []adapter.MCPServer {
	f.mu.Lock()
	f.asked = append(f.asked, fmt.Sprintf("%s %v", harness, transports))
	f.mu.Unlock()
	var out []adapter.MCPServer
	for _, tr := range transports {
		out = append(out, adapter.MCPServer{Name: "user-" + tr})
	}
	return out
}

func (f *fakeMCP) Server(_ context.Context, harness string, transports []string, name string) (adapter.MCPServer, error) {
	if name != "user-http" || !slices.Contains(transports, "http") {
		return adapter.MCPServer{}, errors.New("not this harness's")
	}
	return adapter.MCPServer{Name: name, URL: "https://u.example.com", Headers: map[string]string{"Authorization": "Bearer fresh"}}, nil
}

func useUserMCP(t *testing.T, src MCPSource) {
	t.Helper()
	old := UserMCP
	UserMCP = src
	t.Cleanup(func() { UserMCP = old })
}

// mcpAdapter is a fake harness that takes MCP servers over the given
// transports. Its sessions report on them when control is set.
type mcpAdapter struct {
	*fakeAdapter
	transports []string
	control    bool

	mu      sync.Mutex
	got     []adapter.MCPServer
	reconns []adapter.MCPServer
	status  []adapter.MCPServerStatus
}

func (m *mcpAdapter) MCPTransports() []string { return m.transports }

func (m *mcpAdapter) ConfiguredMCPServers(context.Context, map[string]string) ([]adapter.ConfiguredMCPServer, error) {
	return nil, nil
}

func (m *mcpAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	m.mu.Lock()
	m.got = o.MCPServers
	m.mu.Unlock()
	s, err := m.fakeAdapter.CreateSession(ctx, host, o)
	if err != nil || !m.control {
		return s, err
	}
	return &mcpSession{Session: s, a: m}, nil
}

type mcpSession struct {
	adapter.Session
	a *mcpAdapter
}

func (s *mcpSession) MCPStatus(context.Context) ([]adapter.MCPServerStatus, error) {
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	return s.a.status, nil
}

func (s *mcpSession) ReconnectMCP(_ context.Context, def adapter.MCPServer) error {
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	s.a.reconns = append(s.a.reconns, def)
	for i := range s.a.status {
		if s.a.status[i].Name == def.Name {
			s.a.status[i].Status = "connected"
		}
	}
	return nil
}

func newMCPManager(t *testing.T, ad adapter.Adapter) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, t.Logf, ad)
	t.Cleanup(mgr.Shutdown)
	return mgr
}

func TestSessionsGetUserServersOnlyThroughMCPHost(t *testing.T) {
	src := &fakeMCP{}
	useUserMCP(t, src)
	old := ToolServers
	ToolServers = func(threadID, home string) []adapter.MCPServer { return []adapter.MCPServer{{Name: "omniplex"}} }
	t.Cleanup(func() { ToolServers = old })
	ctx := context.Background()

	host := &mcpAdapter{fakeAdapter: &fakeAdapter{}, transports: []string{"stdio"}}
	mgr := newMCPManager(t, host)
	if _, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", ""); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	got := host.got
	host.mu.Unlock()
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"omniplex", "user-stdio"}) {
		t.Errorf("host session got %v", names)
	}
	if len(src.asked) != 1 || src.asked[0] != "fake [stdio]" {
		t.Errorf("asked %v", src.asked)
	}

	// A harness that does not implement MCPHost is never asked and gets
	// only the tool servers.
	src.asked = nil
	plain := &fakeAdapter{}
	servers := harnessExtras(ctx, newMCPManager(t, plain).store, plain, store.ThreadMeta{ID: "t"}, t.TempDir(), t.Logf).mcp
	if len(servers) != 1 || servers[0].Name != "omniplex" || len(src.asked) != 0 {
		t.Errorf("plain adapter got %v, source asked %v", servers, src.asked)
	}
}

func TestMCPStatusAndReconnect(t *testing.T) {
	useUserMCP(t, &fakeMCP{})
	ctx := context.Background()
	host := &mcpAdapter{
		fakeAdapter: &fakeAdapter{}, transports: []string{"http"}, control: true,
		status: []adapter.MCPServerStatus{{Name: "user-http", Status: "needs_auth"}},
	}
	mgr := newMCPManager(t, host)
	actor, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })

	live, servers, err := mgr.MCPStatus(ctx, actor.ID)
	if err != nil || !live || len(servers) != 1 || servers[0].Status != "needs_auth" {
		t.Fatalf("status = %v %+v %v", live, servers, err)
	}

	if err := mgr.ReconnectMCP(ctx, actor.ID, "user-http"); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	reconns := host.reconns
	host.mu.Unlock()
	if len(reconns) != 1 || reconns[0].Headers["Authorization"] != "Bearer fresh" {
		t.Errorf("reconnected with %+v", reconns)
	}
	if _, servers, _ := mgr.MCPStatus(ctx, actor.ID); servers[0].Status != "connected" {
		t.Errorf("after reconnect %+v", servers)
	}

	// Servers omniplex has no definition for cannot be reconnected.
	if err := mgr.ReconnectMCP(ctx, actor.ID, "harness-own"); err == nil {
		t.Error("reconnected a server the source refused")
	}

	// No running session: not live, and no error.
	live, servers, err = mgr.MCPStatus(ctx, "no-such-thread")
	if live || servers != nil || err != nil {
		t.Errorf("missing thread = %v %v %v", live, servers, err)
	}
	if err := mgr.ReconnectMCP(ctx, "no-such-thread", "user-http"); err == nil {
		t.Error("reconnected on a missing thread")
	}
}

func TestMCPStatusOnSessionWithoutControl(t *testing.T) {
	useUserMCP(t, &fakeMCP{})
	ctx := context.Background()
	host := &mcpAdapter{fakeAdapter: &fakeAdapter{}, transports: []string{"http"}}
	mgr := newMCPManager(t, host)
	actor, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })
	if _, _, err := mgr.MCPStatus(ctx, actor.ID); !errors.Is(err, adapter.ErrMCPUnsupported) {
		t.Errorf("status err = %v", err)
	}
	if err := mgr.ReconnectMCP(ctx, actor.ID, "user-http"); !errors.Is(err, adapter.ErrMCPUnsupported) {
		t.Errorf("reconnect err = %v", err)
	}
}

func TestReconnectNeedsAnMCPHost(t *testing.T) {
	useUserMCP(t, &fakeMCP{})
	ctx := context.Background()
	mgr := newMCPManager(t, &fakeAdapter{})
	actor, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.ReconnectMCP(ctx, actor.ID, "user-http"); !errors.Is(err, adapter.ErrMCPUnsupported) {
		t.Errorf("reconnect err = %v", err)
	}
}
