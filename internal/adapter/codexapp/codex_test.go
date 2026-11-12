package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	calls  []recordedCall
}

type recordedCall struct {
	method string
	params json.RawMessage
}

// interrupts returns every turn/interrupt seen so far as threadId → turnId.
func (r *recorder) interrupts(t *testing.T) map[string]string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for _, c := range r.calls {
		if c.method != "turn/interrupt" {
			continue
		}
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		if err := json.Unmarshal(c.params, &p); err != nil {
			t.Fatal(err)
		}
		if prev, dup := out[p.ThreadID]; dup {
			t.Fatalf("thread %s interrupted twice (%s, then %s)", p.ThreadID, prev, p.TurnID)
		}
		out[p.ThreadID] = p.TurnID
	}
	return out
}

// sent is the params of the last request for method. Unlike last, it holds
// when a background request (the quota read after a turn) lands in between.
func (r *recorder) sent(method string) json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.calls) - 1; i >= 0; i-- {
		if r.calls[i].method == method {
			return r.calls[i].params
		}
	}
	return nil
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
		rec.calls = append(rec.calls, recordedCall{method, params})
		rec.mu.Unlock()
		if r, ok := reply[method]; ok {
			// A func answers from the request, and may push notifications
			// first, the way codex writes them ahead of a response.
			if f, isFunc := r.(func(json.RawMessage) (any, error)); isFunc {
				return f(params)
			}
			// An error in the reply map is a refusal, not a result.
			if err, isErr := r.(error); isErr {
				return nil, err
			}
			return r, nil
		}
		return map[string]any{}, nil
	}
	_ = jsonrpc.NewConn(sr, sw, handler, nil, nil)
	client := jsonrpc.NewConn(cr, cw, nil, nil, nil)
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
	s := &session{host: host, turnID: "omniplex-turn"}
	got, err := s.handleRequest(context.Background(), "item/tool/requestUserInput", json.RawMessage(`{
		"turnId":"codex-turn","threadId":"thread-1","itemId":"item-1","isBlocking":true,
		"questions":[{"id":"colour","header":"Colour","question":"Pick one","options":[{"label":"Blue","description":"Cool"}]}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	// Omniplex's turn id, which the projection settles pending requests by.
	if host.request.TurnID != "omniplex-turn" || len(host.request.Schema) == 0 {
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
	s := &session{host: host, turnID: "omniplex-turn"}
	got, err := s.handleRequest(context.Background(), "mcpServer/elicitation/request", json.RawMessage(`{
		"threadId":"thread-1","turnId":"codex-turn","serverName":"demo","mode":"form",
		"message":"Your name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if host.request.TurnID != "omniplex-turn" {
		t.Fatalf("elicitation turn=%q, want omniplex's", host.request.TurnID)
	}
	blob, _ := json.Marshal(got)
	if string(blob) != `{"action":"accept","content":{"name":"Ada"}}` {
		t.Fatalf("response=%s", blob)
	}
}

// permissionHost answers every permission request with a fixed outcome, or
// fails it when err is set.
type permissionHost struct {
	elicitHost
	outcome string
	err     error
	request adapter.PermissionRequest
}

func (h *permissionHost) RequestPermission(_ context.Context, req adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	h.request = req
	return adapter.PermissionOutcome{Outcome: h.outcome}, h.err
}

// TestPermissionsApprovalResponse checks the reply against codex's
// PermissionsRequestApprovalResponse: {permissions: GrantedPermissionProfile,
// scope: "turn"|"session"}. Allowing grants exactly what was asked for; anything
// else grants nothing.
func TestPermissionsApprovalResponse(t *testing.T) {
	params := json.RawMessage(`{
		"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","cwd":"/repo","startedAtMs":1,
		"reason":"Needs network to fetch deps",
		"permissions":{"network":{"enabled":true},"fileSystem":{"write":["/tmp/cache"]}}
	}`)
	requested := `{"network":{"enabled":true},"fileSystem":{"write":["/tmp/cache"]}}`
	for _, tc := range []struct {
		name    string
		outcome string
		err     error
		want    string
	}{
		{"allow", proto.OutcomeAllowOnce, nil, `{"permissions":` + requested + `,"scope":"turn"}`},
		{"allow always", proto.OutcomeAllowAlways, nil, `{"permissions":` + requested + `,"scope":"session"}`},
		{"deny", proto.OutcomeRejectOnce, nil, `{"permissions":{},"scope":"turn"}`},
		{"cancelled", proto.OutcomeCancelled, nil, `{"permissions":{},"scope":"turn"}`},
		{"host error", "", errors.New("host gone"), `{"permissions":{},"scope":"turn"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &permissionHost{outcome: tc.outcome, err: tc.err}
			s := &session{host: host}
			got, err := s.handleRequest(context.Background(), "item/permissions/requestApproval", params)
			if err != nil {
				t.Fatal(err)
			}
			blob, _ := json.Marshal(got)
			if string(blob) != tc.want {
				t.Fatalf("response=%s, want %s", blob, tc.want)
			}
			if host.request.Title != "Needs network to fetch deps" || host.request.ToolCallID != "item-1" {
				t.Fatalf("request=%+v", host.request)
			}
		})
	}
}

// A request without a profile still gets an object back: permissions is
// required and must not be null.
func TestPermissionsApprovalWithoutProfileGrantsEmptyObject(t *testing.T) {
	s := &session{host: &permissionHost{outcome: proto.OutcomeAllowOnce}}
	got, err := s.handleRequest(context.Background(), "item/permissions/requestApproval",
		json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(got)
	if string(blob) != `{"permissions":{},"scope":"turn"}` {
		t.Fatalf("response=%s", blob)
	}
}

func TestCommandApprovalDecision(t *testing.T) {
	for outcome, want := range map[string]string{
		proto.OutcomeAllowOnce:   "accept",
		proto.OutcomeAllowAlways: "acceptForSession",
		proto.OutcomeRejectOnce:  "decline",
		proto.OutcomeCancelled:   "cancel",
	} {
		s := &session{host: &permissionHost{outcome: outcome}}
		got, err := s.handleRequest(context.Background(), "item/commandExecution/requestApproval",
			json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","command":"curl example.com\nmore"}`))
		if err != nil {
			t.Fatal(err)
		}
		blob, _ := json.Marshal(got)
		if string(blob) != `{"decision":"`+want+`"}` {
			t.Fatalf("%s: response=%s", outcome, blob)
		}
	}
}

