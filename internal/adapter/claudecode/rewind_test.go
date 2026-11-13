package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

// fakeBridgeEnv names the directory a fake bridge records its configs in.
// Set, it turns this test binary into the bridge (TestFakeBridgeProcess).
const fakeBridgeEnv = "OMNIPLEX_FAKE_BRIDGE"

// failForkEnv makes a fake bridge started on a fork die on boot, the way the
// CLI does when it cannot resume.
const failForkEnv = "OMNIPLEX_FAKE_BRIDGE_FAIL_FORK"

// TestFakeBridgeProcess is the bridge the rewind tests start, not a test. It
// answers every request, and runs each prompt as the CLI would at its
// smallest: the prompt goes into the conversation's transcript under the
// uuid it was stamped with, followed by one answer, then is echoed and
// finished with a result.
func TestFakeBridgeProcess(t *testing.T) {
	dir := os.Getenv(fakeBridgeEnv)
	if dir == "" {
		return
	}
	raw := os.Args[len(os.Args)-1]
	var cfg sidecarConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		os.Exit(3)
	}
	log, _ := os.OpenFile(filepath.Join(dir, "configs"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintln(log, raw)
	_ = log.Close()

	var outMu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		outMu.Lock()
		os.Stdout.Write(append(b, '\n'))
		outMu.Unlock()
	}
	message := func(m map[string]any) {
		send(map[string]any{"jsonrpc": "2.0", "method": "message", "params": map[string]any{"message": m}})
	}
	if cfg.Fork && os.Getenv(failForkEnv) != "" {
		send(map[string]any{"jsonrpc": "2.0", "method": "fatal", "params": map[string]any{"message": "No message found with message.uuid of: " + cfg.ResumeAt}})
		os.Exit(1)
	}
	sid := cfg.SessionID
	if !cfg.Fork && cfg.Resume != "" {
		sid = cfg.Resume
	}
	configDir := filepath.Join(dir, ".claude")
	transcript := filepath.Join(configDir, "projects", nonAlphanumeric.ReplaceAllString(filepath.Clean(cfg.Cwd), "-"), sid+".jsonl")
	last := cfg.ResumeAt

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		var f struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Text string `json:"text"`
				UUID string `json:"uuid"`
			} `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &f) != nil {
			continue
		}
		if len(f.ID) > 0 {
			result := map[string]any{}
			if f.Method == "supportedCommands" {
				result["commands"] = []any{}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": result})
			continue
		}
		if f.Method != "prompt" {
			continue
		}
		var parent any
		if last != "" {
			parent = last
		}
		answer := f.Params.UUID + "-answer"
		_ = os.MkdirAll(filepath.Dir(transcript), 0o700)
		tf, _ := os.OpenFile(transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		for _, e := range []map[string]any{
			{"type": "user", "uuid": f.Params.UUID, "parentUuid": parent, "sessionId": sid},
			{"type": "assistant", "uuid": answer, "parentUuid": f.Params.UUID, "sessionId": sid},
		} {
			b, _ := json.Marshal(e)
			tf.Write(append(b, '\n'))
		}
		_ = tf.Close()
		last = answer
		message(map[string]any{
			"type": "user", "isReplay": true, "uuid": f.Params.UUID, "session_id": sid,
			"message": map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": f.Params.Text}}},
		})
		message(map[string]any{"type": "result", "subtype": "success", "session_id": sid, "stop_reason": "end_turn"})
	}
	os.Exit(0)
}

type fakeBridge struct {
	dir, cwd, configDir string
	s                   *session
}

// startFakeBridge starts a session on this test binary as its bridge, after
// setup (if any) has prepared its directories.
func startFakeBridge(t *testing.T, o adapter.CreateOptions, setup func(*fakeBridge)) *fakeBridge {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake bridge is started by a shell script")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("HOME", dir)
	t.Setenv(fakeBridgeEnv, dir)
	configDir := filepath.Join(dir, ".claude")
	claude := filepath.Join(dir, "claude")
	bridge := filepath.Join(dir, "bridge")
	writeFile(t, claude, "#!/bin/sh\necho 2.0.0\n")
	writeFile(t, bridge, fmt.Sprintf("#!/bin/sh\nexec %q -test.run='^TestFakeBridgeProcess$' -- \"$@\"\n", os.Args[0]))
	for _, p := range []string{claude, bridge} {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cwd := filepath.Join(dir, "work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if o.ThreadID == "" {
		o.ThreadID = uuid.NewString()
	}
	o.Cwd = cwd
	o.Env = map[string]string{"CLAUDE_CONFIG_DIR": configDir}
	f := &fakeBridge{dir: dir, cwd: cwd, configDir: configDir}
	if setup != nil {
		setup(f)
	}
	a := &Adapter{ClaudePath: claude, bundledSidecar: bridge}
	sess, err := a.CreateSession(context.Background(), &fakeHost{}, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	f.s = sess.(*session)
	return f
}

// transcript writes conversation id's transcript: one line per entry, each
// the child of the one before.
func (f *fakeBridge) transcript(t *testing.T, id string, entries ...string) {
	t.Helper()
	var b strings.Builder
	var parent any
	for _, e := range entries {
		line, _ := json.Marshal(map[string]any{"type": "user", "uuid": e, "parentUuid": parent, "sessionId": id})
		b.Write(append(line, '\n'))
		parent = e
	}
	writeFile(t, filepath.Join(f.configDir, "projects", nonAlphanumeric.ReplaceAllString(filepath.Clean(f.cwd), "-"), id+".jsonl"), b.String())
}

// configs is every config a bridge of this session was started with, in
// order.
func (f *fakeBridge) configs(t *testing.T) []sidecarConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, "configs"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []sidecarConfig
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var c sidecarConfig
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// until reads events until one of type typ, and returns it with everything
// read before it.
func (f *fakeBridge) until(t *testing.T, typ string) (proto.Emission, []proto.Emission) {
	t.Helper()
	var before []proto.Emission
	deadline := time.After(10 * time.Second)
	for {
		select {
		case em, open := <-f.s.Events():
			if !open {
				t.Fatalf("events closed waiting for %s; read %v", typ, types(before))
			}
			if em.Type == typ {
				return em, before
			}
			before = append(before, em)
		case <-deadline:
			t.Fatalf("no %s; read %v", typ, types(before))
		}
	}
}

func types(ems []proto.Emission) []string {
	var out []string
	for _, em := range ems {
		out = append(out, em.Type)
	}
	return out
}

// turn runs one prompt to its finish and returns the finish.
func (f *fakeBridge) turn(t *testing.T, id string) proto.TurnFinishedPayload {
	t.Helper()
	if err := f.s.Prompt(context.Background(), adapter.PromptInput{TurnID: id, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	em, _ := f.until(t, proto.TurnFinished)
	p := em.Payload.(proto.TurnFinishedPayload)
	if p.TurnID != id {
		t.Fatalf("finished %s, want %s", p.TurnID, id)
	}
	return p
}

func (f *fakeBridge) id() string {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	return f.s.harnessSessionID
}

func (f *fakeBridge) harnessID(t *testing.T) string {
	t.Helper()
	em, _ := f.until(t, proto.ThreadConfigChanged)
	return em.Payload.(proto.ThreadConfigChangedPayload).HarnessSessionID
}

// Rewinding to a later turn forks the conversation at the last entry before
// it, under a new id, and the session carries on on the fork: the same event
// channel, no turn finished or ended by the swap, and the next turn answered
// by the new bridge. The fork is reported by the id that rebuilds it until
// its first turn is on disk, and by its own id from then on.
func TestRewindForksBeforeTheTurn(t *testing.T) {
	f := startFakeBridge(t, adapter.CreateOptions{}, nil)
	orig := f.harnessID(t)
	if got := f.turn(t, "turn-1").HarnessRef; got != "turn-1" {
		t.Fatalf("turn 1 ref = %q, want its prompt's uuid", got)
	}
	if got := f.turn(t, "turn-2").HarnessRef; got != "turn-2" {
		t.Fatalf("turn 2 ref = %q, want its prompt's uuid", got)
	}

	if err := f.s.Rewind(context.Background(), adapter.RewindTarget{Turn: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	cfgs := f.configs(t)
	if len(cfgs) != 2 {
		t.Fatalf("%d bridges started, want 2", len(cfgs))
	}
	fork := cfgs[1]
	if fork.Resume != orig || fork.ResumeAt != "turn-1-answer" || !fork.Fork || fork.SessionID == "" || fork.SessionID == orig {
		t.Fatalf("fork config = resume %q at %q fork %v session %q; want resume %q at turn-1-answer, forked under a new id",
			fork.Resume, fork.ResumeAt, fork.Fork, fork.SessionID, orig)
	}
	em, before := f.until(t, proto.ThreadConfigChanged)
	if len(before) != 0 {
		t.Fatalf("the rewind emitted %v before its config change", types(before))
	}
	pending := em.Payload.(proto.ThreadConfigChangedPayload).HarnessSessionID
	if c := parseConversation(pending); c.id != fork.SessionID || c.from != orig || c.at != "turn-1-answer" {
		t.Fatalf("reported %q, want the fork of %s at turn-1-answer under %s", pending, orig, fork.SessionID)
	}

	if err := f.s.Prompt(context.Background(), adapter.PromptInput{TurnID: "turn-3", Text: "again"}); err != nil {
		t.Fatalf("the fork's bridge does not take a prompt: %v", err)
	}
	// The first finish since the rewind is this turn's: the swap finished
	// nothing of its own.
	em, before = f.until(t, proto.TurnFinished)
	if p := em.Payload.(proto.TurnFinishedPayload); p.TurnID != "turn-3" || p.HarnessRef != "turn-3" {
		t.Fatalf("finish = %+v, want turn-3 anchored at its prompt", p)
	}
	settled := ""
	for _, b := range before {
		if b.Type == proto.ThreadConfigChanged {
			settled = b.Payload.(proto.ThreadConfigChangedPayload).HarnessSessionID
		}
	}
	if settled != fork.SessionID {
		t.Fatalf("after its first turn the fork reported %q, want its own id %s", settled, fork.SessionID)
	}
}

// Rewinding to the first turn forgets the whole conversation: the new bridge
// starts an empty one under a new id rather than forking at nothing.
func TestRewindToTheFirstTurnStartsEmpty(t *testing.T) {
	f := startFakeBridge(t, adapter.CreateOptions{}, nil)
	f.harnessID(t)
	f.turn(t, "turn-1")
	if err := f.s.Rewind(context.Background(), adapter.RewindTarget{Turn: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	cfgs := f.configs(t)
	fresh := cfgs[len(cfgs)-1]
	if len(cfgs) != 2 || fresh.Resume != "" || fresh.Fork || fresh.SessionID == "" || fresh.SessionID == cfgs[0].SessionID {
		t.Fatalf("configs = %+v, want a second bridge on a new, empty conversation", cfgs)
	}
	if got := parseConversation(f.harnessID(t)); got.id != fresh.SessionID || !got.fresh {
		t.Fatalf("reported %+v, want the new empty conversation %s, pending", got, fresh.SessionID)
	}
}

// A turn the transcript does not have is refused before anything starts: a
// fork at an unknown entry would fail every turn after it.
func TestRewindToAnUnknownTurnChangesNothing(t *testing.T) {
	f := startFakeBridge(t, adapter.CreateOptions{}, nil)
	orig := f.harnessID(t)
	f.turn(t, "turn-1")
	if err := f.s.Rewind(context.Background(), adapter.RewindTarget{Turn: "elsewhere"}); err == nil {
		t.Fatal("rewound to a turn the conversation does not have")
	}
	if n := len(f.configs(t)); n != 1 {
		t.Fatalf("%d bridges started, want the one", n)
	}
	if got := f.id(); got != orig {
		t.Fatalf("harness id = %q, want %q", got, orig)
	}
	f.turn(t, "turn-2")
}

// A fork the CLI will not boot fails the rewind and leaves the session on
// the bridge it had, conversation and all.
func TestRewindThatCannotBootKeepsTheSession(t *testing.T) {
	f := startFakeBridge(t, adapter.CreateOptions{}, nil)
	orig := f.harnessID(t)
	f.turn(t, "turn-1")
	f.turn(t, "turn-2")
	t.Setenv(failForkEnv, "1")
	err := f.s.Rewind(context.Background(), adapter.RewindTarget{Turn: "turn-2"})
	if err == nil || !strings.Contains(err.Error(), "No message found") {
		t.Fatalf("rewind error = %v, want the bridge's own reason", err)
	}
	if got := f.id(); got != orig {
		t.Fatalf("harness id = %q, want %q", got, orig)
	}
	f.turn(t, "turn-3")
}

// A rewind is refused while a turn runs: the conversation is still moving.
func TestRewindRefusedMidTurn(t *testing.T) {
	s := newTestSession()
	s.turnID = "running"
	if err := s.Rewind(context.Background(), adapter.RewindTarget{Turn: "x"}); err == nil {
		t.Fatal("rewound mid-turn")
	}
}

// A restart hands back whatever id the session last reported. A pending
// fork is started again from its recipe; once the fork is on disk it is
// resumed by its own id; an empty one starts empty.
func TestResumeAPendingRewind(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		written bool
		want    sidecarConfig
	}{
		{"fork not yet written", "N;from=O;at=X", false, sidecarConfig{Resume: "O", ResumeAt: "X", Fork: true, SessionID: "N"}},
		{"fork written", "N;from=O;at=X", true, sidecarConfig{Resume: "N"}},
		{"empty not yet written", "N;new", false, sidecarConfig{SessionID: "N"}},
		{"plain", "N", true, sidecarConfig{Resume: "N"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeBridge(t, adapter.CreateOptions{Resume: true, HarnessSessionID: tc.id}, func(f *fakeBridge) {
				f.transcript(t, "O", "X")
				if tc.written {
					f.transcript(t, "N", "Y")
				}
			})
			waitFor(t, func() bool { return len(f.configs(t)) == 1 })
			got := f.configs(t)[0]
			if got.Resume != tc.want.Resume || got.ResumeAt != tc.want.ResumeAt || got.Fork != tc.want.Fork || got.SessionID != tc.want.SessionID {
				t.Fatalf("config = resume %q at %q fork %v session %q, want %+v", got.Resume, got.ResumeAt, got.Fork, got.SessionID, tc.want)
			}
		})
	}
}

// finishRef is the HarnessRef of the one turn.finished among ems.
func finishRef(t *testing.T, ems []proto.Emission) string {
	t.Helper()
	var refs []string
	for _, em := range ems {
		if em.Type == proto.TurnFinished {
			refs = append(refs, em.Payload.(proto.TurnFinishedPayload).HarnessRef)
		}
	}
	if len(refs) != 1 {
		t.Fatalf("%d turn.finished, want 1: %v", len(refs), types(ems))
	}
	return refs[0]
}

func sdkResult(t *testing.T) map[string]json.RawMessage {
	return rawSDK(t, map[string]any{"type": "result", "subtype": "success"})
}

// A prompted turn starts at its prompt, which the CLI echoes once it is in
// the transcript. What arrives before the echo is the tail of the turn
// before (an interrupted turn writes its tool result and notice late), and
// a turn whose prompt never went in has nothing to cut before.
func TestPromptedTurnRefIsItsPrompt(t *testing.T) {
	for _, echoed := range []bool{true, false} {
		s := newTestSession()
		s.turnID = "turn-1"
		s.handleSDKMessage(rawSDK(t, map[string]any{
			"type": "user", "uuid": "late-tool-result",
			"message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "x"}}},
		}))
		if echoed {
			s.handleSDKMessage(rawSDK(t, replay(t, "turn-1", "hi")))
		}
		s.handleSDKMessage(rawSDK(t, map[string]any{"type": "assistant", "uuid": "answer", "message": map[string]any{"content": []any{}}}))
		s.handleSDKMessage(sdkResult(t))
		want := ""
		if echoed {
			want = "turn-1"
		}
		if got := finishRef(t, drainEvents(s)); got != want {
			t.Fatalf("echoed %v: ref = %q, want %q", echoed, got, want)
		}
	}
}

// A turn the CLI started with a steer read after the last turn ended starts
// at that steer.
func TestSteerOpenedTurnRefIsTheSteer(t *testing.T) {
	s := newTestSession()
	s.steers["queued"] = false
	s.handleSDKMessage(rawSDK(t, replay(t, "queued", "also")))
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "assistant", "uuid": "answer", "message": map[string]any{"content": []any{}}}))
	s.handleSDKMessage(sdkResult(t))
	if got := finishRef(t, drainEvents(s)); got != "queued" {
		t.Fatalf("ref = %q, want the steer's uuid", got)
	}
}

// A turn the harness opened by itself starts at its first top-level entry;
// a subagent's messages live in another transcript and do not count. The
// next turn starts over.
func TestHarnessOpenedTurnRefIsItsFirstEntry(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "assistant", "uuid": "sub", "parent_tool_use_id": "task", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "x"}}}}))
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "assistant", "uuid": "first", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "x"}}}}))
	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "assistant", "uuid": "second", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "x"}}}}))
	s.handleSDKMessage(sdkResult(t))
	if got := finishRef(t, drainEvents(s)); got != "first" {
		t.Fatalf("ref = %q, want the turn's first top-level entry", got)
	}

	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "assistant", "uuid": "next", "message": map[string]any{"content": []map[string]any{{"type": "text", "text": "x"}}}}))
	s.handleSDKMessage(sdkResult(t))
	if got := finishRef(t, drainEvents(s)); got != "next" {
		t.Fatalf("second turn ref = %q, want its own first entry", got)
	}
}

// A cancelled turn is finished by Cancel, and its finish carries the ref.
func TestCancelledTurnKeepsItsRef(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	server := jsonrpc.NewConn(serverRead, serverWrite, func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]any{}, nil
	}, nil, nil)
	client := jsonrpc.NewConn(clientRead, clientWrite, nil, nil, nil)
	t.Cleanup(func() {
		_ = clientWrite.Close()
		_ = serverWrite.Close()
		<-server.Done()
	})
	s := newTestSession()
	s.conn = client
	s.turnID = "turn-1"
	s.handleSDKMessage(rawSDK(t, replay(t, "turn-1", "hi")))
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := finishRef(t, drainEvents(s)); got != "turn-1" {
		t.Fatalf("ref = %q, want turn-1", got)
	}
}

// A rewound conversation not yet written is rebuilt from the one it was cut
// from, so an account switch carries that; once written it carries itself.
func TestMoveConversationOfAPendingRewind(t *testing.T) {
	for _, written := range []bool{false, true} {
		top := t.TempDir()
		from, to := filepath.Join(top, "a"), filepath.Join(top, "b")
		cwd, key := "/home/me/app", "-home-me-app"
		writeFile(t, filepath.Join(from, "projects", key, "O.jsonl"), "source")
		if written {
			writeFile(t, filepath.Join(from, "projects", key, "N.jsonl"), "fork")
		}
		err := (&Adapter{}).MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": from}, map[string]string{"CLAUDE_CONFIG_DIR": to}, cwd, "N;from=O;at=X")
		if err != nil {
			t.Fatal(err)
		}
		moved := "O"
		if written {
			moved = "N"
		}
		if !exists(filepath.Join(to, "projects", key, moved+".jsonl")) {
			t.Fatalf("written %v: %s was not moved", written, moved)
		}
	}
	if err := (&Adapter{}).MoveConversation(map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()}, map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()}, "/x", "N;new"); err != nil {
		t.Fatalf("an empty pending conversation has nothing to move, got %v", err)
	}
}
