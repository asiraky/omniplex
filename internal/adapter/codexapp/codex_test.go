package codexapp

import (
	"context"
	"encoding/json"
	"errors"
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
			// An error in the reply map is a refusal, not a result.
			if err, isErr := r.(error); isErr {
				return nil, err
			}
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

func TestFailedCancelCanBeRetried(t *testing.T) {
	conn, rec := pairedConn(t, map[string]any{
		"turn/interrupt": errors.New("temporary interrupt failure"),
	})
	s := &session{conn: conn, threadID: "thread-1", serverTurnID: "codex-turn-42"}
	for i := 0; i < 2; i++ {
		if err := s.Cancel(context.Background()); err == nil {
			t.Fatalf("Cancel #%d succeeded, want the RPC failure", i)
		}
	}
	if n := rec.count("turn/interrupt"); n != 2 {
		t.Fatalf("turn/interrupt sent %d times, want the failed request retried", n)
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

// drain collects every emission a session has queued so far.
func drain(s *session) []proto.Emission {
	var out []proto.Emission
	for {
		select {
		case e := <-s.events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func subagentSession(t *testing.T) *session {
	t.Helper()
	return &session{
		threadID: "thread-1",
		events:   make(chan proto.Emission, 16),
		done:     make(chan struct{}),
		streamed: map[string]bool{},
		host:     &elicitHost{},
	}
}

// TestSubagentStreamDoesNotOpenATurn is the stop button bug: interrupting this
// thread does not stop a subagent it spawned, so the subagent's stream arrives
// after the turn is cancelled. Opening a turn for it made the session look like
// it had restarted the work, and that turn could not be stopped because its
// codex turn id belongs to another thread.
func TestSubagentStreamDoesNotOpenATurn(t *testing.T) {
	s := subagentSession(t)
	s.handleNotification("item/agentMessage/delta", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","itemId":"msg-1","delta":"I'll trace"}`))
	s.handleNotification("item/started", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","item":{"type":"commandExecution","id":"c1","command":"ls"}}`))

	if got := drain(s); len(got) != 0 {
		t.Fatalf("subagent output while idle emitted %d events, want none: %+v", len(got), got)
	}
	if s.turnID != "" || s.serverTurnID != "" {
		t.Fatalf("subagent opened a turn: turnID=%q serverTurnID=%q", s.turnID, s.serverTurnID)
	}
}

// TestSubagentStreamJoinsTheOpenTurn keeps the useful half: while this thread
// is working, a subagent it spawned is its work, and its output belongs in the
// turn that spawned it.
func TestSubagentStreamJoinsTheOpenTurn(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-42"
	s.handleNotification("item/agentMessage/delta", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","itemId":"msg-1","delta":"working"}`))

	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.MessageChunk {
		t.Fatalf("emissions = %+v, want one message chunk", got)
	}
	payload := got[0].Payload.(proto.MessageChunkPayload)
	if payload.TurnID != "omniplex-turn" {
		t.Fatalf("chunk labelled turn %q, want the spawning turn", payload.TurnID)
	}
	if s.serverTurnID != "codex-turn-42" {
		t.Fatalf("serverTurnID overwritten with the subagent's: %q", s.serverTurnID)
	}
}

// TestSubagentTurnCompletionDoesNotCloseOurTurn: a subagent finishing is not
// this session finishing, and the stop button lives on the difference.
func TestSubagentTurnCompletionDoesNotCloseOurTurn(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-42"
	s.handleNotification("turn/completed", json.RawMessage(
		`{"threadId":"subagent-9","turn":{"id":"subagent-turn","status":"completed"}}`))
	s.handleNotification("thread/tokenUsage/updated", json.RawMessage(
		`{"threadId":"subagent-9","tokenUsage":{"total":{"inputTokens":10},"last":{"inputTokens":10},"contextWindow":100}}`))

	if got := drain(s); len(got) != 0 {
		t.Fatalf("subagent thread state leaked into this session: %+v", got)
	}
	if s.turnID != "omniplex-turn" {
		t.Fatalf("our turn was closed by the subagent's completion: %q", s.turnID)
	}
}

// TestFailedInterruptDoesNotWedgeTheStopButton: a refused interrupt is never
// answered by a turn/completed, so nothing else would clear the coalescing
// flag and every later stop would be swallowed.
func TestFailedInterruptDoesNotWedgeTheStopButton(t *testing.T) {
	conn, rec := pairedConn(t, map[string]any{"turn/interrupt": errors.New("no such turn")})
	s := &session{conn: conn, threadID: "thread-1", serverTurnID: "codex-turn-42"}
	if err := s.Cancel(context.Background()); err == nil {
		t.Fatal("Cancel: want the refusal surfaced")
	}
	if s.interrupting {
		t.Fatal("interrupting still set after a refused interrupt")
	}
	if err := s.Cancel(context.Background()); err == nil {
		t.Fatal("second Cancel was coalesced away")
	}
	if n := rec.count("turn/interrupt"); n != 2 {
		t.Fatalf("turn/interrupt sent %d times, want 2", n)
	}
}

// TestSubagentOutlivingItsTurnStaysOutOfTheNextOne: stopping a turn does not
// stop a subagent it spawned, so its stream can still be arriving when the next
// prompt opens a turn. That work belongs to the turn that spawned it, not to
// whatever is running now.
func TestSubagentOutlivingItsTurnStaysOutOfTheNextOne(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "turn-a", "codex-turn-a"
	s.handleNotification("item/agentMessage/delta", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","itemId":"msg-1","delta":"during A"}`))
	if got := drain(s); len(got) != 1 {
		t.Fatalf("subagent output during its own turn = %+v, want one chunk", got)
	}

	// Turn A is stopped and turn B starts while the subagent keeps talking.
	s.turnID, s.serverTurnID = "turn-b", "codex-turn-b"
	s.handleNotification("item/agentMessage/delta", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","itemId":"msg-1","delta":"after A"}`))

	if got := drain(s); len(got) != 0 {
		t.Fatalf("orphaned subagent output leaked into turn B: %+v", got)
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

// TestCollabSpawnOpensAJobRow: the whole point of the polyfill. Codex runs a
// subagent as its own thread and says so through a collab tool call; without
// this, a spawned agent was a tool call that never visibly ended and the
// roster stayed empty.
func TestCollabSpawnOpensAJobRow(t *testing.T) {
	s := subagentSession(t)
	s.handleItem(false, "turn-a", "", json.RawMessage(`{"item":{"type":"collabAgentToolCall","id":"call-1","tool":"spawnAgent","prompt":"trace the leak","receiverThreadIds":["agent-9"]}}`))

	got := drain(s)
	if len(got) != 2 {
		t.Fatalf("emissions = %+v, want the tool call and the job", got)
	}
	if got[0].Type != proto.ToolCallStarted {
		t.Fatalf("first emission = %s, want a tool call", got[0].Type)
	}
	if got[1].Type != proto.JobStarted {
		t.Fatalf("second emission = %s, want job.started", got[1].Type)
	}
	job := got[1].Payload.(proto.JobPayload)
	if job.JobID != "agent-9" || job.Kind != proto.JobAgent || job.ToolCallID != "call-1" {
		t.Fatalf("job = %+v, want the spawned thread tied to its tool call", job)
	}
}

// TestSubagentFinishingClosesItsJob is the bug the user hit: the subagents had
// finished and nothing in omniplex ever said so.
func TestSubagentFinishingClosesItsJob(t *testing.T) {
	s := subagentSession(t)
	s.handleItem(false, "turn-a", "", json.RawMessage(`{"item":{"type":"collabAgentToolCall","id":"call-1","tool":"spawnAgent","receiverThreadIds":["agent-9"]}}`))
	drain(s)

	s.handleItem(true, "turn-a", "", json.RawMessage(`{"item":{"type":"subAgentActivity","id":"act-1","agentThreadId":"agent-9","agentPath":"reviewer","kind":"completed"}}`))

	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.JobFinished {
		t.Fatalf("emissions = %+v, want one job.finished", got)
	}
	if p := got[0].Payload.(proto.JobPayload); p.JobID != "agent-9" || p.Status != proto.JobCompleted {
		t.Fatalf("finish = %+v, want agent-9 completed", p)
	}

	// And only once, however many ways codex says it again.
	s.handleItem(true, "turn-a", "", json.RawMessage(`{"item":{"type":"subAgentActivity","id":"act-1","agentThreadId":"agent-9","kind":"completed"}}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("repeated completion emitted %+v, want nothing", got)
	}
}

// A subagent's thread knows its own context window — the reading the claude
// path cannot produce — and it belongs on that subagent's row.
func TestSubagentTokenUsageLandsOnItsJob(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	s.handleItem(false, "turn-a", "", json.RawMessage(`{"item":{"type":"collabAgentToolCall","id":"call-1","tool":"spawnAgent","receiverThreadIds":["agent-9"]}}`))
	drain(s)

	s.handleSubagentNotification("thread/tokenUsage/updated", "agent-9", "turn-a", json.RawMessage(
		`{"threadId":"agent-9","tokenUsage":{"total":{"inputTokens":800,"outputTokens":200},"last":{"totalTokens":50000},"modelContextWindow":200000}}`))

	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.JobUpdated {
		t.Fatalf("emissions = %+v, want one job.updated", got)
	}
	u := got[0].Payload.(proto.JobPayload).Usage
	if u == nil || u.TotalTokens != 1000 || u.ContextUsed != 50000 || u.ContextWindow != 200000 {
		t.Fatalf("usage = %+v, want 1000 tokens and 50k of a 200k window", u)
	}
}

// A subagent going idle is waiting for its parent's next instruction, not
// done: reaping it there would take the row away mid-conversation.
func TestIdleSubagentIsNotFinished(t *testing.T) {
	s := subagentSession(t)
	s.handleItem(false, "turn-a", "", json.RawMessage(`{"item":{"type":"collabAgentToolCall","id":"call-1","tool":"spawnAgent","receiverThreadIds":["agent-9"]}}`))
	drain(s)

	s.handleSubagentNotification("thread/status/changed", "agent-9", "turn-a", json.RawMessage(
		`{"threadId":"agent-9","status":{"type":"idle","activeFlags":[]}}`))

	for _, e := range drain(s) {
		if e.Type == proto.JobFinished {
			t.Fatalf("idle closed the subagent's row: %+v", e)
		}
	}
	if j := s.agentJobs["agent-9"]; j == nil || j.done {
		t.Fatalf("agent job = %+v, want it still open", j)
	}
}

// Codex's own answer to "are you still working", which is the signal a spinner
// cannot give: busy and stuck-waiting-on-a-human look identical without it.
func TestThreadStatusBecomesActivity(t *testing.T) {
	s := subagentSession(t)
	s.handleNotification("thread/status/changed", json.RawMessage(
		`{"threadId":"thread-1","status":{"type":"active","activeFlags":["waitingOnApproval"]}}`))

	got := drain(s)
	if len(got) != 1 || got[0].Type != proto.ActivityUpdated {
		t.Fatalf("emissions = %+v, want one activity.updated", got)
	}
	p := got[0].Payload.(proto.ActivityUpdatedPayload)
	if p.Activity != "waiting for approval" || !p.Blocked {
		t.Fatalf("activity = %+v, want a blocked wait for approval", p)
	}

	// Level-triggered: codex resends the same status freely, and this log is
	// durable, so only the edge travels.
	s.handleNotification("thread/status/changed", json.RawMessage(
		`{"threadId":"thread-1","status":{"type":"active","activeFlags":["waitingOnApproval"]}}`))
	if got := drain(s); len(got) != 0 {
		t.Fatalf("a resent status emitted %+v, want nothing", got)
	}
}