// waitingHost holds every prompt open until its context ends, the way a human
// who never answers does, and reports when each call starts and is released.
type waitingHost struct {
	entered, released chan struct{}
}

func (h *waitingHost) RequestPermission(ctx context.Context, _ adapter.PermissionRequest) (adapter.PermissionOutcome, error) {
	h.entered <- struct{}{}
	<-ctx.Done()
	h.released <- struct{}{}
	return adapter.PermissionOutcome{Outcome: proto.OutcomeCancelled}, ctx.Err()
}
func (h *waitingHost) Elicit(ctx context.Context, _ adapter.ElicitationRequest) (adapter.ElicitationResult, error) {
	h.entered <- struct{}{}
	<-ctx.Done()
	h.released <- struct{}{}
	return adapter.ElicitationResult{Action: "cancel"}, ctx.Err()
}
func (*waitingHost) Logf(string, ...any) {}

// TestResolvedRequestReleasesItsPrompt is issue #253: codex settles a request
// itself when its turn ends, and the prompt it withdrew stayed pending forever.
// The resolution names a subagent's thread, which must not route it away from
// the connection that owns the request id.
func TestResolvedRequestReleasesItsPrompt(t *testing.T) {
	for _, tc := range []struct{ name, request string }{
		{"approval", `{"jsonrpc":"2.0","id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1","itemId":"c1","command":"rm -rf /"}}`},
		{"user input", `{"jsonrpc":"2.0","id":7,"method":"item/tool/requestUserInput","params":{"threadId":"thread-1","questions":[{"id":"q","question":"Sure?"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &waitingHost{entered: make(chan struct{}, 1), released: make(chan struct{}, 1)}
			// peer writes -> session reads; session writes -> peer reads.
			sessionRead, peerWrite := io.Pipe()
			peerRead, sessionWrite := io.Pipe()
			s := &session{host: host, threadID: "thread-1", turnID: "omniplex-turn"}
			s.conn = jsonrpc.NewConn(sessionRead, sessionWrite, s.handleRequest, s.handleNotification, nil)
			t.Cleanup(func() { _ = peerWrite.Close(); _ = peerRead.Close() })

			send := func(line string) {
				t.Helper()
				if _, err := io.WriteString(peerWrite, line+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			wait := func(ch chan struct{}, what string) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(5 * time.Second):
					t.Fatalf("prompt never %s", what)
				}
			}

			send(tc.request)
			wait(host.entered, "reached the host")
			send(`{"jsonrpc":"2.0","method":"serverRequest/resolved","params":{"threadId":"subagent-9","requestId":7}}`)
			wait(host.released, "released")

			// Codex is not waiting on 7, so the first answer it reads is the
			// next request's.
			send(`{"jsonrpc":"2.0","id":8,"method":"no/such/method"}`)
			line, err := bufio.NewReader(peerRead).ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			var got jsonrpc.Message
			if err := json.Unmarshal(line, &got); err != nil {
				t.Fatal(err)
			}
			if string(got.ID) != "8" {
				t.Fatalf("first response was %s, want id 8 with no answer to the withdrawn 7", line)
			}
		})
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
	// A connection, because a turn of ours ending reads the quota off it.
	conn, _ := pairedConn(t, nil)
	return &session{
		conn:     conn,
		threadID: "thread-1",
		events:   make(chan proto.Emission, 256),
		done:     make(chan struct{}),
		streamed: map[string]bool{},
		host:     &elicitHost{},
	}
}

// TestSubagentStreamDoesNotOpenATurn is the stop button bug: interrupting this
// thread does not stop a subagent it spawned, so the subagent's stream arrives
// after the turn is cancelled. Opening a turn for it made the session look like
// it had restarted the work, and that turn could not be stopped because its
// codex turn id belongs to another thread. A thread no spawn named is not a
// job either, so its output goes nowhere.
func TestSubagentStreamDoesNotOpenATurn(t *testing.T) {
	s := subagentSession(t)
	s.handleNotification("item/agentMessage/delta", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","itemId":"msg-1","delta":"I'll trace"}`))
	s.handleNotification("item/started", json.RawMessage(
		`{"threadId":"subagent-9","turnId":"subagent-turn","item":{"type":"commandExecution","id":"c1","command":"ls"}}`))

	if got := drain(s); len(got) != 0 {
		t.Fatalf("unregistered thread output emitted %d events, want none: %+v", len(got), got)
	}
	if s.turnID != "" || s.serverTurnID != "" {
		t.Fatalf("subagent opened a turn: turnID=%q serverTurnID=%q", s.turnID, s.serverTurnID)
	}
}

