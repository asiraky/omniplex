package adapter

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/asiraky/omniplex/internal/proto"
)

// ToolImageStore is an optional HostServices extension. A harness hands back
// images as base64 inside a tool result — an MCP screenshot, Pi reading a
// PNG — and those bytes must not ride the event log: every replay and every
// reattach on a phone would pay for them again. The host keeps the bytes and
// returns image content that refers to them by URL.
type ToolImageStore interface {
	StoreToolImage(data []byte) (proto.ToolContent, error)
}

var errNoImageStore = errors.New("host keeps no images")

// MaxToolDiff bounds the patch a file change adds to its tool call. Past it
// the diff is dropped: a whole-file write would otherwise ride every replay.
const MaxToolDiff = 64 << 10

// ToolImage turns one base64 image from a tool result into content a
// presenter can show. It never returns the bytes: if the image cannot be
// stored, the content says so in words instead.
func ToolImage(host HostServices, b64 string) proto.ToolContent {
	content, err := storeToolImage(host, b64)
	if err != nil {
		host.Logf("tool result image: %v", err)
		return proto.ToolContent{Type: "text", Text: "[image not shown: " + err.Error() + "]"}
	}
	return content
}

func storeToolImage(host HostServices, b64 string) (proto.ToolContent, error) {
	store, ok := host.(ToolImageStore)
	if !ok {
		return proto.ToolContent{}, errNoImageStore
	}
	// A data URL is accepted as well as bare base64: MCP servers send both.
	if strings.HasPrefix(b64, "data:") {
		if _, after, found := strings.Cut(b64, ","); found {
			b64 = after
		}
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(b64), ""))
	if err != nil {
		return proto.ToolContent{}, errors.New("not valid base64")
	}
	return store.StoreToolImage(data)
}
