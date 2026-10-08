package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

type fakeHost struct {
	transports []string
	// byDir is what ConfiguredMCPServers returns for env["DIR"].
	byDir map[string][]adapter.ConfiguredMCPServer
	// inFolder is what ProjectMCPServers returns for a folder, whatever the
	// env.
	inFolder map[string][]adapter.ConfiguredMCPServer
	fail     bool
}

func (h *fakeHost) MCPTransports() []string { return h.transports }

func (h *fakeHost) ConfiguredMCPServers(_ context.Context, env map[string]string) ([]adapter.ConfiguredMCPServer, error) {
	if h.fail {
		return nil, errors.New("config unreadable")
	}
	return h.byDir[env["DIR"]], nil
}

func (h *fakeHost) ProjectMCPServers(_ context.Context, _ map[string]string, dir string) ([]adapter.ConfiguredMCPServer, error) {
	if h.fail {
		return nil, errors.New("config unreadable")
	}
	return h.inFolder[dir], nil
}

// withProjects gives c these projects, by ID, with their folders' paths.
func withProjects(c *Connections, projects map[string][]string) {
	c.folders = func(_ context.Context, id string) ([]string, error) {
		dirs, ok := projects[id]
		if !ok {
			return nil, fmt.Errorf("no project %q", id)
		}
		return dirs, nil
	}
}

func newConns(t *testing.T, client *http.Client, hosts ...Host) *Connections {
	t.Helper()
	return NewConnections(newStore(t), client, 4321, func() []Host { return hosts }, nil, t.Logf)
}

func save(t *testing.T, c *Connections, d Draft) {
	t.Helper()
	if _, err := c.store.SaveServer(d, ""); err != nil {
		t.Fatal(err)
	}
}

// signIn stores tokens for the server with this key.
func signIn(t *testing.T, c *Connections, key, url string, rec map[string]any) {
	t.Helper()
	rec["url"] = url
	b, _ := json.Marshal(rec)
	if err := c.store.Secrets().Put(secretID(key), OAuthKey, string(b)); err != nil {
		t.Fatal(err)
	}
}

