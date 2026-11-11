package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/tts"
)

// scribeUpload is what the fake Scribe was sent.
type scribeUpload struct {
	audio    []byte
	mime     string
	filename string
	model    string
}

// fakeScribe keeps every upload and answers each with the same transcript.
type fakeScribe struct {
	fakeEleven
	mu      sync.Mutex
	uploads []scribeUpload
}

func newFakeScribe(transcript string) *fakeScribe {
	f := &fakeScribe{}
	f.transcribe = func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"detail": err.Error()})
			return
		}
		up := scribeUpload{model: r.FormValue("model_id")}
		if files := r.MultipartForm.File["file"]; len(files) == 1 {
			up.mime = files[0].Header.Get("Content-Type")
			up.filename = files[0].Filename
			if file, err := files[0].Open(); err == nil {
				up.audio, _ = io.ReadAll(file)
				file.Close()
			}
		}
		f.mu.Lock()
		f.uploads = append(f.uploads, up)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"text": transcript})
	}
	return f
}

func (f *fakeScribe) received() []scribeUpload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]scribeUpload(nil), f.uploads...)
}

func (r *speechRig) dictate(threadID, mime string, audio []byte) *http.Response {
	r.t.Helper()
	path := "/api/dictation"
	if threadID != "" {
		path += "?" + url.Values{"thread": {threadID}}.Encode()
	}
	res, err := http.Post(r.ts.URL+path, mime, bytes.NewReader(audio))
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { res.Body.Close() })
	return res
}

func (r *speechRig) setDictationCleanup(on bool) {
	r.t.Helper()
	if _, err := run(r.t, r.c, "save_user_config", map[string]any{"config": map[string]any{"dictationCleanup": on}}); err != nil {
		r.t.Fatal(err)
	}
}

func transcript(t *testing.T, res *http.Response) string {
	t.Helper()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, readBody(t, res))
	}
	var body struct {
		Text *string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Text == nil {
		t.Fatal("the reply has no text")
	}
	return *body.Text
}

func untagTranscript(input string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(input, "<transcript>"), "</transcript>"))
}

func TestDictationSendsTheRecordingToScribeAndAnswersWithItsText(t *testing.T) {
	up := newFakeScribe("  Fix the login bug on the settings page.  ")
	r := newSpeechRig(t, up)
	r.setKey("good")

	audio := []byte("\x1aE\xdf\xa3 not really opus")
	got := transcript(t, r.dictate("", "audio/webm;codecs=opus", audio))
	if got != "Fix the login bug on the settings page." {
		t.Fatalf("answered %q, want Scribe's transcript trimmed", got)
	}
	uploads := up.received()
	if len(uploads) != 1 {
		t.Fatalf("Scribe was sent %d uploads, want 1", len(uploads))
	}
	u := uploads[0]
	if !bytes.Equal(u.audio, audio) {
		t.Errorf("Scribe was sent %q, want the recording", u.audio)
	}
	if u.mime != "audio/webm;codecs=opus" {
		t.Errorf("the file part was typed %q, want what the browser recorded in", u.mime)
	}
	if u.filename != "audio.webm" {
		t.Errorf("the file was named %q, want a webm extension", u.filename)
	}
	if u.model != tts.STTModel {
		t.Errorf("model_id was %q, want the speech-to-text model", u.model)
	}
}

func TestDictationWithoutKeyAsksForOne(t *testing.T) {
	up := newFakeScribe("hello")
	r := newSpeechRig(t, up)
	res := r.dictate("", "audio/webm", []byte("audio"))
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", res.StatusCode)
	}
	if msg := errorMessage(t, res); msg != noSpeechKey {
		t.Fatalf("said %q", msg)
	}
	if n := len(up.received()); n != 0 {
		t.Fatalf("Scribe was sent %d uploads without a key", n)
	}
}

func TestDictationRefusesAnEmptyRecording(t *testing.T) {
	up := newFakeScribe("hello")
	r := newSpeechRig(t, up)
	r.setKey("good")
	if res := r.dictate("", "audio/webm", nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", res.StatusCode)
	}
	if n := len(up.received()); n != 0 {
		t.Fatalf("Scribe was sent %d empty uploads", n)
	}
}

func TestDictationRefusesARecordingOverTheCap(t *testing.T) {
	up := newFakeScribe("hello")
	r := newSpeechRig(t, up)
	r.setKey("good")
	if res := r.dictate("", "audio/webm", make([]byte, maxDictationBytes+1)); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", res.StatusCode)
	}
	if n := len(up.received()); n != 0 {
		t.Fatalf("Scribe was sent %d oversized uploads", n)
	}
	// Exactly the cap is still a recording.
	if res := r.dictate("", "audio/webm", make([]byte, maxDictationBytes)); res.StatusCode != http.StatusOK {
		t.Fatalf("a recording at the cap returned %d", res.StatusCode)
	}
}

