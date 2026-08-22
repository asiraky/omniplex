package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
)

// webAssets serves the embedded UI, using the cache policy Vite's build output
// is designed for.
//
// Asset filenames carry a content hash, so an asset can be cached forever: when
// its content changes, its name changes, and the old name is simply never
// requested again. The document is the one thing that cannot be cached that
// way, because its URL never changes — so it is revalidated on every load, and
// therefore always names the current hashes.
//
// Getting this wrong is not a small performance issue. Sending no headers at
// all let a browser cache the document on its own judgement; it went on naming
// a hash that the next build had deleted, and a missing file cannot
// revalidate. The page rendered, the script 404'd, and the app was silently
// dead — no WebSocket, so no sessions and no harnesses, which reads like three
// unrelated bugs. That happened on a real phone. The fix is the document being
// fresh, which makes a stale asset reference impossible.
type webAssets struct {
	fsys fs.FS

	// index is index.html with the build id stamped in, prepared once.
	index     []byte
	indexETag string

	// buildID identifies this bundle. It is a content hash, not a version or a
	// path: it travels to the client, so it must reveal nothing.
	buildID string

	etags map[string]string
}

// buildPlaceholder is replaced at startup with the real build id. It stays as
// written when the Vite dev server serves the page, which is how the client
// knows to skip build-mismatch checks during development.
const buildPlaceholder = `<meta name="omniplex-build" content="dev" />`

func newWebAssets(fsys fs.FS) (*webAssets, error) {
	if fsys == nil {
		return nil, errors.New("no web bundle")
	}

	w := &webAssets{fsys: fsys, etags: map[string]string{}}

	// Hashed once, at startup. Per request would put a read of the whole
	// bundle on the hot path for content that cannot change while we run.
	var names []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		w.etags[p] = `"` + hex.EncodeToString(sum[:])[:16] + `"`
		names = append(names, p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil, err
	}

	// The build id covers names as well as contents, so a bundle that merely
	// renames a file still counts as a new build.
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte(w.etags[n]))
	}
	w.buildID = hex.EncodeToString(h.Sum(nil))[:16]

	w.index = []byte(strings.Replace(
		string(index),
		buildPlaceholder,
		`<meta name="omniplex-build" content="`+w.buildID+`" />`,
		1,
	))
	sum := sha256.Sum256(w.index)
	w.indexETag = `"` + hex.EncodeToString(sum[:])[:16] + `"`

	return w, nil
}

// BuildID is the identity of the embedded bundle.
func (w *webAssets) BuildID() string {
	if w == nil {
		return ""
	}
	return w.buildID
}

func (w *webAssets) handler() http.Handler {
	fileServer := http.FileServer(http.FS(w.fsys))

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}

		if _, err := fs.Stat(w.fsys, p); err != nil {
			// A missing asset is a genuine 404. Falling through to the
			// document would answer a <script> request with HTML, which the
			// browser reports as a syntax error — an obscure symptom for a
			// plain missing file. Everything else is a client route and
			// resolves to the document.
			if strings.HasPrefix(p, "assets/") {
				http.NotFound(rw, r)
				return
			}
			w.serveIndex(rw, r)
			return
		}
		if p == "index.html" {
			w.serveIndex(rw, r)
			return
		}

		// Go's table has no entry for .webmanifest on some systems, and
		// content sniffing calls it plain text — which browsers refuse to
		// install as a manifest. Naming it here is cheaper than depending on
		// the host's mime database.
		if strings.HasSuffix(p, ".webmanifest") {
			rw.Header().Set("Content-Type", "application/manifest+json")
		}

		// Hashed assets are immutable: the name changes with the content, so a
		// cached copy can never be wrong. Anything else revalidates.
		if strings.HasPrefix(p, "assets/") {
			rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			rw.Header().Set("Cache-Control", "no-cache")
		}

		// Embedded files have a zero ModTime, so http.FileServer emits no
		// validator of its own and a revalidation could never answer 304.
		tag := w.etags[p]
		if tag != "" {
			rw.Header().Set("ETag", tag)
			if matchesETag(r.Header.Get("If-None-Match"), tag) {
				rw.WriteHeader(http.StatusNotModified)
				return
			}
		}
		fileServer.ServeHTTP(rw, r)
	})
}

// serveIndex answers with the document. It is never cached without
// revalidation: it is the only file whose URL survives a rebuild, so it is the
// only thing standing between a browser and a deleted asset name.
func (w *webAssets) serveIndex(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("ETag", w.indexETag)

	if matchesETag(r.Header.Get("If-None-Match"), w.indexETag) {
		rw.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = rw.Write(w.index)
}

// matchesETag implements the subset of If-None-Match that matters here: a
// comma-separated list, a weak prefix, or the wildcard.
func matchesETag(header, tag string) bool {
	if header == "" || tag == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == tag || strings.TrimPrefix(candidate, "W/") == tag {
			return true
		}
	}
	return false
}
