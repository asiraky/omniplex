package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// connServer is a server with connections set up, its MCP files in a temp
// folder; client is what it checks remote servers with.
func connServer(t *testing.T, client *http.Client) (http.Handler, *mcp.Connections) {
	h, conns, _ := connServerWithStore(t, client)
	return h, conns
}

// connServerWithStore is connServer that also hands back its thread store,
// whose projects the connections know.
func connServerWithStore(t *testing.T, client *http.Client) (http.Handler, *mcp.Connections, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "conns.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mst, err := mcp.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folders := func(ctx context.Context, id string) ([]string, error) {
		_, err := st.Project(ctx, id)
		return nil, err
	}
	conns := mcp.NewConnections(mst, client, auth.DefaultPort, nil, folders, t.Logf)
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), Connections: conns})
	return srv.Handler(), conns, st
}

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
	ctx  context.Context
	n    int
	// other holds frames read while waiting for an ack.
	other []serverFrame
}

func dialWS(t *testing.T, h http.Handler) *wsClient {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	// Loopback is trusted, so no pairing is needed.
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(8 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	c := &wsClient{t: t, conn: conn, ctx: ctx}
	c.write(clientFrame{Type: "hello", ProtocolVersion: 1})
	return c
}

func (c *wsClient) write(f clientFrame) {
	c.t.Helper()
	b, _ := json.Marshal(f)
	if err := c.conn.Write(c.ctx, websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) read() serverFrame {
	c.t.Helper()
	_, b, err := c.conn.Read(c.ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	var f serverFrame
	if err := json.Unmarshal(b, &f); err != nil {
		c.t.Fatal(err)
	}
	return f
}

// do sends a command and returns its ack's raw result and error.
func (c *wsClient) do(command string, args any) (json.RawMessage, string) {
	c.t.Helper()
	c.n++
	id := fmt.Sprintf("c%d", c.n)
	raw, _ := json.Marshal(args)
	c.write(clientFrame{Type: "command", CommandID: id, Command: command, Args: raw})
	for {
		f := c.read()
		if f.Type == "ack" && f.CommandID == id {
			return f.Result, f.Error
		}
		c.other = append(c.other, f)
	}
}

func (c *wsClient) ok(command string, args any, out any) json.RawMessage {
	c.t.Helper()
	res, errMsg := c.do(command, args)
	if errMsg != "" {
		c.t.Fatalf("%s: %s", command, errMsg)
	}
	if out != nil {
		if err := json.Unmarshal(res, out); err != nil {
			c.t.Fatalf("%s: %v in %s", command, err, res)
		}
	}
	return res
}

func TestConnectionCommandsEndToEnd(t *testing.T) {
	var probes atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			probes.Add(1)
		}
		if r.Header.Get("X-Key") != "hunter2" {
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer remote.Close()
	h, conns := connServer(t, remote.Client())
	c := dialWS(t, h)

	var parsed struct{ Draft mcp.Draft }
	c.ok("parse_mcp_server", map[string]string{"text": "claude mcp add --transport http work " + remote.URL + ` -H "X-Key: hunter2"`}, &parsed)
	if parsed.Draft.Name != "work" || parsed.Draft.URL != remote.URL || parsed.Draft.Headers["X-Key"] != "hunter2" {
		t.Fatalf("draft %+v", parsed.Draft)
	}

	var saved struct{ Server mcp.ServerView }
	res := c.ok("save_mcp_server", map[string]any{"server": parsed.Draft}, &saved)
	if saved.Server.Status != mcp.StatusConnected || len(saved.Server.HeaderNames) != 1 {
		t.Fatalf("saved %+v", saved.Server)
	}
	if strings.Contains(string(res), "hunter2") {
		t.Error("a credential went back to the client")
	}

	// Re-saving with the value left empty keeps it: the check still passes.
	parsed.Draft.Headers["X-Key"] = ""
	c.ok("save_mcp_server", map[string]any{"server": parsed.Draft, "previousName": "work"}, &saved)
	if saved.Server.Status != mcp.StatusConnected {
		t.Errorf("after keeping the value %+v", saved.Server)
	}

	before := probes.Load()
	c.ok("set_mcp_server_off", map[string]any{"name": "work", "off": []string{"x"}}, &saved)
	if !equalStrings(saved.Server.Off, []string{"x"}) || probes.Load() != before {
		t.Errorf("set off %+v, probes %d -> %d", saved.Server, before, probes.Load())
	}
	if got := conns.Servers(context.Background(), "x", []string{"http"}, ""); len(got) != 0 {
		t.Errorf("harness switched off still gets %+v", got)
	}
	got := conns.Servers(context.Background(), "y", []string{"http"}, "")
	if len(got) != 1 || got[0].Headers["X-Key"] != "" || !strings.Contains(got[0].URL, mcp.ProxyPrefix+"work") {
		t.Fatalf("harness y gets %+v", got)
	}

	// A harness reaches the server through this server's proxy, which
	// adds the header value, without pairing as a device.
	proxied := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, mcp.ProxyPrefix+"work", strings.NewReader("{}"))
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Authorization", key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	before = probes.Load()
	if rec := proxied(got[0].Headers["Authorization"]); rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("proxied: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if probes.Load() != before+1 {
		t.Error("the proxied request never reached the server")
	}
	if rec := proxied("Bearer guess"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "proxy key") {
		t.Errorf("wrong key: %d %s", rec.Code, rec.Body)
	}

	var list mcp.Listing
	res = c.ok("list_connections", nil, &list)
	if len(list.Servers) != 1 || list.Servers[0].Name != "work" || list.Found == nil || list.Harnesses == nil || list.CLIs == nil {
		t.Errorf("list %s", res)
	}
	if strings.Contains(string(res), "hunter2") {
		t.Error("the listing carries a credential")
	}

	if _, e := c.do("save_mcp_server", map[string]any{"server": mcp.Draft{Name: "Bad Name", Command: "x"}}); e == "" {
		t.Error("saved a bad name")
	}
	if _, e := c.do("check_mcp_server", map[string]string{"name": "missing"}); e == "" {
		t.Error("checked a missing server")
	}

	var cli struct{ CLI mcp.CLIView }
	c.ok("save_cli", map[string]any{"cli": mcp.CLI{ID: "tool", StatusCommand: "true", SignInCommand: "true"}}, &cli)
	c.ok("add_cli_account", map[string]string{"id": "tool", "account": "me"}, &cli)
	c.ok("check_cli", map[string]string{"id": "tool"}, &cli)
	if len(cli.CLI.Accounts) != 1 || cli.CLI.Accounts[0].Status != mcp.AccountSignedIn {
		t.Errorf("cli %+v", cli.CLI)
	}
	c.ok("remove_cli_account", map[string]string{"id": "tool", "account": "me"}, &cli)
	c.ok("remove_cli", map[string]string{"id": "tool"}, nil)

	c.ok("remove_mcp_server", map[string]string{"name": "work"}, nil)
	c.ok("list_connections", nil, &list)
	if len(list.Servers) != 0 {
		t.Errorf("after remove %+v", list.Servers)
	}

	var status struct {
		Live    bool
		Servers []json.RawMessage
	}
	res = c.ok("thread_mcp_status", map[string]string{"threadId": "no-such-thread"}, &status)
	if status.Live || status.Servers == nil {
		t.Errorf("status of a thread with no session %s", res)
	}
}

// Every command that names a server reaches the one in the scope it names,
// and deleting a project takes its servers with it.
func TestProjectScopedConnectionCommands(t *testing.T) {
	h, conns, st := connServerWithStore(t, nil)
	ctx := context.Background()
	for _, id := range []string{"p1", "p2"} {
		if err := st.CreateProject(ctx, project.Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	c := dialWS(t, h)
	saveIn := func(project, value string) {
		d := mcp.Draft{Name: "tools", Project: project, Command: "tools", Env: map[string]string{"KEY": value}}
		c.ok("save_mcp_server", map[string]any{"server": d}, nil)
	}
	saveIn("", "global")
	saveIn("p1", "one")
	saveIn("p2", "two")
	c.ok("save_mcp_server", map[string]any{"server": mcp.Draft{Name: "gmail", Command: "gmail"}}, nil)
	if _, e := c.do("save_mcp_server", map[string]any{"server": mcp.Draft{Name: "x", Project: "nope", Command: "x"}}); e == "" {
		t.Error("saved into a project that does not exist")
	}

	var list mcp.Listing
	c.ok("list_connections", map[string]string{"projectId": "p1"}, &list)
	var keys []string
	for _, s := range list.Servers {
		keys = append(keys, s.Project+"/"+s.Name)
		if s.OffIn == nil {
			t.Errorf("%s has no offIn", s.Name)
		}
	}
	if strings.Join(keys, ",") != "/tools,p1/tools,/gmail" {
		t.Errorf("p1 lists %v", keys)
	}

	var saved struct{ Server mcp.ServerView }
	c.ok("set_mcp_server_project_off", map[string]any{"name": "gmail", "projectId": "p1", "off": true}, &saved)
	if !equalStrings(saved.Server.OffIn, []string{"p1"}) {
		t.Errorf("offIn %v", saved.Server.OffIn)
	}
	c.ok("set_mcp_server_off", map[string]any{"name": "tools", "project": "p2", "off": []string{"y"}}, &saved)
	if saved.Server.Project != "p2" || !equalStrings(saved.Server.Off, []string{"y"}) {
		t.Errorf("set off %+v", saved.Server)
	}

	got := func(harness, project string) string {
		var out []string
		for _, d := range conns.Servers(ctx, harness, []string{"stdio"}, project) {
			out = append(out, d.Name+"="+d.Env["KEY"])
		}
		return strings.Join(out, ",")
	}
	if g := got("x", "p1"); g != "tools=one" {
		t.Errorf("p1 session gets %s", g)
	}
	if g := got("y", "p2"); g != "gmail=" {
		t.Errorf("p2 session on y gets %s", g)
	}
	c.ok("set_mcp_server_project_off", map[string]any{"name": "gmail", "projectId": "p1", "off": false}, &saved)
	if g := got("x", "p1"); g != "tools=one,gmail=" {
		t.Errorf("p1 session after letting gmail back gets %s", g)
	}

	c.ok("remove_mcp_server", map[string]string{"name": "tools", "project": "p1"}, nil)
	if g := got("x", "p1"); g != "tools=global,gmail=" {
		t.Errorf("p1 session after removing its tools gets %s", g)
	}

	if _, e := c.do("delete_project", map[string]string{"projectId": "p2"}); e != "" {
		t.Fatal(e)
	}
	list = mcp.Listing{}
	c.ok("list_connections", nil, &list)
	keys = nil
	for _, s := range list.Servers {
		keys = append(keys, s.Project+"/"+s.Name)
	}
	if strings.Join(keys, ",") != "/tools,/gmail" {
		t.Errorf("after deleting p2 %v", keys)
	}
}

func TestMCPSignInRunsUnderTheFlowEngine(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer remote.Close()
	h, _ := connServer(t, remote.Client())
	c := dialWS(t, h)
	c.ok("save_mcp_server", map[string]any{"server": mcp.Draft{Name: "remote", URL: remote.URL}}, nil)
	c.ok("save_mcp_server", map[string]any{"server": mcp.Draft{Name: "local", Command: "run"}}, nil)

	if _, e := c.do("auth_begin", map[string]string{"mcpServer": "local", "origin": "http://localhost:4321"}); e == "" {
		t.Error("began a sign-in for a command server")
	}
	if _, e := c.do("auth_begin", map[string]string{"cli": "missing", "account": "me"}); e == "" {
		t.Error("began a sign-in for a missing CLI")
	}

	var begun struct{ FlowID string }
	c.ok("auth_begin", map[string]string{"mcpServer": "remote", "origin": "http://localhost:4321"}, &begun)
	if begun.FlowID == "" {
		t.Fatal("no flow id")
	}
	// The remote has no sign-in to offer, so the flow ends with an error.
	frames := c.other
	for {
		var done *thread.AuthFlowEvent
		for _, f := range frames {
			if f.Type == "auth_event" && f.AuthFlow != nil && f.AuthFlow.FlowID == begun.FlowID && f.AuthFlow.Done {
				done = f.AuthFlow
			}
		}
		if done != nil {
			if done.Err == "" {
				t.Error("a sign-in with nothing to sign in to succeeded")
			}
			return
		}
		frames = []serverFrame{c.read()}
	}
}

func TestConnectionCommandsWithoutConnections(t *testing.T) {
	handler, _ := testServer(t)
	c := dialWS(t, handler)
	if _, e := c.do("list_connections", nil); e == "" {
		t.Error("listed connections on a server without them")
	}
	// Parsing needs nothing stored.
	var parsed struct{ Draft mcp.Draft }
	c.ok("parse_mcp_server", map[string]string{"text": "https://mcp.example.com/mcp"}, &parsed)
	if parsed.Draft.URL == "" {
		t.Errorf("draft %+v", parsed.Draft)
	}
}

// The authorization server sends the browser back to the callback, and the
// browser may be a device that never paired: it must get there.
func TestOAuthCallbackIsPublic(t *testing.T) {
	h, _ := connServer(t, nil)
	ts := httptest.NewServer(asRemote(h))
	defer ts.Close()
	res, err := http.Get(ts.URL + mcp.CallbackPath + "?state=unknown&code=x")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusFound {
		t.Fatalf("callback from an unpaired device got %d", res.StatusCode)
	}
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}
