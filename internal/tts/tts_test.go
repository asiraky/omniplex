package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/provider"
)

func newService(t *testing.T, upstream http.Handler) *Service {
	t.Helper()
	ts := httptest.NewServer(upstream)
	t.Cleanup(ts.Close)
	t.Setenv("OMNIPLEX_ELEVENLABS_URL", ts.URL)
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	secrets, err := provider.OpenSecretStoreAt(filepath.Join(t.TempDir(), "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(secrets)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVoicesFollowPagesAndAreHeld(t *testing.T) {
	var calls atomic.Int32
	s := newService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("page_size") == "1" {
			_, _ = io.WriteString(w, `{"voices":[],"has_more":true,"next_page_token":"x"}`)
			return
		}
		calls.Add(1)
		switch r.URL.Query().Get("next_page_token") {
		case "":
			_, _ = io.WriteString(w, `{"voices":[{"voice_id":"a","name":"A","category":"premade","description":"warm","labels":{"accent":"british"},"preview_url":"https://p/a.mp3","samples":[1,2]}],"has_more":true,"next_page_token":"two"}`)
		case "two":
			_, _ = io.WriteString(w, `{"voices":[{"voice_id":"b","name":"B","category":"cloned"}],"has_more":false}`)
		default:
			t.Errorf("unexpected token %q", r.URL.Query().Get("next_page_token"))
		}
	}))
	if err := s.SetKey(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	voices, err := s.Voices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Voice{
		{ID: "a", Name: "A", Category: "premade", Description: "warm", Labels: map[string]string{"accent": "british"}, PreviewURL: "https://p/a.mp3"},
		{ID: "b", Name: "B", Category: "cloned"},
	}
	if fmt.Sprint(voices) != fmt.Sprint(want) {
		t.Fatalf("voices = %+v", voices)
	}
	if _, err := s.Voices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("%d page requests; the second listing should have been held", calls.Load())
	}
	// A key set again may be another account, with other voices.
	if err := s.SetKey(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Voices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("%d page requests; setting the key should have dropped the held list", calls.Load())
	}
}

func TestVoicesStopAtThePageCap(t *testing.T) {
	var calls atomic.Int32
	s := newService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"voices":[{"voice_id":"a","name":"A"}],"has_more":true,"next_page_token":"again"}`)
	}))
	if err := s.SetKey(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)
	voices, err := s.Voices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != maxVoicePages || len(voices) != maxVoicePages {
		t.Fatalf("%d requests, %d voices; a server always saying has_more must be cut off", calls.Load(), len(voices))
	}
}

func TestSetKeyKeepsAKeyElevenLabsCouldNotCheck(t *testing.T) {
	s := newService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	if err := s.SetKey(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if !s.HasKey() {
		t.Fatal("an outage refused the key")
	}
}

func TestSetKeyRefusesARejectedKey(t *testing.T) {
	s := newService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`)
	}))
	err := s.SetKey(context.Background(), "nope")
	if !errors.Is(err, ErrKeyRejected) || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("err = %v", err)
	}
	if s.HasKey() {
		t.Fatal("a rejected key was stored")
	}
	if _, err := s.Voices(context.Background()); !errors.Is(err, ErrNoKey) {
		t.Fatalf("voices without a key: %v", err)
	}
}

func TestStreamSendsTextAndReportsUpstreamErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusBadRequest, `{"detail":{"status":"voice_not_found","message":"A voice with that id was not found."}}`, "A voice with that id was not found."},
		{http.StatusBadRequest, `{"detail":{"status":"voice_not_found"}}`, "voice_not_found"},
		{http.StatusPaymentRequired, `{"detail":"quota exceeded"}`, "quota exceeded"},
		{http.StatusUnprocessableEntity, `{"detail":[{"loc":["body","text"],"msg":"field required"},{"msg":"too short"}]}`, "field required; too short"},
		{http.StatusBadGateway, `<html>`, "Bad Gateway"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			s := newService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			if err := s.SetKey(context.Background(), "k"); err != nil {
				t.Fatal(err)
			}
			_, err := s.Stream(context.Background(), "v", "hello")
			var up *UpstreamError
			if !errors.As(err, &up) || up.Status != tc.status || up.Message != tc.want {
				t.Fatalf("err = %#v, want status %d message %q", err, tc.status, tc.want)
			}
		})
	}

	s := newService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			return
		}
		b, _ := io.ReadAll(r.Body)
		_, _ = fmt.Fprintf(w, "%s %s %s", r.URL.Path, r.URL.Query().Get("output_format"), b)
	}))
	if err := s.SetKey(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	body, err := s.Stream(context.Background(), "v1", `say "hi"`)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	want := `/v1/text-to-speech/v1/stream ` + Format + ` {"model_id":"` + Model + `","text":"say \"hi\""}`
	if string(got) != want {
		t.Fatalf("upstream saw %s\nwant %s", got, want)
	}
}

func TestPrepareText(t *testing.T) {
	if _, err := PrepareText(" \n\t "); !errors.Is(err, ErrEmptyText) {
		t.Fatalf("blank: %v", err)
	}
	if _, err := PrepareText(strings.Repeat("é", MaxChars+1)); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("too long: %v", err)
	}
	// The limit is in characters, not bytes, and surrounding space is free.
	long := strings.Repeat("é", MaxChars)
	if got, err := PrepareText("  " + long + "\n"); err != nil || got != long {
		t.Fatalf("at the limit: %v", err)
	}
}

func writeEntry(t *testing.T, c *Cache, key string, size int, age time.Duration) {
	t.Helper()
	p, err := c.Create(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(c.path(key), when, when); err != nil {
		t.Fatal(err)
	}
}

func TestCacheEvictsLeastRecentlyPlayed(t *testing.T) {
	c, err := OpenCache(t.TempDir(), 300)
	if err != nil {
		t.Fatal(err)
	}
	writeEntry(t, c, "oldest", 100, 3*time.Hour)
	writeEntry(t, c, "played", 100, 2*time.Hour)
	writeEntry(t, c, "older", 100, time.Hour)
	// Playing an old entry makes it the most recent.
	f, ok := c.Open("played")
	if !ok {
		t.Fatal("entry missing")
	}
	f.Close()

	writeEntry(t, c, "newest", 100, 0)
	writeEntry(t, c, "newer", 50, 0)

	for key, want := range map[string]bool{"oldest": false, "older": false, "played": true, "newest": true, "newer": true} {
		f, ok := c.Open(key)
		if ok {
			f.Close()
		}
		if ok != want {
			t.Errorf("%s kept = %v, want %v", key, ok, want)
		}
	}
}

func TestCacheEntryAppearsOnlyOnCommit(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCache(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Create("k")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write([]byte("half"))
	if _, ok := c.Open("k"); ok {
		t.Fatal("an unfinished entry was served")
	}
	p.Discard()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("discard left %d files", len(entries))
	}

	// A pending file a crash left behind is swept once it is stale, and a
	// fresh one belonging to a live stream is not.
	stale, _ := os.CreateTemp(dir, pendingPrefix+"*")
	stale.Close()
	old := time.Now().Add(-2 * pendingMaxAge)
	_ = os.Chtimes(stale.Name(), old, old)
	live, _ := c.Create("live")
	defer live.Discard()
	writeEntry(t, c, "done", 4, 0)
	if _, err := os.Stat(stale.Name()); !os.IsNotExist(err) {
		t.Fatal("an abandoned pending file survived")
	}
	if _, err := os.Stat(live.f.Name()); err != nil {
		t.Fatal("a live pending file was swept")
	}
}