// TestSubagentTurnCompletionDoesNotCloseOurTurn: a subagent finishing is not
// this session finishing, and the stop button lives on the difference. Its
// token meter is its job's, not this session's.
func TestSubagentTurnCompletionDoesNotCloseOurTurn(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "omniplex-turn", "codex-turn-42"
	spawn(s, "spawn-1", "look around", "subagent-9")
	drain(s)
	s.handleNotification("turn/completed", json.RawMessage(
		`{"threadId":"subagent-9","turn":{"id":"subagent-turn","status":"completed"}}`))
	s.handleNotification("thread/tokenUsage/updated", json.RawMessage(
		`{"threadId":"subagent-9","tokenUsage":{"total":{"inputTokens":10},"last":{"inputTokens":10},"contextWindow":100}}`))

	for _, e := range drain(s) {
		if e.Type == proto.TurnFinished || e.Type == proto.UsageUpdated {
			t.Fatalf("subagent thread state leaked into this session: %+v", e)
		}
	}
	if s.turnID != "omniplex-turn" || s.serverTurnID != "codex-turn-42" {
		t.Fatalf("our turn was touched by the subagent: %q / %q", s.turnID, s.serverTurnID)
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

// fakeAppServer stands in for `codex app-server`: it records every line it
// reads under dir and answers each request with a thread, or refuses
// skills/extraRoots/set when REFUSE_SKILLS is set, as a codex without it does.
func fakeAppServer(t *testing.T, dir string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
DIR=%q
while IFS= read -r line; do
  printf '%%s\n' "$line" >> "$DIR/lines"
  id=$(printf '%%s' "$line" | sed -n 's/^{"jsonrpc":"2.0","id":\([0-9]*\),.*/\1/p')
  [ -z "$id" ] && continue
  case "$line" in
    *'"method":"skills/extraRoots/set"'*)
      if [ -n "$REFUSE_SKILLS" ]; then
        printf '{"jsonrpc":"2.0","id":%%s,"error":{"code":-32601,"message":"unknown method"}}\n' "$id"
        continue
      fi
      ;;
  esac
  printf '{"jsonrpc":"2.0","id":%%s,"result":{"thread":{"id":"th-1"}}}\n' "$id"
done
`, dir)
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// A project's private skills folder reaches codex before its thread starts or
// resumes, so the first turn already sees them; a codex that refuses the
// request still starts, and a session with none sends nothing.
func TestSkillDirsReachCodexBeforeTheThread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	skills := []string{"/home/p/Omniplex/bowerbird/.agents/skills"}
	for _, tc := range []struct {
		name   string
		o      adapter.CreateOptions
		thread string
		want   []string
	}{
		{"start", adapter.CreateOptions{SkillDirs: skills}, "thread/start", nil},
		// omniplex's bundled plugin has no plugin host in codex: its skills
		// folder is one more root.
		{"plugin", adapter.CreateOptions{SkillDirs: skills, Plugins: []string{"/data/plugin"}}, "thread/start", append(skills, "/data/plugin/skills")},
		{"plugin only", adapter.CreateOptions{Plugins: []string{"/data/plugin"}}, "thread/start", []string{"/data/plugin/skills"}},
		{"resume", adapter.CreateOptions{SkillDirs: skills, Resume: true, HarnessSessionID: "th-old"}, "thread/resume", nil},
		{"refused", adapter.CreateOptions{SkillDirs: skills, Env: map[string]string{"REFUSE_SKILLS": "1"}}, "thread/start", nil},
		{"none", adapter.CreateOptions{}, "thread/start", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.o.Cwd = dir
			s, err := New(fakeAppServer(t, dir)).CreateSession(context.Background(), &elicitHost{}, tc.o)
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			_ = s.Close()

			raw, err := os.ReadFile(filepath.Join(dir, "lines"))
			if err != nil {
				t.Fatal(err)
			}
			var methods []string
			var roots struct {
				ExtraRoots []string `json:"extraRoots"`
			}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var m struct {
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("line %q: %v", line, err)
				}
				methods = append(methods, m.Method)
				if m.Method == "skills/extraRoots/set" {
					if err := json.Unmarshal(m.Params, &roots); err != nil {
						t.Fatal(err)
					}
				}
			}
			set, thread := slices.Index(methods, "skills/extraRoots/set"), slices.Index(methods, tc.thread)
			if thread < 0 {
				t.Fatalf("no %s in %v", tc.thread, methods)
			}
			want := tc.want
			if want == nil {
				want = tc.o.SkillDirs
			}
			if len(want) == 0 {
				if set >= 0 {
					t.Fatalf("a session with no skill dirs set extra roots: %v", methods)
				}
				return
			}
			if set < 0 || set > thread {
				t.Fatalf("extra roots not set before %s: %v", tc.thread, methods)
			}
			if !slices.Equal(roots.ExtraRoots, want) {
				t.Fatalf("extraRoots = %v, want %v", roots.ExtraRoots, want)
			}
		})
	}
}

// Omniplex's instructions reach codex as developer instructions on the call
// that opens the thread, whether it starts or resumes, so codex keeps its own
// base prompt; a session with none sends no field at all.
func TestInstructionsReachCodexAsDeveloperInstructions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in codex is a shell script")
	}
	for _, tc := range []struct {
		name   string
		o      adapter.CreateOptions
		thread string
	}{
		{"start", adapter.CreateOptions{Instructions: "be brief"}, "thread/start"},
		{"resume", adapter.CreateOptions{Instructions: "be brief", Resume: true, HarnessSessionID: "th-old"}, "thread/resume"},
		{"none", adapter.CreateOptions{}, "thread/start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.o.Cwd = dir
			s, err := New(fakeAppServer(t, dir)).CreateSession(context.Background(), &elicitHost{}, tc.o)
			if err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			_ = s.Close()

			raw, err := os.ReadFile(filepath.Join(dir, "lines"))
			if err != nil {
				t.Fatal(err)
			}
			var params map[string]any
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				var m struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("line %q: %v", line, err)
				}
				if m.Method == tc.thread {
					params = m.Params
				}
			}
			if params == nil {
				t.Fatalf("no %s sent", tc.thread)
			}
			if _, replaced := params["baseInstructions"]; replaced {
				t.Fatalf("%s replaced codex's base prompt: %v", tc.thread, params)
			}
			got, sent := params["developerInstructions"]
			if tc.o.Instructions == "" {
				if sent {
					t.Fatalf("developerInstructions sent with none given: %v", got)
				}
				return
			}
			if got != tc.o.Instructions {
				t.Fatalf("developerInstructions = %v, want %q", got, tc.o.Instructions)
			}
		})
	}
}

// stopSession is a session wired to a fake app-server, with somewhere to put
// what it emits.
func stopSession(t *testing.T, reply map[string]any) (*session, *recorder) {
	t.Helper()
	conn, rec := pairedConn(t, reply)
	s := subagentSession(t)
	s.conn = conn
	return s, rec
}

func notify(s *session, method, params string) {
	s.handleNotification(method, json.RawMessage(params))
}

// finishedTurns returns the turn.finished payloads among the emissions.
func finishedTurns(got []proto.Emission) []proto.TurnFinishedPayload {
	var out []proto.TurnFinishedPayload
	for _, e := range got {
		if e.Type == proto.TurnFinished {
			out = append(out, e.Payload.(proto.TurnFinishedPayload))
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// reviewReply is a fake app-server's review/start, answered as codex answers
// it: with one turn, while it runs and will only interrupt another, which only
// turn/started names.
func reviewReply() map[string]any {
	return map[string]any{
		"review/start": map[string]any{"turn": map[string]any{"id": "review-turn"}, "reviewThreadId": "thread-1"},
		"turn/interrupt": func(params json.RawMessage) (any, error) {
			var p struct {
				TurnID string `json:"turnId"`
			}
			_ = json.Unmarshal(params, &p)
			if p.TurnID != "running-turn" {
				return nil, fmt.Errorf("expected active turn id %s but found running-turn", p.TurnID)
			}
			return map[string]any{}, nil
		},
	}
}

// turnInterrupts returns the turn ids of every turn/interrupt seen so far, in
// order.
func (r *recorder) turnInterrupts(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if c.method != "turn/interrupt" {
			continue
		}
		var p struct {
			TurnID string `json:"turnId"`
		}
		if err := json.Unmarshal(c.params, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.TurnID)
	}
	return out
}

// Stop reaches a review through the turn codex runs it as, not the one
// review/start answered with, and the review ends when the answered one
// completes.
func TestReviewCanBeStopped(t *testing.T) {
	s, rec := stopSession(t, reviewReply())
	if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "omniplex-turn", Action: "review"}); err != nil {
		t.Fatalf("review: %v", err)
	}
	notify(s, "item/started", `{"threadId":"thread-1","turnId":"review-turn","item":{"type":"userMessage","id":"u1"}}`)
	notify(s, "turn/started", `{"threadId":"thread-1","turn":{"id":"running-turn"}}`)
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := rec.turnInterrupts(t); len(got) != 1 || got[0] != "running-turn" {
		t.Fatalf("interrupts = %v, want running-turn", got)
	}

	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"review-turn","status":"interrupted"}}`)
	done := finishedTurns(drain(s))
	if len(done) != 1 || done[0].TurnID != "omniplex-turn" || done[0].StopReason != proto.StopCancelled {
		t.Fatalf("finished = %+v, want omniplex-turn cancelled", done)
	}
}

