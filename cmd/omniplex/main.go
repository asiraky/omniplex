// Command omniplex runs the harness multiplexer: one server driving Claude Code,
// Codex and Pi behind a single canonical protocol, with any number of UIs attached.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/adapter/claudecode"
	"github.com/asiraky/omniplex/internal/adapter/codexapp"
	"github.com/asiraky/omniplex/internal/adapter/piapp"
	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/attachment"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/banner"
	"github.com/asiraky/omniplex/internal/bundled"
	"github.com/asiraky/omniplex/internal/datalock"
	"github.com/asiraky/omniplex/internal/endpoints"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/netinfo"
	"github.com/asiraky/omniplex/internal/overlay"
	"github.com/asiraky/omniplex/internal/procgroup"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/server"
	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// exitDataInUse is the status when another server holds the database. The
// desktop app (desktop/src/server.ts) knows it.
const exitDataInUse = 17

// web/dist is embedded when it has been built. The directory always contains a
// placeholder so the build works without a UI bundle present.
//
//go:embed all:webdist
var webdist embed.FS

// version is the release this binary is, stamped by release builds with
// -ldflags "-X main.version=1.2.3". Anything else is "dev".
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := runMCP(os.Stdin, os.Stdout); err != nil {
			log.Fatalf("mcp: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "relocate" {
		if err := runRelocateCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
			log.Fatalf("relocate project: %v", err)
		}
		return
	}
	var (
		addr       = flag.String("addr", "", "bind one specific address, e.g. 192.168.1.20:8787 (default: every private and overlay address)")
		port       = flag.Int("port", envInt("OMNIPLEX_PORT", 8787), "port to listen on")
		bindPublic = flag.Bool("bind-public", false, "also bind globally routable addresses, exposing omniplex to the internet")
		stdinLife  = flag.Bool("exit-on-stdin-close", false, "shut down when stdin closes; for a parent process (the desktop app) that must not leave this server behind if it dies")
		private    = flag.Bool("private", os.Getenv("OMNIPLEX_PRIVATE") == "1", "bind loopback and overlay (tailnet) addresses only, leaving out the local network")
		dbPath     = flag.String("db", envStr("OMNIPLEX_DB", defaultDB()), "path to the event log database")
		claudePath = flag.String("claude-path", "", "path to the Claude Code executable (default: discover it)")
		codexBin   = flag.String("codex", "codex", "path to the codex CLI")
		piBin      = flag.String("pi", "pi", "path to the pi CLI")
		dev        = flag.Bool("dev", false, "development mode: serve the UI from the Vite dev server instead of the embedded bundle")
		vitePort   = flag.Int("vite-port", envInt("OMNIPLEX_VITE_PORT", 5199), "port the Vite dev server listens on (with -dev)")
	)
	flag.Parse()

	bindOpts := netinfo.Options{
		Override:      *addr,
		Port:          *port,
		IncludePublic: *bindPublic,
		Private:       *private,
	}
	plan, err := netinfo.Plan(bindOpts)
	if err != nil {
		log.Fatalf("choose addresses: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	dbLock, err := datalock.Acquire(*dbPath)
	if errors.Is(err, datalock.ErrHeld) {
		// A distinct status so the desktop app can say what is wrong rather
		// than retrying a start that cannot succeed.
		fmt.Fprintf(os.Stderr, "%s is in use by another Omniplex server; stop it first\n", *dbPath)
		os.Exit(exitDataInUse)
	}
	if err != nil {
		log.Fatalf("lock data: %v", err)
	}
	defer dbLock.Release()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open log: %v", err)
	}
	defer st.Close()

	// The bind decision drives the auth policy: reachable from another
	// machine means every request from one needs a paired device. It is
	// re-derived below from what actually bound.
	// plan.Port, not the flag: a cookie is scoped by host and never by port, so
	// a worktree instance beside the primary one would otherwise share its
	// cookie and evict its device tokens on every pairing.
	guard := auth.New(st, plan.Reachable, plan.Port)

	logf := func(format string, args ...any) { log.Printf(format, args...) }

	// Trees a previous server left behind (a crash, a SIGKILL) are killed
	// before this one starts any of its own.
	procgroup.Sweep()

	claude := claudecode.New(*claudePath)
	mgr := thread.NewManager(st, logf,
		claude,
		codexapp.New(*codexBin),
		piapp.New(*piBin),
	)

	// Images attached to prompts live beside the event log, not inside it, and
	// go away with the thread that collected them.
	attachments := attachment.New(filepath.Join(filepath.Dir(*dbPath), "attachments"))
	mgr.SetAttachments(attachments)

	// What threads produce lives beside the attachments, outside every
	// worktree, so it outlives the checkout it was made in.
	artefacts := artefact.New(filepath.Join(filepath.Dir(*dbPath), "artefacts"))
	signer, err := artefact.LoadSigner(artefacts.Dir())
	if err != nil {
		log.Fatalf("artefact key: %v", err)
	}
	mgr.SetArtefacts(artefacts)
	thread.ToolServers = artefactTools(signer, plan.Port)
	// Values an agent passes to add_mcp_server stay out of the event log.
	thread.RedactToolInput = server.RedactAgentToolInput

	// omniplex's own skill ships in the binary. Each session loads it from
	// beside the data, for itself alone; it never goes into a skills library.
	if dir, err := bundled.Extract(filepath.Join(filepath.Dir(*dbPath), "plugin")); err != nil {
		logf("bundled skill off: %v", err)
	} else {
		thread.BundledPlugin = dir
	}
	defer mgr.Shutdown()

	// Provider instances: configured accounts layered over the default
	// (ambient-credential) instance each adapter already has. A broken or
	// unknowable config degrades to defaults; it never stops the server.
	configureProviders(mgr, logf)

	// The user's own MCP servers and sign-ins. Set before any thread resumes,
	// so every session starts with them; a store that will not open leaves
	// the feature off rather than the server down.
	conns := openConnections(mgr, st, plan.Port, logf)
	if conns != nil {
		thread.UserMCP = conns
	}

	// Work that was in flight when the last process stopped comes back now,
	// rather than when someone opens a browser. A restart should cost an agent
	// a turn boundary, not its task.
	mgr.ResumeInterrupted()
	mgr.StartScheduler()

	// The skills Claude Code ships with are asked of the CLI in the
	// background, once per version it updates itself to.
	claudeBundled := skills.NewBundledProbe(filepath.Join(filepath.Dir(*dbPath), "claude-bundled-skills.json"), claude.Executable)
	watchCtx, stopWatching := context.WithCancel(context.Background())
	defer stopWatching()
	go claudeBundled.Watch(watchCtx, time.Hour, logf)

	webFS, hasUI := embeddedUI()

	// In development the UI comes from Vite through this server rather than
	// from the embedded bundle, so a stale or absent build is irrelevant.
	devViteURL := ""
	if *dev {
		devViteURL = fmt.Sprintf("http://127.0.0.1:%d", *vitePort)
		hasUI = true
	}

	// Bind first, then advertise: an interface can disappear between being
	// enumerated and being bound, and a QR pointing at an address nothing is
	// listening on sends a phone nowhere. `bound` is what actually opened.
	listeners, bound, err := plan.Listen()
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	plan = bound

	access := endpoints.NewBuilder(plan, plan.Port)

	srv := server.New(server.Options{
		Manager:        mgr,
		Store:          st,
		Guard:          guard,
		Endpoints:      access,
		WebFS:          webFS,
		DevViteURL:     devViteURL,
		Attachments:    attachments,
		Artefacts:      artefacts,
		ArtefactSigner: signer,
		Commit:         buildCommit(),
		Version:        version,
		Connections:    conns,
		ClaudeBundled:  claudeBundled,
		Logf:           logf,
		// Nothing is cross-origin any more: the browser talks to this server
		// and this server talks to Vite, so the upgrade check can stay on.
		AllowAnyOrigin: false,
	})

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var harnesses []string
	for _, h := range mgr.Harnesses(context.Background()) {
		status := "ready"
		if !h.Availability.OK() {
			status = "unavailable — " + h.Availability.Reason
		}
		harnesses = append(harnesses, fmt.Sprintf("%-12s %s", h.Name, status))
	}

	// Built from the same source the client is told about, so the banner and
	// the app never disagree about how to reach this machine.
	set := access.Build(context.Background())

	var lines []banner.Line
	for _, e := range set.Endpoints {
		lines = append(lines, banner.Line{Label: e.Label, URL: e.URL, Insecure: !e.Encrypted})
	}

	opts := banner.Options{
		DBPath:    *dbPath,
		Projects:  projectsDir(),
		Harness:   harnesses,
		Addrs:     lines,
		HasUI:     hasUI,
		Reachable: plan.Reachable,
	}

	// A code is only worth minting when another device could use it.
	if plan.Reachable {
		code, err := guard.NewPairingCode(context.Background())
		if err != nil {
			log.Printf("could not mint a pairing code: %v", err)
		} else {
			// Prefer the overlay hostname: pairing binds a device token to
			// one origin, and that name is the only one that reaches this
			// machine both at home and away.
			base := set.BestPairingURL()
			if base == "" {
				base = plan.BestPairingURL()
			}
			opts.PairingCode = auth.FormatCode(code)
			opts.PairingRaw = code
			// The code rides in the fragment so it is never sent to the
			// server in a request line, and so cannot reach an access log.
			opts.PairingURL = base + "/pair#c=" + code
		}
	}

	// `tailscale serve` proxies the tailnet to our loopback address, which
	// would otherwise make every remote request look local and grant it access
	// with no pairing. The mapping can be turned on outside this process and
	// survives reboots, so it is watched rather than read once.
	watchProxy(guard, access, plan.Port)

	banner.Write(os.Stdout, opts)

	for _, ln := range listeners {
		go func(l net.Listener) {
			if err := httpSrv.Serve(l); err != nil && err != http.ErrServerClosed {
				log.Printf("serve on %s stopped: %v", l.Addr(), err)
			}
		}(ln)
	}

	// Chosen automatically, what to bind can grow after startup: at login the
	// tailnet (or the wifi) often comes up after this process does, and a
	// server that only ever bound loopback would never be reachable from the
	// phone. A named address is exactly that address, so it is left alone.
	if *addr == "" {
		go watchAddrs(bindOpts, plan, httpSrv, guard, access)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	// The desktop app holds our stdin open for as long as it lives. The pipe
	// closes when it quits and when it crashes, so this is both the graceful
	// stop on every platform (Windows has no SIGTERM to send) and what keeps an
	// orphaned server from holding the port the next launch wants.
	if *stdinLife {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			log.Println("stdin closed")
			sig <- os.Interrupt
		}()
	}
	<-sig

	log.Println("shutting down; disposing harnesses")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	mgr.Shutdown()
}

