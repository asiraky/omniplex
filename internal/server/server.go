package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/attachment"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/endpoints"
	"github.com/asiraky/omniplex/internal/overlay"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/store"
)

// Server exposes the sync protocol over WebSocket and a small HTTP API, and
// serves the web UI.
//
// The UI is a separate application, but it is always reached through this
// server: in a release build from the embedded bundle, and in development by
// proxying to the Vite dev server. One origin either way, which is what avoids
// mixed-content and CORS problems from another device — and what lets pairing
// behave the same in development as in production.
type Server struct {
	id         string
	mgr        *session.Manager
	store      *store.Store
	guard      *auth.Guard
	defaultCwd string
	webFS      fs.FS
	web        *webAssets
	devProxy   http.Handler
	allowAny   bool
	endpoints  *endpoints.Builder
	// commit is the git revision this binary was built from, empty when it
	// was not built from a checkout.
	commit string
	// attachments holds images a human added to a prompt. Nil in tests that
	// never upload one, in which case the endpoints report the feature off.
	attachments *attachment.Store

	// live tracks open WebSockets so revoking a device can close the ones it
	// already holds.
	liveMu sync.Mutex
	live   map[*conn]struct{}

	// termLive tracks open terminal sockets by device, for the same reason —
	// a revoked phone must not keep an interactive shell.
	termMu   sync.Mutex
	termLive map[*websocket.Conn]string
}

type Options struct {
	Manager    *session.Manager
	Store      *store.Store
	Guard      *auth.Guard
	Endpoints  *endpoints.Builder
	DefaultCwd string
	// WebFS serves the built UI when non-nil.
	WebFS fs.FS
	// DevViteURL turns on development mode: the UI is proxied to the Vite dev
	// server at this address instead of being served from WebFS, so an edit
	// reaches the browser without a build. Empty in a release.
	DevViteURL string
	// AllowAnyOrigin permits cross-origin WebSocket upgrades, which the Vite
	// dev server needs. Off for release builds serving their own bundle.
	AllowAnyOrigin bool
	// Attachments stores images attached to prompts. Nil turns the feature
	// off: uploads are refused and nothing else changes.
	Attachments *attachment.Store
	// Commit is the git revision this binary was built from. It is what makes
	// a deploy verifiable: without it "the server restarted" and "the server
	// restarted running the new binary" look identical from outside.
	Commit string
}

