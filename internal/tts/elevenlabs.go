// Package tts reads chat messages aloud through ElevenLabs. The API key stays
// on the server: the browser asks this server for audio, and this server asks
// ElevenLabs, keeping what it gets back on disk so a replay costs nothing.
// Dictation goes the other way on the same key: recorded speech in, text out.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// Model is ElevenLabs' most natural model, the one the proply tutorials
	// were narrated with. Flash started faster but sounded robotic.
	Model = "eleven_v4"
	// Format is mono 128 kbps mp3, about 1 MB a minute. At 64 kbps the
	// encoder's artefacts sound like road noise behind the voice.
	Format = "mp3_44100_128"
	// MaxChars is what v4 accepts in one request.
	MaxChars = 10000
	// sttModel is Scribe, the model dictation is transcribed with.
	sttModel = "scribe_v2"

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
	// Code is ElevenLabs' machine-readable reason (detail.status), such as
	// "quota_exceeded", when the body carries one. The HTTP status alone
	// cannot tell an empty account from a bad key: both come back as 401.
	Code string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("ElevenLabs: %s", e.Message)
}

// Rejected reports whether ElevenLabs refused the key itself (or its
// permissions), as opposed to the request. An account out of credit also
// answers 401, but the key is fine, so that is not a rejection.
func (e *UpstreamError) Rejected() bool {
	if e.Quota() {
		return false
	}
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// Quota reports whether the account has run out of credit, so the user is
// told to top up rather than that the connection dropped or the key is bad.
// ElevenLabs says so with a 401 carrying "quota_exceeded"; 402 means the same.
func (e *UpstreamError) Quota() bool {
	return e.Code == "quota_exceeded" || e.Status == http.StatusPaymentRequired
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

// do sends body as contentType, or no body at all when it is nil.
func (c client) do(ctx context.Context, key, method, path, contentType string, body []byte) (*http.Response, error) {
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
		req.Header.Set("Content-Type", contentType)
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
	res, err := c.do(ctx, key, http.MethodGet, "/v2/voices?"+q.Encode(), "", nil)
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
	res, err := c.do(ctx, key, http.MethodPost, path, "application/json", body)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

// transcribe sends audio to Scribe as the multipart form it asks for. The
// recording is at most a few megabytes, so it is built in memory rather than
// streamed.
func (c client) transcribe(ctx context.Context, key string, audio io.Reader, mimeType string) (string, error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	fields := [][2]string{
		{"model_id", sttModel},
		// Without this Scribe writes in what it heard besides speech, such
		// as "(laughter)", which nobody dictating wants in their message.
		{"tag_audio_events", "false"},
		{"timestamps_granularity", "none"},
	}
	for _, f := range fields {
		if err := form.WriteField(f[0], f[1]); err != nil {
			return "", err
		}
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, audioFilename(mimeType)))
	h.Set("Content-Type", mimeType)
	part, err := form.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, audio); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}
	res, err := c.do(ctx, key, http.MethodPost, "/v1/speech-to-text", form.FormDataContentType(), buf.Bytes())
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ElevenLabs transcript: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// audioFilename names the upload after its container. Scribe works out the
// format from the bytes, but a name whose extension contradicts them is
// asking to be refused. Safari records mp4 and Chrome and Firefox webm or ogg.
func audioFilename(mimeType string) string {
	base, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return "audio"
	}
	switch base {
	case "audio/webm", "video/webm":
		return "audio.webm"
	case "audio/mp4", "video/mp4":
		return "audio.mp4"
	case "audio/ogg", "application/ogg":
		return "audio.ogg"
	case "audio/x-m4a", "audio/m4a", "audio/aac":
		return "audio.m4a"
	}
	return "audio"
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
	return &UpstreamError{Status: res.StatusCode, Message: msg, Code: detailStatus(b)}
}

// detailStatus is the status string of an object-shaped detail, empty for the
// other shapes.
func detailStatus(b []byte) string {
	var env struct {
		Detail struct {
			Status string `json:"status"`
		} `json:"detail"`
	}
	if json.Unmarshal(b, &env) != nil {
		return ""
	}
	return env.Detail.Status
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
