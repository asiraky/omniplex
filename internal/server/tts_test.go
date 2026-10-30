package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
	"github.com/asiraky/omniplex/internal/tts"
)

// speechRig is a server with read-aloud pointed at a fake ElevenLabs. Its
// HTTP side is reached over loopback, which the guard trusts, so requests
// need no pairing.
type speechRig struct {
	t        *testing.T
	ts       *httptest.Server
	c        *conn
	st       *store.Store
	srv      *Server
	cacheDir string
}

func newSpeechRig(t *testing.T, upstream http.Handler) *speechRig {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	t.Setenv("OMNIPLEX_ELEVENLABS_URL", up.URL)
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(cfgDir, "config.json"))

	st, err := store.Open(filepath.Join(t.TempDir(), "tts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := provider.OpenSecretStoreAt(filepath.Join(t.TempDir(), "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	speech, err := tts.New(secrets)
	if err != nil {
		t.Fatal(err)
	}
	mgr := thread.NewManager(st, func(string, ...any) {})
	t.Cleanup(mgr.Shutdown)
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, false, auth.DefaultPort), Speech: speech})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// Last registered, so it runs first: a synthesis outliving the test
	// would write into directories already being removed.
	t.Cleanup(srv.syntheses.close)
	return &speechRig{t: t, ts: ts, c: &conn{srv: srv}, st: st, srv: srv, cacheDir: filepath.Join(cfgDir, "tts-cache")}
}

func (r *speechRig) setKey(key string) {
	r.t.Helper()
	if _, err := run(r.t, r.c, "set_elevenlabs_key", map[string]any{"key": key}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *speechRig) setVoice(voice string) {
	r.t.Helper()
	if _, err := run(r.t, r.c, "save_user_config", map[string]any{"config": map[string]any{"voice": voice}}); err != nil {
		r.t.Fatal(err)
	}
}

// threadIn makes a project with the given voice and a thread in it.
func (r *speechRig) threadIn(voice string) string {
	r.t.Helper()
	ctx := context.Background()
	now := proto.NowMillis()
	id := "p" + strings.ReplaceAll(r.t.Name(), "/", "-") + voice
	if err := r.st.CreateProject(ctx, project.Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
		r.t.Fatal(err)
	}
	if voice != "" {
		if _, err := run(r.t, r.c, "save_project", map[string]any{"projectId": id, "defaults": map[string]any{"voice": voice}}); err != nil {
			r.t.Fatal(err)
		}
	}
	threadID := "t-" + id
	if err := r.st.CreateThread(ctx, store.ThreadMeta{ID: threadID, Cwd: r.t.TempDir(), Harness: "claudecode", ProjectID: id, Phase: "idle", CreatedAt: now, UpdatedAt: now}); err != nil {
		r.t.Fatal(err)
	}
	return threadID
}

func (r *speechRig) get(path string, header map[string]string) *http.Response {
	r.t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.ts.URL+path, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { res.Body.Close() })
	return res
}

func speakPath(text, threadID string) string {
	q := url.Values{"text": {text}}
	if threadID != "" {
		q.Set("thread", threadID)
	}
	return "/api/tts?" + q.Encode()
}

// cacheFiles is everything in the cache directory, finished or not.
func (r *speechRig) cacheFiles() []string {
	r.t.Helper()
	entries, err := os.ReadDir(r.cacheDir)
	if err != nil {
		r.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func errorMessage(t *testing.T, res *http.Response) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Error
}

// fakeEleven answers the key check and voice list, and hands synthesis to
// speak with the voice id it was asked for.
type fakeEleven struct {
	mu     sync.Mutex
	voices []string
	speak  func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeEleven) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("xi-api-key") == "bad" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v2/voices") {
		_, _ = io.WriteString(w, `{"voices":[{"voice_id":"v2","name":"zed"},{"voice_id":"v1","name":"Alice"}],"has_more":false}`)
		return
	}
	voice := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/text-to-speech/"), "/stream")
	f.mu.Lock()
	f.voices = append(f.voices, voice)
	f.mu.Unlock()
	if f.speak != nil {
		f.speak(w, r)
		return
	}
	_, _ = io.WriteString(w, "mp3:"+voice)
}

