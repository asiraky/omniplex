package piapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

// imageHost is fakeHost that keeps tool images, as the real host does, and
// hands back a fixed URL for each.
type imageHost struct {
	fakeHost
	mu     sync.Mutex
	stored [][]byte
}

func (h *imageHost) StoreToolImage(data []byte) (proto.ToolContent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stored = append(h.stored, data)
	return proto.ToolContent{Type: "image", URL: "/api/tool-images/abc123", MediaType: "image/png", Width: 4, Height: 3}, nil
}

// runToolTurn replays one pi turn whose only work is the given tool events
// and returns the tool call's final update.
func runToolTurn(t *testing.T, host adapter.HostServices, toolEvents ...string) proto.ToolCallUpdatedPayload {
	t.Helper()
	dir := t.TempDir()
	events := append([]string{`{"type":"agent_start"}`}, toolEvents...)
	events = append(events, `{"type":"agent_settled"}`)
	if err := os.WriteFile(filepath.Join(dir, "events"), []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(fakePi(t, dir, "")).CreateSession(context.Background(), host, adapter.CreateOptions{ThreadID: "sess-1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "go"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := drain(t, s, func(g []proto.Emission) bool { return hasType(g, proto.TurnFinished) })
	for _, e := range got {
		if p, ok := e.Payload.(proto.ToolCallUpdatedPayload); ok {
			return p
		}
	}
	t.Fatalf("no tool.call.updated in %+v", got)
	return proto.ToolCallUpdatedPayload{}
}

func TestReadImageResultTravelsAsURLNotBase64(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nnot-really-a-png-but-bytes-all-the-same")
	b64 := base64.StdEncoding.EncodeToString(png)
	host := &imageHost{}
	upd := runToolTurn(t, host,
		`{"type":"tool_execution_start","toolCallId":"call_1","toolName":"read","args":{"path":"shot.png"}}`,
		`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"read","isError":false,"result":{"content":[{"type":"text","text":"Read image file [image/png]"},{"type":"image","data":"`+b64+`","mimeType":"image/png"}]}}`,
	)

	wire, err := json.Marshal(upd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), b64) {
		t.Fatalf("base64 image rode the event: %s", wire)
	}
	if !strings.Contains(string(wire), "/api/tool-images/abc123") {
		t.Fatalf("event lacks the stored image's URL: %s", wire)
	}

	if len(host.stored) != 1 || string(host.stored[0]) != string(png) {
		t.Fatalf("host stored %q, want the decoded image bytes", host.stored)
	}
	if len(upd.Content) != 2 || upd.Content[0].Type != "text" || upd.Content[1].Type != "image" {
		t.Fatalf("content = %+v, want the text then the image, in pi's order", upd.Content)
	}
	if upd.Status != proto.StatusCompleted {
		t.Fatalf("status = %q, want completed", upd.Status)
	}
}

func TestImageResultWithoutStoreBecomesText(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("pixels"))
	upd := runToolTurn(t, fakeHost{},
		`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"read","isError":false,"result":{"content":[{"type":"image","data":"`+b64+`","mimeType":"image/png"}]}}`,
	)
	wire, _ := json.Marshal(upd)
	if strings.Contains(string(wire), b64) {
		t.Fatalf("base64 image rode the event: %s", wire)
	}
	if len(upd.Content) != 1 || upd.Content[0].Type != "text" {
		t.Fatalf("content = %+v, want one text placeholder", upd.Content)
	}
}

const piPatch = "--- src/a.ts\n+++ src/a.ts\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"

func TestEditResultBecomesDiff(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"path", `{"path":"src/a.ts","edits":[{"oldText":"two","newText":"TWO"}]}`},
		{"legacy file_path", `{"file_path":"src/a.ts","oldText":"two","newText":"TWO"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			details, _ := json.Marshal(map[string]any{"diff": "-2 two\n+2 TWO", "patch": piPatch, "firstChangedLine": 2})
			upd := runToolTurn(t, fakeHost{},
				`{"type":"tool_execution_start","toolCallId":"call_1","toolName":"edit","args":`+tc.args+`}`,
				`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"edit","isError":false,"result":{"content":[{"type":"text","text":"Successfully replaced 1 block(s) in src/a.ts."}],"details":`+string(details)+`}}`,
			)
			var diff *proto.ToolContent
			for i := range upd.Content {
				if upd.Content[i].Type == "diff" {
					diff = &upd.Content[i]
				}
			}
			if diff == nil {
				t.Fatalf("no diff in %+v", upd.Content)
			}
			if diff.Path != "src/a.ts" || diff.Text != piPatch {
				t.Fatalf("diff = %+v, want path src/a.ts and pi's patch", *diff)
			}
		})
	}
}

func TestErroredEditHasNoDiff(t *testing.T) {
	upd := runToolTurn(t, fakeHost{},
		`{"type":"tool_execution_start","toolCallId":"call_1","toolName":"edit","args":{"path":"src/a.ts","edits":[{"oldText":"nope","newText":"x"}]}}`,
		`{"type":"tool_execution_end","toolCallId":"call_1","toolName":"edit","isError":true,"result":{"content":[{"type":"text","text":"Could not find the exact text in src/a.ts."}],"details":{"patch":"`+strings.ReplaceAll(piPatch, "\n", `\n`)+`"}}}`,
	)
	if upd.Status != proto.StatusFailed {
		t.Fatalf("status = %q, want failed", upd.Status)
	}
	for _, c := range upd.Content {
		if c.Type == "diff" {
			t.Fatalf("errored edit produced a diff: %+v", upd.Content)
		}
	}
}
