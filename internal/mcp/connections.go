package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// tokenWait bounds fetching (and refreshing) one server's token when a
// session starts. A slow authorization server must not hold the session up:
// the server goes in without a token and the harness reports it needs auth.
// Tests shorten it.
var tokenWait = 5 * time.Second

// cliCheckWait backs up the status command's own timeout.
const cliCheckWait = 30 * time.Second

// Host is one harness that takes MCP servers, as the thread manager knows
// it: the adapter's MCPHost and the env overlay of each of its provider
// instances, which can point at different config folders.
type Host struct {
	ID   string
	Name string
	Host adapter.MCPHost
	Envs []map[string]string
}

// Connections is the MCP servers and sign-ins feature: definitions,
// credentials, checks, and what each session gets. It knows harnesses only
// through adapter.MCPHost.
type Connections struct {
	store  *Store
	oauth  *OAuth
	prober *Prober
	hosts  func() []Host
	port   int
	logf   func(string, ...any)

	cliMu  sync.Mutex
	checks map[string]map[string]accountCheck
}

type accountCheck struct {
	status, detail string
	at             time.Time
}

// NewConnections ties the store to an OAuth client and a prober sharing
// client (nil means http.DefaultClient). port is this server's, for the
// loopback OAuth callback; hosts lists the harnesses that take MCP servers.
func NewConnections(store *Store, client *http.Client, port int, hosts func() []Host, logf func(string, ...any)) *Connections {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if hosts == nil {
		hosts = func() []Host { return nil }
	}
	return &Connections{
		store:  store,
		oauth:  NewOAuth(store.Secrets(), client),
		prober: NewProber(client),
		hosts:  hosts,
		port:   port,
		logf:   logf,
		checks: map[string]map[string]accountCheck{},
	}
}

// OAuth is the client that signs in to remote servers; its callback route
// is mounted by the HTTP server.
func (c *Connections) OAuth() *OAuth { return c.oauth }

// --- views (the wire shapes) ---

// HarnessView is a harness that takes MCP servers, for the per-harness
// switches.
type HarnessView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Transports []string `json:"transports"`
}

// ServerView is a server as a client sees it: names of its credentials,
// never their values.
type ServerView struct {
	Name        string     `json:"name"`
	URL         string     `json:"url,omitempty"`
	Command     string     `json:"command,omitempty"`
	Args        []string   `json:"args,omitempty"`
	EnvNames    []string   `json:"envNames"`
	HeaderNames []string   `json:"headerNames"`
	Off         []string   `json:"off"`
	OAuth       bool       `json:"oauth"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	CheckedAt   *time.Time `json:"checkedAt,omitempty"`
}

// FoundView is a server a harness's own config defines.
type FoundView struct {
	Name    string   `json:"name"`
	Harness string   `json:"harness"`
	Origin  string   `json:"origin"`
	URL     string   `json:"url,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Added   bool     `json:"added"`
}

// CLIView is a CLI with its accounts' last known status.
type CLIView struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	StatusCommand   string            `json:"statusCommand"`
	SignedInPattern string            `json:"signedInPattern"`
	SignInCommand   string            `json:"signInCommand"`
	PrepareCommand  string            `json:"prepareCommand"`
	AccountEnv      map[string]string `json:"accountEnv"`
	Accounts        []AccountView     `json:"accounts"`
}

