package codexapp

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

// serverConn pairs a client jsonrpc.Conn with an in-memory server whose handler
// records the last request it saw and replies with the given result.
type recorder struct {
	mu     sync.Mutex
	method string
	params json.RawMessage
	counts map[string]int
}

func (r *recorder) last() (string, json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.method, r.params
}

func (r *recorder) count(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[method]
}

func pairedConn(t *testing.T, reply map[string]any) (*jsonrpc.Conn, *recorder) {
	t.Helper()
	rec := &recorder{counts: map[string]int{}}
	// client writes -> server reads; server writes -> client reads.
	sr, cw := io.Pipe()
	cr, sw := io.Pipe()
	handler := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		rec.mu.Lock()
		rec.method = method
		rec.params = params
		rec.counts[method]++
		rec.mu.Unlock()
		if r, ok := reply[method]; ok {
			return r, nil
		}
		return map[string]any{}, nil
	}
	_ = jsonrpc.NewConn(sr, sw, handler, nil)
	client := jsonrpc.NewConn(cr, cw, nil, nil)
	t.Cleanup(func() { _ = cw.Close(); _ = sw.Close() })
	return client, rec
}

// TestPromptCapturesServerTurnIDForCancel guards the fix for the ChatGPT stop
// button: turn/start returns codex's turn id, and turn/interrupt must carry both
// threadId and that turnId or it is a silent no-op.
func TestPromptCapturesServerTurnIDForCancel(t *testing.T) {
	conn, rec := pairedConn(t, map[string]any{
		"turn/start": map[string]any{"turn": map[string]any{"id": "codex-turn-42"}},
	})
	s := &session{conn: conn, threadID: "thread-1"}

	if err := s.Prompt(context.Background(), adapter.PromptInput{TurnID: "omniplex-turn", Text: "hi"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if s.serverTurnID != "codex-turn-42" {
		t.Fatalf("serverTurnID not captured: %q", s.serverTurnID)
	}

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	method, params := rec.last()
	if method != "turn/interrupt" {
		t.Fatalf("Cancel sent %q, want turn/interrupt", method)
	}
	var got struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if err := json.Unmarshal(params, &got); err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "thread-1" || got.TurnID != "codex-turn-42" {
		t.Fatalf("turn/interrupt params = %+v, want thread-1 / codex-turn-42", got)
	}
}

// TestCancelWithoutActiveTurnIsNoop avoids sending an interrupt with an empty
// turn id, which codex rejects as invalid params.
func TestCancelWithoutActiveTurnIsNoop(t *testing.T) {
	conn, rec := pairedConn(t, nil)
	s := &session{conn: conn, threadID: "thread-1"}
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if method, _ := rec.last(); method != "" {
		t.Fatalf("Cancel sent %q with no active turn, want nothing", method)
	}
}

// TestDoubleCancelIsCoalesced guards against a double-clicked stop button:
// codex leaves a repeated interrupt of the same turn pending forever, and Cancel
// runs on the session actor goroutine, so a second interrupt must not be sent.
func TestDoubleCancelIsCoalesced(t *testing.T) {
	conn, rec := pairedConn(t, nil)
	s := &session{conn: conn, threadID: "thread-1", serverTurnID: "codex-turn-42"}
	for i := 0; i < 3; i++ {
		if err := s.Cancel(context.Background()); err != nil {
			t.Fatalf("Cancel #%d: %v", i, err)
		}
	}
	if n := rec.count("turn/interrupt"); n != 1 {
		t.Fatalf("turn/interrupt sent %d times, want 1", n)
	}
}

func TestTokenUsageUsesCodexModelContextWindow(t *testing.T) {
	s := &session{events: make(chan proto.Emission, 1), done: make(chan struct{})}
	s.handleNotification("thread/tokenUsage/updated", json.RawMessage(`{
		"threadId":"thread-1","turnId":"turn-1","tokenUsage":{
			"total":{"inputTokens":72000,"cachedInputTokens":45000,"cacheWriteInputTokens":123,"outputTokens":500},
			"last":{"inputTokens":52000,"cachedInputTokens":45000,"cacheWriteInputTokens":0,"outputTokens":1000,"totalTokens":53000},
			"modelContextWindow":114000
		}
	}`))

	e := <-s.events
	if e.Type != proto.UsageUpdated {
		t.Fatalf("event type = %q, want %q", e.Type, proto.UsageUpdated)
	}
	u, ok := e.Payload.(proto.UsageUpdatedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want UsageUpdatedPayload", e.Payload)
	}
	if u.ContextUsed != 53000 || u.ContextWindow != 114000 {
		t.Fatalf("context = %d / %d, want 53000 / 114000", u.ContextUsed, u.ContextWindow)
	}
	wantPct := float64(53000) / 114000 * 100
	if u.ContextPct != wantPct {
		t.Fatalf("context pct = %v, want %v", u.ContextPct, wantPct)
	}
}

type elicitHost struct {
	request adapter.ElicitationRequest
	result  adapter.ElicitationResult
}

func (h *elicitHost) RequestPermission(context.Context, adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	return adapter.PermissionOutcome{}, nil
}
func (h *elicitHost) Elicit(_ context.Context, req adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	h.request = req
	return h.result, nil
}
func (*elicitHost) Logf(string, ...any) {}

func TestRequestUserInputIsRoutedThroughDurableElicitation(t *testing.T) {
	host := &elicitHost{result: adapter.ElicitationResult{
		Action: "accept", Value: json.RawMessage(`{"colour":"Blue"}`),
	}}
	s := &session{host: host}
	got, err := s.handleRequest(context.Background(), "item/tool/requestUserInput", json.RawMessage(`{
		"turnId":"turn-1","threadId":"thread-1","itemId":"item-1","isBlocking":true,
		"questions":[{"id":"colour","header":"Colour","question":"Pick one","options":[{"label":"Blue","description":"Cool"}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if host.request.TurnID != "turn-1" || len(host.request.Schema) == 0 {
		t.Fatalf("elicitation request was not canonicalised: %+v", host.request)
	}
	blob, _ := json.Marshal(got)
	if string(blob) != `{"answers":{"colour":{"answers":["Blue"]}}}` {
		t.Fatalf("response=%s", blob)
	}
}

func TestMCPElicitationIsRoutedThroughHost(t *testing.T) {
	host := &elicitHost{result: adapter.ElicitationResult{
		Action: "accept", Value: json.RawMessage(`{"name":"Ada"}`),
	}}
	s := &session{host: host}
	got, err := s.handleRequest(context.Background(), "mcpServer/elicitation/request", json.RawMessage(`{
		"threadId":"thread-1","turnId":"turn-1","serverName":"demo","mode":"form",
		"message":"Your name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(got)
	if string(blob) != `{"action":"accept","content":{"name":"Ada"}}` {
		t.Fatalf("response=%s", blob)
	}
}

// TestTrustArgsGrantsProjectLocalConfig guards the fix for a codex session
// silently losing a repository's own `.codex` agents, hooks and exec policies:
// codex disables them until the folder is trusted, and app-server cannot ask a
// human the way the CLI does.
func TestTrustArgsGrantsProjectLocalConfig(t *testing.T) {
	args := trustArgs("/home/a/code/zero8/.worktrees/feature-x")
	want := []string{"-c", `projects."/home/a/code/zero8/.worktrees/feature-x".trust_level="trusted"`}
	if len(args) != len(want) {
		t.Fatalf("trustArgs = %q, want %q", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("trustArgs = %q, want %q", args, want)
		}
	}
}

// TestTrustArgsSkipsAnEmptyCwd keeps a session with no directory from spawning
// an app-server with a malformed override rather than none at all.
func TestTrustArgsSkipsAnEmptyCwd(t *testing.T) {
	if args := trustArgs(""); args != nil {
		t.Fatalf("trustArgs(\"\") = %q, want nil", args)
	}
}