func TestDictationUpstreamErrorsAreMapped(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		want     int
		contains string
	}{
		{"out of credit", http.StatusUnauthorized, `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota"}}`, http.StatusPaymentRequired, "ElevenLabs is out of credit: This request exceeds your quota"},
		{"rejected key", http.StatusUnauthorized, `{"detail":{"status":"missing_permissions","message":"needs speech_to_text"}}`, http.StatusBadGateway, "ElevenLabs rejected the API key: needs speech_to_text"},
		{"rate limited", http.StatusTooManyRequests, `{"detail":{"status":"too_many_concurrent_requests","message":"slow down"}}`, http.StatusTooManyRequests, "slow down"},
		{"unreadable audio", http.StatusBadRequest, `{"detail":{"status":"invalid_content","message":"could not decode the audio"}}`, http.StatusBadGateway, "could not decode the audio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeEleven{transcribe: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}}
			r := newSpeechRig(t, up)
			r.setKey("good")
			res := r.dictate("", "audio/mp4", []byte("audio"))
			if res.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", res.StatusCode, tc.want)
			}
			if msg := errorMessage(t, res); !strings.Contains(msg, tc.contains) {
				t.Fatalf("message %q, want it to say %q", msg, tc.contains)
			}
		})
	}
}

func TestDictationIsNotCleanedWhenCleanupIsOff(t *testing.T) {
	r := newSpeechRig(t, newFakeScribe("um so fix the uh bug"))
	r.setKey("good")
	calls := r.editWith("m1", func(string) (string, error) { return "So, fix the bug.", nil })
	thread := r.threadIn("")

	if got := transcript(t, r.dictate(thread, "audio/webm", []byte("audio"))); got != "um so fix the uh bug" {
		t.Fatalf("answered %q, want the transcript as Scribe wrote it", got)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the model was asked %d times with cleanup off", n)
	}
}

func TestDictationIsCleanedByTheThreadsModelWhenCleanupIsOn(t *testing.T) {
	r := newSpeechRig(t, newFakeScribe("um so fix the uh bug"))
	r.setKey("good")
	r.setDictationCleanup(true)
	var asked string
	calls := r.editWith("m1", func(input string) (string, error) {
		asked = untagTranscript(input)
		// A model that echoes the tags back still gives clean text.
		return "<transcript>\nSo, fix the bug.\n</transcript>", nil
	})
	thread := r.threadIn("")

	if got := transcript(t, r.dictate(thread, "audio/webm", []byte("audio"))); got != "So, fix the bug." {
		t.Fatalf("answered %q, want the cleaned text", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("the model was asked %d times, want once", n)
	}
	if asked != "um so fix the uh bug" {
		t.Fatalf("the model was given %q, want Scribe's transcript", asked)
	}
}

func TestDictationFallsBackToTheTranscriptWhenCleanupFails(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		err    error
	}{
		{"the model errors", "", errors.New("signed out")},
		{"the model answers with nothing", "  <transcript></transcript> ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newSpeechRig(t, newFakeScribe("um so fix the uh bug"))
			r.setKey("good")
			r.setDictationCleanup(true)
			calls := r.editWith("m1", func(string) (string, error) { return tc.answer, tc.err })
			thread := r.threadIn("")

			if got := transcript(t, r.dictate(thread, "audio/webm", []byte("audio"))); got != "um so fix the uh bug" {
				t.Fatalf("answered %q, want the transcript as Scribe wrote it", got)
			}
			if n := calls.Load(); n != 1 {
				t.Fatalf("the model was asked %d times, want once", n)
			}
		})
	}
}

// The new-thread screen has no thread, so no account to clean up with.
func TestDictationWithoutAThreadIsNotCleaned(t *testing.T) {
	r := newSpeechRig(t, newFakeScribe("um so fix the uh bug"))
	r.setKey("good")
	r.setDictationCleanup(true)
	calls := r.editWith("m1", func(string) (string, error) { return "So, fix the bug.", nil })

	if got := transcript(t, r.dictate("", "audio/webm", []byte("audio"))); got != "um so fix the uh bug" {
		t.Fatalf("answered %q, want the transcript as Scribe wrote it", got)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the model was asked %d times without a thread", n)
	}
}

// Silence transcribes to nothing, and nothing is not worth a model's time.
func TestSilentDictationIsAnEmptyTranscript(t *testing.T) {
	r := newSpeechRig(t, newFakeScribe("   "))
	r.setKey("good")
	r.setDictationCleanup(true)
	calls := r.editWith("m1", func(string) (string, error) { return "Something.", nil })
	thread := r.threadIn("")

	if got := transcript(t, r.dictate(thread, "audio/webm", []byte("audio"))); got != "" {
		t.Fatalf("answered %q for silence", got)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the model was asked %d times to clean nothing", n)
	}
}
