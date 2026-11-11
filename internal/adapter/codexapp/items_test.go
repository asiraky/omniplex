package codexapp

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// itemNotification wraps a recorded ThreadItem in the v2 item/started or
// item/completed envelope for this test session's thread and turn.
func itemNotification(turnID, item string) json.RawMessage {
	return json.RawMessage(`{"threadId":"thread-1","turnId":"` + turnID + `","item":` + item + `}`)
}

// openTurn puts the session mid-turn, as turn/start would have left it.
func openTurn(s *session, turnID string) {
	s.mu.Lock()
	s.turnID, s.serverTurnID = turnID, turnID
	s.mu.Unlock()
}

// onlyToolUpdate is the single ToolCallUpdated a notification produced.
func onlyToolUpdate(t *testing.T, got []proto.Emission) proto.ToolCallUpdatedPayload {
	t.Helper()
	if len(got) != 1 || got[0].Type != proto.ToolCallUpdated {
		t.Fatalf("emitted %+v, want one %s", got, proto.ToolCallUpdated)
	}
	return got[0].Payload.(proto.ToolCallUpdatedPayload)
}

// TestCompletedItemStatus guards the status each completed tool card gets. A
// declined command or file change rendered as completed (or failed) though it
// never ran; a failed MCP call rendered "null" under a green tick; an MCP image
// put kilobytes of base64 into the card as text, and a result the tool
// flagged isError showed as a success.
func TestCompletedItemStatus(t *testing.T) {
	const imageData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	tests := []struct {
		name        string
		item        string
		wantStatus  string
		wantText    []string // content texts, in order; nil means no content
		forbid      []string // substrings no content text may contain
		wantMention []string // substrings some content text must contain
	}{
		{
			name:       "command exits zero",
			item:       `{"type":"commandExecution","id":"call_c1","command":"/bin/bash -lc 'go vet ./...'","cwd":"/repo","processId":"41","source":"agent","status":"completed","commandActions":[{"type":"unknown","command":"go vet ./..."}],"aggregatedOutput":"","exitCode":0,"durationMs":812}`,
			wantStatus: proto.StatusCompleted,
		},
		{
			name:       "command completes with a non-zero exit",
			item:       `{"type":"commandExecution","id":"call_c2","command":"/bin/bash -lc 'go test ./...'","cwd":"/repo","processId":"42","source":"agent","status":"completed","commandActions":[],"aggregatedOutput":"FAIL\tpkg\n","exitCode":1,"durationMs":2210}`,
			wantStatus: proto.StatusFailed,
			wantText:   []string{"FAIL\tpkg\n"},
		},
		{
			name:       "command fails",
			item:       `{"type":"commandExecution","id":"call_c3","command":"/bin/bash -lc 'nope'","cwd":"/repo","processId":null,"source":"agent","status":"failed","commandActions":[],"aggregatedOutput":"bash: nope: command not found\n","exitCode":127,"durationMs":9}`,
			wantStatus: proto.StatusFailed,
			wantText:   []string{"bash: nope: command not found\n"},
		},
		{
			name:       "command declined",
			item:       `{"type":"commandExecution","id":"call_c4","command":"/bin/bash -lc 'rm -rf build'","cwd":"/repo","processId":null,"source":"agent","status":"declined","commandActions":[],"aggregatedOutput":null,"exitCode":null,"durationMs":null}`,
			wantStatus: proto.StatusDeclined,
		},
		{
			name:       "file change applied",
			item:       `{"type":"fileChange","id":"call_f1","changes":[{"path":"/repo/main.go","kind":{"type":"update","move_path":null},"diff":"@@ -1 +1 @@\n-a\n+b\n"}],"status":"completed"}`,
			wantStatus: proto.StatusCompleted,
			wantText:   []string{"@@ -1 +1 @@\n-a\n+b\n"},
		},
		{
			name:       "file change failed",
			item:       `{"type":"fileChange","id":"call_f2","changes":[{"path":"/repo/new.go","kind":{"type":"add"},"diff":"package x\n"}],"status":"failed"}`,
			wantStatus: proto.StatusFailed,
			wantText:   []string{"package x\n"},
		},
		{
			name:       "file change declined",
			item:       `{"type":"fileChange","id":"call_f3","changes":[{"path":"/repo/old.go","kind":{"type":"delete"},"diff":""}],"status":"declined"}`,
			wantStatus: proto.StatusDeclined,
			wantText:   []string{""},
		},
		{
			name:       "mcp text result",
			item:       `{"type":"mcpToolCall","id":"call_m1","server":"docs","tool":"lookup","status":"completed","arguments":{"q":"ctx"},"result":{"content":[{"type":"text","text":"context.Context carries deadlines"}],"structuredContent":null},"error":null,"durationMs":120}`,
			wantStatus: proto.StatusCompleted,
			wantText:   []string{"context.Context carries deadlines"},
		},
		{
			name:       "mcp failure shows its error",
			item:       `{"type":"mcpToolCall","id":"call_m2","server":"docs","tool":"lookup","status":"failed","arguments":{},"result":null,"error":{"message":"tool exploded"},"durationMs":30}`,
			wantStatus: proto.StatusFailed,
			wantText:   []string{"tool exploded"},
			forbid:     []string{"null"},
		},
		{
			name:        "mcp image with no host store becomes a placeholder",
			item:        `{"type":"mcpToolCall","id":"call_m3","server":"browser","tool":"screenshot","status":"completed","arguments":{},"result":{"content":[{"type":"image","data":"` + imageData + `","mimeType":"image/png"}]},"error":null,"durationMs":400}`,
			wantStatus:  proto.StatusCompleted,
			forbid:      []string{imageData},
			wantMention: []string{"image not shown"},
		},
		{
			name:        "mcp audio becomes a placeholder",
			item:        `{"type":"mcpToolCall","id":"call_m8","server":"tts","tool":"say","status":"completed","arguments":{},"result":{"content":[{"type":"audio","data":"` + imageData + `","mimeType":"audio/wav"}]},"error":null,"durationMs":400}`,
			wantStatus:  proto.StatusCompleted,
			forbid:      []string{imageData},
			wantMention: []string{"audio/wav"},
		},
		{
			name:       "mcp result the tool flagged isError",
			item:       `{"type":"mcpToolCall","id":"call_m9","server":"browser","tool":"navigate","status":"completed","arguments":{},"result":{"content":[{"type":"text","text":"page not found"}],"isError":true},"error":null,"durationMs":40}`,
			wantStatus: proto.StatusFailed,
			wantText:   []string{"page not found"},
		},
		{
			name:       "mcp structured content when content is empty",
			item:       `{"type":"mcpToolCall","id":"call_m4","server":"db","tool":"count","status":"completed","arguments":{},"result":{"content":[],"structuredContent":{"rows":3}},"error":null,"durationMs":15}`,
			wantStatus: proto.StatusCompleted,
			wantText:   []string{`{"rows":3}`},
		},
		{
			name:       "mcp structured data behind a text summary",
			item:       `{"type":"mcpToolCall","id":"call_m6","server":"db","tool":"query","status":"completed","arguments":{},"result":{"content":[{"type":"text","text":"2 rows returned"}],"structuredContent":{"rows":[1,2]}},"error":null,"durationMs":9}`,
			wantStatus: proto.StatusCompleted,
			wantText:   []string{"2 rows returned", `{"rows":[1,2]}`},
		},
		{
			name:       "mcp structured data mirrored as text is shown once",
			item:       `{"type":"mcpToolCall","id":"call_m7","server":"db","tool":"query","status":"completed","arguments":{},"result":{"content":[{"type":"text","text":"{\n  \"rows\": [1, 2]\n}"}],"structuredContent":{"rows":[1,2]}},"error":null,"durationMs":9}`,
			wantStatus: proto.StatusCompleted,
			wantText:   []string{"{\n  \"rows\": [1, 2]\n}"},
		},
		{
			name:       "mcp completed with no result",
			item:       `{"type":"mcpToolCall","id":"call_m5","server":"db","tool":"ping","status":"completed","arguments":{},"result":null,"error":null,"durationMs":2}`,
			wantStatus: proto.StatusCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := subagentSession(t)
			openTurn(s, "turn-1")
			s.handleNotification("item/completed", itemNotification("turn-1", tt.item))
			up := onlyToolUpdate(t, drain(s))

			if up.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", up.Status, tt.wantStatus)
			}
			var texts []string
			for _, c := range up.Content {
				texts = append(texts, c.Text)
			}
			if tt.wantMention == nil && !slices.Equal(texts, tt.wantText) {
				t.Errorf("content = %q, want %q", texts, tt.wantText)
			}
			for _, bad := range tt.forbid {
				for _, text := range texts {
					if strings.Contains(text, bad) {
						t.Errorf("content %q contains %q", text, bad)
					}
				}
			}
			for _, want := range tt.wantMention {
				if !strings.Contains(strings.Join(texts, "\n"), want) {
					t.Errorf("content %q does not mention %q", texts, want)
				}
			}
		})
	}
}