func (f *fakeEleven) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.voices...)
}

func TestSpeechWithoutKeyAsksForOne(t *testing.T) {
	r := newSpeechRig(t, &fakeEleven{})
	r.setVoice("v1")
	for _, path := range []string{"/api/tts/voices", speakPath("hello", "")} {
		res := r.get(path, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("GET %s without a key returned %d, want 409", path, res.StatusCode)
		}
		if msg := errorMessage(t, res); msg != noSpeechKey {
			t.Fatalf("GET %s said %q", path, msg)
		}
	}
}

func TestElevenLabsKeyIsSetClearedAndRejected(t *testing.T) {
	r := newSpeechRig(t, &fakeEleven{})
	type keyed struct {
		HasElevenLabsKey bool `json:"hasElevenLabsKey"`
	}
	has := func() bool { return call[keyed](t, r.c, "get_user_config", map[string]any{}).HasElevenLabsKey }

	if has() {
		t.Fatal("a fresh server reports a key")
	}
	if got := call[keyed](t, r.c, "set_elevenlabs_key", map[string]any{"key": " good "}); !got.HasElevenLabsKey {
		t.Fatal("an accepted key was not reported stored")
	}
	if !has() {
		t.Fatal("get_user_config does not see the stored key")
	}

	_, err := run(t, r.c, "set_elevenlabs_key", map[string]any{"key": "bad"})
	if !errors.Is(err, tts.ErrKeyRejected) {
		t.Fatalf("a refused key returned %v", err)
	}
	if !has() {
		t.Fatal("a refused key replaced the good one")
	}

	if got := call[keyed](t, r.c, "set_elevenlabs_key", map[string]any{"key": ""}); got.HasElevenLabsKey {
		t.Fatal("clearing the key still reports one")
	}
	if has() {
		t.Fatal("the cleared key is still stored")
	}
}

