package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
)

// spokenEleven records the text it is asked to synthesise and answers with
// audio that names it.
type spokenEleven struct {
	fakeEleven
	mu     sync.Mutex
	spoken []string
}

func newSpokenEleven() *spokenEleven {
	f := &spokenEleven{}
	f.speak = func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.spoken = append(f.spoken, body.Text)
		f.mu.Unlock()
		_, _ = io.WriteString(w, "mp3:"+body.Text)
	}
	return f
}

func (f *spokenEleven) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.spoken...)
}

// editWith makes every thread's messages rewritten by edit.
func (r *speechRig) editWith(model string, edit func(input string) (string, error)) *atomic.Int32 {
	calls := new(atomic.Int32)
	r.srv.editorFor = func(context.Context, string) (speechEditor, bool) {
		return speechEditor{model: model, prompt: func(_ context.Context, _, input string) (string, error) {
			calls.Add(1)
			return edit(input)
		}}, true
	}
	return calls
}

func unwrap(input string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(input, "<text>"), "</text>"))
}

func TestSpeechReadsTheRewrite(t *testing.T) {
	up := newSpokenEleven()
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")
	calls := r.editWith("m1", func(input string) (string, error) {
		return "Rewritten: " + unwrap(input), nil
	})
	thread := r.threadIn("")

	first := r.get(speakPath("Fixed in c5b2557f9.", thread), nil)
	if got := readBody(t, first); got != "mp3:Rewritten: Fixed in c5b2557f9." {
		t.Fatalf("played %q, want the rewrite read", got)
	}
	// The replay is the cached clip: neither the model nor ElevenLabs is
	// asked again.
	again := r.get(speakPath("Fixed in c5b2557f9.", thread), nil)
	if got := readBody(t, again); got != "mp3:Rewritten: Fixed in c5b2557f9." {
		t.Fatalf("replay played %q", got)
	}
	if calls.Load() != 1 || len(up.texts()) != 1 {
		t.Fatalf("model asked %d times and ElevenLabs %d, want once each", calls.Load(), len(up.texts()))
	}
}

func TestSpeechKeepsNoAudioFromAPartRewrite(t *testing.T) {
	up := newSpokenEleven()
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")
	// Two lines long enough to be rewritten as two parts.
	text := "First " + strings.Repeat("a", 2100) + ".\nSecond " + strings.Repeat("b", 2100) + "."
	var fail atomic.Bool
	fail.Store(true)
	r.editWith("m1", func(input string) (string, error) {
		if fail.Load() && strings.Contains(input, "First") {
			return "", errors.New("model is down")
		}
		return "Better.", nil
	})
	thread := r.threadIn("")

	if got := readBody(t, r.get(speakPath(text, thread), nil)); !strings.Contains(got, "First aaa") || !strings.Contains(got, "Better.") {
		t.Fatalf("played %.40q…, want the failed part as it came and the other rewritten", got)
	}
	fail.Store(false)
	if got := readBody(t, r.get(speakPath(text, thread), nil)); got != "mp3:Better.\n\nBetter." {
		t.Fatalf("second play %.40q…, want a fresh try at the rewrite", got)
	}
}

func TestSpeechLeavesAFailingModelAlone(t *testing.T) {
	up := newSpokenEleven()
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")
	calls := r.editWith("m1", func(string) (string, error) { return "", errors.New("not signed in") })
	thread := r.threadIn("")

	for range 3 {
		if got := readBody(t, r.get(speakPath("As it came.", thread), nil)); got != "mp3:As it came." {
			t.Fatalf("played %q, want the message as it came", got)
		}
	}
	// Asked once, then left alone; the audio made without it is kept.
	if calls.Load() != 1 || len(up.texts()) != 2 {
		t.Fatalf("model asked %d times and ElevenLabs %d; want once and twice", calls.Load(), len(up.texts()))
	}
}

func TestSpeechRewriteIsPartOfTheCacheKey(t *testing.T) {
	up := newSpokenEleven()
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")
	thread := r.threadIn("")

	r.editWith("m1", func(string) (string, error) { return "One.", nil })
	readBody(t, r.get(speakPath("Hello.", thread), nil))
	r.editWith("m2", func(string) (string, error) { return "Two.", nil })
	if got := readBody(t, r.get(speakPath("Hello.", thread), nil)); got != "mp3:Two." {
		t.Fatalf("after the model changed played %q, want audio made by the new one", got)
	}
}

func TestSpeechWithoutEditorReadsTheText(t *testing.T) {
	up := newSpokenEleven()
	r := newSpeechRig(t, up)
	r.setKey("good")
	r.setVoice("v1")
	// threadIn's harness is not one this server has, so there is nobody to
	// rewrite with.
	thread := r.threadIn("")
	if got := readBody(t, r.get(speakPath("Plain.", thread), nil)); got != "mp3:Plain." {
		t.Fatalf("played %q", got)
	}
}

func TestEditorModel(t *testing.T) {
	catalogue := []adapter.ModelMeta{
		{ID: "gpt-6"},
		{ID: "gpt-5.6-luna"},
		{ID: "gpt-6-luna"},
		{ID: "gpt-7-luna", Group: adapter.GroupLegacy},
		{ID: "gpt-5.10-luna"},
	}
	cases := []struct {
		name   string
		driver string
		models []adapter.ModelMeta
		want   string
		ok     bool
	}{
		{"codex takes the newest current luna", "codex", catalogue, "gpt-6-luna", true},
		{"codex with no luna falls back", "codex", []adapter.ModelMeta{{ID: "gpt-6"}}, codexEditorFallback, true},
		{"pi has none", "pi", nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := editorModel(c.driver, c.models)
			if got != c.want || ok != c.ok {
				t.Fatalf("editorModel = %q, %v; want %q, %v", got, ok, c.want, c.ok)
			}
		})
	}
	if got := latestLuna([]adapter.ModelMeta{{ID: "gpt-5.10-luna"}, {ID: "gpt-5.9-luna"}}); got != "gpt-5.10-luna" {
		t.Fatalf("latestLuna = %q, want 5.10 over 5.9", got)
	}
}