func New(o Options) *Server {
	s := &Server{
		live:        map[*conn]struct{}{},
		termLive:    map[*websocket.Conn]string{},
		id:          uuid.NewString(),
		mgr:         o.Manager,
		store:       o.Store,
		guard:       o.Guard,
		endpoints:   o.Endpoints,
		defaultCwd:  o.DefaultCwd,
		webFS:       o.WebFS,
		allowAny:    o.AllowAnyOrigin,
		attachments: o.Attachments,
		commit:      o.Commit,
	}
	if o.WebFS != nil {
		// Prepared once: hashing the bundle per request would put a read of
		// every file on the hot path for content that cannot change.
		if assets, err := newWebAssets(o.WebFS); err == nil {
			s.web = assets
		}
	}
	if o.DevViteURL != "" {
		if target, err := url.Parse(o.DevViteURL); err == nil {
			s.devProxy = devProxy(target)
		}
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated surface, deliberately tiny: the pairing page, the
	// endpoint that redeems a code, and a liveness probe that reveals nothing.
	mux.HandleFunc("GET /pair", s.handlePairPage)
	mux.HandleFunc("POST /api/pair", s.handlePair)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		_, paired := s.guard.Authorize(r)
		body := map[string]any{
			"ok":              true,
			"protocolVersion": ProtocolVersion,
			"paired":          paired,
			"build":           s.web.BuildID(),
		}
		// The commit names the exact build, which is the whole point: a deploy
		// that checks only for a live server cannot tell a successful restart
		// from a failed one that left the old binary running. It is withheld
		// from unauthenticated callers because this probe is deliberately mute.
		//
		// Gating on `paired` alone is deliberate. Authorize already grants a
		// loopback request, which is how the deploy reads it from the box
		// itself, and it withdraws that grant when the request carries proxy
		// headers. Testing auth.IsLocal here as well would hand the commit to
		// an unpaired stranger arriving through `tailscale serve`, undoing the
		// downgrade looksProxied exists to perform.
		if s.commit != "" && paired {
			body["commit"] = s.commit
		}
		writeJSON(w, body)
	})

	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		s.handleListDevices(w, r)
	})
	mux.HandleFunc("DELETE /api/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.handleRevokeDevice(w, r)
	})

	mux.HandleFunc("GET /api/harnesses", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.mgr.Harnesses(r.Context()))
	})

	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions, err := s.mgr.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, sessions)
	})

	mux.HandleFunc("GET /api/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		actor, err := s.mgr.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		state, err := actor.State(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, state)
	})

	// Directory browsing, so the UI can pick a working directory.
	mux.HandleFunc("GET /api/fs", func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("path")
		if dir == "" {
			dir = s.defaultCwd
		}
		if strings.HasPrefix(dir, "~") {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		root := r.URL.Query().Get("root")
		if root != "" {
			rootAbs, rootErr := filepath.Abs(root)
			rel, relErr := filepath.Rel(rootAbs, abs)
			if rootErr != nil || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				http.Error(w, "path is outside project", http.StatusBadRequest)
				return
			}
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dirs := []string{}
		files := []string{}
		for _, e := range entries {
			showHidden := r.URL.Query().Get("files") == "1" && e.Name() != ".git"
			if e.IsDir() && (!strings.HasPrefix(e.Name(), ".") || showHidden) {
				dirs = append(dirs, e.Name())
			}
			if !e.IsDir() && r.URL.Query().Get("files") == "1" {
				if info, infoErr := e.Info(); infoErr == nil && (info.Mode()&0o111 != 0 || isScriptExtension(e.Name())) {
					files = append(files, e.Name())
				}
			}
		}
		writeJSON(w, map[string]any{"path": abs, "parent": filepath.Dir(abs), "dirs": dirs, "files": files})
	})

	// Images a human attached to a prompt. Uploaded once, referred to by id
	// everywhere afterwards, and read back here by whatever device is looking
	// at the transcript — including one that was not in the room when the
	// picture was sent.
	mux.HandleFunc("POST /api/sessions/{id}/attachments", s.handleUploadAttachment)
	mux.HandleFunc("GET /api/sessions/{id}/attachments/{attachmentId}", s.handleGetAttachment)

	mux.HandleFunc("/ws", s.serveWS)

	// A pty per open terminal tab, scoped to the session's checkout. Behind
	// the gate like everything else: private by default.
	mux.HandleFunc("/api/term", s.serveTerm)

	// In development the bundle on disk is stale or absent, so Vite is the
	// source of truth for everything that is not the API.
	switch {
	case s.devProxy != nil:
		mux.Handle("/", s.devProxy)
	case s.web != nil:
		mux.Handle("/", s.web.handler())
	}

	return withCORS(s.gate(mux), s.allowAny)
}

func isScriptExtension(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ts", ".mts", ".js", ".mjs", ".cjs", ".sh":
		return true
	}
	return false
}

// publicPaths are reachable without a paired device. Everything else — the
// app, the API, the WebSocket — requires one.
func publicPaths(path string) bool {
	switch path {
	case "/pair", "/api/pair", "/api/health":
		return true
	}
	return false
}

// gate refuses anything from an unpaired device before it reaches a handler.
// Placing it here rather than per-route means a new route is private by
// default: forgetting to gate something is not possible.
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		device, ok := s.guard.Authorize(r)
		if !ok {
			// A browser asking for a page is sent somewhere useful; anything
			// else gets a status it can act on. The WebSocket lands here too,
			// so an unpaired upgrade is refused before it becomes a socket.
			if wantsHTML(r) {
				http.Redirect(w, r, "/pair", http.StatusFound)
				return
			}
			writeError(w, http.StatusUnauthorized, "pair this device first")
			return
		}
		next.ServeHTTP(w, auth.WithDevice(r, device))
	})
}