func TestVoicesAreSortedByName(t *testing.T) {
	r := newSpeechRig(t, &fakeEleven{})
	r.setKey("good")
	res := r.get("/api/tts/voices", nil)
	var body struct {
		Voices []tts.Voice `json:"voices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 2 || body.Voices[0].Name != "Alice" || body.Voices[1].Name != "zed" {
		t.Fatalf("voices = %+v", body.Voices)
	}
}

func TestSpeechVoicePrecedence(t *testing.T) {
	up := &fakeEleven{}
	r := newSpeechRig(t, up)
	r.setKey("good")
	own := r.threadIn("project-voice")
	plain := r.threadIn("")

	res := r.get(speakPath("one", plain), nil)
	if res.StatusCode != http.StatusConflict || errorMessage(t, res) != noSpeechVoice {
		t.Fatalf("with no voice anywhere: %d", res.StatusCode)
	}

	r.setVoice("global-voice")
	if body := readBody(t, r.get(speakPath("two", plain), nil)); body != "mp3:global-voice" {
		t.Fatalf("a project without a voice read in %q", body)
	}
	if body := readBody(t, r.get(speakPath("three", own), nil)); body != "mp3:project-voice" {
		t.Fatalf("a project with its own voice read in %q", body)
	}
	if body := readBody(t, r.get(speakPath("four", ""), nil)); body != "mp3:global-voice" {
		t.Fatalf("no thread read in %q", body)
	}
	if res := r.get(speakPath("five", "ghost"), nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown thread returned %d", res.StatusCode)
	}
}

func TestSpeechIsCachedAndRevalidated(t *testing.T) {
	up := &fakeEleven{}
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")

	first := r.get(speakPath("  hello  ", ""), nil)
	if first.StatusCode != http.StatusOK || first.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("miss: %d %s", first.StatusCode, first.Header.Get("Content-Type"))
	}
	if body := readBody(t, first); body != "mp3:v1" {
		t.Fatalf("miss body %q", body)
	}
	etag := first.Header.Get("ETag")

	// Trimmed text is the same text, so this is a hit.
	second := r.get(speakPath("hello", ""), nil)
	if body := readBody(t, second); body != "mp3:v1" || second.Header.Get("ETag") != etag {
		t.Fatalf("hit: %q etag %q, want %q", body, second.Header.Get("ETag"), etag)
	}
	if got := up.requested(); len(got) != 1 {
		t.Fatalf("upstream asked %d times; the replay should have come from disk", len(got))
	}

	if res := r.get(speakPath("hello", ""), map[string]string{"If-None-Match": etag}); res.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation returned %d, want 304", res.StatusCode)
	}
	if res := r.get(speakPath("hello", ""), map[string]string{"Range": "bytes=4-"}); res.StatusCode != http.StatusPartialContent || readBody(t, res) != "v1" {
		t.Fatalf("a range on a hit returned %d", res.StatusCode)
	}

	// A changed voice is different audio behind the same URL.
	r.setVoice("v2")
	third := r.get(speakPath("hello", ""), map[string]string{"If-None-Match": etag})
	if third.StatusCode != http.StatusOK || readBody(t, third) != "mp3:v2" {
		t.Fatalf("after a voice change: %d", third.StatusCode)
	}
}

// Playback has to start on the first chunk, through the compression layer,
// rather than when ElevenLabs has finished the whole message.
func TestSpeechStreamsBeforeUpstreamFinishes(t *testing.T) {
	release := make(chan struct{})
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "-rest")
	}}
	r := newSpeechRig(t, up)
	// Registered after the rig, so it runs first: a failing test must not
	// leave the upstream handler blocking the fake server's Close.
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	r.setKey("good")
	r.setVoice("v1")

	// The request runs aside: without a flush even the headers would not
	// arrive, and the test would hang rather than fail.
	type first struct {
		res   *http.Response
		chunk string
		err   error
	}
	got := make(chan first, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, r.ts.URL+speakPath("hello", ""), nil)
		req.Header.Set("Accept-Encoding", "gzip")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- first{err: err}
			return
		}
		buf := make([]byte, 5)
		_, err = io.ReadFull(res.Body, buf)
		got <- first{res: res, chunk: string(buf), err: err}
	}()
	var res *http.Response
	select {
	case f := <-got:
		if f.err != nil || f.chunk != "first" {
			t.Fatalf("first chunk %q, %v", f.chunk, f.err)
		}
		res = f.res
		t.Cleanup(func() { res.Body.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("the first chunk did not arrive until upstream finished")
	}
	if enc := res.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("audio was sent with Content-Encoding %q", enc)
	}
	if names := r.cacheFiles(); len(names) != 1 || !strings.HasPrefix(names[0], ".pending-") {
		t.Fatalf("mid-stream the cache holds %v; only a pending file should exist", names)
	}
	finish()
	if rest := readBody(t, res); rest != "-rest" {
		t.Fatalf("rest %q", rest)
	}
	if names := r.cacheFiles(); len(names) != 1 || !strings.HasSuffix(names[0], ".mp3") {
		t.Fatalf("after the stream the cache holds %v", names)
	}
}

func TestBrokenStreamLeavesNoCacheEntry(t *testing.T) {
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "half")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}}
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")

	// Depending on when it gets there, the request sees the failure as an
	// error status or as a cut connection; either way, not as a whole clip.
	res := r.get(speakPath("hello", ""), nil)
	if _, err := io.ReadAll(res.Body); err == nil && res.StatusCode == http.StatusOK {
		t.Fatal("a stream that broke ended cleanly; the player would take half a clip for the whole")
	}
	if names := r.cacheFiles(); len(names) != 0 {
		t.Fatalf("a stream that broke left %v in the cache", names)
	}
	_ = r.get(speakPath("hello", ""), nil)
	if got := up.requested(); len(got) != 2 {
		t.Fatalf("upstream asked %d times; a broken clip must not be replayed", len(got))
	}
}

// A player that hangs up and asks again finds the clip still being made or
// already cached, rather than paying for it a second time.
func TestHangingUpLeavesTheSynthesisRunning(t *testing.T) {
	release := make(chan struct{})
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "half")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "-rest")
	}}
	r := newSpeechRig(t, up)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	r.setKey("good")
	r.setVoice("v1")

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.ts.URL+speakPath("hello", ""), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(res.Body, buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	res.Body.Close()
	finish()

	deadline := time.Now().Add(5 * time.Second)
	for names := r.cacheFiles(); len(names) != 1 || !strings.HasSuffix(names[0], ".mp3"); names = r.cacheFiles() {
		if time.Now().After(deadline) {
			t.Fatalf("the synthesis was not finished and cached after the hang-up: %v", names)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if body := readBody(t, r.get(speakPath("hello", ""), nil)); body != "half-rest" {
		t.Fatalf("replay %q", body)
	}
	if got := up.requested(); len(got) != 1 {
		t.Fatalf("ElevenLabs was asked %d times, want 1", len(got))
	}
}

// inFlight starts a request aside and hands back its response once the
// headers are in, or its error.
func (r *speechRig) inFlight(method, path string, header map[string]string) <-chan *http.Response {
	r.t.Helper()
	got := make(chan *http.Response, 1)
	req, err := http.NewRequest(method, r.ts.URL+path, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	go func() {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			close(got)
			return
		}
		got <- res
	}()
	return got
}

func await(t *testing.T, got <-chan *http.Response) *http.Response {
	t.Helper()
	select {
	case res, ok := <-got:
		if !ok {
			t.Fatal("the request failed")
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
	return nil
}

// Every request for a clip still being made reads the one synthesis: the
// first, a probe, and a resume past what has been written.
func TestOneSynthesisServesEveryRequest(t *testing.T) {
	release := make(chan struct{})
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "aaaa")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "bbbb")
	}}
	r := newSpeechRig(t, up)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	r.setKey("good")
	r.setVoice("v1")
	path := speakPath("hello", "")

	first := await(t, r.inFlight(http.MethodGet, path, nil))
	if first.StatusCode != http.StatusOK || first.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("first: %d, Accept-Ranges %q", first.StatusCode, first.Header.Get("Accept-Ranges"))
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(first.Body, buf); err != nil || string(buf) != "aaaa" {
		t.Fatalf("first chunk %q, %v", buf, err)
	}

	probe := r.get(path, map[string]string{"Range": "bytes=0-1"})
	if probe.StatusCode != http.StatusPartialContent || probe.Header.Get("Content-Range") != "bytes 0-1/*" {
		t.Fatalf("probe: %d, Content-Range %q", probe.StatusCode, probe.Header.Get("Content-Range"))
	}
	if body := readBody(t, probe); body != "aa" {
		t.Fatalf("probe body %q", body)
	}

	head, err := http.Head(r.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: %d", head.StatusCode)
	}

	// Its end is not known until the clip is finished, so it waits.
	resumed := r.inFlight(http.MethodGet, path, map[string]string{"Range": "bytes=2-"})
	select {
	case <-resumed:
		t.Fatal("an open range was answered before the clip was finished")
	case <-time.After(50 * time.Millisecond):
	}
	finish()
	resume := await(t, resumed)
	if resume.StatusCode != http.StatusPartialContent || resume.Header.Get("Content-Range") != "bytes 2-7/8" {
		t.Fatalf("resume: %d, Content-Range %q", resume.StatusCode, resume.Header.Get("Content-Range"))
	}
	if body := readBody(t, resume); body != "aabbbb" {
		t.Fatalf("resume body %q", body)
	}
	if rest := readBody(t, first); rest != "bbbb" {
		t.Fatalf("first rest %q", rest)
	}
	if got := up.requested(); len(got) != 1 {
		t.Fatalf("ElevenLabs was asked %d times, want 1", len(got))
	}
}

// A synthesis that fails partway fails every request reading it, and none of
// them starts it again.
func TestSynthesisFailingPartwayFailsEveryReader(t *testing.T) {
	release := make(chan struct{})
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "half")
		w.(http.Flusher).Flush()
		<-release
		panic(http.ErrAbortHandler)
	}}
	r := newSpeechRig(t, up)
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	r.setKey("good")
	r.setVoice("v1")
	path := speakPath("hello", "")

	first := await(t, r.inFlight(http.MethodGet, path, nil))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(first.Body, buf); err != nil {
		t.Fatal(err)
	}
	resumed := r.inFlight(http.MethodGet, path, map[string]string{"Range": "bytes=2-"})
	select {
	case <-resumed:
		t.Fatal("an open range was answered before the clip was finished")
	case <-time.After(50 * time.Millisecond):
	}
	finish()

	if _, err := io.ReadAll(first.Body); err == nil {
		t.Fatal("the first request ended cleanly; the player would take half a clip for the whole")
	}
	if resume := await(t, resumed); resume.StatusCode != http.StatusBadGateway {
		t.Fatalf("the waiting resume got %d, want 502", resume.StatusCode)
	}
	if got := up.requested(); len(got) != 1 {
		t.Fatalf("ElevenLabs was asked %d times, want 1", len(got))
	}
	if names := r.cacheFiles(); len(names) != 0 {
		t.Fatalf("a failed synthesis left %v in the cache", names)
	}
}

func TestParseByteRange(t *testing.T) {
	cases := []struct {
		header      string
		first, last int64
		kind        rangeKind
	}{
		{"", 0, 0, rangeNone},
		{"bytes=0-", 0, 0, rangeFromStart},
		{"bytes=0-1", 0, 1, rangeClosed},
		{"bytes=100-199", 100, 199, rangeClosed},
		{"bytes=819200-", 0, 0, rangeOther},
		{"bytes=-500", 0, 0, rangeOther},
		{"bytes=0-1,4-5", 0, 0, rangeOther},
		{"bytes=5-4", 0, 0, rangeOther},
		{"items=0-1", 0, 0, rangeOther},
		{"bytes=x-1", 0, 0, rangeOther},
	}
	for _, c := range cases {
		first, last, kind := parseByteRange(c.header)
		if first != c.first || last != c.last || kind != c.kind {
			t.Errorf("parseByteRange(%q) = %d, %d, %d; want %d, %d, %d", c.header, first, last, kind, c.first, c.last, c.kind)
		}
	}
}

func TestUserAgentSummary(t *testing.T) {
	cases := map[string]string{
		"": "none",
		"AppleCoreMedia/1.0.0.22A3354 (iPhone; U; CPU OS 18_0 like Mac OS X; en_au)":                                                              "ios-coremedia",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1": "ios-safari",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1":  "ios-chrome",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36":                                "android-chrome",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36":                                       "chrome",
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0":                               "edge",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15":                      "safari",
		"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0":                                                                  "firefox",
		"curl/8.5.0": "other",
	}
	for ua, want := range cases {
		if got := userAgentSummary(ua); got != want {
			t.Errorf("userAgentSummary(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestSpeechUpstreamErrorsAreMapped(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		want     int
		contains string
	}{
		{"rejected key", http.StatusUnauthorized, `{"detail":{"status":"missing_permissions","message":"needs text_to_speech"}}`, http.StatusBadGateway, "ElevenLabs rejected the API key: needs text_to_speech"},
		{"rate limited", http.StatusTooManyRequests, `{"detail":{"status":"too_many_concurrent_requests","message":"slow down"}}`, http.StatusTooManyRequests, "slow down"},
		{"out of credit", http.StatusPaymentRequired, `{"detail":"quota exceeded"}`, http.StatusPaymentRequired, "ElevenLabs is out of credit: quota exceeded"},
		{"quota on a good key", http.StatusUnauthorized, `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota"}}`, http.StatusPaymentRequired, "ElevenLabs is out of credit: This request exceeds your quota"},
		{"invalid", http.StatusUnprocessableEntity, `{"detail":[{"loc":["body","text"],"msg":"field required"}]}`, http.StatusBadGateway, "field required"},
		{"opaque", http.StatusInternalServerError, `oops`, http.StatusBadGateway, "Internal Server Error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}}
			r := newSpeechRig(t, up)
			r.setKey("good")
			r.setVoice("v1")
			res := r.get(speakPath("hello", ""), nil)
			if res.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", res.StatusCode, tc.want)
			}
			if msg := errorMessage(t, res); !strings.Contains(msg, tc.contains) {
				t.Fatalf("message %q, want it to say %q", msg, tc.contains)
			}
			if names := r.cacheFiles(); len(names) != 0 {
				t.Fatalf("an error left %v in the cache", names)
			}
		})
	}
}

