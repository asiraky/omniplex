package claudecode

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// imageHost is a host that keeps tool images: it records the bytes it is
// handed and names each by a fixed URL.
type imageHost struct {
	fakeHost
	stored [][]byte
}

func (h *imageHost) StoreToolImage(data []byte) (proto.ToolContent, error) {
	h.stored = append(h.stored, data)
	return proto.ToolContent{Type: "image", URL: "/api/tool-images/abc", MediaType: "image/png", Width: 4, Height: 3}, nil
}

// toolResultUpdates drains the session's events and returns every
// tool.call.updated payload, each with the JSON it would carry on the wire.
func toolResultUpdates(t *testing.T, s *session) ([]proto.ToolCallUpdatedPayload, []string) {
	t.Helper()
	var payloads []proto.ToolCallUpdatedPayload
	var wire []string
	for {
		select {
		case e := <-s.events:
			if e.Type != proto.ToolCallUpdated {
				continue
			}
			b, err := json.Marshal(e.Payload)
			if err != nil {
				t.Fatal(err)
			}
			payloads = append(payloads, e.Payload.(proto.ToolCallUpdatedPayload))
			wire = append(wire, string(b))
		default:
			return payloads, wire
		}
	}
}

func toolResultMessage(t *testing.T, result map[string]any, structured any) map[string]json.RawMessage {
	t.Helper()
	m := map[string]any{
		"type": "user", "session_id": "conv",
		"message": map[string]any{"content": []any{result}},
	}
	if structured != nil {
		m["tool_use_result"] = structured
	}
	return rawSDK(t, m)
}

// The acceptance test for keeping screenshots off the wire: an MCP result
// carrying a base64 image reaches the event as a URL, and the base64 does not
// travel at all.
func TestToolResultImageTravelsAsURL(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nnot-really-a-png-but-bytes")
	b64 := base64.StdEncoding.EncodeToString(png)
	host := &imageHost{}
	s := newTestSession()
	s.host = host

	s.handleSDKMessage(toolResultMessage(t, map[string]any{
		"type": "tool_result", "tool_use_id": "tu1",
		"content": []any{
			map[string]any{"type": "text", "text": "Took a screenshot"},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": b64}},
		},
	}, []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": b64}}}))

	payloads, wire := toolResultUpdates(t, s)
	if len(payloads) != 1 {
		t.Fatalf("got %d tool updates, want 1", len(payloads))
	}
	if strings.Contains(wire[0], b64) {
		t.Fatalf("base64 image travelled on the wire: %s", wire[0])
	}
	if !strings.Contains(wire[0], "/api/tool-images/abc") {
		t.Fatalf("wire frame does not name the stored image: %s", wire[0])
	}
	if len(host.stored) != 1 || string(host.stored[0]) != string(png) {
		t.Fatalf("stored %q, want the decoded image", host.stored)
	}
	got := payloads[0].Content
	if len(got) != 2 || got[0].Type != "text" || got[0].Text != "Took a screenshot" || got[1].Type != "image" {
		t.Fatalf("content = %+v, want text then image", got)
	}
}

// Images keep their order, and one whose source is not base64 becomes a note
// rather than its raw JSON.
func TestToolResultImagesInOrderAndURLSourceIsANote(t *testing.T) {
	host := &imageHost{}
	s := newTestSession()
	s.host = host
	first := base64.StdEncoding.EncodeToString([]byte("first"))
	second := base64.StdEncoding.EncodeToString([]byte("second"))

	s.handleSDKMessage(toolResultMessage(t, map[string]any{
		"type": "tool_result", "tool_use_id": "tu1",
		"content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": first}},
			map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/secret.png"}},
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": second}},
		},
	}, nil))

	payloads, wire := toolResultUpdates(t, s)
	if len(payloads) != 1 {
		t.Fatalf("got %d tool updates, want 1", len(payloads))
	}
	if len(host.stored) != 2 || string(host.stored[0]) != "first" || string(host.stored[1]) != "second" {
		t.Fatalf("stored %q, want first then second", host.stored)
	}
	got := payloads[0].Content
	if len(got) != 3 || got[0].Type != "image" || got[1].Type != "text" || got[2].Type != "image" {
		t.Fatalf("content = %+v, want image, note, image", got)
	}
	if strings.Contains(wire[0], "example.com") || strings.Contains(got[1].Text, "{") {
		t.Fatalf("non-base64 source leaked raw JSON: %s", wire[0])
	}
}

// A lone image block where the SDK would send an array is still an image, not
// a JSON dump of its bytes into the text.
func TestToolResultLoneImageBlockIsNotDumped(t *testing.T) {
	host := &imageHost{}
	s := newTestSession()
	s.host = host
	b64 := base64.StdEncoding.EncodeToString([]byte("lone image bytes"))

	s.handleSDKMessage(toolResultMessage(t, map[string]any{
		"type": "tool_result", "tool_use_id": "tu1",
		"content": map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": b64}},
	}, nil))

	_, wire := toolResultUpdates(t, s)
	if len(wire) != 1 || strings.Contains(wire[0], b64) {
		t.Fatalf("lone image block leaked base64: %v", wire)
	}
	if len(host.stored) != 1 {
		t.Fatalf("stored %d images, want 1", len(host.stored))
	}
}

