package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// mcpTestAdapter is a harness that takes MCP servers. With control set its
// sessions can reconnect one live; without, they cannot.
type mcpTestAdapter struct {
	scheduleBrowserAdapter
	id      string
	control bool

	mu         sync.Mutex
	reconnects []adapter.MCPServer
}

func (a *mcpTestAdapter) ID() string { return a.id }
func (a *mcpTestAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: a.id, Name: "Harness " + a.id}
}
func (*mcpTestAdapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{{ID: "default", Label: "Default"}, {ID: "bypassPermissions", Label: "Bypass", Level: adapter.LevelAll}}
}
func (*mcpTestAdapter) MCPTransports() []string { return []string{"stdio", "http"} }
func (*mcpTestAdapter) ConfiguredMCPServers(context.Context, map[string]string) ([]adapter.ConfiguredMCPServer, error) {
	return nil, nil
}
func (*mcpTestAdapter) ProjectMCPServers(context.Context, map[string]string, string) ([]adapter.ConfiguredMCPServer, error) {
	return nil, nil
}
func (a *mcpTestAdapter) CreateSession(context.Context, adapter.HostServices, adapter.CreateOptions) (adapter.Session, error) {
	s := &scheduleBrowserThread{owner: &a.scheduleBrowserAdapter, events: make(chan proto.Emission, 32)}
	if a.control {
		return &mcpTestSession{scheduleBrowserThread: s, owner: a}, nil
	}
	return s, nil
}

func (a *mcpTestAdapter) reconnected() []adapter.MCPServer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.reconnects)
}

type mcpTestSession struct {
	*scheduleBrowserThread
	owner *mcpTestAdapter
}

func (*mcpTestSession) MCPStatus(context.Context) ([]adapter.MCPServerStatus, error) { return nil, nil }
func (s *mcpTestSession) ReconnectMCP(_ context.Context, def adapter.MCPServer) error {
	s.owner.mu.Lock()
	s.owner.reconnects = append(s.owner.reconnects, def)
	s.owner.mu.Unlock()
	return nil
}

// signInHost answers like a remote MCP server that wants an OAuth sign-in;
// every other host is unreachable, so no test touches the network.
const signInHost = "signin.example.test"

type fakeRemote struct{}

func (fakeRemote) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != signInHost {
		return nil, errors.New("no network in tests")
	}
	h := http.Header{}
	h.Set("WWW-Authenticate", `Bearer resource_metadata="https://`+signInHost+`/.well-known/oauth-protected-resource"`)
	return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

type toolRig struct {
	t      *testing.T
	home   string
	st     *store.Store
	mgr    *thread.Manager
	srv    *Server
	conns  *mcp.Connections
	mst    *mcp.Store
	signer *artefact.Signer
	remote *httptest.Server
	ws     *wsClient
	ctl    *mcpTestAdapter // sessions reconnect a server live
	plain  *mcpTestAdapter // sessions cannot
}

func newToolRig(t *testing.T) *toolRig {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_STATE_HOME"} {
		t.Setenv(key, "")
	}
	notes := "notes"
	fetch := fakeSkillsCLI(t, &notes)

	st, err := store.Open(filepath.Join(t.TempDir(), "tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mst, err := mcp.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctl := &mcpTestAdapter{id: "mcp-live", control: true}
	plain := &mcpTestAdapter{id: "mcp-plain"}
	mgr := thread.NewManager(st, t.Logf, ctl, plain)
	t.Cleanup(mgr.Shutdown)
	hosts := func() []mcp.Host {
		var out []mcp.Host
		for _, h := range mgr.MCPHosts() {
			out = append(out, mcp.Host{ID: h.ID, Name: h.Name, Host: h.Host, Envs: h.Envs})
		}
		return out
	}
	folders := func(ctx context.Context, id string) ([]string, error) {
		p, err := st.Project(ctx, id)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, f := range p.Folders {
			out = append(out, f.Path)
		}
		return out, nil
	}
	conns := mcp.NewConnections(mst, &http.Client{Transport: fakeRemote{}}, auth.DefaultPort, hosts, folders, t.Logf)

	prevMCP, prevRedact := thread.UserMCP, thread.RedactToolInput
	thread.UserMCP, thread.RedactToolInput = conns, RedactAgentToolInput
	t.Cleanup(func() { thread.UserMCP, thread.RedactToolInput = prevMCP, prevRedact })

	signer := artefact.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), ArtefactSigner: signer, Connections: conns})
	srv.skillFetch = fetch
	h := srv.Handler()
	// Agents call from wherever the harness runs: through the device gate,
	// as an unpaired caller.
	remote := httptest.NewServer(asRemote(h))
	t.Cleanup(remote.Close)
	return &toolRig{t: t, home: home, st: st, mgr: mgr, srv: srv, conns: conns, mst: mst, signer: signer,
		remote: remote, ws: dialWS(t, h), ctl: ctl, plain: plain}
}

// thread starts a thread with no project.
func (r *toolRig) thread(harness, mode string) string {
	r.t.Helper()
	a, err := r.mgr.Create(context.Background(), harness, "", r.t.TempDir(), "test-model", mode)
	if err != nil {
		r.t.Fatal(err)
	}
	return a.ID
}

