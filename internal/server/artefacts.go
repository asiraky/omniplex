package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/thread"
)

const (
	previewTTL = time.Hour
	// sandboxCSP makes a served document its own opaque origin: its scripts
	// run, but cannot read the app's cookies, storage or API.
	sandboxCSP = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"
	// bridgeLimit caps the HTML the token routes will read into memory to
	// add the bridge. Anything larger is served as it is, without one.
	bridgeLimit = 8 << 20
)

func (s *Server) routeArtefacts(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/threads/{id}/artefacts", s.handleUploadArtefact)
	mux.HandleFunc("GET /api/threads/{id}/artefacts/{aid}/f/{path...}", s.handleRawArtefact)
	mux.HandleFunc("POST /api/threads/{id}/artefacts/{aid}/preview", s.handlePreviewArtefact)
	mux.HandleFunc("GET /api/threads/{id}/artefacts/{aid}/share", s.handleShareStatus)
	mux.HandleFunc("POST /api/threads/{id}/artefacts/{aid}/share", s.handleShareArtefact)
	mux.HandleFunc("DELETE /api/threads/{id}/artefacts/{aid}/share", s.handleUnshareArtefact)
	// Public: each carries its own signed token, checked by its handler.
	mux.HandleFunc("GET /p/{token}/{path...}", s.handlePreviewToken)
	mux.HandleFunc("GET /s/{token}/{path...}", s.handleShareToken)
	mux.HandleFunc("GET /s/{token}", s.handleShareToken)
	mux.HandleFunc("POST /api/agent/artefacts", s.handleAgentShow)
}

// artefactPublicPath says whether a path carries its own token and so skips
// the device gate.
func artefactPublicPath(p string) bool {
	return strings.HasPrefix(p, "/p/") || strings.HasPrefix(p, "/s/") || p == "/api/agent/artefacts"
}

func (s *Server) artefactsOff(w http.ResponseWriter) bool {
	if s.artefacts == nil || s.signer == nil {
		writeError(w, http.StatusNotImplemented, "this server does not have artefacts")
		return true
	}
	return false
}

func writeArtefactError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), errors.Is(err, artefact.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too large: at most 200 MB and 2000 files")
	case errors.Is(err, artefact.ErrEmpty), errors.Is(err, artefact.ErrBadPath):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, artefact.ErrNotFound), errors.Is(err, thread.ErrNoArtefact):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// threadArtefact finds an artefact a thread has shown.
func (s *Server) threadArtefact(r *http.Request, threadID, id string) (projection.Artefact, error) {
	actor, err := s.mgr.View(r.Context(), threadID)
	if err != nil {
		return projection.Artefact{}, thread.ErrNoArtefact
	}
	return actor.Artefact(r.Context(), id)
}

func artefactFromPayload(p proto.ArtefactShownPayload) projection.Artefact {
	return projection.Artefact{
		ID: p.ArtefactID, Name: p.Name, Path: p.Path, Dir: p.Dir, MediaType: p.MediaType, Size: p.Size,
		Entry: p.Entry, Files: p.Files, ModifiedAt: p.ModifiedAt, Source: p.Source, Note: p.Note,
		TurnID: p.TurnID, ShownAt: proto.NowMillis(),
	}
}

// handleUploadArtefact takes one file a human dropped into the composer, of
// any type. The body is the file itself. It is saved into the project's
// uploads folder, where the agent can read it, and shown straight away, so
// the upload is paid for while the message is still being typed.
func (s *Server) handleUploadArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	threadID := r.PathValue("id")
	actor, err := s.mgr.View(r.Context(), threadID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such thread")
		return
	}
	home, _, err := s.mgr.ArtefactRoots(r.Context(), threadID)
	if err != nil || home == "" {
		writeError(w, http.StatusConflict, "this thread has no folder to save uploads in")
		return
	}
	p, err := artefact.SaveUpload(filepath.Join(home, "uploads"), r.URL.Query().Get("name"), http.MaxBytesReader(w, r.Body, artefact.MaxBytes+1))
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	info, err := artefact.Describe(p)
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	shown, err := actor.ShowArtefact(r.Context(), thread.Show{Path: p, Info: info, Source: proto.ArtefactFromUpload})
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	writeJSON(w, map[string]any{"artefact": artefactFromPayload(shown)})
}