// TestWebSearchTitle guards the web search card's title. Codex often starts
// the search with an empty query and fills it in by completion, so the card
// read "Search: " forever; the action carries the query or page otherwise.
func TestWebSearchTitle(t *testing.T) {
	tests := []struct {
		name    string
		started string // item/started payload, if any
		item    string // item/completed payload
		want    string
	}{
		{
			name:    "query filled in by completion",
			started: `{"type":"webSearch","id":"ws_1","query":"","action":null}`,
			item:    `{"type":"webSearch","id":"ws_1","query":"golang generics","action":{"type":"search","query":"golang generics","queries":null}}`,
			want:    "Search: golang generics",
		},
		{
			name: "action with a list of queries",
			item: `{"type":"webSearch","id":"ws_2","query":"","action":{"type":"search","query":null,"queries":["go 1.24 release","go iterators"]}}`,
			want: "Search: go 1.24 release, go iterators",
		},
		{
			name: "open page",
			item: `{"type":"webSearch","id":"ws_3","query":"","action":{"type":"openPage","url":"https://go.dev/doc/"}}`,
			want: "Open https://go.dev/doc/",
		},
		{
			name: "find in page without a url",
			item: `{"type":"webSearch","id":"ws_5","query":"","action":{"type":"findInPage","pattern":"GOEXPERIMENT","url":null}}`,
			want: `Find "GOEXPERIMENT"`,
		},
		{
			name: "nothing to go on",
			item: `{"type":"webSearch","id":"ws_4","query":"","action":{"type":"other"}}`,
			want: "Web search",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := subagentSession(t)
			openTurn(s, "turn-1")
			if tt.started != "" {
				s.handleNotification("item/started", itemNotification("turn-1", tt.started))
				drain(s)
			}
			s.handleNotification("item/completed", itemNotification("turn-1", tt.item))
			up := onlyToolUpdate(t, drain(s))
			if up.Status != proto.StatusCompleted || up.Title != tt.want {
				t.Errorf("update = %q %q, want %q %q", up.Status, up.Title, proto.StatusCompleted, tt.want)
			}
		})
	}
}