// projectThread starts a thread in a new project with a home and two repos.
func (r *toolRig) projectThread(harness string) (threadID string, p project.Project) {
	r.t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("p%d", time.Now().UnixNano())
	a, b := project.NewFolder("fa", r.t.TempDir()), project.NewFolder("fb", r.t.TempDir())
	for _, f := range []project.Folder{a, b} {
		if _, err := skillsGit(f.Path, "init", "-q", "-b", "main"); err != nil {
			r.t.Skipf("git init: %v", err)
		}
	}
	now := proto.NowMillis()
	p = project.Project{ID: id, Name: "Kiosk", Home: r.t.TempDir(), Folders: []project.Folder{a, b}, CreatedAt: now, UpdatedAt: now}
	if err := r.st.CreateProject(ctx, p); err != nil {
		r.t.Fatal(err)
	}
	threadID = "t-" + id
	meta := store.ThreadMeta{ID: threadID, Cwd: p.Home, Harness: harness, ProjectID: p.ID, Phase: "idle", Model: "test-model", Mode: "default", CreatedAt: now, UpdatedAt: now}
	if err := r.st.CreateThread(ctx, meta); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.mgr.Get(ctx, threadID); err != nil {
		r.t.Fatal(err)
	}
	return threadID, p
}

type toolReply struct {
	code      int
	text, err string
}

func (r *toolRig) callCtx(ctx context.Context, threadID, tool string, args any) (toolReply, error) {
	body, _ := json.Marshal(args)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.remote.URL+"/api/agent/tools/"+tool, bytes.NewReader(body))
	if err != nil {
		return toolReply{}, err
	}
	req.Header.Set("Authorization", "Bearer "+r.signer.Mint(artefact.Claims{Kind: artefact.KindAgent, Thread: threadID}))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return toolReply{}, err
	}
	defer res.Body.Close()
	var out struct{ Text, Error string }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return toolReply{}, fmt.Errorf("%s: %w", res.Status, err)
	}
	return toolReply{code: res.StatusCode, text: out.Text, err: out.Error}, nil
}

