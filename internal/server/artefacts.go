package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/session"
)

const (
	previewTTL = time.Hour
	shareTTL   = 7 * 24 * time.Hour
	// sandboxCSP makes a served document its own opaque origin: its scripts
	// run, but cannot read the app's cookies, storage or API.
	sandboxCSP = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"
	// bridgeLimit caps the HTML the preview route will read into memory to
	// add the bridge. Anything larger is served as it is, without one.
	bridgeLimit = 8 << 20
)

func (s *Server) routeArtefacts(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{id}/artefacts", s.handleUploadArtefact)
	mux.HandleFunc("GET /api/sessions/{id}/artefacts/{aid}/v/{version}/{path...}", s.handleRawArtefact)
	mux.HandleFunc("POST /api/sessions/{id}/artefacts/{aid}/v/{version}/preview", s.handlePreviewArtefact)
	mux.HandleFunc("POST /api/sessions/{id}/artefacts/{aid}/share", s.handleShareArtefact)
	// Public: each carries its own signed token, checked by its handler.
	mux.HandleFunc("GET /p/{token}/{path...}", s.handleTokenArtefact(artefact.KindPreview))
	mux.HandleFunc("GET /s/{token}/{path...}", s.handleTokenArtefact(artefact.KindShare))
	mux.HandleFunc("GET /s/{token}", s.handleTokenArtefact(artefact.KindShare))
	mux.HandleFunc("POST /api/agent/artefacts", s.handleAgentPublish)
}

// artefactPublicPath says whether a path carries its own token and so skips
// the device gate.
func artefactPublicPath(p string) bool {
	return strings.HasPrefix(p, "/p/") || strings.HasPrefix(p, "/s/") || p == "/api/agent/artefacts"
}

func (s *Server) artefactsOff(w http.ResponseWriter) bool {
	if s.artefacts == nil || s.signer == nil {
		writeError(w, http.StatusNotImplemented, "this server does not store artefacts")
		return true
	}
	return false
}

func writeStageError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), errors.Is(err, artefact.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "artefact is larger than 200 MB")
	case errors.Is(err, artefact.ErrEmpty), errors.Is(err, artefact.ErrBadPath), errors.Is(err, session.ErrNoName):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleUploadArtefact takes one file a human dropped into the composer, of
// any type. Like image attachments the body is the file itself. It becomes an
// artefact of the session straight away, so the upload is paid for while the
// message is still being typed.
func (s *Server) handleUploadArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	sessionID := r.PathValue("id")
	name := r.URL.Query().Get("name")
	actor, err := s.mgr.View(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such session")
		return
	}
	staged, err := s.artefacts.StageFile(sessionID, name, http.MaxBytesReader(w, r.Body, artefact.MaxBytes+1))
	if err != nil {
		writeStageError(w, err)
		return
	}
	defer staged.Discard()
	// An upload is always a new artefact: two files called brief.pdf dropped
	// in on different days are not versions of each other.
	pub, err := actor.PublishArtefact(r.Context(), session.Publish{Staged: staged, Name: uniqueName(actor, r, staged.Entry), Source: proto.ArtefactFromUpload})
	if err != nil {
		writeStageError(w, err)
		return
	}
	writeJSON(w, map[string]any{"artefact": artefactFromPayload(pub), "version": pub.Version})
}

// uniqueName keeps an upload from landing as a version of an artefact that
// already has its name, by numbering it the way a download folder would.
func uniqueName(actor *session.Actor, r *http.Request, name string) string {
	st, err := actor.State(r.Context())
	if err != nil {
		return name
	}
	if _, taken := st.ArtefactByName(name); !taken {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, taken := st.ArtefactByName(candidate); !taken {
			return candidate
		}
	}
}

func artefactFromPayload(p proto.ArtefactPublishedPayload) projection.Artefact {
	return projection.Artefact{ID: p.ArtefactID, Name: p.Name, Versions: []projection.ArtefactVersion{{
		Version: p.Version, MediaType: p.MediaType, Size: p.Size, Entry: p.Entry, Files: p.Files,
		Source: p.Source, Note: p.Note, TurnID: p.TurnID, PublishedAt: proto.NowMillis(),
	}}}
}

// handleRawArtefact serves a stored file to the app. Behind the device gate,
// so an <img>, <audio> or fetch gets it with the cookie and nothing else.
func (s *Server) handleRawArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefacts == nil {
		http.NotFound(w, r)
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveArtefactFile(w, r, r.PathValue("id"), r.PathValue("aid"), version, r.PathValue("path"), false)
}