// wantsHTML distinguishes a browser navigating from a program calling.
func wantsHTML(r *http.Request) bool {
	if r.Header.Get("Sec-WebSocket-Key") != "" {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled}
	if s.allowAny {
		opts.InsecureSkipVerify = true
	}
	// A browser refuses the connection unless the server selects one of the
	// subprotocols it offered, so a client that authenticated this way needs
	// its protocol echoed back.
	if proto, ok := auth.TokenSubprotocol(r); ok {
		opts.Subprotocols = []string{proto}
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	// A turn can run for many minutes with no client traffic; the read loop
	// blocks on the socket rather than a deadline, and ping keeps NAT alive.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	device, _ := auth.DeviceFrom(r.Context())
	s.handleWS(ws, ctx, device.ID)
	_ = ws.Close(websocket.StatusNormalClosure, "")
}

func (s *Server) register(c *conn) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	s.live[c] = struct{}{}
}

func (s *Server) unregister(c *conn) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	delete(s.live, c)
}

// closeDevice drops every connection opened by a device. Called on revocation:
// a user who has just revoked a stolen phone expects it cut off now, not at
// its next reconnect, which it need never make.
func (s *Server) closeDevice(id string) {
	if id == "" {
		return
	}

	s.liveMu.Lock()
	var doomed []*conn
	for c := range s.live {
		if c.deviceID == id {
			doomed = append(doomed, c)
		}
	}
	s.liveMu.Unlock()

	for _, c := range doomed {
		_ = c.ws.Close(websocket.StatusPolicyViolation, "device revoked")
	}

	s.termMu.Lock()
	var doomedTerms []*websocket.Conn
	for ws, device := range s.termLive {
		if device == id {
			doomedTerms = append(doomedTerms, ws)
		}
	}
	s.termMu.Unlock()

	for _, ws := range doomedTerms {
		_ = ws.Close(websocket.StatusPolicyViolation, "device revoked")
	}
}

func withCORS(h http.Handler, allowAny bool) http.Handler {
	if !allowAny {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// access builds the endpoint list. It is nil when no builder was supplied,
// which keeps the server usable in tests that do not care about networking.
func (s *Server) access(ctx context.Context) *endpoints.Set {
	if s.endpoints == nil {
		return nil
	}
	set := s.endpoints.Build(ctx)
	return &set
}

// setHTTPS turns `tailscale serve` on or off.
//
// This mutates the user's machine — the mapping persists across reboots — so
// it is only ever reached by an explicit action in the UI, never as a side
// effect of starting up. What it buys is a secure context (home-screen
// install, notifications); the tailnet is already encrypted without it.
func (s *Server) setHTTPS(ctx context.Context, enable bool) (any, error) {
	if s.endpoints == nil {
		return nil, errors.New("endpoint discovery is not configured")
	}

	set := s.endpoints.Build(ctx)
	if !set.Overlay.Running {
		return nil, errors.New("Tailscale is not running on this machine")
	}

	cli := overlay.FindCLI()
	if cli == "" {
		return nil, errors.New("the Tailscale CLI could not be found, so serve cannot be configured from here")
	}

	var args []string
	if enable {
		args = overlay.ServeCommand(cli, s.endpoints.Port())
	} else {
		args = overlay.ServeOffCommand(cli)
	}

	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(runCtx, args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}

	// Tell the guard at once rather than waiting for the periodic check. While
	// serve is on, every tailnet request reaches us from 127.0.0.1, so trusting
	// loopback would hand the whole tailnet an unpaired way in.
	s.guard.SetProxied(enable)

	updated := s.endpoints.Build(ctx)
	return map[string]any{
		"access": updated,
		// Worth saying plainly: enabling this changes the rules for local
		// browsers too, and the user is about to notice.
		"note": httpsNote(enable),
	}, nil
}

func httpsNote(enabled bool) string {
	if enabled {
		return "HTTPS is on. Because Tailscale now proxies every request through this machine's loopback address, browsers on this machine must pair too — pair once from the banner code."
	}
	return "HTTPS is off. Browsers on this machine no longer need to pair."
}