func (r *toolRig) call(threadID, tool string, args any) toolReply {
	r.t.Helper()
	got, err := r.callCtx(context.Background(), threadID, tool, args)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

// start makes a call that waits on a card.
func (r *toolRig) start(ctx context.Context, threadID, tool string, args any) <-chan toolReply {
	out := make(chan toolReply, 1)
	go func() {
		got, err := r.callCtx(ctx, threadID, tool, args)
		if err != nil {
			got.err = err.Error()
		}
		out <- got
	}()
	return out
}

func (r *toolRig) cards(threadID string) []projection.PendingElicitation {
	r.t.Helper()
	a, err := r.mgr.View(context.Background(), threadID)
	if err != nil {
		r.t.Fatal(err)
	}
	state, err := a.State(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	var out []projection.PendingElicitation
	for _, p := range state.Elicitations {
		if p.IsCard() {
			out = append(out, p)
		}
	}
	return out
}

// waitCard waits for the thread to hold n cards and returns the last.
func (r *toolRig) waitCard(threadID string, n int) (projection.PendingElicitation, cardJSON) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		cs := r.cards(threadID)
		if len(cs) == n {
			var card cardJSON
			if err := json.Unmarshal(cs[n-1].Card, &card); err != nil {
				r.t.Fatal(err)
			}
			return cs[n-1], card
		}
		if len(cs) > n || time.Now().After(deadline) {
			r.t.Fatalf("thread holds %d cards, want %d", len(cs), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *toolRig) resolve(threadID, requestID, action string, edits any) (cardOutcome, string) {
	r.t.Helper()
	args := map[string]any{"threadId": threadID, "requestId": requestID, "action": action}
	if edits != nil {
		args["edits"] = edits
	}
	res, errMsg := r.ws.do("resolve_card", args)
	if errMsg != "" {
		return cardOutcome{}, errMsg
	}
	var out struct{ Outcome cardOutcome }
	if err := json.Unmarshal(res, &out); err != nil {
		r.t.Fatal(err)
	}
	return out.Outcome, ""
}

func wait(t *testing.T, ch <-chan toolReply) toolReply {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("the tool call never returned")
		return toolReply{}
	}
}

func (r *toolRig) secret(name, project, key string) (string, bool) {
	id := strings.Replace(mcp.ServerKey(name, project), "/", ".", 1)
	return r.mst.Secrets().Get(id, key)
}

func (r *toolRig) server(name, project string) (mcp.ServerView, bool) {
	r.t.Helper()
	l, err := r.conns.List(context.Background(), project)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, v := range l.Servers {
		if v.Name == name && v.Project == project {
			return v, true
		}
	}
	return mcp.ServerView{}, false
}

func (r *toolRig) skill(threadID, name string) (skills.Skill, bool) {
	r.t.Helper()
	roots, _, err := r.mgr.SkillRoots(context.Background(), threadID, "")
	if err != nil {
		r.t.Fatal(err)
	}
	found, err := skills.Discover(roots)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, s := range found {
		if s.Name == name {
			return s, true
		}
	}
	return skills.Skill{}, false
}

func (r *toolRig) cli(id string) (mcp.CLIView, bool) {
	r.t.Helper()
	l, err := r.conns.List(context.Background(), "")
	if err != nil {
		r.t.Fatal(err)
	}
	for _, c := range l.CLIs {
		if c.ID == id {
			return c, true
		}
	}
	return mcp.CLIView{}, false
}

const skillDoc = "---\nname: tidy\ndescription: Tidies things\n---\nTidy up.\n"

func TestEachAgentWriteRaisesOneCardAndReportsWhatBecameOfIt(t *testing.T) {
	type write struct {
		name   string
		setup  func(r *toolRig, threadID string, p project.Project)
		tool   string
		args   any
		edits  any
		edited bool
		check  func(t *testing.T, r *toolRig, threadID string, p project.Project, saved bool)
	}
	stdio := "claude mcp add --transport stdio airtable --env AIRTABLE_API_KEY=sk-live-123 -- npx -y airtable-mcp-server"
	writes := []write{
		{
			name: "add a server", tool: "add_mcp_server", args: map[string]any{"config": stdio},
			check: func(t *testing.T, r *toolRig, _ string, p project.Project, saved bool) {
				_, ok := r.server("airtable", p.ID)
				if ok != saved {
					t.Fatalf("server saved = %v, want %v", ok, saved)
				}
				if v, _ := r.secret("airtable", p.ID, "env.AIRTABLE_API_KEY"); saved && v != "sk-live-123" {
					t.Errorf("held value = %q", v)
				}
			},
		},
		{
			name: "add a server, the user changing a value", tool: "add_mcp_server", args: map[string]any{"config": stdio},
			edits: map[string]any{"env": map[string]string{"AIRTABLE_API_KEY": "sk-typed-456"}}, edited: true,
			check: func(t *testing.T, r *toolRig, _ string, p project.Project, saved bool) {
				if !saved {
					return
				}
				if v, _ := r.secret("airtable", p.ID, "env.AIRTABLE_API_KEY"); v != "sk-typed-456" {
					t.Errorf("held value = %q, want the one the user typed", v)
				}
			},
		},
		{
			name: "add a server, the user moving it everywhere", tool: "add_mcp_server", args: map[string]any{"config": stdio},
			edits: map[string]any{"scope": "everywhere"}, edited: true,
			check: func(t *testing.T, r *toolRig, _ string, p project.Project, saved bool) {
				_, here := r.server("airtable", p.ID)
				_, everywhere := r.server("airtable", "")
				if here || everywhere != saved {
					t.Errorf("in the project %v, everywhere %v", here, everywhere)
				}
			},
		},
		{
			name: "remove a server", tool: "remove_mcp_server", args: map[string]any{"name": "gone"},
			setup: func(r *toolRig, _ string, p project.Project) {
				if _, err := r.conns.Save(context.Background(), mcp.Draft{Name: "gone", Project: p.ID, Command: "gone-server"}, ""); err != nil {
					r.t.Fatal(err)
				}
			},
			check: func(t *testing.T, r *toolRig, _ string, p project.Project, saved bool) {
				if _, ok := r.server("gone", p.ID); ok == saved {
					t.Errorf("server still there = %v after saved = %v", ok, saved)
				}
			},
		},
		{
			name: "install a skill", tool: "install_skill", args: map[string]any{"source": "acme/show-me"},
			check: func(t *testing.T, r *toolRig, threadID string, p project.Project, saved bool) {
				s, ok := r.skill(threadID, "show-me")
				if ok != saved {
					t.Fatalf("installed = %v, want %v", ok, saved)
				}
				if ok && s.Folder != filepath.Clean(p.Home) {
					t.Errorf("installed in %q, want the project's home", s.Folder)
				}
			},
		},
		{
			name: "install a skill, the user ticking the lone one", tool: "install_skill", args: map[string]any{"source": "acme/show-me"},
			edits: map[string]any{"skills": []string{"show-me"}},
			check: func(t *testing.T, r *toolRig, threadID string, _ project.Project, saved bool) {
				if _, ok := r.skill(threadID, "show-me"); ok != saved {
					t.Fatalf("installed = %v, want %v", ok, saved)
				}
			},
		},
		{
			name: "create a skill", tool: "create_skill", args: map[string]any{"name": "tidy", "description": "Tidies things", "content": skillDoc},
			check: func(t *testing.T, r *toolRig, threadID string, _ project.Project, saved bool) {
				s, ok := r.skill(threadID, "tidy")
				if ok != saved {
					t.Fatalf("created = %v, want %v", ok, saved)
				}
				if ok {
					got, err := os.ReadFile(filepath.Join(s.Dir, "SKILL.md"))
					if err != nil || string(got) != skillDoc {
						t.Errorf("SKILL.md = %q, %v", got, err)
					}
				}
			},
		},
		{
			name: "create a skill, the user moving it to personal", tool: "create_skill",
			args:  map[string]any{"name": "tidy", "description": "Tidies things", "content": skillDoc},
			edits: map[string]any{"destination": ""}, edited: true,
			check: func(t *testing.T, r *toolRig, threadID string, _ project.Project, saved bool) {
				s, ok := r.skill(threadID, "tidy")
				if ok != saved || ok && s.Scope != skills.ScopeUser {
					t.Errorf("created = %v in %q", ok, s.Scope)
				}
			},
		},
		{
			name: "remove a skill", tool: "remove_skill", args: map[string]any{"name": "old"},
			setup: func(r *toolRig, threadID string, _ project.Project) {
				roots, _, err := r.mgr.SkillRoots(context.Background(), threadID, "")
				if err != nil {
					r.t.Fatal(err)
				}
				if _, err := skills.Create(roots, "old", "Old one", ""); err != nil {
					r.t.Fatal(err)
				}
			},
			check: func(t *testing.T, r *toolRig, threadID string, _ project.Project, saved bool) {
				if _, ok := r.skill(threadID, "old"); ok == saved {
					t.Errorf("skill still there = %v after saved = %v", ok, saved)
				}
			},
		},
		{
			name: "add a sign-in", tool: "add_sign_in",
			args: map[string]any{"definition": map[string]any{
				"name": "Wrangler", "statusCommand": "true", "signInCommand": "wrangler login",
				"accountEnv": map[string]string{"WRANGLER_HOME": "~/.wrangler/{account}"}, "accounts": []string{"work"},
			}},
			check: func(t *testing.T, r *toolRig, _ string, _ project.Project, saved bool) {
				c, ok := r.cli("wrangler")
				if ok != saved {
					t.Fatalf("sign-in saved = %v, want %v", ok, saved)
				}
				if ok && (len(c.Accounts) != 1 || c.Accounts[0].Env["WRANGLER_HOME"] != "~/.wrangler/work") {
					t.Errorf("accounts = %+v", c.Accounts)
				}
			},
		},
		{
			name: "add a sign-in, the user fixing its command", tool: "add_sign_in",
			args:  map[string]any{"definition": map[string]any{"name": "Wrangler", "statusCommand": "true", "signInCommand": "wrangler logn"}},
			edits: map[string]any{"cli": map[string]any{"signInCommand": "wrangler login"}}, edited: true,
			check: func(t *testing.T, r *toolRig, _ string, _ project.Project, saved bool) {
				c, ok := r.cli("wrangler")
				if ok != saved || ok && c.SignInCommand != "wrangler login" {
					t.Errorf("saved = %v, sign in with %q", ok, c.SignInCommand)
				}
			},
		},
		{
			name: "add an account", tool: "add_account", args: map[string]any{"cli": "gh", "name": "work"},
			setup: func(r *toolRig, _ string, _ project.Project) {
				if _, err := r.conns.SaveCLI(mcp.CLI{ID: "gh", Name: "GitHub CLI", StatusCommand: "true", SignInCommand: "gh auth login"}, ""); err != nil {
					r.t.Fatal(err)
				}
			},
			check: func(t *testing.T, r *toolRig, _ string, _ project.Project, saved bool) {
				c, _ := r.cli("gh")
				if got := len(c.Accounts) == 1 && c.Accounts[0].Name == "work"; got != saved {
					t.Errorf("accounts = %+v", c.Accounts)
				}
			},
		},
	}
	for _, w := range writes {
		for _, action := range []string{"accept", "decline"} {
			t.Run(w.name+"/"+action, func(t *testing.T) {
				r := newToolRig(t)
				threadID, p := r.projectThread(r.ctl.id)
				if w.setup != nil {
					w.setup(r, threadID, p)
				}
				reply := r.start(context.Background(), threadID, w.tool, w.args)
				pending, card := r.waitCard(threadID, 1)
				if card.Kind != w.tool {
					t.Fatalf("card kind %q, want %q", card.Kind, w.tool)
				}
				out, errMsg := r.resolve(threadID, pending.RequestID, action, w.edits)
				if errMsg != "" {
					t.Fatalf("resolve: %s", errMsg)
				}
				got := wait(t, reply)
				if got.code != http.StatusOK || got.text == "" {
					t.Fatalf("tool replied %d %q %q", got.code, got.text, got.err)
				}
				saved := action == "accept"
				want := projection.CardDeclined
				if saved {
					want = projection.CardSaved
				}
				if out.Result != want || out.Edited != (saved && w.edited) {
					t.Errorf("outcome %+v, want %s edited %v", out, want, saved && w.edited)
				}
				if len(r.cards(threadID)) != 0 {
					t.Error("the card is still pending")
				}
				w.check(t, r, threadID, p, saved)
			})
		}
	}
}

func TestACardStillSavesAfterTheAgentStopsWaiting(t *testing.T) {
	r := newToolRig(t)
	threadID, p := r.projectThread(r.ctl.id)
	ctx, cancel := context.WithCancel(context.Background())
	reply := r.start(ctx, threadID, "add_mcp_server", map[string]any{"config": "npx -y @acme/mcp-db"})
	pending, _ := r.waitCard(threadID, 1)
	cancel()
	if got := wait(t, reply); got.code == http.StatusOK {
		t.Fatalf("a call the agent gave up on still answered: %+v", got)
	}
	out, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil)
	if errMsg != "" || out.Result != projection.CardSaved {
		t.Fatalf("resolve: %+v %s", out, errMsg)
	}
	if _, ok := r.server("mcp-db", p.ID); !ok {
		t.Error("the server was not saved")
	}
}

func TestAWriteThatWaitsTooLongLeavesTheCardAnswerable(t *testing.T) {
	r := newToolRig(t)
	prev := cardWait
	cardWait = 50 * time.Millisecond
	t.Cleanup(func() { cardWait = prev })
	threadID, p := r.projectThread(r.ctl.id)
	got := r.call(threadID, "add_mcp_server", map[string]any{"config": "npx -y @acme/mcp-db"})
	if got.code != http.StatusOK {
		t.Fatalf("tool replied %+v", got)
	}
	pending, _ := r.waitCard(threadID, 1)
	if _, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil); errMsg != "" {
		t.Fatal(errMsg)
	}
	if _, ok := r.server("mcp-db", p.ID); !ok {
		t.Error("the server was not saved")
	}
}

