package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/asiraky/omniplex/internal/adapter"
)

// Sessions reach the user's remote MCP servers through this server rather
// than directly. A session keeps the definition it started with for as long
// as it runs, and an access token in that definition dies within the hour
// (Cloudflare's last an hour) while the session goes on. Here every request
// gets the token current at the time, and one the server turns away is
// refreshed and the request sent again, so no token ever reaches a harness
// and none goes stale inside one.

// ProxyPrefix is the path sessions reach a server at, followed by its key in
// secret-store form (see secretID), which holds no slash.
const ProxyPrefix = "/api/mcp-proxy/"

// proxyMaxBody caps a request body, which is held so the request can be sent
// again with a fresh token. MCP requests are JSON-RPC messages; a tool call
// carrying a whole file is the large case.
const proxyMaxBody = 32 << 20

type proxy struct {
	// key is the bearer a session presents. It only has to be unguessable
	// to other users of the machine: harnesses die with this server, so a
	// key per process is enough.
	key    string
	base   string
	client *http.Client
}

func newProxy(client *http.Client, port int) *proxy {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	// Redirects go back to the harness as they came: following one here
	// would send the token wherever the server pointed. No overall timeout,
	// since a GET stream stays open for as long as the session listens.
	c := *client
	c.Timeout = 0
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &proxy{key: hex.EncodeToString(b), base: "http://127.0.0.1:" + strconv.Itoa(port), client: &c}
}

// remote is the remote server whose key is id in secret-store form.
func (c *Connections) remote(id string) (Server, bool) {
	f, err := c.store.Read()
	if err != nil {
		return Server{}, false
	}
	i := slices.IndexFunc(f.Servers, func(s Server) bool { return s.URL != "" && secretID(s.Key()) == id })
	if i < 0 {
		return Server{}, false
	}
	return f.Servers[i], true
}

func (p *proxy) url(key string) string { return p.base + ProxyPrefix + url.PathEscape(secretID(key)) }

// hopHeaders are the connection's own and never forwarded, nor are the
// session's credentials for the proxy itself.
var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer",
	"Transfer-Encoding", "Upgrade", "Host", "Content-Length", "Authorization", "Cookie",
	// Go's transport asks for and undoes gzip itself, which keeps a stream
	// readable as it arrives.
	"Accept-Encoding",
}

func copyHeaders(dst, src http.Header) {
	drop := map[string]bool{}
	for _, h := range hopHeaders {
		drop[h] = true
	}
	for _, v := range src.Values("Connection") {
		for _, f := range strings.Split(v, ",") {
			drop[http.CanonicalHeaderKey(strings.TrimSpace(f))] = true
		}
	}
	for k, vs := range src {
		if drop[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// ServeProxy forwards one request from a session to the remote server whose
// key is id in secret-store form, with the server's header values and
// current token.
func (c *Connections) ServeProxy(w http.ResponseWriter, r *http.Request, id string) {
	want := "Bearer " + c.proxy.key
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(want)) != 1 {
		http.Error(w, "bad proxy key", http.StatusUnauthorized)
		return
	}
	s, ok := c.remote(id)
	if !ok {
		http.Error(w, "no remote MCP server "+strconv.Quote(id), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, proxyMaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "reading the request: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	def, token := c.upstream(ctx, s, oauthRequestTimeout)
	resp, err := c.proxy.send(ctx, r, def, body)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && token != "" {
		// Turned away before its own expiry: refresh it and try once more.
		tctx, cancel := context.WithTimeout(ctx, oauthRequestTimeout)
		fresh, rerr := c.oauth.Refresh(tctx, s, token)
		cancel()
		if rerr == nil && fresh != "" && fresh != token {
			resp.Body.Close()
			def.Headers["Authorization"] = "Bearer " + fresh
			resp, err = c.proxy.send(ctx, r, def, body)
		} else if rerr != nil && !errors.Is(rerr, ErrSignInNeeded) {
			c.logf("mcp server %s: refresh: %v", s.Key(), rerr)
		}
	}
	if err != nil {
		if ctx.Err() == nil {
			c.logf("mcp server %s: %v", s.Key(), err)
		}
		http.Error(w, "could not reach "+s.Name, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	if resp.StatusCode == http.StatusUnauthorized {
		// The sign-in is Omniplex's to do. A challenge would send the
		// harness off to sign in itself, against an address that is not
		// the server's.
		w.Header().Del("WWW-Authenticate")
	}
	w.WriteHeader(resp.StatusCode)
	stream(w, resp.Body)
}

// send makes one attempt at the request.
func (p *proxy) send(ctx context.Context, r *http.Request, def adapter.MCPServer, body []byte) (*http.Response, error) {
	target := def.URL
	var rd io.Reader = http.NoBody
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, rd)
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, r.Header)
	for k, v := range def.Headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, netErr(err)
	}
	return resp, nil
}

// stream copies a response as it arrives, so a server's event stream reaches
// the session event by event rather than when it ends.
func stream(w http.ResponseWriter, body io.Reader) {
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}