// AccountView is one account and its last known status.
type AccountView struct {
	Name      string            `json:"name"`
	Env       map[string]string `json:"env"`
	Status    string            `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	CheckedAt *time.Time        `json:"checkedAt,omitempty"`
}

// Listing is everything the Connections screen shows.
type Listing struct {
	Harnesses []HarnessView `json:"harnesses"`
	Servers   []ServerView  `json:"servers"`
	Found     []FoundView   `json:"found"`
	CLIs      []CLIView     `json:"clis"`
}

// List answers from what is stored and cached; it never checks a server or
// an account.
func (c *Connections) List(ctx context.Context) (Listing, error) {
	f, err := c.store.Read()
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Harnesses: []HarnessView{}, Servers: []ServerView{}, Found: []FoundView{}, CLIs: []CLIView{}}
	for _, h := range c.hosts() {
		out.Harnesses = append(out.Harnesses, HarnessView{ID: h.ID, Name: h.Name, Transports: nonNil(h.Host.MCPTransports())})
	}
	for _, s := range f.Servers {
		out.Servers = append(out.Servers, c.view(s))
	}
	for _, fs := range c.found(ctx) {
		v := FoundView{Name: fs.server.Name, Harness: fs.harness, Origin: fs.server.Origin, URL: fs.server.URL, Command: fs.server.Command, Args: fs.server.Args}
		// By name, fitted to the naming rule the way AddFound fits it.
		added := slugName(fs.server.Name)
		v.Added = slices.ContainsFunc(f.Servers, func(s Server) bool { return s.Name == added })
		out.Found = append(out.Found, v)
	}
	for _, cli := range f.CLIs {
		out.CLIs = append(out.CLIs, c.cliView(cli))
	}
	return out, nil
}

func (c *Connections) view(s Server) ServerView {
	v := ServerView{
		Name: s.Name, URL: s.URL, Command: s.Command, Args: s.Args,
		EnvNames: nonNil(s.EnvNames), HeaderNames: nonNil(s.HeaderNames), Off: nonNil(s.Off),
		OAuth: s.URL != "" && c.oauth.SignedIn(s.Name), Status: StatusUnchecked,
	}
	if s.URL == "" {
		return v
	}
	if ch, ok := c.prober.Cached(s.Name); ok {
		at := ch.At
		v.Status, v.Error, v.CheckedAt = ch.Status, ch.Error, &at
	}
	return v
}

type foundServer struct {
	harness string
	server  adapter.ConfiguredMCPServer
}

// found asks every provider instance of every harness that takes MCP
// servers for the ones its own config defines. Instances can share a config
// folder, so the same server is listed once.
func (c *Connections) found(ctx context.Context) []foundServer {
	var out []foundServer
	seen := map[string]bool{}
	for _, h := range c.hosts() {
		for _, env := range h.Envs {
			servers, err := h.Host.ConfiguredMCPServers(ctx, env)
			if err != nil {
				c.logf("mcp servers in %s's config: %v", h.ID, err)
				continue
			}
			for _, s := range servers {
				key := h.ID + "\x00" + s.Name + "\x00" + foundWhere(s.MCPServer)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, foundServer{harness: h.ID, server: s})
			}
		}
	}
	return out
}

// --- servers ---

func (c *Connections) server(name string) (Server, error) {
	s, ok, err := c.store.Server(name)
	if err != nil {
		return Server{}, err
	}
	if !ok {
		return Server{}, fmt.Errorf("no MCP server named %q", name)
	}
	return s, nil
}

// Save stores a server (see Store.SaveServer) and checks it.
func (c *Connections) Save(ctx context.Context, d Draft, previous string) (ServerView, error) {
	s, err := c.store.SaveServer(d, previous)
	if err != nil {
		return ServerView{}, err
	}
	if previous != "" {
		c.prober.Forget(previous)
	}
	return c.check(ctx, s), nil
}

// SetOff changes which harnesses do not get a server, and nothing else.
func (c *Connections) SetOff(name string, off []string) (ServerView, error) {
	s, err := c.store.SetOff(name, off)
	if err != nil {
		return ServerView{}, err
	}
	return c.view(s), nil
}

// Remove deletes a server and its secrets.
func (c *Connections) Remove(name string) error {
	if err := c.store.RemoveServer(name); err != nil {
		return err
	}
	c.prober.Forget(name)
	return nil
}

// foundWhere tells apart same-named servers in different instances' configs.
func foundWhere(s adapter.MCPServer) string {
	if s.URL != "" {
		return s.URL
	}
	return s.Command
}

// AddFound copies a server out of a harness's config into omniplex, values
// included. where is its URL or command, as listed: two instances of one
// harness can each define a server of the same name.
func (c *Connections) AddFound(ctx context.Context, harness, name, where string) (ServerView, error) {
	var hit *adapter.ConfiguredMCPServer
	for _, fs := range c.found(ctx) {
		if fs.harness == harness && fs.server.Name == name && (where == "" || foundWhere(fs.server.MCPServer) == where) {
			hit = &fs.server
			break
		}
	}
	if hit == nil {
		return ServerView{}, fmt.Errorf("no server named %q in that agent's config", name)
	}
	d := Draft{Name: slugName(name), URL: hit.URL, Command: hit.Command, Args: hit.Args, Env: hit.Env, Headers: hit.Headers}
	if d.URL != "" {
		d.Env, d.Args, d.Command = nil, nil, ""
	} else {
		d.Headers = nil
	}
	if _, ok, err := c.store.Server(d.Name); err != nil {
		return ServerView{}, err
	} else if ok {
		return ServerView{}, fmt.Errorf("there is already an MCP server named %q", d.Name)
	}
	return c.Save(ctx, d, "")
}

// Check probes one server now.
func (c *Connections) Check(ctx context.Context, name string) (ServerView, error) {
	s, err := c.server(name)
	if err != nil {
		return ServerView{}, err
	}
	return c.check(ctx, s), nil
}

func (c *Connections) check(ctx context.Context, s Server) ServerView {
	if s.URL != "" {
		c.probed(ctx, s)
	}
	return c.view(s)
}

// probed checks a remote server as a session would get it and returns that
// definition. An access token the server turns away is refreshed once and
// checked again: its expiry is only the token's own claim, and a server can
// stop honouring it sooner (a restart, a revocation).
func (c *Connections) probed(ctx context.Context, s Server) adapter.MCPServer {
	def, token := c.definition(ctx, s)
	if c.prober.Probe(ctx, def).Status != StatusSignIn || token == "" {
		return def
	}
	tctx, cancel := context.WithTimeout(ctx, tokenWait)
	_, err := c.oauth.Refresh(tctx, s, token)
	cancel()
	if err != nil {
		if !errors.Is(err, ErrSignInNeeded) {
			c.logf("mcp server %s: refresh: %v", s.Name, err)
		}
		return def
	}
	def, _ = c.definition(ctx, s)
	c.prober.Probe(ctx, def)
	return def
}

// SignOut forgets a server's OAuth tokens and checks it again.
func (c *Connections) SignOut(ctx context.Context, name string) (ServerView, error) {
	s, err := c.server(name)
	if err != nil {
		return ServerView{}, err
	}
	if err := c.oauth.SignOut(name); err != nil {
		return ServerView{}, err
	}
	return c.check(ctx, s), nil
}

// SignIn returns the flow that signs in to a remote server, for the auth
// flow engine to run. origin is the address the person's browser has this
// server at; it decides where the authorization server sends them back.
func (c *Connections) SignIn(name, origin string) (func(context.Context, adapter.AuthInteraction) error, error) {
	s, err := c.server(name)
	if err != nil {
		return nil, err
	}
	if s.URL == "" {
		return nil, fmt.Errorf("%s is run by a command and has no sign-in", name)
	}
	redirect := RedirectURI(origin, c.port)
	return func(ctx context.Context, ia adapter.AuthInteraction) error {
		if err := c.oauth.SignIn(ctx, ia, s, redirect); err != nil {
			return err
		}
		// So the list shows where it stands without another tap.
		c.probed(ctx, s)
		return nil
	}, nil
}

// --- what sessions get ---

// takes reports whether a harness gets a server: it runs that kind and is
// not switched off for it.
func takes(s Server, harness string, transports []string) bool {
	kind := "http"
	if s.URL == "" {
		kind = "stdio"
	}
	return slices.Contains(transports, kind) && !slices.Contains(s.Off, harness)
}

// definition is a server as a session gets it: values from the secret
// store, and a current OAuth access token when there is one, which comes
// back too. A token that cannot be had within tokenWait is left out.
func (c *Connections) definition(ctx context.Context, s Server) (def adapter.MCPServer, token string) {
	env, headers := c.store.Values(s)
	def = adapter.MCPServer{Name: s.Name, URL: s.URL, Command: s.Command, Args: s.Args}
	if len(env) > 0 && s.Command != "" {
		def.Env = env
	}
	if s.URL != "" {
		tctx, cancel := context.WithTimeout(ctx, tokenWait)
		var err error
		token, err = c.oauth.Token(tctx, s)
		cancel()
		switch {
		case err == nil && token != "":
			for k := range headers {
				if strings.EqualFold(k, "Authorization") {
					delete(headers, k)
				}
			}
			headers["Authorization"] = "Bearer " + token
		case err != nil && !errors.Is(err, ErrSignInNeeded):
			c.logf("mcp server %s: token: %v", s.Name, err)
		}
		if len(headers) > 0 {
			def.Headers = headers
		}
	}
	return def, token
}

// Servers is what a session of the given harness gets, in the order the
// user added them. Tokens are fetched concurrently, so the slowest one, not
// their sum, bounds the wait.
func (c *Connections) Servers(ctx context.Context, harness string, transports []string) []adapter.MCPServer {
	f, err := c.store.Read()
	if err != nil {
		c.logf("mcp servers: %v", err)
		return nil
	}
	var picked []Server
	for _, s := range f.Servers {
		if takes(s, harness, transports) {
			picked = append(picked, s)
		}
	}
	out := make([]adapter.MCPServer, len(picked))
	var wg sync.WaitGroup
	for i, s := range picked {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i], _ = c.definition(ctx, s)
		}()
	}
	wg.Wait()
	return out
}

// Server is one server's current definition for a session of the given
// harness, for reconnecting it: checked first, so a token the server now
// turns away is refreshed before the session gets it.
func (c *Connections) Server(ctx context.Context, harness string, transports []string, name string) (adapter.MCPServer, error) {
	s, err := c.server(name)
	if err != nil {
		return adapter.MCPServer{}, err
	}
	if !takes(s, harness, transports) {
		return adapter.MCPServer{}, fmt.Errorf("this thread's agent does not get %s", name)
	}
	if s.URL != "" {
		return c.probed(ctx, s), nil
	}
	def, _ := c.definition(ctx, s)
	return def, nil
}

// --- sign-ins (CLI accounts) ---

func (c *Connections) cli(id string) (CLI, error) {
	f, err := c.store.Read()
	if err != nil {
		return CLI{}, err
	}
	i := slices.IndexFunc(f.CLIs, func(x CLI) bool { return x.ID == id })
	if i < 0 {
		return CLI{}, fmt.Errorf("no sign-in named %q", id)
	}
	return f.CLIs[i], nil
}

func (c *Connections) cliView(cli CLI) CLIView {
	v := CLIView{
		ID: cli.ID, Name: cli.Name, StatusCommand: cli.StatusCommand, SignedInPattern: cli.SignedInPattern,
		SignInCommand: cli.SignInCommand, PrepareCommand: cli.PrepareCommand,
		AccountEnv: nonNilMap(cli.AccountEnv), Accounts: []AccountView{},
	}
	c.cliMu.Lock()
	defer c.cliMu.Unlock()
	for _, a := range cli.Accounts {
		av := AccountView{Name: a.Name, Env: nonNilMap(a.Env), Status: StatusUnchecked}
		if ch, ok := c.checks[cli.ID][a.Name]; ok {
			at := ch.at
			av.Status, av.Detail, av.CheckedAt = ch.status, ch.detail, &at
		}
		v.Accounts = append(v.Accounts, av)
	}
	return v
}

func (c *Connections) noteCheck(id, account, status, detail string) {
	c.cliMu.Lock()
	defer c.cliMu.Unlock()
	if c.checks[id] == nil {
		c.checks[id] = map[string]accountCheck{}
	}
	c.checks[id][account] = accountCheck{status: status, detail: detail, at: time.Now()}
}

func (c *Connections) forgetChecks(id, account string) {
	c.cliMu.Lock()
	defer c.cliMu.Unlock()
	if account == "" {
		delete(c.checks, id)
		return
	}
	delete(c.checks[id], account)
}

// SaveCLI stores a CLI; see Store.SaveCLI.
func (c *Connections) SaveCLI(cli CLI, previous string) (CLIView, error) {
	saved, err := c.store.SaveCLI(cli, previous)
	if err != nil {
		return CLIView{}, err
	}
	if previous != "" && previous != saved.ID {
		c.cliMu.Lock()
		c.checks[saved.ID] = c.checks[previous]
		delete(c.checks, previous)
		c.cliMu.Unlock()
	}
	return c.cliView(saved), nil
}

// RemoveCLI deletes a CLI.
func (c *Connections) RemoveCLI(id string) error {
	if err := c.store.RemoveCLI(id); err != nil {
		return err
	}
	c.forgetChecks(id, "")
	return nil
}

// AddAccount adds an account to a CLI.
func (c *Connections) AddAccount(id, account string) (CLIView, error) {
	cli, err := c.store.AddAccount(id, account)
	if err != nil {
		return CLIView{}, err
	}
	c.forgetChecks(id, account)
	return c.cliView(cli), nil
}

// RemoveAccount removes an account from a CLI.
func (c *Connections) RemoveAccount(id, account string) (CLIView, error) {
	cli, err := c.store.RemoveAccount(id, account)
	if err != nil {
		return CLIView{}, err
	}
	c.forgetChecks(id, account)
	return c.cliView(cli), nil
}

// CheckCLI runs the status check for every account of a CLI at once.
func (c *Connections) CheckCLI(ctx context.Context, id string) (CLIView, error) {
	cli, err := c.cli(id)
	if err != nil {
		return CLIView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, cliCheckWait)
	defer cancel()
	var wg sync.WaitGroup
	for _, a := range cli.Accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, detail := CheckAccount(ctx, cli, a)
			c.noteCheck(cli.ID, a.Name, status, detail)
		}()
	}
	wg.Wait()
	return c.cliView(cli), nil
}

// SignInAccount returns the flow that signs one account of a CLI in, for
// the auth flow engine to run.
func (c *Connections) SignInAccount(id, account string) (func(context.Context, adapter.AuthInteraction) error, error) {
	cli, err := c.cli(id)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(cli.Accounts, func(a Account) bool { return a.Name == account })
	if i < 0 {
		return nil, fmt.Errorf("%s has no account named %q", cli.Name, account)
	}
	acct := cli.Accounts[i]
	return func(ctx context.Context, ia adapter.AuthInteraction) error {
		err := SignInAccount(ctx, ia, cli, acct)
		if err == nil {
			c.noteCheck(cli.ID, acct.Name, AccountSignedIn, "")
		}
		return err
	}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