func TestAThreadWithoutAProjectCannotAimAtOne(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"add_mcp_server", map[string]any{"config": "npx -y @acme/mcp-db", "scope": "project"}},
		{"remove_mcp_server", map[string]any{"name": "x", "scope": "project"}},
		{"install_skill", map[string]any{"source": "acme/show-me", "destination": "project"}},
		{"install_skill", map[string]any{"source": "acme/show-me", "destination": "repo"}},
		{"create_skill", map[string]any{"name": "tidy", "description": "d", "content": skillDoc, "destination": "project"}},
		{"create_skill", map[string]any{"name": "tidy", "description": "d", "content": skillDoc, "destination": "repo"}},
	} {
		if got := r.call(threadID, c.tool, c.args); got.code != http.StatusBadRequest {
			t.Errorf("%s %v: %+v", c.tool, c.args, got)
		}
	}
	if n := len(r.cards(threadID)); n != 0 {
		t.Errorf("%d cards raised", n)
	}
	// Everywhere and personal are still open to it.
	reply := r.start(context.Background(), threadID, "create_skill", map[string]any{"name": "tidy", "description": "d", "content": skillDoc})
	pending, card := r.waitCard(threadID, 1)
	if card.Destination == nil || *card.Destination != "" {
		t.Errorf("destination %v, want personal", card.Destination)
	}
	r.resolve(threadID, pending.RequestID, "decline", nil)
	wait(t, reply)
}