// Stop pressed before codex has said which turn the review runs as goes out
// under the answered one and is refused. The review is interrupted once
// turn/started names its turn.
func TestReviewStoppedBeforeItsTurnStartsIsInterruptedWhenItDoes(t *testing.T) {
	s, rec := stopSession(t, reviewReply())
	if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "omniplex-turn", Action: "review"}); err != nil {
		t.Fatalf("review: %v", err)
	}
	if err := s.Cancel(context.Background()); err == nil {
		t.Fatal("Cancel of the answered turn succeeded; the fake should refuse it")
	}
	notify(s, "turn/started", `{"threadId":"thread-1","turn":{"id":"running-turn"}}`)
	waitFor(t, "the late interrupt", func() bool { return len(rec.turnInterrupts(t)) == 2 })
	if got := rec.turnInterrupts(t); got[1] != "running-turn" {
		t.Fatalf("interrupts = %v, want running-turn last", got)
	}
}

// A turn that was not stopped is not interrupted when it starts.
func TestReviewNotStoppedIsLeftRunning(t *testing.T) {
	s, rec := stopSession(t, reviewReply())
	if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "omniplex-turn", Action: "review"}); err != nil {
		t.Fatalf("review: %v", err)
	}
	notify(s, "turn/started", `{"threadId":"thread-1","turn":{"id":"running-turn"}}`)
	time.Sleep(50 * time.Millisecond)
	if got := rec.turnInterrupts(t); len(got) != 0 {
		t.Fatalf("interrupts = %v, want none", got)
	}
}