// TestTokenUsageInputExcludesCache guards the usage arithmetic. Codex counts
// cached and cache-written tokens inside inputTokens; passing that through
// counted the cached share twice wherever input and cache reads are summed.
func TestTokenUsageInputExcludesCache(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    proto.UsageUpdatedPayload
	}{
		{
			name:    "cached and cache-written share removed",
			payload: `{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":10300,"inputTokens":10000,"cachedInputTokens":8000,"cacheWriteInputTokens":500,"outputTokens":300,"reasoningOutputTokens":120},"last":{"totalTokens":50000,"inputTokens":49000,"cachedInputTokens":40000,"outputTokens":1000,"reasoningOutputTokens":0},"modelContextWindow":200000}}`,
			want:    proto.UsageUpdatedPayload{Input: 1500, Output: 300, CacheRead: 8000, CacheWrite: 500, ContextPct: 25, ContextUsed: 50000, ContextWindow: 200000},
		},
		{
			name:    "no cache leaves input alone",
			payload: `{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":4200,"inputTokens":4000,"cachedInputTokens":0,"outputTokens":200,"reasoningOutputTokens":0},"last":{"totalTokens":100000,"inputTokens":99800,"cachedInputTokens":0,"outputTokens":200,"reasoningOutputTokens":0},"modelContextWindow":400000}}`,
			want:    proto.UsageUpdatedPayload{Input: 4000, Output: 200, ContextPct: 25, ContextUsed: 100000, ContextWindow: 400000},
		},
		{
			name:    "cache larger than input clamps to zero",
			payload: `{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":9100,"inputTokens":9000,"cachedInputTokens":9500,"cacheWriteInputTokens":100,"outputTokens":100,"reasoningOutputTokens":0},"last":{"totalTokens":0,"inputTokens":0,"cachedInputTokens":0,"outputTokens":0,"reasoningOutputTokens":0},"modelContextWindow":null}}`,
			want:    proto.UsageUpdatedPayload{Input: 0, Output: 100, CacheRead: 9500, CacheWrite: 100},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := subagentSession(t)
			openTurn(s, "turn-1")
			s.handleNotification("thread/tokenUsage/updated", json.RawMessage(tt.payload))
			got := drain(s)
			if len(got) != 1 || got[0].Type != proto.UsageUpdated {
				t.Fatalf("emitted %+v, want one %s", got, proto.UsageUpdated)
			}
			if u := got[0].Payload.(proto.UsageUpdatedPayload); !reflect.DeepEqual(u, tt.want) {
				t.Errorf("usage = %+v, want %+v", u, tt.want)
			}
		})
	}
}

