package preview

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Real lsof -F pcn output, trimmed from this machine.
const lsofListeners = `p1049
crapportd
f14
n*:57070
f15
n*:57070
p3245
cmongod
f9
n127.0.0.1:27017
f10
n[::1]:27017
p3255
cPython
f6
n127.0.0.1:8787
p3281
cOneDrive Sync Service
f39
n[::1]:42050
`

func TestParseListeners(t *testing.T) {
	got := parseListeners(lsofListeners)

	// Two file descriptors on one port, and an IPv4/IPv6 pair, are each one
	// service rather than two.
	if n := len(got["1049"]); n != 1 {
		t.Errorf("rapportd: got %d listeners, want 1 (duplicate fds collapsed)", n)
	}
	if n := len(got["3245"]); n != 1 {
		t.Errorf("mongod: got %d listeners, want 1 (v4/v6 pair collapsed)", n)
	}
	if got["3255"][0].port != 8787 || got["3255"][0].command != "Python" {
		t.Errorf("python: got %+v, want port 8787 command Python", got["3255"][0])
	}
	// The command name must not leak from the previous process block.
	if got["3281"][0].command != "OneDrive Sync Service" {
		t.Errorf("onedrive: got command %q", got["3281"][0].command)
	}
}

func TestParseCwds(t *testing.T) {
	got := parseCwds("p1163\nfcwd\nn/\np3255\nfcwd\nn/Users/dev\n")
	if got["3255"] != "/Users/dev" {
		t.Errorf("got %q, want /Users/dev", got["3255"])
	}
	if got["1163"] != "/" {
		t.Errorf("got %q, want /", got["1163"])
	}
}

func TestPortOf(t *testing.T) {
	cases := map[string]int{
		"*:57070":          57070,
		"127.0.0.1:27017":  27017,
		"[::1]:42050":      42050,
		"0.0.0.0:3000":     3000,
		"[::]:8080":        8080,
		"192.168.1.20:443": 0, // bound to one non-loopback address: not reachable at 127.0.0.1
		"127.0.0.1:0":      0,
		"garbage":          0,
	}
	for addr, want := range cases {
		port, ok := portOf(addr)
		if want == 0 && ok {
			t.Errorf("portOf(%q) = %d, want rejected", addr, port)
		}
		if want != 0 && port != want {
			t.Errorf("portOf(%q) = %d, want %d", addr, port, want)
		}
	}
}