func names(defs []adapter.MCPServer) []string {
	var out []string
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func TestServersFiltersByTransportAndOff(t *testing.T) {
	c := newConns(t, nil)
	save(t, c, Draft{Name: "remote", URL: "https://r.example.com/mcp"})
	save(t, c, Draft{Name: "local", Command: "run"})
	save(t, c, Draft{Name: "not-for-a", URL: "https://n.example.com/mcp", Off: []string{"a"}})
	save(t, c, Draft{Name: "local-not-for-b", Command: "run", Off: []string{"b"}})

	cases := []struct {
		harness    string
		transports []string
		want       string
	}{
		{"a", []string{"http", "stdio"}, "remote,local,local-not-for-b"},
		{"b", []string{"stdio", "http"}, "remote,local,not-for-a"},
		{"c", []string{"stdio"}, "local,local-not-for-b"},
		{"d", []string{"http"}, "remote,not-for-a"},
		{"e", nil, ""},
	}
	for _, tc := range cases {
		got := strings.Join(names(c.Servers(context.Background(), tc.harness, tc.transports, "")), ",")
		if got != tc.want {
			t.Errorf("%s %v: got %q, want %q", tc.harness, tc.transports, got, tc.want)
		}
	}

	if _, err := c.Server(context.Background(), "a", []string{"http"}, "", "not-for-a"); err == nil {
		t.Error("Server handed a harness a server switched off for it")
	}
	if _, err := c.Server(context.Background(), "a", []string{"http"}, "", "local"); err == nil {
		t.Error("Server handed a harness a kind it does not run")
	}
	if d, err := c.Server(context.Background(), "b", []string{"http"}, "", "not-for-a"); err != nil || d.URL == "" {
		t.Errorf("Server = %+v, %v", d, err)
	}
}

func TestServersCarryValuesAndToken(t *testing.T) {
	c := newConns(t, nil)
	save(t, c, Draft{Name: "local", Command: "run", Args: []string{"-v"}, Env: map[string]string{"TOKEN": "env-secret"}})
	save(t, c, Draft{Name: "keyed", URL: "https://k.example.com/mcp", Headers: map[string]string{"X-Key": "k", "authorization": "Bearer pasted"}})
	save(t, c, Draft{Name: "moved", URL: "https://new.example.com/mcp"})
	save(t, c, Draft{Name: "bare", URL: "https://b.example.com/mcp"})
	signIn(t, c, "keyed", "https://k.example.com/mcp", map[string]any{"accessToken": "oauth-tok"})
	// Signed in at an address the server has since moved from.
	signIn(t, c, "moved", "https://old.example.com/mcp", map[string]any{"accessToken": "old-tok"})

	defs := c.Servers(context.Background(), "x", []string{"http", "stdio"}, "")
	if len(defs) != 4 {
		t.Fatalf("%+v", defs)
	}
	local, keyed, moved, bare := defs[0], defs[1], defs[2], defs[3]
	if local.Env["TOKEN"] != "env-secret" || local.Command != "run" || local.Args[0] != "-v" || local.Headers != nil {
		t.Errorf("local %+v", local)
	}
	if len(keyed.Headers) != 2 || keyed.Headers["Authorization"] != "Bearer oauth-tok" || keyed.Headers["X-Key"] != "k" {
		t.Errorf("keyed headers %v", keyed.Headers)
	}
	if moved.Headers != nil {
		t.Errorf("token for another address went in: %v", moved.Headers)
	}
	if bare.Headers != nil || bare.Env != nil {
		t.Errorf("bare %+v", bare)
	}
}

func TestServersDoNotWaitLongForATokenRefresh(t *testing.T) {
	old := tokenWait
	tokenWait = 100 * time.Millisecond
	defer func() { tokenWait = old }()
	var hit atomic.Bool
	release := make(chan struct{})
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer as.Close()
	defer close(release)

	c := newConns(t, as.Client())
	for _, n := range []string{"one", "two", "three"} {
		save(t, c, Draft{Name: n, URL: "https://" + n + ".example.com/mcp"})
		signIn(t, c, n, "https://"+n+".example.com/mcp", map[string]any{
			"accessToken": "expired", "refreshToken": "r", "expiry": time.Now().Add(-time.Hour),
			"tokenEndpoint": as.URL + "/token", "clientId": "cid",
		})
	}
	start := time.Now()
	defs := c.Servers(context.Background(), "x", []string{"http"}, "")
	took := time.Since(start)
	if !hit.Load() {
		t.Fatal("the refresh never reached the authorization server")
	}
	if took > time.Second {
		t.Errorf("waited %v for three refreshes", took)
	}
	if len(defs) != 3 {
		t.Fatalf("%+v", defs)
	}
	for _, d := range defs {
		if d.Headers != nil {
			t.Errorf("%s went in with %v", d.Name, d.Headers)
		}
	}
}

func TestListShowsFoundServersOnce(t *testing.T) {
	cf := adapter.ConfiguredMCPServer{MCPServer: adapter.MCPServer{Name: "Cloud Flare", URL: "https://mcp.cloudflare.com/mcp", Headers: map[string]string{"Authorization": "Bearer found-secret"}}, Origin: "User settings"}
	local := adapter.ConfiguredMCPServer{MCPServer: adapter.MCPServer{Name: "fs", Command: "npx", Env: map[string]string{"K": "found-env"}}, Origin: "config.toml"}
	a := &fakeHost{transports: []string{"http", "stdio"}, byDir: map[string][]adapter.ConfiguredMCPServer{
		"one": {cf, local},
		"two": {cf}, // a second instance sharing a config folder
	}}
	b := &fakeHost{transports: []string{"stdio"}, byDir: map[string][]adapter.ConfiguredMCPServer{"": {local}}}
	broken := &fakeHost{fail: true}
	c := newConns(t, nil,
		Host{ID: "a", Name: "A", Host: a, Envs: []map[string]string{{"DIR": "one"}, {"DIR": "two"}}},
		Host{ID: "b", Name: "B", Host: b, Envs: []map[string]string{nil}},
		Host{ID: "z", Name: "Z", Host: broken, Envs: []map[string]string{nil}},
	)
	save(t, c, Draft{Name: "cloud-flare", URL: "https://elsewhere.example.com/mcp", Headers: map[string]string{"X-Key": "stored-secret"}})

	l, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Harnesses) != 3 || l.Harnesses[2].Transports == nil {
		t.Errorf("harnesses %+v", l.Harnesses)
	}
	var got []string
	for _, f := range l.Found {
		got = append(got, f.Harness+"/"+f.Name)
		// Added means a server of that name exists, whatever it points at.
		if want := f.Name == "Cloud Flare"; f.Added != want {
			t.Errorf("%s/%s added=%v", f.Harness, f.Name, f.Added)
		}
	}
	if strings.Join(got, ",") != "a/Cloud Flare,a/fs,b/fs" {
		t.Errorf("found %v", got)
	}
	if len(l.Servers) != 1 || l.Servers[0].Status != StatusUnchecked || l.Servers[0].CheckedAt != nil {
		t.Errorf("servers %+v", l.Servers)
	}
	b2, _ := json.Marshal(l)
	for _, secret := range []string{"found-secret", "found-env", "stored-secret"} {
		if strings.Contains(string(b2), secret) {
			t.Errorf("listing carries %q", secret)
		}
	}
}

