package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
)

func TestProbeOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		status  string
		errHas  string
	}{
		{
			name:    "answers initialize",
			handler: func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)) },
			status:  StatusConnected,
		},
		{
			name: "asks for a bearer token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://x/.well-known/oauth-protected-resource"`)
				w.WriteHeader(http.StatusUnauthorized)
			},
			status: StatusSignIn,
		},
		{
			name:    "401 without a bearer challenge",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
			status:  StatusFailed,
			errHas:  "401",
		},
		{
			name:    "server error",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
			status:  StatusFailed,
			errHas:  "502",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			p := NewProber(srv.Client())
			c := p.Probe(context.Background(), adapter.MCPServer{Name: "s", URL: srv.URL})
			if c.Status != tc.status || !strings.Contains(c.Error, tc.errHas) {
				t.Fatalf("got %+v", c)
			}
			if c.At.IsZero() {
				t.Error("no time on the check")
			}
			if cached, ok := p.Cached("s"); !ok || cached != c {
				t.Errorf("cached %+v, %v", cached, ok)
			}
		})
	}
}

func TestProbeSendsHeadersAndEndsItsSession(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.Header.Get("X-Key")+" "+r.Header.Get("Mcp-Session-Id"))
		mu.Unlock()
		if r.Method == http.MethodPost {
			if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			w.Header().Set("Mcp-Session-Id", "sess-1")
		}
	}))
	defer srv.Close()
	c := NewProber(srv.Client()).Probe(context.Background(), adapter.MCPServer{Name: "s", URL: srv.URL, Headers: map[string]string{"X-Key": "k"}})
	if c.Status != StatusConnected {
		t.Fatalf("%+v", c)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST k ", "DELETE k sess-1"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("requests %q, want %q", seen, want)
	}
}

func TestProbeTimesOut(t *testing.T) {
	old := probeTimeout
	probeTimeout = 50 * time.Millisecond
	defer func() { probeTimeout = old }()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	start := time.Now()
	c := NewProber(srv.Client()).Probe(context.Background(), adapter.MCPServer{Name: "s", URL: srv.URL})
	if c.Status != StatusFailed || c.Error == "" {
		t.Fatalf("%+v", c)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestProbeUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := NewProber(nil).Probe(context.Background(), adapter.MCPServer{Name: "s", URL: url})
	if c.Status != StatusFailed || c.Error == "" || strings.Contains(c.Error, url) {
		t.Fatalf("%+v", c)
	}
}

func TestProbeLeavesCommandsAlone(t *testing.T) {
	p := NewProber(nil)
	p.results["s"] = Check{Status: StatusConnected}
	c := p.Probe(context.Background(), adapter.MCPServer{Name: "s", Command: "never-run"})
	if c.Status != StatusUnchecked {
		t.Fatalf("%+v", c)
	}
	if _, ok := p.Cached("s"); ok {
		t.Error("kept an answer from when it was remote")
	}
}

// A redirect may move within the server, never carry a key header or a token
// request's body to another origin.
func TestRedirectsStayWithinTheOrigin(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/moved":
			http.Redirect(w, r, "/here", http.StatusTemporaryRedirect)
		case "/away":
			http.Redirect(w, r, other.URL+"/token", http.StatusTemporaryRedirect)
		case "/here":
			w.Write([]byte(r.Header.Get("X-Key")))
		}
	}))
	defer srv.Close()
	c := staysHome(srv.Client())

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/moved", nil)
	req.Header.Set("X-Key", "k")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "k" {
		t.Fatalf("same-origin hop: %d %q", res.StatusCode, body)
	}

	res, err = c.Post(srv.URL+"/away", "application/x-www-form-urlencoded", strings.NewReader("refresh_token=secret"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTemporaryRedirect || elsewhere.Load() != 0 {
		t.Fatalf("cross-origin hop followed: %d, %d requests elsewhere", res.StatusCode, elsewhere.Load())
	}
}