func TestSpeechRefusesTextOutOfBounds(t *testing.T) {
	r := newSpeechRig(t, &fakeEleven{})
	r.setKey("good")
	r.setVoice("v1")
	for _, text := range []string{"   ", strings.Repeat("a", tts.MaxChars+1)} {
		if res := r.get(speakPath(text, ""), nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%d characters returned %d, want 400", len(text), res.StatusCode)
		}
	}
	// The longest allowed message, URL-encoded, fits the request line.
	if res := r.get(speakPath(strings.Repeat("é", tts.MaxChars), ""), nil); res.StatusCode != http.StatusOK {
		t.Fatalf("the longest message returned %d", res.StatusCode)
	}
}

// The client re-asks for a failed clip to read why; that must not go to
// ElevenLabs a second time.
func TestSpeechRefusalIsAnsweredFromMemory(t *testing.T) {
	var calls atomic.Int32
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"detail":"quota exceeded"}`)
	}}
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")

	first := r.get(speakPath("hello", ""), nil)
	if first.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status %d, want 402", first.StatusCode)
	}
	head, err := http.Head(r.ts.URL + speakPath("hello", ""))
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("HEAD after a refusal returned %d, want 402", head.StatusCode)
	}
	again := r.get(speakPath("hello", ""), nil)
	if again.StatusCode != http.StatusPaymentRequired || !strings.Contains(errorMessage(t, again), "quota exceeded") {
		t.Fatalf("the repeat did not carry the refusal: %d", again.StatusCode)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("ElevenLabs was asked %d times, want 1", n)
	}

	// Other text is a different clip and gets its own try.
	r.get(speakPath("goodbye", ""), nil)
	if n := calls.Load(); n != 2 {
		t.Fatalf("ElevenLabs was asked %d times after new text, want 2", n)
	}

	// A new key might have the credit the old one lacked.
	r.setKey("other")
	r.get(speakPath("hello", ""), nil)
	if n := calls.Load(); n != 3 {
		t.Fatalf("ElevenLabs was asked %d times after a new key, want 3", n)
	}
}

func TestSpeechRefusalIsForgottenAfterAMinute(t *testing.T) {
	now := time.Unix(1000, 0)
	f := speechFailures{now: func() time.Time { return now }}
	f.remember("a", http.StatusTooManyRequests, "slow down")
	if status, msg, ok := f.recall("a"); !ok || status != http.StatusTooManyRequests || msg != "slow down" {
		t.Fatalf("recall = %d %q %v", status, msg, ok)
	}
	now = now.Add(speechFailureTTL - time.Second)
	if _, _, ok := f.recall("a"); !ok {
		t.Fatal("forgotten before the minute was up")
	}
	now = now.Add(time.Second)
	if _, _, ok := f.recall("a"); ok {
		t.Fatal("still remembered after a minute")
	}
	// An expired entry is dropped when the next one goes in.
	f.remember("b", http.StatusBadGateway, "x")
	if _, kept := f.byKey["a"]; kept {
		t.Fatal("an expired refusal stayed in the map")
	}
}

// A dropped connection is not a refusal: the next request has to try again.
func TestDroppedSpeechIsNotRemembered(t *testing.T) {
	var calls atomic.Int32
	up := &fakeEleven{speak: func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// Dropping the connection without an answer, as an outage would.
			panic(http.ErrAbortHandler)
		}
		_, _ = io.WriteString(w, "mp3")
	}}
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")
	if res := r.get(speakPath("hello", ""), nil); res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", res.StatusCode)
	}
	if res := r.get(speakPath("hello", ""), nil); res.StatusCode != http.StatusOK {
		t.Fatalf("the retry returned %d, want 200", res.StatusCode)
	}
}
