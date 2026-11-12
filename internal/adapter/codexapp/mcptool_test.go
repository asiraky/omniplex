package codexapp

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// imageHost is a host that keeps tool images, recording the bytes it was given.
type imageHost struct {
	elicitHost
	stored [][]byte
}

func (h *imageHost) StoreToolImage(data []byte) (proto.ToolContent, error) {
	h.stored = append(h.stored, data)
	return proto.ToolContent{Type: "image", URL: "/api/tool-images/abc", MediaType: "image/png", Width: 640, Height: 480}, nil
}

// TestMCPImageGoesOutAsURLNotBytes is the issue's acceptance test: a
// screenshot from an MCP tool reaches the wire as a URL with the image's
// size, and its base64 never does.
func TestMCPImageGoesOutAsURLNotBytes(t *testing.T) {
	host := &imageHost{}
	s := subagentSession(t)
	s.host = host
	openTurn(s, "turn-1")
	png := []byte("\x89PNG\r\n\x1a\nfake screenshot bytes")
	b64 := base64.StdEncoding.EncodeToString(png)

	s.handleNotification("item/completed", itemNotification("turn-1", `{"type":"mcpToolCall","id":"m1",
		"server":"browser","tool":"screenshot","status":"completed","arguments":{},
		"result":{"content":[{"type":"text","text":"Took a screenshot"},
			{"type":"image","data":"`+b64+`","mimeType":"image/png"}]}}`))
	p := onlyToolUpdate(t, drain(s))

	if len(host.stored) != 1 || string(host.stored[0]) != string(png) {
		t.Fatalf("host stored %q, want the decoded screenshot", host.stored)
	}
	wire, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), b64) {
		t.Fatalf("base64 image went out on the wire: %s", wire)
	}
	if !strings.Contains(string(wire), "/api/tool-images/abc") {
		t.Fatalf("wire payload has no image URL: %s", wire)
	}
	want := []proto.ToolContent{
		{Type: "text", Text: "Took a screenshot"},
		{Type: "image", URL: "/api/tool-images/abc", MediaType: "image/png", Width: 640, Height: 480},
	}
	if p.Status != proto.StatusCompleted || !slices.Equal(p.Content, want) {
		t.Fatalf("payload %+v, want completed with %+v", p, want)
	}
}