// handleRawArtefact serves an artefact's live file to the app. Behind the
// device gate, so an <img>, <audio> or fetch gets it with the cookie and
// nothing else.
func (s *Server) handleRawArtefact(w http.ResponseWriter, r *http.Request) {
	a, err := s.threadArtefact(r, r.PathValue("id"), r.PathValue("aid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := artefact.Resolve(a.Path, a.Dir, r.PathValue("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveArtefactFile(w, r, p, false)
}

// handlePreviewArtefact mints a short-lived URL for the in-app viewer. The
// iframe it goes in is sandboxed without allow-same-origin, so it sends no
// cookie: the token in the path is what lets it, and every file it links to
// relatively, load.
func (s *Server) handlePreviewArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	threadID, id := r.PathValue("id"), r.PathValue("aid")
	a, err := s.threadArtefact(r, threadID, id)
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	info, err := artefact.Describe(a.Path)
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	exp := time.Now().Add(previewTTL).UnixMilli()
	tok := s.signer.Mint(artefact.Claims{Kind: artefact.KindPreview, Thread: threadID, Artefact: id, ExpiresAt: exp})
	writeJSON(w, map[string]any{"url": "/p/" + tok + "/" + escapePath(info.Entry), "expiresAt": exp})
}

// shareStatus is what the app is told about a share.
type shareStatus struct {
	URL       string `json:"url"`
	SharedAt  int64  `json:"sharedAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (s *Server) shareStatusOf(r *http.Request, threadID, id string, sh artefact.Share) shareStatus {
	tok := s.signer.Mint(artefact.Claims{Kind: artefact.KindShare, Thread: threadID, Artefact: id, Nonce: sh.Nonce})
	return shareStatus{URL: requestOrigin(r) + "/s/" + tok, SharedAt: sh.SharedAt, ExpiresAt: sh.ExpiresAt}
}

// handleShareStatus says whether an artefact is shared, and where. An expired
// share reads as not shared.
func (s *Server) handleShareStatus(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	threadID, id := r.PathValue("id"), r.PathValue("aid")
	sh, err := s.artefacts.Share(threadID, id)
	if err != nil || sh.Expired(time.Now()) {
		writeJSON(w, map[string]any{"share": nil})
		return
	}
	writeJSON(w, map[string]any{"share": s.shareStatusOf(r, threadID, id, sh)})
}

// handleShareArtefact copies the artefact as it is now and returns a link to
// the copy. Sharing again updates the copy behind the same link, so the
// colleague sent it on Monday sees Wednesday's revision only once someone
// chose to send it.
func (s *Server) handleShareArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	threadID, id := r.PathValue("id"), r.PathValue("aid")
	a, err := s.threadArtefact(r, threadID, id)
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	sh, err := s.artefacts.Snapshot(threadID, id, a.Path, time.Now())
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	writeJSON(w, map[string]any{"share": s.shareStatusOf(r, threadID, id, sh)})
}

// handleUnshareArtefact deletes the copy. The link stops working at once.
func (s *Server) handleUnshareArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	if err := s.artefacts.Unshare(r.PathValue("id"), r.PathValue("aid")); err != nil {
		writeArtefactError(w, err)
		return
	}
	writeJSON(w, map[string]any{"share": nil})
}

const deadLink = "This link has expired or is not valid."

// handlePreviewToken serves the in-app viewer's live files.
func (s *Server) handlePreviewToken(w http.ResponseWriter, r *http.Request) {
	if s.artefacts == nil || s.signer == nil {
		http.NotFound(w, r)
		return
	}
	c, err := s.signer.Check(r.PathValue("token"), artefact.KindPreview, time.Now())
	if err != nil {
		http.Error(w, deadLink, http.StatusNotFound)
		return
	}
	a, err := s.threadArtefact(r, c.Thread, c.Artefact)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := artefact.Resolve(a.Path, a.Dir, r.PathValue("path"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveArtefactFile(w, r, p, true)
}

// handleShareToken serves a share's snapshot to whoever holds the link.
func (s *Server) handleShareToken(w http.ResponseWriter, r *http.Request) {
	if s.artefacts == nil || s.signer == nil {
		http.NotFound(w, r)
		return
	}
	c, err := s.signer.Check(r.PathValue("token"), artefact.KindShare, time.Now())
	if err != nil {
		http.Error(w, deadLink, http.StatusNotFound)
		return
	}
	sh, err := s.artefacts.Share(c.Thread, c.Artefact)
	if err != nil || sh.Nonce != c.Nonce || sh.Expired(time.Now()) {
		http.Error(w, deadLink, http.StatusNotFound)
		return
	}
	rel := r.PathValue("path")
	if rel == "" {
		// The bare link, or one missing its trailing slash: send it to the
		// entry so relative URLs inside resolve under the token.
		http.Redirect(w, r, "/s/"+r.PathValue("token")+"/"+escapePath(sh.Entry), http.StatusFound)
		return
	}
	p, err := s.artefacts.OpenShared(c.Thread, c.Artefact, rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveArtefactFile(w, r, p, true)
}

// serveArtefactFile writes one file. The raw route is same-origin with the
// app, so a document served there is sandboxed with no scripts at all; the
// token routes let scripts run in an opaque origin. bridge adds the viewer
// bridge to HTML.
func serveArtefactFile(w http.ResponseWriter, r *http.Request, p string, bridge bool) {
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mediaType := artefact.MediaType(p)
	h := w.Header()
	h.Set("Content-Type", mediaType)
	h.Set("X-Content-Type-Options", "nosniff")
	// The files are live: the agent revises them in place. Revalidate every
	// time; an unchanged file costs a 304.
	h.Set("Cache-Control", "private, no-cache")
	tokenised := strings.HasPrefix(r.URL.Path, "/p/") || strings.HasPrefix(r.URL.Path, "/s/")
	if tokenised {
		// The token is the whole credential: a link out of the page must not
		// carry it to another site in the Referer.
		h.Set("Referrer-Policy", "no-referrer")
		// The page runs in an opaque origin, so its module scripts and
		// fetches of its own files are cross-origin requests. The token is
		// the whole credential and no cookie rides along, so any origin may
		// read what the link already gives.
		h.Set("Access-Control-Allow-Origin", "*")
	}
	if activeContent(mediaType) {
		if tokenised {
			h.Set("Content-Security-Policy", sandboxCSP)
		} else {
			h.Set("Content-Security-Policy", "sandbox")
		}
	}
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	}
	if bridge && strings.HasPrefix(mediaType, "text/html") && info.Size() <= bridgeLimit {
		body, err := io.ReadAll(f)
		if err == nil {
			http.ServeContent(w, r, "", info.ModTime(), bytes.NewReader(injectBridge(body)))
			return
		}
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// activeContent is what a browser would run script in if it opened it: HTML,
// SVG, XML. PDFs are left alone because Chrome will not render a PDF inside a
// sandboxed document at all.
func activeContent(mediaType string) bool {
	mt, _, _ := mime.ParseMediaType(mediaType)
	switch {
	case mt == "text/html", mt == "application/xhtml+xml", mt == "image/svg+xml", strings.HasSuffix(mt, "+xml"), mt == "text/xml", mt == "application/xml":
		return true
	}
	return false
}

// bridgeScript runs first in every HTML page on the token routes.
//
// The sandbox gives the page an opaque origin, so localStorage and
// sessionStorage throw, and a prototype that saves its state dies on load. The
// script swaps in an in-memory store that lasts as long as the page does.
//
// When framed by the viewer it also carries the browser chrome across the
// sandbox boundary: the page reports where it is and what it logged, and takes
// back and forward from the parent.
const bridgeScript = `<script>(function(){
function mem(){var d={};return{getItem:function(k){k=String(k);return Object.prototype.hasOwnProperty.call(d,k)?d[k]:null},setItem:function(k,v){d[String(k)]=String(v)},removeItem:function(k){delete d[String(k)]},clear:function(){d={}},key:function(i){var ks=Object.keys(d);return i<ks.length?ks[i]:null},get length(){return Object.keys(d).length}}}
["localStorage","sessionStorage"].forEach(function(n){try{window[n].length}catch(e){try{Object.defineProperty(window,n,{value:mem(),configurable:true})}catch(e2){}}});
var P=window.parent;if(P===window)return;
function send(m){try{P.postMessage(m,"*")}catch(e){}}
function nav(){send({omniplex:"nav",url:location.pathname+location.search+location.hash,title:document.title})}
addEventListener("load",nav);addEventListener("popstate",nav);addEventListener("hashchange",nav);
function fmt(a){if(typeof a==="string")return a;try{return JSON.stringify(a)}catch(e){return String(a)}}
["log","info","warn","error"].forEach(function(l){var o=console[l];console[l]=function(){send({omniplex:"console",level:l,text:Array.prototype.map.call(arguments,fmt).join(" ")});return o.apply(console,arguments)}});
addEventListener("error",function(e){send({omniplex:"console",level:"error",text:String(e.message)+(e.filename?" ("+e.filename.split("/").pop()+":"+e.lineno+")":"")})});
addEventListener("unhandledrejection",function(e){send({omniplex:"console",level:"error",text:"Unhandled rejection: "+fmt(e.reason)})});
addEventListener("message",function(e){if(e.source!==P||!e.data)return;if(e.data.omniplex==="back")history.back();if(e.data.omniplex==="forward")history.forward()});
})();</script>`

// viewportMeta is added to a page that has none. Without it a phone lays the
// page out 980px wide and shrinks it to fit, so a shared link opens as a
// postage stamp. In the viewer's frame it changes nothing: a frame is already
// as wide as it is drawn.
const viewportMeta = `<meta name="viewport" content="width=device-width, initial-scale=1">`

var hasViewport = regexp.MustCompile(`<meta[^>]*name\s*=\s*["']?viewport`)

// injectBridge puts the bridge first in the document's head, so it hooks the
// console before the page's own scripts run.
func injectBridge(doc []byte) []byte {
	head := bytes.ToLower(doc[:min(len(doc), 16384)])
	if i := bytes.Index(head, []byte("</head")); i >= 0 {
		head = head[:i]
	}
	inject := bridgeScript
	if !hasViewport.Match(head) {
		inject += viewportMeta
	}
	lower := head[:min(len(head), 4096)]
	at := 0
	if i := bytes.Index(lower, []byte("<head")); i >= 0 {
		if j := bytes.IndexByte(doc[i:], '>'); j >= 0 {
			at = i + j + 1
		}
	} else if i := bytes.Index(lower, []byte("<html")); i >= 0 {
		if j := bytes.IndexByte(doc[i:], '>'); j >= 0 {
			at = i + j + 1
		}
	}
	out := make([]byte, 0, len(doc)+len(inject))
	out = append(out, doc[:at]...)
	out = append(out, inject...)
	return append(out, doc[at:]...)
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// requestOrigin is the origin the request was addressed to, which is the one
// a shared link has to use to be reachable by whoever it is sent to.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	return scheme + "://" + host
}

// handleAgentShow is where the omniplex MCP server a harness runs sends a
// file the agent wants to show. The path has to be inside the thread's
// project: its home folder, its working directory or the project's root.
func (s *Server) handleAgentShow(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, err := s.signer.Check(tok, artefact.KindAgent, time.Now())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "bad agent token")
		return
	}
	var body struct {
		Path  string `json:"path"`
		Title string `json:"title"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actor, err := s.mgr.View(r.Context(), c.Thread)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such thread")
		return
	}
	home, roots, err := s.mgr.ArtefactRoots(r.Context(), c.Thread)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !filepath.IsAbs(body.Path) {
		writeError(w, http.StatusBadRequest, "path must be absolute")
		return
	}
	p, err := filepath.EvalSymlinks(filepath.Clean(body.Path))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such file: "+body.Path)
		return
	}
	if !insideAny(roots, p) {
		writeError(w, http.StatusForbidden, "that is outside this project. Move it into "+home+" and show it from there.")
		return
	}
	info, err := artefact.Describe(p)
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	shown, err := actor.ShowArtefact(r.Context(), thread.Show{
		Path: p, Name: strings.TrimSpace(body.Title), Note: strings.TrimSpace(body.Note), Info: info, Source: proto.ArtefactFromAgent,
	})
	if err != nil {
		writeArtefactError(w, err)
		return
	}
	writeJSON(w, shown)
}

// insideAny reports whether p is inside one of roots, with the roots' own
// symlinks resolved the way p's were.
func insideAny(roots []string, p string) bool {
	for _, root := range roots {
		if root == "" {
			continue
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if artefact.Within(real, p) {
			return true
		}
	}
	return false
}
