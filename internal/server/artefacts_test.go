package server

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	return &artefactRig{srv: srv, local: local, remote: remote, session: a.ID,
		agent: signer.Mint(artefact.Claims{Kind: artefact.KindAgent, Session: a.ID})}
}

func tarBody(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	return &buf
}

func (r *artefactRig) publish(t *testing.T, token, name string, files map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", r.remote.URL+"/api/agent/artefacts?name="+name, tarBody(t, files))
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

func TestAgentPublishVersionsByNameAndNeedsItsToken(t *testing.T) {
	r := newArtefactRig(t)
	bundle := map[string]string{"index.html": "<html><head><title>v1</title></head><body><script src=app.js></script></body></html>", "app.js": "console.log(1)"}

	res, _ := r.publish(t, "forged", "proto", bundle)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged token: %d", res.StatusCode)
	}
	res, first := r.publish(t, r.agent, "proto", bundle)
	if res.StatusCode != 200 || first["version"] != float64(1) || first["entry"] != "index.html" || first["files"] != float64(2) {
		t.Fatalf("first publish: %d %v", res.StatusCode, first)
	}
	_, second := r.publish(t, r.agent, "proto", map[string]string{"index.html": "v2"})
	if second["version"] != float64(2) || second["artefactId"] != first["artefactId"] {
		t.Fatalf("second publish = %v, want v2 of %v", second, first["artefactId"])
	}
	_, other := r.publish(t, r.agent, "notes", map[string]string{"notes.md": "# n"})
	if other["version"] != float64(1) || other["artefactId"] == first["artefactId"] {
		t.Fatalf("a new name must be a new artefact: %v", other)
	}
}

func TestPreviewTokenServesBundleSandboxedWithBridge(t *testing.T) {
	r := newArtefactRig(t)
	_, pub := r.publish(t, r.agent, "proto", map[string]string{
		"index.html": "<html><head><title>x</title></head><body>hi</body></html>",
		"app.js":     "1",
	})
	id := pub["artefactId"].(string)

	// The raw route is the app's own origin: behind the gate, and a document
	// there never gets to run script.
	res, _ := get(t, r.remote.URL+"/api/sessions/"+r.session+"/artefacts/"+id+"/v/1/index.html")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("raw route from an unpaired device: %d", res.StatusCode)
	}
	res, _ = get(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts/"+id+"/v/1/index.html")
	if res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("raw html: %d csp=%q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}

	preview := post(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts/"+id+"/v/1/preview", "")
	url := preview["url"].(string)
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
	res, body = get(t, r.remote.URL+strings.TrimSuffix(url, "index.html")+"app.js")
	if res.StatusCode != 200 || body != "1" {
		t.Fatalf("relative asset: %d %q", res.StatusCode, body)
	}
	res, _ = get(t, r.remote.URL+strings.TrimSuffix(url, "index.html")+"..%2f..%2f..%2fa.db")
	if res.StatusCode != 404 {
		t.Fatalf("traversal through the preview route: %d", res.StatusCode)
	}
	// A preview token is not a share link.
	tok := strings.Split(url, "/")[2]
	if res, _ := get(t, r.remote.URL+"/s/"+tok+"/index.html"); res.StatusCode != 404 {
		t.Fatalf("preview token opened the share route: %d", res.StatusCode)
	}
}

func TestShareLinkFollowsLatestUnlessPinned(t *testing.T) {
	r := newArtefactRig(t)
	_, pub := r.publish(t, r.agent, "report", map[string]string{"report.md": "one"})
	id := pub["artefactId"].(string)

	latest := post(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts/"+id+"/share", "")["url"].(string)
	pinned := post(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts/"+id+"/share", `{"version":1}`)["url"].(string)
	// The share route answers with the host the request came to.
	if !strings.HasPrefix(latest, r.local.URL+"/s/") {
		t.Fatalf("share url %q", latest)
	}

	// Version 2 renames its file: a latest link must still land on it.
	r.publish(t, r.agent, "report", map[string]string{"final.md": "two"})

	remote := func(u string) string { return strings.Replace(u, r.local.URL, r.remote.URL, 1) }
	if _, body := get(t, remote(latest)); body != "two" {
		t.Fatalf("latest link served %q", body)
	}
	if _, body := get(t, remote(pinned)); body != "one" {
		t.Fatalf("pinned link served %q", body)
	}
}

func TestUploadsNeverBecomeVersionsAndTrailerNamesThem(t *testing.T) {
	r := newArtefactRig(t)
	upload := func() map[string]any {
		return post(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts?name=brief.pdf", "%PDF-1.4 x")
	}
	first, second := upload(), upload()
	a1 := first["artefact"].(map[string]any)
	a2 := second["artefact"].(map[string]any)
	if a1["name"] != "brief.pdf" || a2["name"] != "brief (2).pdf" || a1["id"] == a2["id"] {
		t.Fatalf("uploads: %v / %v", a1, a2)
	}

	actor, err := r.srv.mgr.View(context.Background(), r.session)
	if err != nil {
		t.Fatal(err)
	}
	trailer, err := r.srv.attachedFiles(context.Background(), actor, r.session, []promptFile{{ArtefactID: a2["id"].(string), Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trailer, "- brief (2).pdf (application/pdf, 10 B, artefact "+a2["id"].(string)+"@1): /") {
		t.Fatalf("trailer = %q", trailer)
	}
	if _, err := r.srv.attachedFiles(context.Background(), actor, r.session, []promptFile{{ArtefactID: "nope", Version: 1}}); err == nil {
		t.Fatal("an unknown file was accepted")
	}
}

func TestSharedPageGetsAViewportOnlyWhenItHasNone(t *testing.T) {
	r := newArtefactRig(t)
	serve := func(name, doc string) string {
		_, pub := r.publish(t, r.agent, name, map[string]string{"index.html": doc})
		url := post(t, r.local.URL+"/api/sessions/"+r.session+"/artefacts/"+pub["artefactId"].(string)+"/share", "")["url"].(string)
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