// A host that keeps no images still never receives the bytes: the image
// becomes words.
func TestToolResultImageWithoutStoreIsWords(t *testing.T) {
	s := newTestSession()
	s.host = &fakeHost{}
	b64 := base64.StdEncoding.EncodeToString([]byte("unstored"))

	s.handleSDKMessage(toolResultMessage(t, map[string]any{
		"type": "tool_result", "tool_use_id": "tu1",
		"content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": b64}}},
	}, nil))

	payloads, wire := toolResultUpdates(t, s)
	if len(payloads) != 1 || strings.Contains(wire[0], b64) {
		t.Fatalf("unstored image leaked base64: %v", wire)
	}
	if got := payloads[0].Content; len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("content = %+v, want one text note", got)
	}
}

func editResult(isError bool) map[string]any {
	return map[string]any{
		"type": "tool_result", "tool_use_id": "tu1", "is_error": isError,
		"content": "The file /repo/main.go has been updated.",
	}
}

var editOutput = map[string]any{
	"filePath": "/repo/main.go", "oldString": "a := 1", "newString": "a := 2",
	"structuredPatch": []any{map[string]any{
		"oldStart": 10, "oldLines": 3, "newStart": 10, "newLines": 3,
		"lines": []any{" func f() {", "-\ta := 1", "+\ta := 2", " }"},
	}},
}

func diffOf(content []proto.ToolContent) (proto.ToolContent, bool) {
	for _, c := range content {
		if c.Type == "diff" {
			return c, true
		}
	}
	return proto.ToolContent{}, false
}

func TestEditResultCarriesDiff(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(toolResultMessage(t, editResult(false), editOutput))

	payloads, _ := toolResultUpdates(t, s)
	if len(payloads) != 1 {
		t.Fatalf("got %d tool updates, want 1", len(payloads))
	}
	diff, ok := diffOf(payloads[0].Content)
	if !ok {
		t.Fatalf("no diff in %+v", payloads[0].Content)
	}
	if diff.Path != "/repo/main.go" {
		t.Fatalf("diff path = %q", diff.Path)
	}
	lines := strings.Split(diff.Text, "\n")
	want := []string{"@@ -10,3 +10,3 @@", " func f() {", "-\ta := 1", "+\ta := 2", " }"}
	at := -1
	for i, l := range lines {
		if l == want[0] {
			at = i
		}
	}
	if at < 0 || len(lines) < at+len(want) {
		t.Fatalf("patch has no hunk header %q:\n%s", want[0], diff.Text)
	}
	for i, w := range want {
		if lines[at+i] != w {
			t.Fatalf("patch line %d = %q, want %q:\n%s", at+i, lines[at+i], w, diff.Text)
		}
	}
	if payloads[0].Content[0].Type != "text" {
		t.Fatalf("the tool's text no longer leads: %+v", payloads[0].Content)
	}
}

func TestFailedEditHasNoDiff(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(toolResultMessage(t, editResult(true), editOutput))

	payloads, _ := toolResultUpdates(t, s)
	if len(payloads) != 1 || payloads[0].Status != proto.StatusFailed {
		t.Fatalf("payloads = %+v", payloads)
	}
	if _, ok := diffOf(payloads[0].Content); ok {
		t.Fatalf("failed edit carried a diff: %+v", payloads[0].Content)
	}
}

// tool_use_result belongs to the message, so with two results in it there is
// no telling whose it is.
func TestStructuredOutputIgnoredWithTwoResults(t *testing.T) {
	s := newTestSession()
	second := editResult(false)
	second["tool_use_id"] = "tu2"
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "user", "session_id": "conv",
		"message":         map[string]any{"content": []any{editResult(false), second}},
		"tool_use_result": editOutput,
	}))

	payloads, _ := toolResultUpdates(t, s)
	if len(payloads) != 2 {
		t.Fatalf("got %d tool updates, want 2", len(payloads))
	}
	for _, p := range payloads {
		if _, ok := diffOf(p.Content); ok {
			t.Fatalf("%s carried a diff it cannot be matched to", p.ToolCallID)
		}
	}
}

// Writing a new file has no hunks; its content is the patch, every line
// added.
func TestWriteCreateIsAllAdded(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(toolResultMessage(t, editResult(false), map[string]any{
		"type": "create", "filePath": "/repo/new.txt", "content": "one\ntwo\n",
		"structuredPatch": []any{}, "originalFile": nil,
	}))

	payloads, _ := toolResultUpdates(t, s)
	diff, ok := diffOf(payloads[0].Content)
	if !ok {
		t.Fatalf("no diff in %+v", payloads[0].Content)
	}
	if !strings.Contains(diff.Text, "@@ -0,0 +1,2 @@\n+one\n+two\n") {
		t.Fatalf("create patch = %q", diff.Text)
	}
}

// A non-file tool's structured output — Bash's, say — adds nothing.
func TestOtherStructuredOutputHasNoDiff(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(toolResultMessage(t, editResult(false), map[string]any{
		"stdout": "hi", "stderr": "", "interrupted": false,
	}))

	payloads, _ := toolResultUpdates(t, s)
	if _, ok := diffOf(payloads[0].Content); ok {
		t.Fatalf("bash output became a diff: %+v", payloads[0].Content)
	}
}