// TestContextCompactionCountsPerTurn guards repeat compactions. Keying the
// dedupe on the turn id alone swallowed a second compaction in the same turn;
// a transitional server reports each compaction twice (item and notice), and
// those must still be one notice each.
func TestContextCompactionCountsPerTurn(t *testing.T) {
	const (
		item   = "item"
		notice = "notice"
	)
	type signal struct{ kind, turn string }
	tests := []struct {
		name    string
		signals []signal
		want    int
	}{
		{"two items in one turn", []signal{{item, "turn-1"}, {item, "turn-1"}}, 2},
		{"item and notice for each of two", []signal{{item, "turn-1"}, {notice, "turn-1"}, {item, "turn-1"}, {notice, "turn-1"}}, 2},
		{"notice before item for each of two", []signal{{notice, "turn-1"}, {item, "turn-1"}, {notice, "turn-1"}, {item, "turn-1"}}, 2},
		{"notices only", []signal{{notice, "turn-1"}, {notice, "turn-1"}, {notice, "turn-1"}}, 3},
		{"a new turn starts the count again", []signal{{item, "turn-1"}, {notice, "turn-1"}, {item, "turn-2"}, {notice, "turn-2"}}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := subagentSession(t)
			for _, sig := range tt.signals {
				openTurn(s, sig.turn)
				if sig.kind == item {
					s.handleNotification("item/completed", itemNotification(sig.turn, `{"type":"contextCompaction","id":"cmp_`+sig.turn+`"}`))
				} else {
					s.handleNotification("thread/compacted", json.RawMessage(`{"threadId":"thread-1","turnId":"`+sig.turn+`"}`))
				}
			}
			n := 0
			for _, e := range drain(s) {
				if e.Type != proto.ContextCompacted {
					t.Fatalf("unexpected emission %+v", e)
				}
				n++
			}
			if n != tt.want {
				t.Errorf("%d context.compacted, want %d", n, tt.want)
			}
		})
	}
}
