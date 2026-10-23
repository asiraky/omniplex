// Package tts reads chat messages aloud through ElevenLabs. The API key stays
// on the server: the browser asks this server for audio, and this server asks
// ElevenLabs, keeping what it gets back on disk so a replay costs nothing.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// Model is ElevenLabs' low-latency model: speech starts in well under a
	// second, which is what a play button needs.
	Model = "eleven_flash_v2_5"
	// Format is mono 64 kbps mp3, about 0.5 MB a minute. Speech gains nothing
	// audible from more, and it all goes over 4G.
	Format = "mp3_44100_64"
	// MaxChars is what Flash v2.5 accepts in one request.
	MaxChars = 40000

	defaultBaseURL = "https://api.elevenlabs.io"
	// maxVoicePages bounds the voice list walk. At 100 a page that is far more
	// voices than anyone has, and a server that never stops saying has_more
	// cannot hold a request open forever.
	maxVoicePages = 10
)

// Voice is the slice of an ElevenLabs voice the picker shows. The upstream
// record carries samples, settings and fine-tuning state the UI never reads.
type Voice struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	PreviewURL  string            `json:"previewUrl,omitempty"`
}

// UpstreamError is ElevenLabs answering with something other than success.
type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("ElevenLabs: %s", e.Message)
}

// Rejected reports whether ElevenLabs refused the key itself (or its
// permissions), as opposed to the request.
func (e *UpstreamError) Rejected() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

type client struct {
	base string
	http *http.Client
}

func newClient() client {
	base := strings.TrimRight(os.Getenv("OMNIPLEX_ELEVENLABS_URL"), "/")
	if base == "" {
		base = defaultBaseURL
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	// No overall timeout: a long message streams for minutes. A server that
	// has not started answering in this long is not going to.
	t.ResponseHeaderTimeout = 30 * time.Second
	return client{base: base, http: &http.Client{Transport: t}}
}

func (c client) do(ctx context.Context, key, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		defer res.Body.Close()
		return nil, upstreamError(res)
	}
	return res, nil
}

type voicePage struct {
	Voices []struct {
		VoiceID     string            `json:"voice_id"`
		Name        string            `json:"name"`
		Category    string            `json:"category"`
		Description string            `json:"description"`
		Labels      map[string]string `json:"labels"`
		PreviewURL  string            `json:"preview_url"`
	} `json:"voices"`
	HasMore       bool   `json:"has_more"`
	NextPageToken string `json:"next_page_token"`
}

func (c client) voicePage(ctx context.Context, key string, size int, token string) (voicePage, error) {
	q := url.Values{"page_size": {fmt.Sprint(size)}}
	if token != "" {
		q.Set("next_page_token", token)
	}
	var page voicePage
	res, err := c.do(ctx, key, http.MethodGet, "/v2/voices?"+q.Encode(), nil)
	if err != nil {
		return page, err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return page, fmt.Errorf("ElevenLabs voice list: %w", err)
	}
	return page, nil
}

func (c client) voices(ctx context.Context, key string) ([]Voice, error) {
	out := []Voice{}
	token := ""
	for range maxVoicePages {
		page, err := c.voicePage(ctx, key, 100, token)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Voices {
			out = append(out, Voice{
				ID:          v.VoiceID,
				Name:        v.Name,
				Category:    v.Category,
				Description: v.Description,
				Labels:      v.Labels,
				PreviewURL:  v.PreviewURL,
			})
		}
		if !page.HasMore || page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	return out, nil
}

func (c client) stream(ctx context.Context, key, voiceID, text string) (io.ReadCloser, error) {
	body, err := json.Marshal(map[string]string{"text": text, "model_id": Model})
	if err != nil {
		return nil, err
	}
	path := "/v1/text-to-speech/" + url.PathEscape(voiceID) + "/stream?output_format=" + Format
	res, err := c.do(ctx, key, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

// upstreamError reads the reason out of an error body. ElevenLabs puts it
// under "detail" in three shapes: an object with a message, a bare string,
// and for a validation failure a list of field complaints.
func upstreamError(res *http.Response) *UpstreamError {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	msg := detailMessage(b)
	if msg == "" {
		msg = http.StatusText(res.StatusCode)
	}
	if msg == "" {
		msg = res.Status
	}
	return &UpstreamError{Status: res.StatusCode, Message: msg}
}

func detailMessage(b []byte) string {
	var env struct {
		Detail  json.RawMessage `json:"detail"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(b, &env) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(env.Detail, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(env.Detail, &obj) == nil {
		if obj.Message != "" {
			return obj.Message
		}
		if obj.Status != "" {
			return obj.Status
		}
	}
	var list []struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if json.Unmarshal(env.Detail, &list) == nil {
		var parts []string
		for _, item := range list {
			if item.Msg != "" {
				parts = append(parts, item.Msg)
			} else if item.Message != "" {
				parts = append(parts, item.Message)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	return env.Message
}
