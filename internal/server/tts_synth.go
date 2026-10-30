package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asiraky/omniplex/internal/tts"
)

const (
	// synthesisTimeout bounds one synthesis, rewrite included. The longest
	// message ElevenLabs takes is a few minutes of audio, made faster than
	// it plays.
	synthesisTimeout = 15 * time.Minute
	// uncachedTTL is how long a clip that is not cached, because the model
	// only partly rewrote it, stays readable for the player's later range
	// requests. A play after that gets a fresh try at the rewrite.
	uncachedTTL = 15 * time.Minute
	// partwayMessage is what a player is told when a clip broke after it
	// had started.
	partwayMessage = "Reading stopped partway: the audio from ElevenLabs broke off. Try again."
)

// synthesis is one clip being made. Every request for its key while it runs
// reads the same growing file, so a clip is paid for once however the player
// asks for it: a first request, a probe, a range, a resume after the
// connection dropped.
//
// It runs on its own clock, not the request's: a player that hangs up and
// asks again a moment later finds it still going, rather than paying for a
// second one that starts again from nothing.
type synthesis struct {
	// ready is closed once audio has started, or failed to.
	ready chan struct{}
	// pending is the audio; nil when it failed before any came.
	pending *tts.Pending
	status  int
	msg     string
	// expires is when a finished clip that is not cached stops being
	// served; zero for one still being made or on its way to the cache.
	expires time.Time
}

func (sy *synthesis) refuse(status int, msg string) {
	sy.status, sy.msg = status, msg
	close(sy.ready)
}

// syntheses are the clips being made, by cache key.
type syntheses struct {
	mu    sync.Mutex
	byKey map[string]*synthesis
	wg    sync.WaitGroup
	// ctx is every synthesis' parent; stop cancels it.
	ctx  context.Context
	stop context.CancelFunc
	// now is the clock; nil is the real one.
	now func() time.Time
}

func (ss *syntheses) clock() time.Time {
	if ss.now != nil {
		return ss.now()
	}
	return time.Now()
}

func (ss *syntheses) get(key string) *synthesis {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.live(key)
}

// live is the synthesis under key, dropping one that has expired. Callers
// hold mu.
func (ss *syntheses) live(key string) *synthesis {
	sy := ss.byKey[key]
	if sy != nil && !sy.expires.IsZero() && !ss.clock().Before(sy.expires) {
		delete(ss.byKey, key)
		sy.pending.Discard()
		return nil
	}
	return sy
}

// keepUncached keeps sy, finished but not cached, for uncachedTTL.
func (ss *syntheses) keepUncached(key string, sy *synthesis) {
	ss.mu.Lock()
	sy.expires = ss.clock().Add(uncachedTTL)
	ss.mu.Unlock()
	time.AfterFunc(uncachedTTL, func() {
		ss.drop(key, sy)
		sy.pending.Discard()
	})
}

// start returns the synthesis for key, starting it with run when there is
// none. fresh says whether this call started it.
func (ss *syntheses) start(key string, run func(ctx context.Context, sy *synthesis)) (sy *synthesis, fresh bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if sy := ss.live(key); sy != nil {
		return sy, false
	}
	if ss.byKey == nil {
		ss.byKey = map[string]*synthesis{}
		ss.ctx, ss.stop = context.WithCancel(context.Background())
	}
	sy = &synthesis{ready: make(chan struct{})}
	ss.byKey[key] = sy
	ctx, cancel := context.WithTimeout(ss.ctx, synthesisTimeout)
	ss.wg.Add(1)
	go func() {
		defer ss.wg.Done()
		defer cancel()
		run(ctx, sy)
	}()
	return sy, true
}

// drop forgets sy, if it is still the one under key.
func (ss *syntheses) drop(key string, sy *synthesis) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.byKey[key] == sy {
		delete(ss.byKey, key)
	}
}

// close cancels every synthesis and waits for them to wind up.
func (ss *syntheses) close() {
	ss.mu.Lock()
	if ss.stop != nil {
		ss.stop()
	}
	ss.mu.Unlock()
	ss.wg.Wait()
}

