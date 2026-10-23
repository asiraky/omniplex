package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/store"
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
		writeSpeechError(w, err)
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
	voice, err := s.speechVoice(r.Context(), q)
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

	key := tts.CacheKey(voice, text)
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
	if r.Method == http.MethodHead {
		// Synthesis is billed; a HEAD is not worth paying for.
		audioHeaders()
		w.WriteHeader(http.StatusOK)
		return
	}

	// A miss ignores Range and sends the whole clip: there is nothing to seek
	// in until it exists, and a media element takes a 200 for a range request.
	body, err := s.speech.Stream(r.Context(), voice, text)
	if err != nil {
		writeSpeechError(w, err)
		return
	}
	defer body.Close()

	pending, err := s.speech.Cache().Create(key)
	if err != nil {
		// Playing uncached beats not playing.
		s.logf("tts cache: %v", err)
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
			// is incomplete and must not be kept.
			return
		}
	}
	if pending != nil {
		if err := pending.Commit(); err != nil {
			s.logf("tts cache: %v", err)
		}
		pending = nil
	}
	s.logf("tts cache=miss text_bytes=%d audio_bytes=%d duration_ms=%d", len(text), sent, time.Since(started).Milliseconds())
}

// speechVoice picks the voice: the thread's project's own, else the user's
// default. A project that has since been deleted defers to the default
// rather than refusing to read its old threads.
func (s *Server) speechVoice(ctx context.Context, q url.Values) (string, error) {
	projectID := q.Get("project")
	if id := q.Get("thread"); id != "" {
		meta, err := s.store.Thread(ctx, id)
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

// writeSpeechError reports a failure before any audio went out. The client
// cannot read an <audio> element's error body, so it fetches the same URL to
// show the message; it has to be one a person can act on.
func writeSpeechError(w http.ResponseWriter, err error) {
	var up *tts.UpstreamError
	switch {
	case errors.Is(err, tts.ErrNoKey):
		writeError(w, http.StatusConflict, noSpeechKey)
	case errors.As(err, &up) && up.Rejected():
		writeError(w, http.StatusBadGateway, "ElevenLabs rejected the API key: "+up.Message)
	case errors.As(err, &up) && up.Status == http.StatusTooManyRequests:
		writeError(w, http.StatusTooManyRequests, up.Message)
	case errors.As(err, &up):
		writeError(w, http.StatusBadGateway, up.Message)
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}