func TestBypassModeStillAsks(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "bypassPermissions")
	reply := r.start(context.Background(), threadID, "add_mcp_server", map[string]any{"config": "npx -y @acme/mcp-db"})
	pending, _ := r.waitCard(threadID, 1)
	if _, ok := r.server("mcp-db", ""); ok {
		t.Fatal("saved before the user answered")
	}
	r.resolve(threadID, pending.RequestID, "decline", nil)
	wait(t, reply)
	if _, ok := r.server("mcp-db", ""); ok {
		t.Error("saved after the user declined")
	}
}

func TestValuesReachTheSecretStoreAndNothingElse(t *testing.T) {
	r := newToolRig(t)
	threadID, p := r.projectThread(r.ctl.id)
	config := `{"mcpServers": {"cf": {"command": "npx",
		"args": ["-y", "mcp-remote@latest", "https://cf.example.com/mcp", "--header", "Authorization: Bearer ${TOKEN}"],
		"env": {"TOKEN": "tok-agent-passed"}}}}`
	other := `claude mcp add --transport stdio db --env DB_PASSWORD=pw-agent-passed -- db-server`

	// The agent's own call, as its harness reports it.
	actor, err := r.mgr.Get(context.Background(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"config": config})
	if err := actor.Emit(context.Background(), proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
		ToolCallID: "call-1", Kind: proto.KindOther, Title: "mcp__omniplex__add_mcp_server", Status: proto.StatusInProgress, RawInput: input,
	})); err != nil {
		t.Fatal(err)
	}

	reply := r.start(context.Background(), threadID, "add_mcp_server", map[string]any{"config": config})
	pending, card := r.waitCard(threadID, 1)
	if len(card.Server.Headers) != 1 || !card.Server.Headers[0].Held {
		t.Errorf("card headers %+v", card.Server.Headers)
	}
	out, errMsg := r.resolve(threadID, pending.RequestID, "accept", map[string]any{"headers": map[string]string{"Authorization": "Bearer tok-user-typed"}})
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	got := wait(t, reply)

	reply = r.start(context.Background(), threadID, "add_mcp_server", map[string]any{"config": other})
	pending, _ = r.waitCard(threadID, 1)
	if _, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil); errMsg != "" {
		t.Fatal(errMsg)
	}
	got2 := wait(t, reply)

	if v, _ := r.secret("cf", p.ID, "header.Authorization"); v != "Bearer tok-user-typed" {
		t.Errorf("header held as %q", v)
	}
	if v, _ := r.secret("db", p.ID, "env.DB_PASSWORD"); v != "pw-agent-passed" {
		t.Errorf("env held as %q", v)
	}
	// The resolve went over the socket without a ledger row.
	if _, done, err := r.st.ClaimCommand(context.Background(), fmt.Sprintf("c%d", r.ws.n), threadID); err != nil || done {
		t.Errorf("resolve_card left a ledger row: done %v, %v", done, err)
	}

	events, err := r.st.ReadEvents(context.Background(), threadID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(events)
	outcome, _ := json.Marshal(out)
	everything := string(stored) + string(outcome) + got.text + got2.text
	for _, secret := range []string{"tok-agent-passed", "tok-user-typed", "pw-agent-passed"} {
		if strings.Contains(everything, secret) {
			t.Errorf("%s reached the log, the outcome or the agent", secret)
		}
	}
	if !strings.Contains(string(stored), "mcp-remote") {
		t.Error("the tool call's input was not stored at all")
	}
}