// configureProviders loads provider instances from the user config, sweeping
// any literal sensitive values into the secret store (and rewriting the config
// so no secret stays on disk in it), then installs them on the manager.
func configureProviders(mgr *thread.Manager, logf func(string, ...any)) {
	cfg, err := userconfig.Load()
	if err != nil {
		logf("load user config: %v (provider instances skipped)", err)
		return
	}
	secrets, err := provider.OpenSecretStore()
	if err != nil {
		logf("open secret store: %v (provider instances skipped)", err)
		return
	}
	instances, rewritten, changed, err := provider.LoadInstances(cfg.Providers, secrets, logf)
	if err != nil {
		logf("load provider instances: %v (provider instances skipped)", err)
		return
	}
	if changed {
		cfg.Providers = rewritten
		if _, err := userconfig.Save(cfg); err != nil {
			logf("rewrite user config after sweeping secrets: %v", err)
		}
	}
	// Clearing a variable's sensitive flag deletes its stored secret.
	if err := secrets.Sync(instances); err != nil {
		logf("sync secret store: %v", err)
	}
	mgr.ConfigureInstances(instances, secrets)
}

// openConnections opens connections.json and its secret store beside the
// user config, and hands the MCP layer the harnesses that take MCP servers
// and the folders of each project.
func openConnections(mgr *thread.Manager, st *store.Store, port int, logf func(string, ...any)) *mcp.Connections {
	dir, err := mcp.Dir()
	if err != nil {
		logf("mcp servers: %v (feature off)", err)
		return nil
	}
	mst, err := mcp.OpenStore(dir)
	if err != nil {
		logf("mcp servers: %v (feature off)", err)
		return nil
	}
	hosts := func() []mcp.Host {
		var out []mcp.Host
		for _, h := range mgr.MCPHosts() {
			out = append(out, mcp.Host{ID: h.ID, Name: h.Name, Host: h.Host, Envs: h.Envs})
		}
		return out
	}
	folders := func(ctx context.Context, projectID string) ([]string, error) {
		p, err := st.Project(ctx, projectID)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, f := range p.Folders {
			out = append(out, f.Path)
		}
		return out, nil
	}
	return mcp.NewConnections(mst, nil, port, hosts, folders, logf)
}