// speechJob is what to synthesise.
type speechJob struct {
	key, voice, text string
	editing          bool
	editor           speechEditor
}

// synthesise makes one clip into sy, and keeps it once it is whole.
func (s *Server) synthesise(ctx context.Context, sy *synthesis, job speechJob) {
	started := time.Now()
	spoken, whole, rewrite := job.text, true, "off"
	if job.editing {
		spoken, whole = s.rewriter.Rewrite(ctx, job.editor.model, job.editor.prompt, job.text)
		rewrite = "whole"
		if !whole {
			rewrite = "partial"
		}
	}
	// Each failure forgets the synthesis before its readers hear of it, so a
	// request after the failure is answered from speechFailures or tried
	// afresh, never joined to the dead one.
	refuse := func(status int, msg string) {
		s.syntheses.drop(job.key, sy)
		sy.refuse(status, msg)
	}
	body, err := s.speech.Stream(ctx, job.voice, spoken)
	if err != nil {
		status, msg := speechErrorReply(err)
		var refused *tts.UpstreamError
		if errors.As(err, &refused) {
			s.speechFailures.remember(job.key, status, msg)
		}
		refuse(status, msg)
		return
	}
	defer body.Close()
	pending, err := s.speech.Cache().Create(job.key)
	if err != nil {
		s.logf("tts cache: %v", err)
		refuse(http.StatusInternalServerError, "Couldn't store the audio: "+err.Error())
		return
	}
	sy.pending = pending
	close(sy.ready)

	// A media element that loses its stream asks again by itself, with a
	// range from where it got to. Answering that with a new synthesis would
	// bill the clip twice, so for a moment the failure is the answer.
	fail := func(err error) {
		s.speechFailures.rememberFor(job.key, http.StatusBadGateway, partwayMessage, partwayFailureTTL)
		s.syntheses.drop(job.key, sy)
		pending.Fail(err)
	}
	var sent int64
	buf := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			if _, err := pending.Write(buf[:n]); err != nil {
				s.logf("tts cache: %v", err)
				fail(err)
				return
			}
			sent += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			s.logf("tts stream: clip=%s %v", clipID(job.key), readErr)
			fail(readErr)
			return
		}
	}
	logMiss := func() {
		s.logf("tts cache=miss clip=%s rewrite=%s text_bytes=%d spoken_bytes=%d audio_bytes=%d duration_ms=%d",
			clipID(job.key), rewrite, len(job.text), len(spoken), sent, time.Since(started).Milliseconds())
	}
	// Audio from a text the model only partly rewrote is played but not
	// kept, so the next play gets another try at the rewrite.
	if whole {
		err := pending.Commit()
		if err == nil {
			// Committed before dropped: a request that misses the synthesis
			// finds the cache entry.
			s.syntheses.drop(job.key, sy)
			logMiss()
			return
		}
		s.logf("tts cache: %v", err)
	} else {
		pending.Finish()
	}
	logMiss()
	s.syntheses.keepUncached(job.key, sy)
}