func TestAddFoundCopiesTheDefinition(t *testing.T) {
	h := &fakeHost{transports: []string{"http", "stdio"}, byDir: map[string][]adapter.ConfiguredMCPServer{"": {
		{MCPServer: adapter.MCPServer{Name: "My Tools", Command: "tools", Args: []string{"serve"}, Env: map[string]string{"K": "v"}}},
		{MCPServer: adapter.MCPServer{Name: "remote", URL: "https://r.example.com/mcp", Headers: map[string]string{"X-Key": "hk"}, Env: map[string]string{"IGNORED": "x"}}},
	}}}
	c := newConns(t, nil, Host{ID: "a", Host: h, Envs: []map[string]string{nil}})

	v, err := c.AddFound(context.Background(), "a", "My Tools", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "my-tools" || v.Command != "tools" || len(v.EnvNames) != 1 {
		t.Errorf("%+v", v)
	}
	env, _ := values(t, c.store, "my-tools")
	if env["K"] != "v" {
		t.Errorf("env %v", env)
	}
	if _, err := c.AddFound(context.Background(), "a", "My Tools", "", ""); err == nil {
		t.Error("added the same server twice")
	}
	if _, err := c.AddFound(context.Background(), "b", "remote", "", ""); err == nil {
		t.Error("added from a harness that does not list it")
	}
}

// Two instances can each define a server of the same name; the one picked is
// the one imported.
func TestAddFoundTellsInstancesApart(t *testing.T) {
	h := &fakeHost{transports: []string{"stdio"}, byDir: map[string][]adapter.ConfiguredMCPServer{
		"one": {{MCPServer: adapter.MCPServer{Name: "tools", Command: "first"}}},
		"two": {{MCPServer: adapter.MCPServer{Name: "tools", Command: "second"}}},
	}}
	c := newConns(t, nil, Host{ID: "a", Host: h, Envs: []map[string]string{{"DIR": "one"}, {"DIR": "two"}}})
	v, err := c.AddFound(context.Background(), "a", "tools", "second", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Command != "second" {
		t.Fatalf("imported %q, want the second instance's", v.Command)
	}
}

func TestAddFoundRemoteDropsCommandOnlyFields(t *testing.T) {
	var auth atomic.Value
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("X-Key"))
	}))
	defer mcpSrv.Close()
	h := &fakeHost{transports: []string{"http"}, byDir: map[string][]adapter.ConfiguredMCPServer{"": {
		{MCPServer: adapter.MCPServer{Name: "remote", URL: mcpSrv.URL, Headers: map[string]string{"X-Key": "hk"}, Env: map[string]string{"IGNORED": "x"}, Args: []string{"x"}}},
	}}}
	c := newConns(t, mcpSrv.Client(), Host{ID: "a", Host: h, Envs: []map[string]string{nil}})
	v, err := c.AddFound(context.Background(), "a", "remote", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != StatusConnected || auth.Load() != "hk" {
		t.Errorf("view %+v, probe sent %v", v, auth.Load())
	}
}

