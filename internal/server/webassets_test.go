package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// These assert the cache policy directly, because the bug it prevents is
// invisible in development and only surfaces on a device that held a page
// across a rebuild. Weakening any of this should fail the build rather than a
// phone.

// testBundle mirrors what Vite emits: a document at a fixed URL naming assets
// whose filenames carry a content hash.
func testBundle(assetName, script string) fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(
			`<!doctype html><html><head><meta name="omniplex-build" content="dev" /></head>` +
				`<body><script type="module" src="/assets/` + assetName + `"></script></body></html>`)},
		"assets/" + assetName: &fstest.MapFile{Data: []byte(script)},
	}
}

func newTestAssets(t *testing.T, assetName, script string) *webAssets {
	t.Helper()
	assets, err := newWebAssets(testBundle(assetName, script))
	if err != nil {
		t.Fatal(err)
	}
	return assets
}

func serve(t *testing.T, assets *webAssets, target string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	assets.handler().ServeHTTP(rec, req)
	return rec.Result()
}

// The hinge of the whole policy. The document's URL survives a rebuild while
// its asset names do not, so if a browser may cache the document it can go on
// naming a file that has been deleted — and a missing file cannot revalidate.
// That is exactly how the app ended up silently dead on a phone.
func TestDocumentIsNeverCachedWithoutRevalidation(t *testing.T) {
	assets := newTestAssets(t, "index-aaaa1111.js", "console.log('app')")

	for _, target := range []string{"/", "/index.html", "/some/client/route"} {
		res := serve(t, assets, target, nil)
		got := res.Header.Get("Cache-Control")
		if got != "no-cache" {
			t.Errorf("%s Cache-Control = %q; want no-cache, or a cached document can outlive the assets it names", target, got)
		}
		if strings.Contains(got, "immutable") || strings.Contains(got, "max-age=3") {
			t.Errorf("%s is cacheable (%q); a rebuild could never reach a browser holding it", target, got)
		}
		if res.Header.Get("ETag") == "" {
			t.Errorf("%s has no ETag; embedded files have a zero ModTime, so without one revalidation can never answer 304", target)
		}
	}
}

// Hashed assets are safe to cache forever precisely because the name changes
// with the content. Serving them no-cache instead would work, but would pay a
// round trip per load for nothing.
func TestHashedAssetsAreImmutable(t *testing.T) {
	assets := newTestAssets(t, "index-aaaa1111.js", "console.log('app')")

	got := serve(t, assets, "/assets/index-aaaa1111.js", nil).Header.Get("Cache-Control")
	if !strings.Contains(got, "immutable") {
		t.Errorf("asset Cache-Control = %q; hashed assets should be cacheable indefinitely", got)
	}
}

// A rebuild renames the asset, and the document must name the new one. The old
// name going missing is fine — nothing can still be asking for it, because the
// document is never stale.
func TestRebuildRenamesAssetsAndTheDocumentFollows(t *testing.T) {
	before := newTestAssets(t, "index-aaaa1111.js", "console.log('v1')")
	after := newTestAssets(t, "index-bbbb2222.js", "console.log('v2')")

	if body := read(t, serve(t, before, "/", nil)); !strings.Contains(body, "index-aaaa1111.js") {
		t.Fatal("the first document does not name its own asset")
	}
	body := read(t, serve(t, after, "/", nil))
	if !strings.Contains(body, "index-bbbb2222.js") {
		t.Fatal("the rebuilt document does not name the new asset")
	}
	if strings.Contains(body, "index-aaaa1111.js") {
		t.Fatal("the rebuilt document still names the old asset")
	}
}

// Revalidating the document must be cheap, or serving it no-cache would cost a
// full download on every load.
func TestUnchangedDocumentRevalidatesTo304(t *testing.T) {
	assets := newTestAssets(t, "index-aaaa1111.js", "console.log('app')")

	tag := serve(t, assets, "/", nil).Header.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag on the document")
	}
	res := serve(t, assets, "/", map[string]string{"If-None-Match": tag})
	if res.StatusCode != http.StatusNotModified {
		t.Errorf("status %d for a matching If-None-Match; want 304", res.StatusCode)
	}
}

// A rebuild must invalidate: the same request with the old validator has to
// return the new document, not a 304.
func TestChangedDocumentIsNotRevalidatedAway(t *testing.T) {
	before := newTestAssets(t, "index-aaaa1111.js", "console.log('v1')")
	after := newTestAssets(t, "index-bbbb2222.js", "console.log('v2')")

	stale := serve(t, before, "/", nil).Header.Get("ETag")
	res := serve(t, after, "/", map[string]string{"If-None-Match": stale})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d; a stale validator must return the new document", res.StatusCode)
	}
}

func TestBuildIDIsStampedIntoTheDocument(t *testing.T) {
	assets := newTestAssets(t, "index-aaaa1111.js", "console.log('app')")

	if assets.BuildID() == "" {
		t.Fatal("no build id")
	}
	page := read(t, serve(t, assets, "/", nil))

	if !strings.Contains(page, `content="`+assets.BuildID()+`"`) {
		t.Fatal("the build id is not in the document, so an open tab cannot tell it is stale")
	}
	if strings.Contains(page, `content="dev"`) {
		t.Fatal("the dev placeholder survived; the client would skip build checks in production")
	}
}

func TestBuildIDTracksContent(t *testing.T) {
	if newTestAssets(t, "index-aaaa1111.js", "v1").BuildID() == newTestAssets(t, "index-bbbb2222.js", "v2").BuildID() {
		t.Fatal("two different bundles share a build id, so an open tab is never told to reload")
	}
}

// Unknown paths are client routes and must resolve to the document, not 404.
func TestUnknownPathServesTheApp(t *testing.T) {
	assets := newTestAssets(t, "index-aaaa1111.js", "console.log('app')")

	res := serve(t, assets, "/some/client/route", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d; client routes must fall through to index.html", res.StatusCode)
	}
}

func read(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	buf := make([]byte, 8192)
	n, _ := res.Body.Read(buf)
	return string(buf[:n])
}

// A vanished asset must 404 rather than fall through to the document. Answering
// a <script> request with HTML surfaces as "unexpected token '<'", which hides
// what is actually a missing file.
func TestMissingAssetIsNotTheDocument(t *testing.T) {
	assets := newTestAssets(t, "index-aaaa1111.js", "console.log('app')")

	res := serve(t, assets, "/assets/index-deleted0.js", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d for a missing asset; want 404, not the document", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Fatalf("a missing asset returned %s; a script request must not receive HTML", ct)
	}
}

// The manifest only installs if it arrives as JSON. Content sniffing calls it
// text on a host whose mime table has no .webmanifest entry, and a home-screen
// install then silently falls back to defaults.
func TestManifestIsServedAsJSON(t *testing.T) {
	fsys := testBundle("index-aaaa1111.js", "console.log('app')")
	fsys["manifest.webmanifest"] = &fstest.MapFile{Data: []byte(`{"name":"Omniplex"}`)}
	assets, err := newWebAssets(fsys)
	if err != nil {
		t.Fatal(err)
	}

	res := serve(t, assets, "/manifest.webmanifest", nil)
	if got := res.Header.Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("Content-Type = %q; want application/manifest+json", got)
	}
}