// A review that finishes before its review/start response is read must not
// have its id resurrected as the running turn's.
func TestReviewFinishedBeforeItsResponseStaysFinished(t *testing.T) {
	var s *session
	s, _ = stopSession(t, map[string]any{
		"review/start": func(json.RawMessage) (any, error) {
			notify(s, "turn/started", `{"threadId":"thread-1","turn":{"id":"review-turn"}}`)
			notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"review-turn","status":"completed"}}`)
			return map[string]any{"turn": map[string]any{"id": "review-turn"}}, nil
		},
	})
	if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "omniplex-turn", Action: "review"}); err != nil {
		t.Fatalf("review: %v", err)
	}
	if s.turnID != "" || s.serverTurnID != "" {
		t.Fatalf("finished review left turnID=%q serverTurnID=%q", s.turnID, s.serverTurnID)
	}
}

// thread/compact/start answers with nothing, so a /compact turn's id comes from
// the notifications that follow it.
func TestCompactCanBeStopped(t *testing.T) {
	for _, tc := range []struct{ name, method, params string }{
		{"turn started", "turn/started", `{"threadId":"thread-1","turn":{"id":"compact-turn"}}`},
		{"item started", "item/started", `{"threadId":"thread-1","turnId":"compact-turn","item":{"type":"contextCompaction","id":"c1"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := stopSession(t, nil)
			if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "omniplex-turn", Action: "compact"}); err != nil {
				t.Fatalf("compact: %v", err)
			}
			notify(s, tc.method, tc.params)
			if err := s.Cancel(context.Background()); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			if got := rec.interrupts(t); got["thread-1"] != "compact-turn" || len(got) != 1 {
				t.Fatalf("interrupts = %v, want thread-1/compact-turn", got)
			}

			notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"compact-turn","status":"interrupted"}}`)
			done := finishedTurns(drain(s))
			if len(done) != 1 || done[0].TurnID != "omniplex-turn" {
				t.Fatalf("finished = %+v, want omniplex-turn", done)
			}
		})
	}
}

// A straggler from the turn that just completed can arrive after the next
// turn opened. Taking its id would aim Stop at a finished turn, and drop the
// open turn's own completion as stale.
func TestStragglerDoesNotLendItsTurnID(t *testing.T) {
	s, _ := stopSession(t, nil)
	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-a","status":"completed"}}`)

	if _, err := s.RunComposerAction(context.Background(), adapter.ComposerActionInput{TurnID: "turn-b", Action: "compact"}); err != nil {
		t.Fatalf("compact: %v", err)
	}
	notify(s, "item/agentMessage/delta", `{"threadId":"thread-1","turnId":"codex-a","itemId":"m1","delta":"late"}`)
	if s.serverTurnID != "" {
		t.Fatalf("straggler's id recorded: %q", s.serverTurnID)
	}
	notify(s, "item/started", `{"threadId":"thread-1","turnId":"codex-b","item":{"type":"contextCompaction","id":"c1"}}`)
	if s.serverTurnID != "codex-b" {
		t.Fatalf("serverTurnID = %q, want codex-b", s.serverTurnID)
	}
}

