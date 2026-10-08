package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// proxyFor puts c's proxy handler on a test server and returns a request
// builder that presents the key a session would.
func proxyFor(t *testing.T, c *Connections) func(method, name, body string) *http.Request {
	t.Helper()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.ServeProxy(w, r, strings.TrimPrefix(r.URL.Path, ProxyPrefix))
	}))
	t.Cleanup(front.Close)
	return func(method, name, body string) *http.Request {
		req, err := http.NewRequest(method, front.URL+ProxyPrefix+name, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+c.proxy.key)
		return req
	}
}

func do(t *testing.T, req *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestProxyForwardsWithTheServersCredentials(t *testing.T) {
	type seen struct{ auth, key, session, body, method string }
	got := make(chan seen, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.Header.Get("Authorization"), r.Header.Get("X-Key"), r.Header.Get("Mcp-Session-Id"), string(b), r.Method}
		w.Header().Set("Mcp-Session-Id", "sess-2")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer upstream.Close()

	c := newConns(t, upstream.Client())
	save(t, c, Draft{Name: "s", URL: upstream.URL + "/mcp", Headers: map[string]string{"X-Key": "k"}})
	signIn(t, c, "s", upstream.URL+"/mcp", map[string]any{"accessToken": "oauth-tok"})
	req := proxyFor(t, c)("POST", "s", `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req.Header.Set("Mcp-Session-Id", "sess-1")

	resp, body := do(t, req)
	if resp.StatusCode != http.StatusOK || body != `{"jsonrpc":"2.0","id":1,"result":{}}` || resp.Header.Get("Mcp-Session-Id") != "sess-2" {
		t.Fatalf("response %d %q %v", resp.StatusCode, body, resp.Header)
	}
	s := <-got
	if s.auth != "Bearer oauth-tok" || s.key != "k" || s.session != "sess-1" || s.method != "POST" || s.body != `{"jsonrpc":"2.0","id":1,"method":"ping"}` {
		t.Errorf("upstream saw %+v", s)
	}
}

// The case the proxy exists for: a session that started an hour ago, whose
// token has since expired, still gets through.
func TestProxyRefreshesAnExpiredToken(t *testing.T) {
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refreshes.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"new","token_type":"Bearer","refresh_token":"r2","expires_in":3600}`)
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer new" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, "ok")
		}
	}))
	defer srv.Close()
	c := newConns(t, srv.Client())
	save(t, c, Draft{Name: "s", URL: srv.URL + "/mcp"})
	signIn(t, c, "s", srv.URL+"/mcp", map[string]any{
		"accessToken": "old", "refreshToken": "r1", "expiry": time.Now().Add(-time.Minute),
		"tokenEndpoint": srv.URL + "/token", "clientId": "cid",
	})

	if resp, body := do(t, proxyFor(t, c)("POST", "s", "{}")); resp.StatusCode != http.StatusOK || body != "ok" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if n := refreshes.Load(); n != 1 {
		t.Errorf("refreshes = %d", n)
	}
}

// A token the server turns away before its own expiry is refreshed and the
// request, body and all, sent once more.
func TestProxyRetriesARejectedTokenOnce(t *testing.T) {
	var refreshes atomic.Int32
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refreshes.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"good","token_type":"Bearer","expires_in":3600}`)
		case "/mcp":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(b))
			mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer good" {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+"https://elsewhere.example/prm"+`"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, "ok")
		}
	}))
	defer srv.Close()
	c := newConns(t, srv.Client())
	save(t, c, Draft{Name: "s", URL: srv.URL + "/mcp"})
	signIn(t, c, "s", srv.URL+"/mcp", map[string]any{
		"accessToken": "revoked", "refreshToken": "r1", "expiry": time.Now().Add(time.Hour),
		"tokenEndpoint": srv.URL + "/token", "clientId": "cid",
	})

	resp, body := do(t, proxyFor(t, c)("POST", "s", `{"id":7}`))
	if resp.StatusCode != http.StatusOK || body != "ok" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if refreshes.Load() != 1 || len(bodies) != 2 || bodies[0] != `{"id":7}` || bodies[1] != `{"id":7}` {
		t.Errorf("refreshes %d, bodies %q", refreshes.Load(), bodies)
	}
}

// With no sign-in to fall back on, the 401 goes back to the session without
// the server's challenge, which would send the harness off to sign in itself.
func TestProxyPassesAFinal401WithoutTheChallenge(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()
	c := newConns(t, upstream.Client())
	save(t, c, Draft{Name: "s", URL: upstream.URL + "/mcp"})

	resp, _ := do(t, proxyFor(t, c)("POST", "s", "{}"))
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("%d %v", resp.StatusCode, resp.Header)
	}
	if hits.Load() != 1 {
		t.Errorf("sent %d times with nothing to refresh", hits.Load())
	}
}

func TestProxyRefusesWithoutItsKey(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer upstream.Close()
	c := newConns(t, upstream.Client())
	save(t, c, Draft{Name: "s", URL: upstream.URL + "/mcp"})
	save(t, c, Draft{Name: "local", Command: "run"})
	req := proxyFor(t, c)

	wrong := req("POST", "s", "{}")
	wrong.Header.Set("Authorization", "Bearer guess")
	if resp, _ := do(t, wrong); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong key: %d", resp.StatusCode)
	}
	none := req("POST", "s", "{}")
	none.Header.Del("Authorization")
	if resp, _ := do(t, none); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no key: %d", resp.StatusCode)
	}
	for _, name := range []string{"missing", "local"} {
		if resp, _ := do(t, req("POST", name, "{}")); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("upstream reached %d times", hits.Load())
	}
}

// An event stream reaches the session event by event, not when it ends.
func TestProxyStreamsEvents(t *testing.T) {
	done := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(done)
	c := newConns(t, upstream.Client())
	save(t, c, Draft{Name: "s", URL: upstream.URL + "/mcp"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := proxyFor(t, c)("GET", "s", "").WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: one\n" {
		t.Fatalf("first event %q, %v", line, err)
	}
}

// A server that moves its endpoint on its own origin is followed there, with
// the request's body and credentials, rather than the harness being sent to
// a path on this server.
func TestProxyFollowsARedirectOnTheServersOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			http.Redirect(w, r, "/mcp/", http.StatusTemporaryRedirect)
			return
		}
		b, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.Header.Get("Authorization"), b)
	}))
	defer upstream.Close()
	c := newConns(t, upstream.Client())
	save(t, c, Draft{Name: "s", URL: upstream.URL + "/mcp"})
	signIn(t, c, "s", upstream.URL+"/mcp", map[string]any{"accessToken": "tok"})

	resp, body := do(t, proxyFor(t, c)("POST", "s", `{"id":1}`))
	if resp.StatusCode != http.StatusOK || body != `POST Bearer tok {"id":1}` {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
}
