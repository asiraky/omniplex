package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
	"github.com/asiraky/omniplex/internal/tts"
	"github.com/asiraky/omniplex/internal/userconfig"
)

const (
	noSpeechKey   = "Add an ElevenLabs API key in Settings"
	noSpeechVoice = "Pick a voice in Settings"
)

var errNoVoice = errors.New(noSpeechVoice)

// routeTTS serves read-aloud. Both routes sit behind the device gate, which
// an <audio> element passes because its request carries the device cookie.
func (s *Server) routeTTS(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tts/voices", s.handleVoices)
	mux.HandleFunc("GET /api/tts", s.handleSpeech)
}

func (s *Server) speechOff(w http.ResponseWriter) bool {
	if s.speech == nil {
		writeError(w, http.StatusNotImplemented, "this server does not have text-to-speech")
		return true
	}
	return false
}

func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	if s.speechOff(w) {
		return
	}
	voices, err := s.speech.Voices(r.Context())
	if err != nil {
		status, msg := speechErrorReply(err)
		writeError(w, status, msg)
		return
	}
	sorted := append([]tts.Voice(nil), voices...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
	})
	writeJSON(w, map[string]any{"voices": sorted})
}

// handleSpeech answers GET /api/tts?text=…&thread=… with mp3.
//
// The text is in the URL so that a plain <audio src> can play it, with the
// browser's own buffering, seeking and lock-screen controls. The ETag is the
// cache key, which already covers the voice: replaying sends If-None-Match
// and gets a 304, so audio crosses the network once per device. It is not
// immutable because the same URL means a different voice after the setting
// changes.
func (s *Server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	if s.speechOff(w) {
		return
	}
	started := time.Now()
	q := r.URL.Query()
	text, err := tts.PrepareText(q.Get("text"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.speech.HasKey() {
		writeError(w, http.StatusConflict, noSpeechKey)
		return
	}
	voice, err := s.speechVoice(r.Context(), q.Get("thread"))
	switch {
	case errors.Is(err, errNoVoice):
		writeError(w, http.StatusConflict, noSpeechVoice)
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such thread")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The audio is keyed on the text as it came and who rewrites it, so a
	// replay is a cache hit without waiting on the model again. A model that
	// has just failed outright is skipped, so the audio made without it can
	// be kept rather than paid for on every play.
	editor, editing := s.editorFor(r.Context(), q.Get("thread"))
	editing = editing && !s.rewriter.Down(editor.model)
	key := tts.CacheKey(voice, text)
	if editing {
		key = tts.CacheKey(voice, tts.RewriteKey(editor.model, text))
	}
	audioHeaders := func() {
		h := w.Header()
		h.Set("Content-Type", "audio/mpeg")
		h.Set("ETag", `"`+key+`"`)
		h.Set("Cache-Control", "private, no-cache")
	}

	if f, ok := s.speech.Cache().Open(key); ok {
		defer f.Close()
		audioHeaders()
		// No modtime: a hit touches it, so Last-Modified would change on
		// every play and add nothing the ETag does not already say.
		http.ServeContent(w, r, "", time.Time{}, f)
		s.logf("tts cache=hit text_bytes=%d duration_ms=%d", len(text), time.Since(started).Milliseconds())
		return
	}
	if status, msg, ok := s.speechFailures.recall(key); ok {
		writeError(w, status, msg)
		return
	}
	if r.Method == http.MethodHead {
		// Synthesis is billed; a HEAD is not worth paying for.
		audioHeaders()
		w.WriteHeader(http.StatusOK)
		return
	}

	spoken, whole, rewrite := text, true, "off"
	if editing {
		spoken, whole = s.rewriter.Rewrite(r.Context(), editor.model, editor.prompt, text)
		rewrite = "whole"
		if !whole {
			rewrite = "partial"
		}
	}
	if r.Context().Err() != nil {
		return
	}

	// A miss ignores Range and sends the whole clip: there is nothing to seek
	// in until it exists, and a media element takes a 200 for a range request.
	body, err := s.speech.Stream(r.Context(), voice, spoken)
	if err != nil {
		status, msg := speechErrorReply(err)
		var refused *tts.UpstreamError
		if errors.As(err, &refused) {
			s.speechFailures.remember(key, status, msg)
		}
		writeError(w, status, msg)
		return
	}
	defer body.Close()

	// Audio from a text the model only partly rewrote is played but not
	// kept, so the next play gets another try at the rewrite.
	var pending *tts.Pending
	if whole {
		if pending, err = s.speech.Cache().Create(key); err != nil {
			// Playing uncached beats not playing.
			s.logf("tts cache: %v", err)
		}
	}
	discard := func() {
		if pending != nil {
			pending.Discard()
			pending = nil
		}
	}
	defer discard()

	audioHeaders()
	w.WriteHeader(http.StatusOK)
	// Flushed per chunk so playback starts with the first one rather than
	// when the whole clip has been generated.
	rc := http.NewResponseController(w)
	sent := 0
	buf := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			_ = rc.Flush()
			sent += n
			if pending != nil {
				if _, err := pending.Write(buf[:n]); err != nil {
					s.logf("tts cache: %v", err)
					discard()
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			// The client went away, or ElevenLabs did. Either way the clip
			// is incomplete and must not be kept. Ending the body cleanly
			// would pass the truncated clip off as the whole message, so the
			// connection is cut and the player sees an error instead.
			if r.Context().Err() == nil {
				s.logf("tts stream: %v", readErr)
			}
			panic(http.ErrAbortHandler)
		}
	}
	if pending != nil {
		if err := pending.Commit(); err != nil {
			s.logf("tts cache: %v", err)
		}
		pending = nil
	}
	s.logf("tts cache=miss rewrite=%s text_bytes=%d spoken_bytes=%d audio_bytes=%d duration_ms=%d", rewrite, len(text), len(spoken), sent, time.Since(started).Milliseconds())
}

// speechEditor is the model that rewrites a thread's messages for the voice,
// and how to ask it.
type speechEditor struct {
	model  string
	prompt tts.Prompt
}

// The models that rewrite for the voice are small and fast ones, because the
// listener waits for them before the first word.
const (
	claudeEditor = "claude-haiku-5-5"
	// codexEditorFallback is used when the catalogue names no Luna.
	codexEditorFallback = "gpt-6-luna"
	editorEffort        = "low"
)

// threadEditor rewrites with a model on the account the thread runs under.
// A thread on a harness with no model for it, or none at all, is read as
// it came.
func (s *Server) threadEditor(ctx context.Context, threadID string) (speechEditor, bool) {
	if threadID == "" || s.mgr == nil {
		return speechEditor{}, false
	}
	p, err := s.mgr.PrompterFor(ctx, threadID)
	if err != nil {
		if !errors.Is(err, thread.ErrNoPrompter) {
			s.logf("tts rewrite: %v", err)
		}
		return speechEditor{}, false
	}
	model, ok := editorModel(p.Driver, p.Models)
	if !ok {
		return speechEditor{}, false
	}
	return speechEditor{model: model, prompt: func(ctx context.Context, instructions, input string) (string, error) {
		return p.Prompt(ctx, adapter.PromptRequest{Model: model, Effort: editorEffort, Instructions: instructions, Input: input})
	}}, true
}

// editorModel is Haiku on Claude and the newest Luna on Codex.
func editorModel(driver string, models []adapter.ModelMeta) (string, bool) {
	switch driver {
	case "claude":
		return claudeEditor, true
	case "codex":
		return latestLuna(models), true
	}
	return "", false
}

var versionNumber = regexp.MustCompile(`\d+(?:\.\d+)*`)

// latestLuna is the current Luna with the highest version in its id
// (gpt-6-luna over gpt-5.6-luna), or the fallback when there is none.
func latestLuna(models []adapter.ModelMeta) string {
	best, bestVersion := "", []int(nil)
	for _, m := range models {
		if !strings.Contains(strings.ToLower(m.ID), "luna") || m.Group == adapter.GroupLegacy {
			continue
		}
		v := idVersion(m.ID)
		if best == "" || slices.Compare(v, bestVersion) > 0 {
			best, bestVersion = m.ID, v
		}
	}
	if best == "" {
		return codexEditorFallback
	}
	return best
}

// idVersion reads the first dotted number in a model id: "gpt-5.6-luna" is
// [5 6].
func idVersion(id string) []int {
	var v []int
	for _, part := range strings.Split(versionNumber.FindString(id), ".") {
		if n, err := strconv.Atoi(part); err == nil {
			v = append(v, n)
		}
	}
	return v
}

// speechVoice picks the voice: the thread's project's own, else the user's
// default. A project that has since been deleted defers to the default
// rather than refusing to read its old threads.
func (s *Server) speechVoice(ctx context.Context, threadID string) (string, error) {
	projectID := ""
	if threadID != "" {
		meta, err := s.store.Thread(ctx, threadID)
		if err != nil {
			return "", err
		}
		projectID = meta.ProjectID
	}
	if projectID != "" {
		p, err := s.store.Project(ctx, projectID)
		switch {
		case err == nil && p.Defaults.Voice != "":
			return p.Defaults.Voice, nil
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return "", err
		}
	}
	cfg, err := userconfig.Load()
	if err != nil {
		return "", err
	}
	if cfg.Voice == "" {
		return "", errNoVoice
	}
	return cfg.Voice, nil
}

// speechErrorReply is the status and message for a failure before any audio
// went out. The client cannot read an <audio> element's error body, so it
// asks the same URL again (HEAD, then GET only if that fails) to show the
// message; it has to be one a person can act on.
func speechErrorReply(err error) (int, string) {
	var up *tts.UpstreamError
	switch {
	case errors.Is(err, tts.ErrNoKey):
		return http.StatusConflict, noSpeechKey
	case errors.As(err, &up) && up.Rejected():
		return http.StatusBadGateway, "ElevenLabs rejected the API key: " + up.Message
	case errors.As(err, &up) && up.Status == http.StatusTooManyRequests:
		return http.StatusTooManyRequests, up.Message
	case errors.As(err, &up):
		return http.StatusBadGateway, up.Message
	default:
		return http.StatusBadGateway, err.Error()
	}
}

// speechFailureTTL is how long a refusal is answered from memory. The client
// asks again within a second of the <audio> element failing, to read the
// message; a person pressing play again a minute later gets a fresh try.
const speechFailureTTL = time.Minute

// speechFailures is ElevenLabs' last refusal per cache key. The <audio>
// element cannot read an error body, so the client asks for a failed clip a
// second time to get the message. Without this that request goes upstream
// again, and if ElevenLabs has recovered in between it synthesises, and bills,
// audio nobody will play. Only refusals are kept: a client hanging up or the
// network dropping says nothing about the next try.
type speechFailures struct {
	mu    sync.Mutex
	byKey map[string]speechFailure
	// now is the clock; nil is the real one.
	now func() time.Time
}

type speechFailure struct {
	status int
	msg    string
	at     time.Time
}

func (f *speechFailures) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *speechFailures) remember(key string, status int, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.clock()
	if f.byKey == nil {
		f.byKey = map[string]speechFailure{}
	}
	// Pruned on the way in, so the map only ever holds the last minute's.
	for k, v := range f.byKey {
		if now.Sub(v.at) >= speechFailureTTL {
			delete(f.byKey, k)
		}
	}
	f.byKey[key] = speechFailure{status: status, msg: msg, at: now}
}

func (f *speechFailures) recall(key string) (int, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.byKey[key]
	if !ok || f.clock().Sub(v.at) >= speechFailureTTL {
		return 0, "", false
	}
	return v.status, v.msg, true
}

// forget drops every remembered refusal: a new key can succeed where the old
// one was refused or out of credit.
func (f *speechFailures) forget() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byKey = nil
}