// Stop ends the subagents working for the stopped turn, not just the thread
// that spawned them. A subagent of an earlier turn, and one whose turn has
// already finished, are left alone.
func TestCancelInterruptsTheTurnsRunningSubagents(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID = "turn-0"
	spawn(s, "spawn-0", "old", "sub-old")
	childTurn(s, "sub-old", "sub-old-turn")

	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	// Its turn heard about before the spawn naming it.
	childTurn(s, "sub-1", "sub-1-turn")
	spawn(s, "spawn-1", "one", "sub-1")
	// Its turn heard about only through an item.
	spawn(s, "spawn-2", "two", "sub-2")
	notify(s, "item/started", `{"threadId":"sub-2","turnId":"sub-2-turn","item":{"type":"commandExecution","id":"c1","command":"ls"}}`)
	spawn(s, "spawn-3", "three", "sub-3")
	childTurn(s, "sub-3", "sub-3-turn")
	childDone(s, "sub-3", "sub-3-turn", "completed")
	// A straggler from sub-3's finished turn does not mark it running again.
	notify(s, "item/completed", `{"threadId":"sub-3","turnId":"sub-3-turn","item":{"type":"agentMessage","id":"m1","text":"done"}}`)

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	want := map[string]string{"thread-1": "codex-a", "sub-1": "sub-1-turn", "sub-2": "sub-2-turn"}
	if got := rec.interrupts(t); !maps.Equal(got, want) {
		t.Fatalf("interrupts = %v, want %v", got, want)
	}

	// A second press does not repeat interrupts codex never answers twice.
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
	if got := rec.interrupts(t); len(got) != len(want) {
		t.Fatalf("second Cancel repeated interrupts: %v", got)
	}
}