func TestSaveChecksAndSetOffDoesNot(t *testing.T) {
	var posts atomic.Int32
	var bearer atomic.Value
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		posts.Add(1)
		bearer.Store(r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer mcpSrv.Close()
	c := newConns(t, mcpSrv.Client())

	v, err := c.Save(context.Background(), Draft{Name: "s", URL: mcpSrv.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != StatusSignIn || v.OAuth || v.CheckedAt == nil {
		t.Errorf("before sign-in %+v", v)
	}

	signIn(t, c, "s", mcpSrv.URL, map[string]any{"accessToken": "tok"})
	v, err = c.Check(context.Background(), "s", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != StatusConnected || !v.OAuth || bearer.Load() != "Bearer tok" {
		t.Errorf("after sign-in %+v, sent %v", v, bearer.Load())
	}

	before := posts.Load()
	v, err = c.SetOff("s", "", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != before {
		t.Error("SetOff checked the server")
	}
	if v.Status != StatusConnected || len(v.Off) != 1 {
		t.Errorf("SetOff view %+v", v)
	}

	v, err = c.SignOut(context.Background(), "s", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.OAuth || v.Status != StatusSignIn {
		t.Errorf("after sign-out %+v", v)
	}

	// A rename starts the new name's check fresh and drops the old one's.
	if _, err := c.Save(context.Background(), Draft{Name: "t", URL: mcpSrv.URL}, "s"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.prober.Cached("s"); ok {
		t.Error("old name's check survived the rename")
	}
	if err := c.Remove("t", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.prober.Cached("t"); ok {
		t.Error("removed server's check survived")
	}
}

func TestSignInRefusesCommandServers(t *testing.T) {
	c := newConns(t, nil)
	save(t, c, Draft{Name: "local", Command: "run"})
	if _, err := c.SignIn("local", "", "http://localhost:4321"); err == nil {
		t.Error("began a sign-in for a command server")
	}
	if _, err := c.SignIn("missing", "", "http://localhost:4321"); err == nil {
		t.Error("began a sign-in for a missing server")
	}
}

func TestCheckCLIRecordsEachAccount(t *testing.T) {
	c := newConns(t, nil)
	cli := CLI{
		ID: "tool", StatusCommand: `test "$WHO" = in`, SignInCommand: "true",
		AccountEnv: map[string]string{"WHO": "{account}"},
	}
	if _, err := c.SaveCLI(cli, ""); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"in", "out"} {
		v, err := c.AddAccount("tool", a)
		if err != nil {
			t.Fatal(err)
		}
		if st := v.Accounts[len(v.Accounts)-1].Status; st != StatusUnchecked {
			t.Errorf("new account status %q", st)
		}
	}
	v, err := c.CheckCLI(context.Background(), "tool")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range v.Accounts {
		got[a.Name] = a.Status
		if a.CheckedAt == nil {
			t.Errorf("%s: no check time", a.Name)
		}
	}
	if got["in"] != AccountSignedIn || got["out"] != AccountSignedOut {
		t.Errorf("statuses %v", got)
	}

	// The answers follow a rename and are listed without running anything.
	cli.ID = "renamed"
	cli.StatusCommand = "exit 3"
	if _, err := c.SaveCLI(cli, "tool"); err != nil {
		t.Fatal(err)
	}
	l, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.CLIs) != 1 || l.CLIs[0].Accounts[0].Status != AccountSignedIn {
		t.Errorf("listing %+v", l.CLIs)
	}

	if _, err := c.SignInAccount("renamed", "nobody"); err == nil {
		t.Error("began a sign-in for a missing account")
	}
}

// A server can turn a token away before its expiry says so (it restarted, or
// revoked it). Check and reconnect refresh it then, rather than handing the
// session the same dead token.
func TestRejectedTokenIsRefreshed(t *testing.T) {
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refreshes.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"good","token_type":"Bearer","expires_in":3600}`)
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer good" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		}
	}))
	defer srv.Close()

	c := newConns(t, srv.Client())
	url := srv.URL + "/mcp"
	save(t, c, Draft{Name: "s", URL: url})
	reset := func() {
		signIn(t, c, "s", url, map[string]any{
			"accessToken": "dead", "refreshToken": "r", "expiry": time.Now().Add(time.Hour),
			"tokenEndpoint": srv.URL + "/token", "clientId": "cid",
		})
	}

	reset()
	view, err := c.Check(context.Background(), "s", "")
	if err != nil || view.Status != StatusConnected {
		t.Fatalf("check = %+v, %v", view, err)
	}

	reset()
	def, err := c.Server(context.Background(), "x", []string{"http"}, "", "s")
	if err != nil || def.Headers["Authorization"] != "Bearer good" {
		t.Fatalf("reconnect got %+v, %v", def.Headers, err)
	}
	if n := refreshes.Load(); n != 2 {
		t.Errorf("refreshes = %d, want one per rejection", n)
	}

	// A token the server takes is not refreshed.
	if _, err := c.Server(context.Background(), "x", []string{"http"}, "", "s"); err != nil {
		t.Fatal(err)
	}
	if n := refreshes.Load(); n != 2 {
		t.Errorf("refreshed a token the server accepted: %d", n)
	}
}

// what a session in each project gets: its own servers first, then the
// ones that go everywhere that it neither keeps out nor replaces.
func TestServersFollowTheThreadsProject(t *testing.T) {
	c := newConns(t, nil)
	save(t, c, Draft{Name: "everywhere", Command: "e"})
	save(t, c, Draft{Name: "linear", URL: "https://linear.example.com/mcp", Headers: map[string]string{"X-Key": "mine"}})
	save(t, c, Draft{Name: "gmail", Command: "gmail"})
	save(t, c, Draft{Name: "shadow", Command: "global-shadow"})
	save(t, c, Draft{Name: "linear", Project: "p1", URL: "https://linear.example.com/mcp", Headers: map[string]string{"X-Key": "client"}})
	save(t, c, Draft{Name: "sentry", Project: "p1", Command: "sentry"})
	save(t, c, Draft{Name: "db", Project: "p1", Command: "db", Off: []string{"x"}})
	// Replaces the global shadow in p1 even for x, which it is off for.
	save(t, c, Draft{Name: "shadow", Project: "p1", Command: "p1-shadow", Off: []string{"x"}})
	save(t, c, Draft{Name: "recipes", Project: "p2", Command: "recipes"})
	if _, err := c.store.SetProjectOff("gmail", "p1", true); err != nil {
		t.Fatal(err)
	}

	all := []string{"http", "stdio"}
	cases := []struct {
		project, harness, want string
	}{
		{"", "x", "everywhere,linear,gmail,shadow"},
		{"p1", "y", "linear,sentry,db,shadow,everywhere"},
		{"p1", "x", "linear,sentry,everywhere"},
		{"p2", "x", "recipes,everywhere,linear,gmail,shadow"},
	}
	for _, tc := range cases {
		defs := c.Servers(context.Background(), tc.harness, all, tc.project)
		if got := strings.Join(names(defs), ","); got != tc.want {
			t.Errorf("%q/%s: got %q, want %q", tc.project, tc.harness, got, tc.want)
		}
		for _, d := range defs {
			if d.Name == "linear" {
				want := "mine"
				if tc.project == "p1" {
					want = "client"
				}
				if d.Headers["X-Key"] != want {
					t.Errorf("%q/%s: linear went in with %v", tc.project, tc.harness, d.Headers)
				}
			}
		}
	}

	// Reconnecting resolves the same way.
	ctx := context.Background()
	if d, err := c.Server(ctx, "y", all, "p1", "linear"); err != nil || d.Headers["X-Key"] != "client" {
		t.Errorf("p1 linear = %+v, %v", d, err)
	}
	if d, err := c.Server(ctx, "y", all, "", "linear"); err != nil || d.Headers["X-Key"] != "mine" {
		t.Errorf("linear = %+v, %v", d, err)
	}
	if _, err := c.Server(ctx, "y", all, "p1", "gmail"); err == nil {
		t.Error("reconnected a server kept out of the project")
	}
	if _, err := c.Server(ctx, "x", all, "p1", "shadow"); err == nil {
		t.Error("fell back to the global server a project server replaces")
	}
	if _, err := c.Server(ctx, "y", all, "p2", "sentry"); err == nil {
		t.Error("reconnected another project's server")
	}
}

// Two projects' servers of one name have their own values and their own
// sign-ins.
func TestProjectServersKeepTheirOwnCredentials(t *testing.T) {
	c := newConns(t, nil)
	url := "https://linear.example.com/mcp"
	for _, p := range []string{"", "p1", "p2"} {
		save(t, c, Draft{Name: "linear", Project: p, URL: url, Headers: map[string]string{"X-Key": "key-" + p}})
		signIn(t, c, ServerKey("linear", p), url, map[string]any{"accessToken": "tok-" + p})
	}
	for _, p := range []string{"", "p1", "p2"} {
		defs := c.Servers(context.Background(), "x", []string{"http"}, p)
		if len(defs) != 1 {
			t.Fatalf("%q: %+v", p, defs)
		}
		if h := defs[0].Headers; h["X-Key"] != "key-"+p || h["Authorization"] != "Bearer tok-"+p {
			t.Errorf("%q got %v", p, h)
		}
	}

	// Signing one out and removing another leaves the rest as they were.
	if _, err := c.SignOut(context.Background(), "linear", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("linear", "p2"); err != nil {
		t.Fatal(err)
	}
	l, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	signedIn := map[string]bool{}
	for _, v := range l.Servers {
		signedIn[ServerKey(v.Name, v.Project)] = v.OAuth
	}
	if want := map[string]bool{"linear": true, "p1/linear": false}; !maps.Equal(signedIn, want) {
		t.Errorf("signed in %v, want %v", signedIn, want)
	}
	if h := c.Servers(context.Background(), "x", []string{"http"}, "")[0].Headers; h["Authorization"] != "Bearer tok-" {
		t.Errorf("global after the others changed: %v", h)
	}
}

func TestListForAProject(t *testing.T) {
	user := adapter.ConfiguredMCPServer{MCPServer: adapter.MCPServer{Name: "fs", Command: "fs"}, Origin: "User settings"}
	repoTools := adapter.ConfiguredMCPServer{MCPServer: adapter.MCPServer{Name: "Repo Tools", Command: "tools", Env: map[string]string{"K": "repo-secret"}}, Origin: ".mcp.json"}
	docs := adapter.ConfiguredMCPServer{MCPServer: adapter.MCPServer{Name: "docs", URL: "https://docs.example.com/mcp"}, Origin: "Local settings"}
	h := &fakeHost{
		transports: []string{"http", "stdio"},
		byDir:      map[string][]adapter.ConfiguredMCPServer{"": {user}},
		inFolder: map[string][]adapter.ConfiguredMCPServer{
			"/src/app": {repoTools},
			"/src/api": {docs},
			"/src/one": {repoTools},
		},
	}
	c := newConns(t, nil, Host{ID: "a", Name: "A", Host: h, Envs: []map[string]string{nil}})
	withProjects(c, map[string][]string{"p1": {"/src/app", "/src/api"}, "p2": {"/src/one"}})
	save(t, c, Draft{Name: "everywhere", Command: "e"})
	save(t, c, Draft{Name: "mine", Project: "p1", Command: "m"})
	save(t, c, Draft{Name: "theirs", Project: "p2", Command: "t"})

	found := func(l Listing) []string {
		var out []string
		for _, f := range l.Found {
			out = append(out, fmt.Sprintf("%s|%s|%s|%v", f.Name, f.Origin, f.Project, f.Added))
		}
		return out
	}
	servers := func(l Listing) []string {
		var out []string
		for _, s := range l.Servers {
			out = append(out, ServerKey(s.Name, s.Project))
		}
		return out
	}

	l, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := servers(l); !slices.Equal(got, []string{"everywhere", "p1/mine", "p2/theirs"}) {
		t.Errorf("all servers %v", got)
	}
	if got := found(l); !slices.Equal(got, []string{"fs|User settings||false"}) {
		t.Errorf("found with no project %v", got)
	}

	l, err = c.List(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got := servers(l); !slices.Equal(got, []string{"everywhere", "p1/mine"}) {
		t.Errorf("p1 servers %v", got)
	}
	want := []string{"fs|User settings||false", "Repo Tools|app: .mcp.json|p1|false", "docs|api: Local settings|p1|false"}
	if got := found(l); !slices.Equal(got, want) {
		t.Errorf("found in p1 %v, want %v", got, want)
	}

	// Adding one adds it to the project it was found in, values and all.
	v, err := c.AddFound(context.Background(), "a", "Repo Tools", "tools", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "repo-tools" || v.Project != "p1" {
		t.Errorf("added %+v", v)
	}
	srv, ok, err := c.store.Server("repo-tools", "p1")
	if env, _ := c.store.Values(srv); !ok || err != nil || env["K"] != "repo-secret" {
		t.Errorf("stored %+v %v: env %v", srv, err, env)
	}
	// It is added in p1 only: p2 finds the same server, not yet added.
	l, _ = c.List(context.Background(), "p1")
	if got := found(l)[1]; got != "Repo Tools|app: .mcp.json|p1|true" {
		t.Errorf("p1 after adding %v", got)
	}
	l, _ = c.List(context.Background(), "p2")
	if got := found(l); !slices.Equal(got, []string{"fs|User settings||false", "Repo Tools|.mcp.json|p2|false"}) {
		t.Errorf("found in p2 %v", got)
	}
	if _, err := c.AddFound(context.Background(), "a", "Repo Tools", "tools", "p1"); err == nil {
		t.Error("added the same server to p1 twice")
	}
	if _, err := c.AddFound(context.Background(), "a", "docs", "", ""); err == nil {
		t.Error("added a project folder's server everywhere")
	}

	// A project that does not exist takes no servers.
	if _, err := c.Save(context.Background(), Draft{Name: "x", Project: "gone", Command: "x"}, ""); err == nil {
		t.Error("saved a server into a missing project")
	}
	if _, err := c.SetProjectOff(context.Background(), "everywhere", "gone", true); err == nil {
		t.Error("kept a server out of a missing project")
	}
}

// A sign-in to a project server stores its tokens under that server's key,
// beside a server of the same name everywhere at the same address.
func TestSignInToAProjectServer(t *testing.T) {
	f := newOAFake(t)
	c := newConns(t, nil)
	u := f.server().URL
	save(t, c, Draft{Name: "cf", URL: u})
	save(t, c, Draft{Name: "cf", Project: "p1", URL: u})

	run, err := c.SignIn("cf", "p1", "http://localhost:4321")
	if err != nil {
		t.Fatal(err)
	}
	ia := newOAIA()
	done := make(chan error, 1)
	go func() { done <- run(context.Background(), ia) }()
	authURL := ia.authURL(t)
	// The other cf is another login at the same host.
	if q, _ := neturl.Parse(authURL); q.Query().Get("prompt") != "login" {
		t.Errorf("no fresh login asked for: %s", authURL)
	}
	ia.prompt(t).answer <- f.authorize(authURL)
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.store.Secrets().Get("p1.cf", OAuthKey); !ok {
		t.Error("no tokens under p1's cf")
	}
	if _, ok := c.store.Secrets().Get("cf", OAuthKey); ok {
		t.Error("tokens went to the cf that goes everywhere")
	}
	defs := c.Servers(context.Background(), "x", []string{"http"}, "p1")
	if len(defs) != 1 || defs[0].Headers["Authorization"] != "Bearer at-1" {
		t.Errorf("p1 session got %+v", defs)
	}
	if defs := c.Servers(context.Background(), "x", []string{"http"}, ""); len(defs) != 1 || defs[0].Headers != nil {
		t.Errorf("a session in no project got %+v", defs)
	}
}

func TestRemoveProjectForgetsItsChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := newConns(t, srv.Client())
	withProjects(c, map[string][]string{"p1": nil})
	for _, p := range []string{"", "p1"} {
		if _, err := c.Save(context.Background(), Draft{Name: "s", Project: p, URL: srv.URL}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RemoveProject("p1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.prober.Cached("p1/s"); ok {
		t.Error("the removed project's check survived")
	}
	if _, ok := c.prober.Cached("s"); !ok {
		t.Error("the check of the server that goes everywhere went too")
	}
}

// A project deleted while one of its servers was being saved, after that
// save checked for it, takes the server and its secrets with it.
func TestSaveIntoAProjectDeletedMeanwhile(t *testing.T) {
	c := newConns(t, http.DefaultClient)
	checks := 0
	c.folders = func(context.Context, string) ([]string, error) {
		if checks++; checks > 1 {
			return nil, errors.New("no project")
		}
		return []string{t.TempDir()}, nil
	}
	_, err := c.Save(context.Background(), Draft{Name: "db", Project: "p1", Command: "db", Env: map[string]string{"TOKEN": "t"}}, "")
	if err == nil {
		t.Fatal("saved into a deleted project")
	}
	if _, ok, _ := c.store.Server("db", "p1"); ok {
		t.Error("the server outlived its project")
	}
	if _, ok := c.store.Secrets().Get("p1.db", envKey+"TOKEN"); ok {
		t.Error("its secret outlived its project")
	}
}