// watchProxy keeps the guard's view of any reverse proxy current.
//
// It checks immediately, so a mapping left over from a previous run is in
// force before the first request is served, and then on a ticker, because the
// mapping can be changed by another program at any time. The interval bounds
// how long a newly-added proxy could go unnoticed; requests relayed by
// Tailscale also carry headers the guard treats as proof of relaying, which
// covers that window.
func watchProxy(guard *auth.Guard, access *endpoints.Builder, port int) {
	check := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		ts := overlay.Detect(ctx)
		if !ts.Running || ts.DNSName == "" {
			guard.SetProxied(false)
			return
		}
		guard.SetProxied(overlay.CheckServe(ctx, ts.CLI, port, ts.DNSName).Enabled)
	}

	check()

	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for range t.C {
			check()
		}
	}()
}

// watchAddrs binds addresses that appear after startup. Addresses that go
// away keep their listener: it costs nothing idle, and it is back in service
// if the address returns.
func watchAddrs(opts netinfo.Options, bound netinfo.BindPlan, srv *http.Server, guard *auth.Guard, access *endpoints.Builder) {
	// An address that will not bind (a tentative IPv6 address, one taken by
	// another process) is retried slowly rather than logged every tick.
	failed := map[string]time.Time{}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for range t.C {
		want, err := netinfo.Plan(opts)
		if err != nil {
			continue
		}
		var missing []netinfo.Addr
		for _, a := range bound.Missing(want) {
			if time.Since(failed[a.IP.String()]) > 5*time.Minute {
				missing = append(missing, a)
			}
		}
		if len(missing) == 0 {
			continue
		}
		listeners, more, _ := netinfo.BindPlan{Addrs: missing, Port: bound.Port}.Listen()
		for _, a := range bound.With(more).Missing(netinfo.BindPlan{Addrs: missing}) {
			failed[a.IP.String()] = time.Now()
		}
		if len(listeners) == 0 {
			continue
		}
		// Reachable before serving: a remote request must never be judged
		// under the loopback policy.
		if more.Reachable {
			guard.SetReachable()
		}
		bound = bound.With(more)
		access.SetPlan(bound)
		for _, ln := range listeners {
			log.Printf("now also listening on %s", ln.Addr())
			go func(l net.Listener) {
				if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
					log.Printf("serve on %s stopped: %v", l.Addr(), err)
				}
			}(ln)
		}
	}
}