// Real `docker ps` port summaries from this machine's Compose projects.
func TestParsePublishedPorts(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    []int
	}{{
		name:    "v4 and v6 publish of one port is one port",
		summary: "0.0.0.0:10026->1025/tcp, [::]:10026->1025/tcp, 0.0.0.0:22026->8025/tcp, [::]:22026->8025/tcp",
		want:    []int{10026, 22026},
	}, {
		name:    "udp is dropped",
		summary: "0.0.0.0:3478->3478/udp, [::]:3478->3478/udp, 0.0.0.0:3478->3478/tcp",
		want:    []int{3478},
	}, {
		name:    "an unpublished port is not reachable from the host",
		summary: "3000/tcp",
		want:    nil,
	}, {
		// Forty RTP ports are never a web UI, and listing them would bury
		// the real link.
		name:    "a published range is dropped whole",
		summary: "0.0.0.0:20000-20039->20000-20039/udp, [::]:20000-20039->20000-20039/udp",
		want:    nil,
	}, {
		name:    "a tcp range is dropped too",
		summary: "0.0.0.0:5060-5061->5060-5061/tcp",
		want:    nil,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parsePublishedPorts(c.summary)
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestWithin(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "packages", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if !within(root, sub) {
		t.Error("a subdirectory should be inside the root")
	}
	if !within(root, root) {
		t.Error("the root should be inside itself")
	}
	if within(root, filepath.Dir(root)) {
		t.Error("the parent should not be inside the root")
	}
	// The classic prefix bug: /tmp/rootother is not inside /tmp/root.
	if within(root, root+"other") {
		t.Error("a sibling sharing a name prefix should not be inside the root")
	}
	if within("", sub) || within(root, "") {
		t.Error("an empty path should never match")
	}
}

func TestFromResources(t *testing.T) {
	// The shape the workspace lifecycle spec documents.
	got := FromResources(map[string]any{
		"appUrl":   "http://app--session-inbox.zero8.test:8204",
		"apiUrl":   "http://api--session-inbox.zero8.test:8104",
		"database": "zero8_feature_session_inbox",
		"redisDb":  4,
	})
	if len(got) != 2 {
		t.Fatalf("got %d services, want 2 (non-URL resources ignored): %+v", len(got), got)
	}
	// Sorted by key: apiUrl before appUrl.
	if got[0].Label != "api" || got[0].Port != 8104 {
		t.Errorf("got %+v, want label api port 8104", got[0])
	}
	if got[1].Label != "app" || got[1].Port != 8204 {
		t.Errorf("got %+v, want label app port 8204", got[1])
	}
	for _, f := range got {
		if f.Source != SourceDeclared {
			t.Errorf("%s: got source %q, want declared", f.Label, f.Source)
		}
	}
}

func TestHostLabel(t *testing.T) {
	cases := []struct{ service, branch, want string }{
		{"web", "add-previews", "web-add-previews"},
		{"web", "feature/session-inbox", "web-feature-session-inbox"},
		{"node", "", "node"},
		{"", "main", "main"},
		{"", "", "preview"},
		{"OneDrive Sync Service", "", "onedrive-sync-service"},
		// A dot would create a second label, which no wildcard certificate
		// can match.
		{"app.web", "v1.2", "app-web-v1-2"},
	}
	for _, c := range cases {
		if got := hostLabel(c.service, c.branch); got != c.want {
			t.Errorf("hostLabel(%q, %q) = %q, want %q", c.service, c.branch, got, c.want)
		}
	}

	long := hostLabel("service", string(make([]byte, 0, 100))+"a-very-long-branch-name-that-just-keeps-going-and-going-and-going-past-the-limit")
	if len(long) > maxLabel {
		t.Errorf("label is %d chars, want <= %d", len(long), maxLabel)
	}
}

// An id must survive the service it names being restarted, or every bookmark
// and every open WebSocket breaks whenever a dev server reloads.
func TestIdentityIsStableAcrossRestarts(t *testing.T) {
	r := NewRegistry()
	found := []Found{{Port: 5050, Label: "web", Source: SourceDocker}}

	first := r.identify("session-1", "add-previews", found)
	if first[0].ID != "web-add-previews" {
		t.Fatalf("got id %q, want web-add-previews", first[0].ID)
	}

	// The label changes (the process reports itself differently); the port
	// does not. The identity must not move.
	again := r.identify("session-1", "add-previews", []Found{{Port: 5050, Label: "node", Source: SourceProcess}})
	if again[0].ID != first[0].ID {
		t.Errorf("id moved from %q to %q across a restart", first[0].ID, again[0].ID)
	}
}

// Two worktrees of one project run the same service on the same-looking name.
func TestIdentityCollisionsAreSeparated(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()

	a := r.identify("session-1", "same", []Found{{Port: 5050, Label: "web"}})
	r.Refresh(ctx, "session-1", t.TempDir(), "same", nil) // no-op detection
	r.mu.Lock()
	r.bySession["session-1"] = a
	r.owner[a[0].ID] = a[0]
	r.mu.Unlock()

	b := r.identify("session-2", "same", []Found{{Port: 5051, Label: "web"}})
	if b[0].ID == a[0].ID {
		t.Fatalf("two sessions were given the same id %q", a[0].ID)
	}
	if b[0].ID != "web-same-2" {
		t.Errorf("got %q, want web-same-2", b[0].ID)
	}
}

// The proxy's safety property: only a registered id resolves, so a retired
// preview cannot be used to reach whatever later takes its port.
func TestLookupRetiresWithTheSession(t *testing.T) {
	r := NewRegistry()
	srv := httpServerOnLoopback(t)

	if !r.Refresh(context.Background(), "s1", t.TempDir(), "br", []Found{{Port: srv, Label: "web", Scheme: "http", Source: SourceDeclared}}) {
		t.Fatal("first refresh should report a change")
	}
	ps := r.ForSession("s1")
	if len(ps) != 1 {
		t.Fatalf("got %d previews, want 1", len(ps))
	}
	if _, ok := r.Lookup(ps[0].ID); !ok {
		t.Fatal("a live preview should resolve")
	}

	r.Forget("s1")
	if _, ok := r.Lookup(ps[0].ID); ok {
		t.Error("a forgotten preview must not resolve")
	}
}

// Refresh must stay quiet when nothing moved: it runs on a timer, and a
// notification wakes every attached phone.
func TestRefreshOnlyReportsRealChanges(t *testing.T) {
	r := NewRegistry()
	port := httpServerOnLoopback(t)
	declared := []Found{{Port: port, Label: "web", Scheme: "http", Source: SourceDeclared}}
	root := t.TempDir()

	if !r.Refresh(context.Background(), "s1", root, "br", declared) {
		t.Fatal("the first refresh should report a change")
	}
	if r.Refresh(context.Background(), "s1", root, "br", declared) {
		t.Error("an unchanged refresh must not report a change")
	}
}

func TestProbe(t *testing.T) {
	ctx := context.Background()

	if scheme, ok := probe(ctx, httpServerOnLoopback(t)); !ok || scheme != "http" {
		t.Errorf("got (%q, %v), want (http, true)", scheme, ok)
	}

	// A listener that never answers is not a web UI, and must not hang the
	// detector past the probe timeout.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if _, ok := probe(probeCtx, ln.Addr().(*net.TCPAddr).Port); ok {
		t.Error("a silent listener should not be recognised as a web service")
	}

	// Nothing listening at all.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	if _, ok := probe(ctx, port); ok {
		t.Error("a closed port should not be recognised as a web service")
	}
}

// A 404 still proves there is a web server there; whether the app has a route
// at / is not the detector's business.
func TestProbeAcceptsErrorStatuses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	if _, ok := probe(context.Background(), ln.Addr().(*net.TCPAddr).Port); !ok {
		t.Error("a 404 should still count as a web service")
	}
}

// A TLS server must be reported as https. Found on this machine: a Caddy
// container on 443 answers a plaintext GET with "HTTP/1.1 400 Bad Request",
// which passes a naive HTTP check and then fails for real when proxied as
// http://.
func TestProbeDetectsTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	addr := srv.Listener.Addr().(*net.TCPAddr)
	scheme, ok := probe(context.Background(), addr.Port)
	if !ok || scheme != "https" {
		t.Errorf("got (%q, %v), want (https, true)", scheme, ok)
	}
}

// A declared service keeps the scheme its URL carried and is never probed, so
// a dev server that has not finished booting still appears.
func TestKeepServedTrustsDeclared(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	got := keepServed(context.Background(), []Found{
		{Port: port, Label: "app", Scheme: "https", Source: SourceDeclared},
		{Port: port + 1, Label: "noise", Source: SourceProcess},
	})
	if len(got) != 1 {
		t.Fatalf("got %d kept, want 1: %+v", len(got), got)
	}
	if got[0].Label != "app" || got[0].Scheme != "https" {
		t.Errorf("got %+v, want the declared app on https", got[0])
	}
}

func httpServerOnLoopback(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}