func TestListsNeverShowAValue(t *testing.T) {
	r := newToolRig(t)
	ctx := context.Background()
	threadID, p := r.projectThread(r.ctl.id)
	loose := r.thread(r.ctl.id, "default")
	if err := r.st.CreateProject(ctx, project.Project{ID: "elsewhere", Name: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []mcp.Draft{
		{Name: "shared", Command: "shared-server", Env: map[string]string{"SHARED_KEY": "val-shared"}},
		{Name: "mine", Project: p.ID, URL: "https://mine.example.com/mcp", Headers: map[string]string{"X-Key": "val-mine"}},
		{Name: "theirs", Project: "elsewhere", Command: "theirs-server", Env: map[string]string{"THEIR_KEY": "val-theirs"}},
	} {
		if _, err := r.conns.Save(ctx, d, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.conns.SaveCLI(mcp.CLI{ID: "gh", Name: "GitHub CLI", StatusCommand: "true", SignInCommand: "gh auth login",
		AccountEnv: map[string]string{"GH_CONFIG_DIR": "~/.gh/{account}"}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.conns.AddAccount("gh", "work"); err != nil {
		t.Fatal(err)
	}

	roots, _, err := r.mgr.SkillRoots(ctx, threadID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Create(roots, "tidy", "Tidies things", ""); err != nil {
		t.Fatal(err)
	}

	inProject := r.call(threadID, "list_mcp_servers", nil)
	outside := r.call(loose, "list_mcp_servers", nil)
	signIns := r.call(threadID, "list_sign_ins", nil)
	listed := r.call(threadID, "list_skills", nil)
	for _, got := range []toolReply{inProject, outside, signIns, listed} {
		if got.code != http.StatusOK {
			t.Fatalf("list replied %+v", got)
		}
		for _, v := range []string{"val-shared", "val-mine", "val-theirs"} {
			if strings.Contains(got.text, v) {
				t.Errorf("a list showed %s:\n%s", v, got.text)
			}
		}
	}
	has := func(text, s string) bool { return strings.Contains(text, s) }
	if !has(inProject.text, "shared") || !has(inProject.text, "mine") || has(inProject.text, "theirs") {
		t.Errorf("in the project:\n%s", inProject.text)
	}
	if !has(outside.text, "shared") || has(outside.text, "mine") || has(outside.text, "theirs") {
		t.Errorf("outside any project:\n%s", outside.text)
	}
	if !has(inProject.text, "SHARED_KEY") || !has(inProject.text, "X-Key") {
		t.Errorf("value names missing:\n%s", inProject.text)
	}
	if want := filepath.Join(r.home, ".gh", "work"); !has(signIns.text, want) {
		t.Errorf("sign-ins lack the expanded env %s:\n%s", want, signIns.text)
	}
	if !has(listed.text, "tidy") {
		t.Errorf("skills:\n%s", listed.text)
	}
}

func TestAddServerTakesEveryFormatParseReads(t *testing.T) {
	inputs := []string{
		"  https://mcp.cloudflare.com/mcp \n",
		"https://observability.mcp.cloudflare.com/mcp",
		"http://127.0.0.1:8123/mcp",
		`claude mcp add sentry https://mcp.sentry.dev/mcp --transport http -H "X-Api-Key: abc" --header 'X-Org: acme'`,
		"claude mcp add --transport stdio airtable --env AIRTABLE_API_KEY=YOUR_KEY -- npx -y airtable-mcp-server",
		"claude mcp add -e A=1 B=x=y db -- ./server --port 3",
		"$ claude mcp add --transport http \\\n  linear \\\n  https://mcp.linear.app/mcp",
		`claude mcp add-json weather '{"type":"stdio","command":"/opt/weather","args":["--units","metric"],"env":{"CACHE":"/tmp"}}'`,
		"codex mcp add docs --url https://developers.openai.com/mcp",
		"codex mcp add gh --env GITHUB_TOKEN=ghp_x -- npx -y @modelcontextprotocol/server-github",
		`{"mcpServers": {"zeta": {"type": "http", "url": "https://z.example.com/mcp", "headers": {"Authorization": "Bearer t"}}, "alpha": {"command": "alpha"}}}`,
		`{"type": "http", "url": "https://api.githubcopilot.com/mcp/"}`,
		`"Linear Tools": {"serverUrl": "https://mcp.linear.app/mcp"},`,
		`{"mcpServers": {"cf-observability": {"command": "npx", "args": ["-y", "mcp-remote@latest", "https://observability.mcp.cloudflare.com/mcp", "--header", "Authorization: Bearer ${TOKEN}"], "env": {"TOKEN": "secret"}}}}`,
		"[mcp_servers.figma]\nurl = \"https://mcp.figma.com/mcp\"\nhttp_headers = { \"X-Team\" = \"t1\" }\nenv_http_headers = { \"X-Key\" = \"FIGMA_KEY\" }\n",
		"[mcp_servers.fs]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-filesystem\", \"/tmp\"]\n[mcp_servers.fs.env]\nDEBUG = \"1\"\n",
		"npx -y @modelcontextprotocol/server-github@1.2.0",
	}
	r := newToolRig(t)
	threadID, p := r.projectThread(r.ctl.id)
	for _, in := range inputs {
		want, err := mcp.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(want.Name, func(t *testing.T) {
			reply := r.start(context.Background(), threadID, "add_mcp_server", map[string]any{"config": in})
			pending, card := r.waitCard(threadID, 1)
			if card.Server == nil || card.Server.Name != want.Name {
				t.Fatalf("card server %+v", card.Server)
			}
			if _, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil); errMsg != "" {
				t.Fatal(errMsg)
			}
			if got := wait(t, reply); got.code != http.StatusOK {
				t.Fatalf("tool replied %+v", got)
			}
			v, ok := r.server(want.Name, p.ID)
			if !ok || v.URL != want.URL || v.Command != want.Command || !slices.Equal(v.Args, want.Args) {
				t.Fatalf("saved %+v, parsed %+v", v, want)
			}
			for prefix, values := range map[string]map[string]string{"env.": want.Env, "header.": want.Headers} {
				for name, value := range values {
					if value == "" {
						continue
					}
					if got, _ := r.secret(want.Name, p.ID, prefix+name); got != value {
						t.Errorf("%s%s held as %q", prefix, name, got)
					}
				}
			}
		})
	}
}

func TestSkillsGoWhereTheAgentAsks(t *testing.T) {
	for _, dest := range []string{"", "project", "repo", "personal"} {
		for _, tool := range []string{"install_skill", "create_skill"} {
			t.Run(tool+"/"+dest, func(t *testing.T) {
				r := newToolRig(t)
				threadID, p := r.projectThread(r.ctl.id)
				args := map[string]any{"source": "acme/show-me", "destination": dest}
				name := "show-me"
				if tool == "create_skill" {
					args = map[string]any{"name": "tidy", "description": "Tidies things", "content": skillDoc, "destination": dest}
					name = "tidy"
				}
				reply := r.start(context.Background(), threadID, tool, args)
				pending, card := r.waitCard(threadID, 1)
				out, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil)
				if errMsg != "" {
					t.Fatal(errMsg)
				}
				wait(t, reply)
				s, ok := r.skill(threadID, name)
				if !ok {
					t.Fatal("not there")
				}
				switch dest {
				case "", "project":
					if s.Folder != filepath.Clean(p.Home) {
						t.Errorf("in %q, want the project home", s.Folder)
					}
				case "repo":
					if s.Folder != filepath.Clean(p.Folders[0].Path) {
						t.Errorf("in %q, want the first repo", s.Folder)
					}
				case "personal":
					if s.Scope != skills.ScopeUser {
						t.Errorf("scope %q, want personal", s.Scope)
					}
				}
				if card.Destinations == nil || out.Destination == "" {
					t.Errorf("card destinations %v, outcome %+v", card.Destinations, out)
				}
			})
		}
	}
}

func TestDecliningAnInstallDropsWhatWasFetched(t *testing.T) {
	r := newToolRig(t)
	threadID, _ := r.projectThread(r.ctl.id)
	reply := r.start(context.Background(), threadID, "install_skill", map[string]any{"source": "acme/show-me"})
	pending, card := r.waitCard(threadID, 1)
	staged, _ := filepath.Glob(filepath.Join(os.TempDir(), "omniplex-skills-"+card.Staged.ID))
	if len(staged) != 1 {
		t.Fatalf("nothing staged for %s", card.Staged.ID)
	}
	r.resolve(threadID, pending.RequestID, "decline", nil)
	wait(t, reply)
	if _, err := os.Stat(staged[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staged copy still there: %v", err)
	}
}

func TestASavedServerReachesTheLiveSessionWhenItCan(t *testing.T) {
	for _, live := range []bool{true, false} {
		t.Run(fmt.Sprint(live), func(t *testing.T) {
			r := newToolRig(t)
			harness := r.plain
			if live {
				harness = r.ctl
			}
			threadID, _ := r.projectThread(harness.id)
			reply := r.start(context.Background(), threadID, "add_mcp_server",
				map[string]any{"config": "claude mcp add --transport stdio db --env DB_PASSWORD=pw-1 -- db-server"})
			pending, _ := r.waitCard(threadID, 1)
			out, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil)
			if errMsg != "" {
				t.Fatal(errMsg)
			}
			wait(t, reply)
			got := harness.reconnected()
			if live {
				if out.Live != liveNow || len(got) != 1 || got[0].Name != "db" || got[0].Env["DB_PASSWORD"] != "pw-1" {
					t.Errorf("live %q, reconnected %+v", out.Live, got)
				}
				return
			}
			if out.Live != liveNextSession || len(got) != 0 {
				t.Errorf("live %q, reconnected %+v", out.Live, got)
			}
		})
	}
}

func TestARemoteServerThatWantsASignInSaysSo(t *testing.T) {
	r := newToolRig(t)
	threadID, _ := r.projectThread(r.ctl.id)
	reply := r.start(context.Background(), threadID, "add_mcp_server", map[string]any{"config": "https://" + signInHost + "/mcp"})
	pending, _ := r.waitCard(threadID, 1)
	out, errMsg := r.resolve(threadID, pending.RequestID, "accept", nil)
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	wait(t, reply)
	if !out.NeedsSignIn {
		t.Errorf("outcome %+v", out)
	}
}

func TestResolvingACard(t *testing.T) {
	r := newToolRig(t)
	threadID, p := r.projectThread(r.ctl.id)
	reply := r.start(context.Background(), threadID, "add_mcp_server",
		map[string]any{"config": "claude mcp add --transport stdio db --env DB_PASSWORD=pw-1 -- db-server"})
	pending, _ := r.waitCard(threadID, 1)

	// An edit the server cannot apply leaves the card waiting.
	if _, errMsg := r.resolve(threadID, pending.RequestID, "accept", map[string]any{"env": map[string]string{"NOPE": "x"}}); errMsg == "" {
		t.Fatal("an edit to a value the server does not have was applied")
	}
	if len(r.cards(threadID)) != 1 {
		t.Fatal("a failed apply answered the card")
	}
	if _, ok := r.server("db", p.ID); ok {
		t.Fatal("a failed apply saved the server")
	}

	out, errMsg := r.resolve(threadID, pending.RequestID, "accept", map[string]any{"env": map[string]string{"DB_PASSWORD": "pw-2"}})
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if got := wait(t, reply); !strings.Contains(got.text, "DB_PASSWORD") || strings.Contains(got.text, "pw-2") {
		t.Errorf("the agent was told %q", got.text)
	}
	if v, _ := r.secret("db", p.ID, "env.DB_PASSWORD"); v != "pw-2" {
		t.Errorf("held %q", v)
	}

	// The same answer again, as a client resends after a reconnect, gets
	// the same reply; a different one is refused.
	again, errMsg := r.resolve(threadID, pending.RequestID, "accept", map[string]any{"env": map[string]string{"DB_PASSWORD": "pw-2"}})
	if errMsg != "" || again.Result != out.Result || again.Summary != out.Summary {
		t.Errorf("resent: %+v %s", again, errMsg)
	}
	if _, errMsg := r.resolve(threadID, pending.RequestID, "decline", nil); errMsg == "" {
		t.Error("a second, different answer was taken")
	}
}

func TestACardTheServerLostIsCancelled(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	actor, err := r.mgr.Get(context.Background(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	// As a restart leaves one: on the thread, but held by no server.
	if err := actor.RaiseCard(context.Background(), "card_lost", "Add db", json.RawMessage(`{"kind":"add_mcp_server"}`)); err != nil {
		t.Fatal(err)
	}
	if _, errMsg := r.resolve(threadID, "card_lost", "accept", nil); errMsg == "" {
		t.Fatal("a card nobody holds was applied")
	}
	if n := len(r.cards(threadID)); n != 0 {
		t.Fatalf("%d cards still pending", n)
	}
	if _, errMsg := r.resolve(threadID, "card_unknown", "accept", nil); errMsg == "" {
		t.Error("a card that was never raised was answered")
	}
}

func TestAgentToolsRefuseBadCallers(t *testing.T) {
	r := newToolRig(t)
	threadID := r.thread(r.ctl.id, "default")
	req, _ := http.NewRequest(http.MethodPost, r.remote.URL+"/api/agent/tools/list_skills", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+r.signer.Mint(artefact.Claims{Kind: artefact.KindPreview, Thread: threadID}))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a preview token got %d", res.StatusCode)
	}
	if got := r.call(threadID, "drop_tables", nil); got.code != http.StatusNotFound {
		t.Errorf("an unknown tool got %+v", got)
	}
}

func TestRedactAgentToolInput(t *testing.T) {
	cfg := "claude mcp add --transport stdio db --env DB_PASSWORD=hunter2 -- db-server --user dbuser"
	input, _ := json.Marshal(map[string]string{"config": cfg})
	for _, name := range []string{"mcp__omniplex__add_mcp_server", "omniplex/add_mcp_server"} {
		got := string(RedactAgentToolInput(name, input))
		if strings.Contains(got, "hunter2") || !strings.Contains(got, "db-server") {
			t.Errorf("%s: %s", name, got)
		}
	}
	if got := RedactAgentToolInput("Bash", input); !bytes.Equal(got, input) {
		t.Errorf("another tool's input changed: %s", got)
	}
	unreadable, _ := json.Marshal(map[string]string{"config": "claude mcp add x \"unclosed hunter2"})
	if got := string(RedactAgentToolInput("omniplex/add_mcp_server", unreadable)); strings.Contains(got, "hunter2") {
		t.Errorf("an unreadable config kept its text: %s", got)
	}
	// A short value does not take bites out of words around it.
	short, _ := json.Marshal(map[string]string{"config": "claude mcp add -e A=1 db -- server1 --port 1"})
	if got := string(RedactAgentToolInput("omniplex/add_mcp_server", short)); !strings.Contains(got, "server1") || strings.Contains(got, "port 1\"") {
		t.Errorf("short value: %s", got)
	}
}