// A subagent refusing its interrupt is not this thread's stop failing.
func TestSubagentInterruptFailureDoesNotFailCancel(t *testing.T) {
	s, rec := stopSession(t, map[string]any{
		"turn/interrupt": func(params json.RawMessage) (any, error) {
			if strings.Contains(string(params), `"sub-1"`) {
				return nil, errors.New("no such turn")
			}
			return map[string]any{}, nil
		},
	})
	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	spawn(s, "spawn-1", "one", "sub-1")
	childTurn(s, "sub-1", "sub-1-turn")

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel failed with the subagent's refusal: %v", err)
	}
	if got := rec.interrupts(t); got["thread-1"] != "codex-a" || got["sub-1"] != "sub-1-turn" {
		t.Fatalf("interrupts = %v", got)
	}
	if s.children["sub-1"].interrupting {
		t.Fatal("refused subagent interrupt left claimed; a later stop could not retry it")
	}
}

// With no id for this thread's turn there is nothing of its own to interrupt,
// but its subagents still are.
func TestCancelWithoutServerTurnStillStopsSubagents(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "one", "sub-1")
	childTurn(s, "sub-1", "sub-1-turn")
	s.serverTurnID = "" // the spawn item named it

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	want := map[string]string{"sub-1": "sub-1-turn"}
	if got := rec.interrupts(t); !maps.Equal(got, want) {
		t.Fatalf("interrupts = %v, want %v", got, want)
	}
}