// handlePreviewArtefact mints a short-lived URL for the in-app viewer. The
// iframe it goes in is sandboxed without allow-same-origin, so it sends no
// cookie: the token in the path is what lets it, and every file it links to
// relatively, load.
func (s *Server) handlePreviewArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	sessionID, id := r.PathValue("id"), r.PathValue("aid")
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad version")
		return
	}
	meta, err := s.artefacts.Entry(sessionID, id, version)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such artefact")
		return
	}
	exp := time.Now().Add(previewTTL).UnixMilli()
	tok := s.signer.Mint(artefact.Claims{Kind: artefact.KindPreview, Session: sessionID, Artefact: id, Version: version, ExpiresAt: exp})
	writeJSON(w, map[string]any{"url": "/p/" + tok + "/" + escapePath(meta.Entry), "expiresAt": exp})
}

// handleShareArtefact mints a link anyone holding it can open, for a week.
// Without a version it follows the latest one, so the link sent to a
// colleague on Monday shows Wednesday's revision.
func (s *Server) handleShareArtefact(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	var body struct {
		Version int `json:"version"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	sessionID, id := r.PathValue("id"), r.PathValue("aid")
	check := body.Version
	if check == 0 {
		v, err := s.artefacts.Latest(sessionID, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such artefact")
			return
		}
		check = v
	}
	if _, err := s.artefacts.Entry(sessionID, id, check); err != nil {
		writeError(w, http.StatusNotFound, "no such artefact")
		return
	}
	exp := time.Now().Add(shareTTL).UnixMilli()
	tok := s.signer.Mint(artefact.Claims{Kind: artefact.KindShare, Session: sessionID, Artefact: id, Version: body.Version, ExpiresAt: exp})
	// The link has no path: it redirects to whichever version's entry is
	// current when it is opened, which a latest-following link needs.
	writeJSON(w, map[string]any{"url": requestOrigin(r) + "/s/" + tok, "expiresAt": exp})
}

// handleTokenArtefact serves the preview and share routes.
func (s *Server) handleTokenArtefact(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.artefacts == nil || s.signer == nil {
			http.NotFound(w, r)
			return
		}
		c, err := s.signer.Check(r.PathValue("token"), kind, time.Now())
		if err != nil {
			http.Error(w, "This link has expired or is not valid.", http.StatusNotFound)
			return
		}
		version := c.Version
		if version == 0 {
			if version, err = s.artefacts.Latest(c.Session, c.Artefact); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		rel := r.PathValue("path")
		if rel == "" {
			// The bare link, or one missing its trailing slash: send it to
			// the entry so relative URLs inside resolve under the token.
			meta, err := s.artefacts.Entry(c.Session, c.Artefact, version)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/"+kind+"/"+r.PathValue("token")+"/"+escapePath(meta.Entry), http.StatusFound)
			return
		}
		s.serveArtefactFile(w, r, c.Session, c.Artefact, version, rel, true)
	}
}

// serveArtefactFile writes one stored file. The raw route is same-origin with
// the app, so a document served there is sandboxed with no scripts at all; the
// token routes let scripts run in an opaque origin. bridge adds the viewer
// bridge to HTML.
func (s *Server) serveArtefactFile(w http.ResponseWriter, r *http.Request, sessionID, id string, version int, rel string, bridge bool) {
	p, err := s.artefacts.Open(sessionID, id, version, rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
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
	// A version never changes, so the URL is the cache key; private because
	// the bytes are someone's work.
	h.Set("Cache-Control", "private, max-age=3600")
	tokenised := strings.HasPrefix(r.URL.Path, "/p/") || strings.HasPrefix(r.URL.Path, "/s/")
	if tokenised {
		// The token is the whole credential: a link out of the page must not
		// carry it to another site in the Referer.
		h.Set("Referrer-Policy", "no-referrer")
	}
	if activeContent(mediaType) {
		if tokenised {
			h.Set("Content-Security-Policy", sandboxCSP)
		} else {
			h.Set("Content-Security-Policy", "sandbox")
		}
	}
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(rel)}))
	}
	if bridge && strings.HasPrefix(mediaType, "text/html") && info.Size() <= bridgeLimit {
		body, err := os.ReadFile(p)
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

// handleAgentPublish is where the omniplex MCP server a harness runs sends
// what the agent published. The body is a tar of the file or directory, read
// by the MCP process with the harness's own permissions, so the server never
// opens a path an agent names.
func (s *Server) handleAgentPublish(w http.ResponseWriter, r *http.Request) {
	if s.artefactsOff(w) {
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, err := s.signer.Check(tok, artefact.KindAgent, time.Now())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "bad agent token")
		return
	}
	actor, err := s.mgr.View(r.Context(), c.Session)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such session")
		return
	}
	staged, err := s.artefacts.StageTar(c.Session, http.MaxBytesReader(w, r.Body, artefact.MaxBytes+1<<20))
	if err != nil {
		writeStageError(w, err)
		return
	}
	defer staged.Discard()
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = path.Base(staged.Entry)
	}
	pub, err := actor.PublishArtefact(r.Context(), session.Publish{Staged: staged, Name: name, Source: proto.ArtefactFromAgent, Note: r.URL.Query().Get("note")})
	if err != nil {
		writeStageError(w, err)
		return
	}
	writeJSON(w, pub)
}