// serveSynthesis answers one request from sy's growing file. A request for
// the whole clip streams it as it comes. A closed range is answered with
// 206 once its bytes are there. Anything else, an open range past the start
// included, waits for the clip to finish so it can be answered exactly: its
// end is not known before then.
func (s *Server) serveSynthesis(w http.ResponseWriter, r *http.Request, sy *synthesis, audioHeaders func()) {
	select {
	case <-sy.ready:
	case <-r.Context().Done():
		return
	}
	if sy.pending == nil {
		writeError(w, sy.status, sy.msg)
		return
	}
	p := sy.pending
	f, err := p.Open()
	if err != nil {
		writeError(w, http.StatusBadGateway, partwayMessage)
		return
	}
	defer f.Close()
	audioHeaders()
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()
	first, last, kind := parseByteRange(r.Header.Get("Range"))
	// How much has to be there before the answer can start.
	var need int64
	switch kind {
	case rangeNone, rangeFromStart:
		// The first byte, so a clip that fails before any comes is
		// answered with an error status rather than a cut 200.
		need = 1
	case rangeClosed:
		need = last + 1
	default:
		need = math.MaxInt64
	}
	_, done, err := p.Wait(ctx, need)
	if err != nil {
		if ctx.Err() == nil {
			writeError(w, http.StatusBadGateway, partwayMessage)
		}
		return
	}
	if done {
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}
	if kind == rangeClosed {
		h := w.Header()
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/*", first, last))
		h.Set("Content-Length", strconv.FormatInt(last-first+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.Copy(w, io.NewSectionReader(f, first, last-first+1))
		return
	}
	s.streamGrowing(w, ctx, p, f)
}

// streamGrowing sends the whole clip with 200, as it is written. A clip that
// fails partway cuts the connection: ending the body cleanly would pass the
// truncated clip off as the whole message.
func (s *Server) streamGrowing(w http.ResponseWriter, ctx context.Context, p *tts.Pending, f io.ReaderAt) {
	w.WriteHeader(http.StatusOK)
	// Flushed per chunk so playback starts with the first one rather than
	// when the whole clip has been generated.
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	var off int64
	for {
		size, done, err := p.Wait(ctx, off+1)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			panic(http.ErrAbortHandler)
		}
		for off < size {
			n, readErr := f.ReadAt(buf[:min(int64(len(buf)), size-off)], off)
			if n > 0 {
				if _, err := w.Write(buf[:n]); err != nil {
					return
				}
				off += int64(n)
			}
			if readErr != nil && readErr != io.EOF {
				panic(http.ErrAbortHandler)
			}
		}
		_ = rc.Flush()
		if done && off >= size {
			return
		}
	}
}

type rangeKind int

const (
	rangeNone      rangeKind = iota
	rangeFromStart           // bytes=0-
	rangeClosed              // bytes=a-b
	rangeOther               // anything else: an open range past 0, a suffix, several
)

// parseByteRange sorts a Range header into what serveSynthesis can answer
// before the clip is finished. first and last are set for rangeClosed.
func parseByteRange(h string) (first, last int64, kind rangeKind) {
	if h == "" {
		return 0, 0, rangeNone
	}
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, 0, rangeOther
	}
	a, b, ok := strings.Cut(strings.TrimSpace(spec), "-")
	if !ok || a == "" {
		return 0, 0, rangeOther
	}
	first, err := strconv.ParseInt(a, 10, 64)
	if err != nil || first < 0 {
		return 0, 0, rangeOther
	}
	if b == "" {
		if first == 0 {
			return 0, 0, rangeFromStart
		}
		return 0, 0, rangeOther
	}
	last, err = strconv.ParseInt(b, 10, 64)
	if err != nil || last < first {
		return 0, 0, rangeOther
	}
	return first, last, rangeClosed
}

// ttsRecorder records what one /api/tts request was answered with.
type ttsRecorder struct {
	http.ResponseWriter
	status int
	sent   int64
}

func (l *ttsRecorder) WriteHeader(code int) {
	if l.status == 0 {
		l.status = code
	}
	l.ResponseWriter.WriteHeader(code)
}

func (l *ttsRecorder) Write(b []byte) (int, error) {
	if l.status == 0 {
		l.status = http.StatusOK
	}
	n, err := l.ResponseWriter.Write(b)
	l.sent += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the flusher underneath.
func (l *ttsRecorder) Unwrap() http.ResponseWriter { return l.ResponseWriter }

// userAgentSummary names the client in a word or two: enough to tell the
// phone's media loader from a desktop browser in the log.
func userAgentSummary(ua string) string {
	ios := strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad")
	switch {
	case ua == "":
		return "none"
	case strings.Contains(ua, "AppleCoreMedia"):
		if ios {
			return "ios-coremedia"
		}
		return "coremedia"
	case ios && strings.Contains(ua, "CriOS"):
		return "ios-chrome"
	case ios:
		return "ios-safari"
	case strings.Contains(ua, "Edg/"):
		return "edge"
	case strings.Contains(ua, "Android") && strings.Contains(ua, "Chrome/"):
		return "android-chrome"
	case strings.Contains(ua, "Chrome/"):
		return "chrome"
	case strings.Contains(ua, "Firefox/"):
		return "firefox"
	case strings.Contains(ua, "Safari/"):
		return "safari"
	}
	return "other"
}