// A subagent of the stopped turn that only starts a turn after Stop is stopped
// as it starts, until a new turn of ours opens and may hand it new work.
func TestSubagentTurnStartingAfterStopIsInterrupted(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	spawn(s, "spawn-1", "one", "sub-1")
	childTurn(s, "sub-1", "sub-1-first")
	childDone(s, "sub-1", "sub-1-first", "completed")

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := rec.interrupts(t); len(got) != 1 {
		t.Fatalf("interrupts = %v, want only this thread's", got)
	}
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-a","status":"interrupted"}}`)

	childTurn(s, "sub-1", "sub-1-late")
	waitFor(t, "the late subagent interrupt", func() bool { return rec.interrupts(t)["sub-1"] == "sub-1-late" })

	// The next turn of ours may reuse the subagent; that is not stopped.
	childDone(s, "sub-1", "sub-1-late", "interrupted")
	s.turnID, s.serverTurnID = "turn-b", "codex-b"
	childTurn(s, "sub-1", "sub-1-reused")
	time.Sleep(50 * time.Millisecond)
	if got := rec.interrupts(t)["sub-1"]; got != "sub-1-late" {
		t.Fatalf("subagent's turn in the next turn of ours was interrupted: %s", got)
	}
}

// A child whose turn started before the spawn naming it arrived, after its
// turn was already stopped, missed Cancel; registering it stops it.
func TestSubagentRegisteredAfterStopIsInterrupted(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	childTurn(s, "sub-1", "sub-1-turn")
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	spawn(s, "spawn-1", "one", "sub-1")
	waitFor(t, "the late subagent interrupt", func() bool { return rec.interrupts(t)["sub-1"] == "sub-1-turn" })
}

// Codex reuses a subagent across turns of ours. One spawned in turn A and
// handed new work in turn B is B's work: Stop during B ends it rather than
// leaving it spending after B is gone. Its output stays under its job.
func TestReusedSubagentIsStoppedWithTheTurnReusingIt(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	spawn(s, "spawn-1", "one", "sub-1")
	childTurn(s, "sub-1", "sub-1-first")
	childDone(s, "sub-1", "sub-1-first", "completed")
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-a","status":"completed"}}`)
	drain(s)

	s.turnID, s.serverTurnID = "turn-b", "codex-b"
	childTurn(s, "sub-1", "sub-1-reused")
	notify(s, "item/agentMessage/delta", `{"threadId":"sub-1","turnId":"sub-1-reused","itemId":"m2","delta":"on it"}`)
	chunks := ofType(drain(s), proto.MessageChunk)
	if len(chunks) != 1 || chunks[0].Payload.(proto.MessageChunkPayload).ParentToolCallID != "spawn-1" {
		t.Fatalf("reused subagent's output = %+v, want one chunk under its spawn", chunks)
	}

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	want := map[string]string{"thread-1": "codex-b", "sub-1": "sub-1-reused"}
	if got := rec.interrupts(t); !maps.Equal(got, want) {
		t.Fatalf("interrupts = %v, want %v", got, want)
	}
}

// A codex that dies on turn/start fails the prompt before watchExit can finish
// the turn, so the prompt's own error has to say why codex is gone.
func TestPromptToADeadCodexSaysWhy(t *testing.T) {
	cmd := exec.Command("sh", "-c", `echo "thread 'main' panicked at codex-rs/core" >&2; exit 101`)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	s := &session{
		conn:     jsonrpc.NewConn(pr, io.Discard, nil, nil, nil),
		proc:     adapter.WatchProcess(cmd, stderr, func(string, ...any) {}, "codex"),
		threadID: "thread-1",
	}
	_ = pw.Close()
	<-s.conn.Done()

	err = s.Prompt(context.Background(), adapter.PromptInput{TurnID: "t1", Text: "hi"})
	want := "codex exited (exit status 101)\nthread 'main' panicked at codex-rs/core"
	if err == nil || err.Error() != want {
		t.Fatalf("Prompt err = %v\nwant:\n%s", err, want)
	}
}
