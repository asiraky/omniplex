package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

type bareHost struct{ logs []string }

func (h *bareHost) RequestPermission(context.Context, PermissionRequest) (PermissionOutcome, error) {
	return PermissionOutcome{}, nil
}
func (h *bareHost) Elicit(context.Context, ElicitationRequest) (ElicitationResult, error) {
	return ElicitationResult{}, nil
}
func (h *bareHost) Logf(format string, args ...any) { h.logs = append(h.logs, format) }

// imageHost keeps what it is handed and answers with a URL, as the thread
// package's host does.
type imageHost struct {
	bareHost
	got  [][]byte
	fail error
}

func (h *imageHost) StoreToolImage(data []byte) (proto.ToolContent, error) {
	if h.fail != nil {
		return proto.ToolContent{}, h.fail
	}
	h.got = append(h.got, data)
	return proto.ToolContent{Type: "image", URL: "/img/1", MediaType: "image/png", Width: 2, Height: 3}, nil
}

var pngHead = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")

func TestToolImageStoresTheDecodedBytes(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(pngHead)
	wrapped := b64[:8] + "\n  " + b64[8:] + "\n"
	for name, in := range map[string]string{
		"bare base64":    b64,
		"data URL":       "data:image/png;base64," + b64,
		"wrapped base64": wrapped,
	} {
		t.Run(name, func(t *testing.T) {
			host := &imageHost{}
			got := ToolImage(host, in)
			if got.Type != "image" || got.URL != "/img/1" || got.Text != "" {
				t.Fatalf("content = %+v, want the image the host stored", got)
			}
			if len(host.got) != 1 || !bytes.Equal(host.got[0], pngHead) {
				t.Fatalf("host was handed %q, want the decoded bytes", host.got)
			}
		})
	}
}

// Whatever goes wrong, the bytes never end up in the content: it says in
// words that there was an image instead.
func TestToolImageFallsBackToWords(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(pngHead)
	for name, tc := range map[string]struct {
		host HostServices
		in   string
	}{
		"invalid base64":    {&imageHost{}, "not*base64!"},
		"host keeps none":   {&bareHost{}, b64},
		"host fails to put": {&imageHost{fail: errors.New("disk full")}, b64},
	} {
		t.Run(name, func(t *testing.T) {
			got := ToolImage(tc.host, tc.in)
			if got.Type != "text" || got.URL != "" || !strings.HasPrefix(got.Text, "[image not shown") {
				t.Fatalf("content = %+v, want a text placeholder", got)
			}
			if strings.Contains(got.Text, b64) {
				t.Fatal("placeholder carries the image bytes")
			}
			if h, ok := tc.host.(*imageHost); ok && len(h.got) != 0 {
				t.Fatal("host was handed bytes for an image that failed")
			}
		})
	}
}
