package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

// Omniplex's own view of a server, as opposed to what a session reports.
const (
	StatusConnected = "connected"
	StatusSignIn    = "sign_in"
	StatusFailed    = "failed"
	// StatusUnchecked is a server run by a command, which is never spawned
	// just to look at it, or one not checked since the server started.
	StatusUnchecked = "unchecked"
)

// probeTimeout bounds one check; tests shorten it.
var probeTimeout = 10 * time.Second

// Check is the outcome of one probe.
type Check struct {
	Status string
	Error  string
	At     time.Time
}

// Prober checks remote servers and remembers the answers in memory, by
// server key (see Server.Key).
type Prober struct {
	client  *http.Client
	mu      sync.Mutex
	results map[string]Check
}

// NewProber makes a prober; nil means http.DefaultClient.
func NewProber(client *http.Client) *Prober {
	return &Prober{client: staysHome(client), results: map[string]Check{}}
}

// staysHome copies client (nil is http.DefaultClient) so that it follows a
// redirect only within the origin it started at. Go drops Authorization on a
// cross-host hop but not a custom key header, and a 307 or 308 resends a token
// request's body with its code, verifier or refresh token. A redirect
// elsewhere comes back as the 3xx it is.
func staysHome(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		first := via[0].URL
		if len(via) >= 10 || req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &c
}

// Cached is the last answer for the server with this key, if there is one.
func (p *Prober) Cached(key string) (Check, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.results[key]
	return c, ok
}

// Forget drops the answer for the server with this key.
func (p *Prober) Forget(key string) {
	p.mu.Lock()
	delete(p.results, key)
	p.mu.Unlock()
}

// Probe checks the server with this key as a session would get it (def),
// headers included, and remembers the answer. A server run by a command is
// never checked.
func (p *Prober) Probe(ctx context.Context, key string, def adapter.MCPServer) Check {
	if def.URL == "" {
		p.Forget(key)
		return Check{Status: StatusUnchecked}
	}
	c := p.probe(ctx, def)
	c.At = time.Now()
	p.mu.Lock()
	p.results[key] = c
	p.mu.Unlock()
	return c
}

// initialize is the JSON-RPC request every MCP session opens with; a server
// that answers it is up and accepts these credentials.
var initialize = []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"omniplex","version":"1"}}}`)

func (p *Prober) probe(ctx context.Context, def adapter.MCPServer) Check {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, def.URL, bytes.NewReader(initialize))
	if err != nil {
		return Check{Status: StatusFailed, Error: shortError(err)}
	}
	for k, v := range def.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Check{Status: StatusFailed, Error: fmt.Sprintf("no answer in %s", probeTimeout)}
		}
		return Check{Status: StatusFailed, Error: shortError(err)}
	}
	// The answer itself does not matter, and an event stream may not end.
	res.Body.Close()
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		p.endSession(def, res.Header.Get("Mcp-Session-Id"))
		return Check{Status: StatusConnected}
	case (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) && bearerChallenge(res.Header):
		return Check{Status: StatusSignIn}
	default:
		return Check{Status: StatusFailed, Error: fmt.Sprintf("the server answered %s", res.Status)}
	}
}

// endSession closes the session the check opened, so a check does not leave
// one behind on the server. Best effort.
func (p *Prober) endSession(def adapter.MCPServer, id string) {
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, def.URL, nil)
	if err != nil {
		return
	}
	for k, v := range def.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Mcp-Session-Id", id)
	if res, err := p.client.Do(req); err == nil {
		res.Body.Close()
	}
}

func bearerChallenge(h http.Header) bool {
	for _, v := range h.Values("WWW-Authenticate") {
		if len(v) >= 6 && strings.EqualFold(v[:6], "bearer") {
			return true
		}
	}
	return false
}

// shortError is a transport error without the method and URL Go wraps
// around it, cut to a line.
func shortError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
