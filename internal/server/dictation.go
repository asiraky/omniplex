package server

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/tts"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// Dictation turns a recording made in the composer into text for the
// composer. The browser records, this server sends the audio to ElevenLabs'
// Scribe on the key read-aloud already uses, and the text comes back for the
// person to read over before they send it.

//go:embed dictation_cleanup.txt
var dictationCleanupInstructions string

const (
	// maxDictationBytes caps an upload. Opus at about 3 KB a second is under
	// 2 MB for ten minutes of talking, so this is far past any real message.
	maxDictationBytes = 10 << 20
	// dictationTimeout gives up on Scribe. It transcribes far faster than
	// real time; one that has not answered in this long has failed.
	dictationTimeout = 60 * time.Second
	// cleanupTimeout gives up on the tidying model, and the raw transcript
	// is used. The person is waiting to see their words appear.
	cleanupTimeout = 20 * time.Second
)

func (s *Server) routeDictation(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/dictation", s.handleDictation)
}

// handleDictation answers POST /api/dictation?thread=… with {"text": …}. The
// body is the recording itself, typed with whatever the browser recorded in;
// thread is absent on the new-thread screen, where there is no account to
// tidy the transcript with.
//
// One line is logged per request, never with the transcript in it.
func (s *Server) handleDictation(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	lw := &statusRecorder{ResponseWriter: w}
	w = lw
	var size int
	cleaned := false
	defer func() {
		s.logf("dictation status=%d bytes=%d mime=%q cleaned=%t ua=%s duration_ms=%d",
			lw.status, size, r.Header.Get("Content-Type"), cleaned,
			userAgentSummary(r.UserAgent()), time.Since(started).Milliseconds())
	}()
	if s.speech == nil {
		writeError(w, http.StatusNotImplemented, "this server does not have dictation")
		return
	}
	if !s.speech.HasKey() {
		writeError(w, http.StatusConflict, noSpeechKey)
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDictationBytes))
	size = len(audio)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "the recording is too long")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(audio) == 0 {
		writeError(w, http.StatusBadRequest, "the recording is empty")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), dictationTimeout)
	defer cancel()
	text, err := s.speech.Transcribe(ctx, bytes.NewReader(audio), r.Header.Get("Content-Type"))
	if err != nil {
		status, msg := dictationErrorReply(err)
		writeError(w, status, msg)
		s.logf("dictation: %v", err)
		return
	}
	if text != "" {
		text, cleaned = s.cleanDictation(r.Context(), r.URL.Query().Get("thread"), text)
	}
	writeJSON(w, map[string]string{"text": text})
}

// dictationErrorReply is speechErrorReply, except that a failure to get an
// answer at all says so in words: the person sees it on a phone, with a
// Retry beside it, and a Go dial error tells them nothing.
func dictationErrorReply(err error) (int, string) {
	var up *tts.UpstreamError
	switch {
	case errors.Is(err, tts.ErrNoKey), errors.As(err, &up):
		return speechErrorReply(err)
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "ElevenLabs took too long to transcribe that"
	}
	return http.StatusBadGateway, "Couldn't reach ElevenLabs to transcribe that"
}

// cleanDictation tidies a transcript with the thread's small model when the
// user has asked for that, and reports whether it did. Anything going wrong
// gives the transcript back as Scribe wrote it: an untidy message is still
// the person's message, and better than none.
func (s *Server) cleanDictation(ctx context.Context, threadID, text string) (string, bool) {
	if threadID == "" {
		return text, false
	}
	cfg, err := userconfig.Load()
	if err != nil {
		s.logf("dictation cleanup: %v", err)
		return text, false
	}
	if !cfg.DictationCleanup {
		return text, false
	}
	editor, ok := s.editorFor(ctx, threadID)
	if !ok || s.rewriter.Down(editor.model) {
		return text, false
	}
	ctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	got, err := editor.prompt(ctx, dictationCleanupInstructions, "<transcript>\n"+text+"\n</transcript>")
	if err != nil {
		s.logf("dictation cleanup: %v", err)
		return text, false
	}
	got = strings.TrimSpace(got)
	got = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(got, "<transcript>"), "</transcript>"))
	if got == "" {
		s.logf("dictation cleanup: the model answered with nothing")
		return text, false
	}
	return got, true
}
