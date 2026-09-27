package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/store"
)

type artefactRig struct {
	srv     *Server
	local   *httptest.Server // trusted: the device gate lets it through
	remote  *httptest.Server // an unpaired device
	session string
	dir     string // the session's folder, which is also its home
	agent   string // agent token for the session
}

func newArtefactRig(t *testing.T) *artefactRig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &scheduleBrowserAdapter{}
	mgr := session.NewManager(st, t.Logf, fa)
	t.Cleanup(mgr.Shutdown)
	arts := artefact.New(filepath.Join(dir, "artefacts"))
	mgr.SetArtefacts(arts)
	signer := artefact.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), DefaultCwd: dir, Artefacts: arts, ArtefactSigner: signer})
	a, err := mgr.Create(context.Background(), fa.ID(), "", dir, "test-model", "default")
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	local := httptest.NewServer(h)
	remote := httptest.NewServer(asRemote(h))
	t.Cleanup(local.Close)
	t.Cleanup(remote.Close)
	return &artefactRig{srv: srv, local: local, remote: remote, session: a.ID, dir: dir,
		agent: signer.Mint(artefact.Claims{Kind: artefact.KindAgent, Session: a.ID})}
}

// write puts files under the session's folder and returns the path of name.
func (r *artefactRig) write(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(r.dir, name, filepath.FromSlash(rel))
		if len(files) == 1 && rel == "" {
			p = filepath.Join(r.dir, name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(r.dir, name)
}

func (r *artefactRig) show(t *testing.T, token, path string) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": path})
	req, _ := http.NewRequest("POST", r.remote.URL+"/api/agent/artefacts", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func (r *artefactRig) api(id string) string {
	return r.local.URL + "/api/sessions/" + r.session + "/artefacts/" + id
}

func send(t *testing.T, method, url string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%s %s: %d %s", method, url, res.StatusCode, b)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return out
}

func post(t *testing.T, url, body string) map[string]any {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("POST %s: %d %s", url, res.StatusCode, b)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return out
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestAgentShowNeedsItsTokenAndAPathInTheSession(t *testing.T) {
	r := newArtefactRig(t)
	proto := r.write(t, "proto", map[string]string{"index.html": "<h1>", "app.js": "1"})

	if res, _ := r.show(t, "forged", proto); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged token: %d", res.StatusCode)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	os.WriteFile(outside, []byte("x"), 0o644)
	res, out := r.show(t, r.agent, outside)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(out["error"].(string), r.dir) {
		t.Fatalf("outside the session: %d %v", res.StatusCode, out)
	}
	if res, _ := r.show(t, r.agent, "proto"); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("relative path: %d", res.StatusCode)
	}

	res, first := r.show(t, r.agent, proto)
	if res.StatusCode != 200 || first["entry"] != "index.html" || first["files"] != float64(2) || first["dir"] != true {
		t.Fatalf("first show: %d %v", res.StatusCode, first)
	}
	// Showing the same folder again, after a revision, is the same artefact.
	r.write(t, "proto", map[string]string{"extra.css": "b{}"})
	_, second := r.show(t, r.agent, proto)
	if second["artefactId"] != first["artefactId"] || second["files"] != float64(3) {
		t.Fatalf("second show = %v, want a revision of %v", second, first["artefactId"])
	}
	_, other := r.show(t, r.agent, r.write(t, "notes.md", map[string]string{"": "# n"}))
	if other["artefactId"] == first["artefactId"] {
		t.Fatalf("another path must be another artefact: %v", other)
	}
}

func TestPreviewServesLiveFilesSandboxedWithBridge(t *testing.T) {
	r := newArtefactRig(t)
	proto := r.write(t, "proto", map[string]string{
		"index.html": "<html><head><title>x</title></head><body>hi</body></html>",
		"app.js":     "1",
		".env":       "SECRET=1",
	})
	_, shown := r.show(t, r.agent, proto)
	id := shown["artefactId"].(string)

	// The raw route is the app's own origin: behind the gate, and a document
	// there never gets to run script.
	raw := "/api/sessions/" + r.session + "/artefacts/" + id + "/f/index.html"
	if res, _ := get(t, r.remote.URL+raw); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("raw route from an unpaired device: %d", res.StatusCode)
	}
	res, _ := get(t, r.local.URL+raw)
	if res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("raw html: %d csp=%q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}

	url := post(t, r.api(id)+"/preview", "")["url"].(string)
	if !strings.HasSuffix(url, "/index.html") {
		t.Fatalf("preview url %q", url)
	}
	// The sandboxed iframe sends no cookie: the token alone must open it, and
	// the page's relative assets.
	res, body := get(t, r.remote.URL+url)
	csp := res.Header.Get("Content-Security-Policy")
	if res.StatusCode != 200 || !strings.Contains(csp, "allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		t.Fatalf("preview: %d csp=%q", res.StatusCode, csp)
	}
	if res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("a preview page can leak its token through the Referer")
	}
	head := strings.Index(body, "<head>")
	if head < 0 || !strings.HasPrefix(body[head+len("<head>"):], "<script>") || !strings.Contains(body, "<body>hi</body>") {
		t.Fatalf("bridge not first in head: %q", body)
	}
	base := r.remote.URL + strings.TrimSuffix(url, "index.html")
	// Live: the agent's edit shows without showing the file again.
	r.write(t, "proto", map[string]string{"app.js": "2"})
	if res, body := get(t, base+"app.js"); res.StatusCode != 200 || body != "2" {
		t.Fatalf("relative asset: %d %q", res.StatusCode, body)
	}
	for _, rel := range []string{"..%2f..%2fa.db", ".env"} {
		if res, _ := get(t, base+rel); res.StatusCode != 404 {
			t.Fatalf("%s through the preview route: %d", rel, res.StatusCode)
		}
	}
	// A preview token is not a share link.
	tok := strings.Split(url, "/")[2]
	if res, _ := get(t, r.remote.URL+"/s/"+tok+"/index.html"); res.StatusCode != 404 {
		t.Fatalf("preview token opened the share route: %d", res.StatusCode)
	}
}

func TestShareIsASnapshotUntilUpdatedAndDiesWhenStopped(t *testing.T) {
	r := newArtefactRig(t)
	report := r.write(t, "report.md", map[string]string{"": "one"})
	_, shown := r.show(t, r.agent, report)
	id := shown["artefactId"].(string)

	if st := send(t, "GET", r.api(id)+"/share"); st["share"] != nil {
		t.Fatalf("shared before anyone shared it: %v", st)
	}
	link := send(t, "POST", r.api(id)+"/share")["share"].(map[string]any)["url"].(string)
	// The share route answers with the host the request came to.
	if !strings.HasPrefix(link, r.local.URL+"/s/") {
		t.Fatalf("share url %q", link)
	}
	remote := strings.Replace(link, r.local.URL, r.remote.URL, 1)

	os.WriteFile(report, []byte("two"), 0o644)
	if _, body := get(t, remote); body != "one" {
		t.Fatalf("the link followed an edit nobody shared: %q", body)
	}
	if st := send(t, "GET", r.api(id)+"/share")["share"].(map[string]any); st["url"] != link {
		t.Fatalf("status url %v, want %s", st["url"], link)
	}

	updated := send(t, "POST", r.api(id)+"/share")["share"].(map[string]any)["url"].(string)
	if updated != link {
		t.Fatalf("update changed the link: %s", updated)
	}
	if _, body := get(t, remote); body != "two" {
		t.Fatalf("updated link served %q", body)
	}

	send(t, "DELETE", r.api(id)+"/share")
	if res, _ := get(t, remote); res.StatusCode != 404 {
		t.Fatalf("stopped link: %d", res.StatusCode)
	}
	again := send(t, "POST", r.api(id)+"/share")["share"].(map[string]any)["url"].(string)
	if again == link {
		t.Fatal("sharing again brought the stopped link back")
	}
	if res, _ := get(t, remote); res.StatusCode != 404 {
		t.Fatalf("stopped link after sharing again: %d", res.StatusCode)
	}
}

func TestUploadsLandInTheUploadsFolderAndTrailerNamesThem(t *testing.T) {
	r := newArtefactRig(t)
	upload := func() map[string]any {
		return post(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts?name=brief.pdf", "%PDF-1.4 x")["artefact"].(map[string]any)
	}
	a1, a2 := upload(), upload()
	if a1["path"] != filepath.Join(r.dir, "uploads", "brief.pdf") || a2["name"] != "brief (2).pdf" || a1["id"] == a2["id"] {
		t.Fatalf("uploads: %v / %v", a1, a2)
	}

	actor, err := r.srv.mgr.View(context.Background(), r.session)
	if err != nil {
		t.Fatal(err)
	}
	trailer, err := r.srv.attachedFiles(context.Background(), actor, []promptFile{{ArtefactID: a2["id"].(string)}})
	if err != nil {
		t.Fatal(err)
	}
	want := "- brief (2).pdf (application/pdf, 10 B, artefact " + a2["id"].(string) + "): " + filepath.Join(r.dir, "uploads", "brief (2).pdf")
	if !strings.Contains(trailer, want) {
		t.Fatalf("trailer = %q", trailer)
	}
	if _, err := r.srv.attachedFiles(context.Background(), actor, []promptFile{{ArtefactID: "nope"}}); err == nil {
		t.Fatal("an unknown file was accepted")
	}
}

func TestSharedPageGetsAViewportOnlyWhenItHasNone(t *testing.T) {
	r := newArtefactRig(t)
	serve := func(name, doc string) string {
		_, shown := r.show(t, r.agent, r.write(t, name, map[string]string{"index.html": doc}))
		url := send(t, "POST", r.api(shown["artefactId"].(string))+"/share")["share"].(map[string]any)["url"].(string)
		_, body := get(t, url)
		return body
	}
	bare := serve("bare", "<html><head><title>x</title></head><body>hi</body></html>")
	if strings.Count(bare, `name="viewport"`) != 1 || !strings.Contains(bare, "<body>hi</body>") {
		t.Fatalf("page without a viewport: %q", bare)
	}
	own := serve("own", `<html><HEAD><META NAME=viewport CONTENT="width=500"></HEAD><body>hi</body></html>`)
	if strings.Contains(own, "device-width") || !strings.Contains(own, `CONTENT="width=500"`) {
		t.Fatalf("page with its own viewport: %q", own)
	}
	// A viewport mentioned in the body is not one in the head.
	late := serve("late", `<html><head></head><body><pre>&lt;meta name="viewport"&gt;</pre><meta name="viewport" content="x"></body></html>`)
	if !strings.Contains(late, "device-width") {
		t.Fatalf("a viewport after the head counted: %q", late)
	}
}