// embeddedUI returns the built bundle, or (nil, false) when only the
// placeholder is present.
func embeddedUI() (fs.FS, bool) {
	sub, err := fs.Sub(webdist, "webdist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}

// envInt reads an integer from the environment, falling back when it is unset
// or unparseable. The dev scripts set these so Go and Vite agree on ports.
func envInt(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// envStr reads a string from the environment, falling back when it is unset.
// Together with envInt this is what lets a provisioned worktree run its own
// server: it needs a port and a database of its own, and the dev script has
// nowhere to pass flags through to.
func envStr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// buildCommit reports the git revision this binary was built from.
//
// Go stamps this automatically when building inside a checkout, so nothing has
// to pass -ldflags and a local build is as identifiable as a released one. A
// binary built outside a checkout reports nothing rather than lying.
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" {
		return ""
	}
	// An uncommitted build is not the commit it claims, and a deploy that
	// cannot tell the difference is worse than one that reports nothing.
	if modified == "true" {
		return rev + "-dirty"
	}
	return rev
}

func defaultDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "omniplex.db"
	}
	return filepath.Join(home, ".omniplex", "omniplex.db")
}

func projectsDir() string {
	cfg, _ := userconfig.Load()
	dir, err := cfg.ProjectsDirOrDefault()
	if err != nil {
		return err.Error()
	}
	return dir
}

// artefactTools is the MCP server every harness gets: this binary, run as
// `omniplex mcp`, pointed back at this server over loopback with a token for
// its one thread. Every tool is pre-approved: the writes raise omniplex's own
// card, which is the approval, and they wait on it for up to mcpToolTimeout.
func artefactTools(signer *artefact.Signer, port int) func(threadID, home string) []adapter.MCPServer {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("artefact tools off: %v", err)
		return nil
	}
	return func(threadID, home string) []adapter.MCPServer {
		return []adapter.MCPServer{{
			Name:    "omniplex",
			Command: exe,
			Args:    []string{"mcp"},
			Env: map[string]string{
				"OMNIPLEX_URL":         fmt.Sprintf("http://127.0.0.1:%d", port),
				"OMNIPLEX_AGENT_TOKEN": signer.Mint(artefact.Claims{Kind: artefact.KindAgent, Thread: threadID}),
				"OMNIPLEX_HOME":        home,
			},
			Tools:       mcpToolNames(),
			ToolTimeout: mcpToolTimeout,
		}}
	}
}
