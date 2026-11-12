package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// spawn plays a v1 spawnAgent call on this session's thread: started with no
// receivers, completed naming them.
func spawn(s *session, itemID, prompt string, receivers ...string) {
	spawnOn(s, "thread-1", itemID, prompt, receivers...)
}

func spawnOn(s *session, thread, itemID, prompt string, receivers ...string) {
	item := func(status string, rs []string) string {
		b, _ := json.Marshal(map[string]any{
			"threadId": thread, "turnId": "codex-turn",
			"item": map[string]any{
				"type": "collabAgentToolCall", "id": itemID, "tool": "spawnAgent",
				"status": status, "prompt": prompt, "receiverThreadIds": rs,
			},
		})
		return string(b)
	}
	notify(s, "item/started", item("inProgress", []string{}))
	notify(s, "item/completed", item("completed", receivers))
}

func childTurn(s *session, thread, turn string) {
	notify(s, "turn/started", fmt.Sprintf(`{"threadId":%q,"turn":{"id":%q,"status":"inProgress"}}`, thread, turn))
}

func childDone(s *session, thread, turn, status string) {
	notify(s, "turn/completed", fmt.Sprintf(`{"threadId":%q,"turn":{"id":%q,"status":%q,"error":null}}`, thread, turn, status))
}

func childSays(s *session, thread, itemID, phase, text string) {
	b, _ := json.Marshal(map[string]any{
		"threadId": thread,
		"item":     map[string]any{"type": "agentMessage", "id": itemID, "phase": phase, "text": text},
	})
	notify(s, "item/completed", string(b))
}

func ofType(es []proto.Emission, typ string) []proto.Emission {
	var out []proto.Emission
	for _, e := range es {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func jobs(es []proto.Emission, typ string) []proto.JobPayload {
	var out []proto.JobPayload
	for _, e := range ofType(es, typ) {
		out = append(out, e.Payload.(proto.JobPayload))
	}
	return out
}

func TestSpawnBecomesAToolCallAndAJobPerChild(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "  Read package.json\nthen report", "child-a", "child-b")
	got := drain(s)

	started := ofType(got, proto.ToolCallStarted)
	if len(started) != 1 {
		t.Fatalf("tool calls started = %+v, want one", started)
	}
	call := started[0].Payload.(proto.ToolCallStartedPayload)
	if call.ToolCallID != "spawn-1" || call.Kind != proto.KindAgent || call.TurnID != "turn-a" ||
		call.Title != "Read package.json" || call.ParentToolCallID != "" {
		t.Fatalf("spawn tool call = %+v", call)
	}
	updated := ofType(got, proto.ToolCallUpdated)
	if len(updated) != 1 || updated[0].Payload.(proto.ToolCallUpdatedPayload).Status != proto.StatusCompleted {
		t.Fatalf("spawn completion = %+v", updated)
	}

	js := jobs(got, proto.JobStarted)
	if len(js) != 2 {
		t.Fatalf("jobs started = %+v, want two", js)
	}
	want := map[string]string{"child-a": "spawn-1", "child-b": "spawn-1:child-b"}
	for _, j := range js {
		if j.ToolCallID != want[j.JobID] || j.ParentJobID != "" || j.Kind != proto.JobAgent ||
			j.Status != proto.JobRunning || j.Name != "Read package.json" {
			t.Fatalf("job = %+v", j)
		}
	}

	// A second sighting of the same child is not a second job.
	spawn(s, "spawn-1", "Read package.json", "child-a")
	if js := jobs(drain(s), proto.JobStarted); len(js) != 0 {
		t.Fatalf("re-registering a child started %+v", js)
	}
}

func TestSpawnStatusMapsOntoItsToolCall(t *testing.T) {
	for status, want := range map[string]string{
		"completed":   proto.StatusCompleted,
		"failed":      proto.StatusFailed,
		"interrupted": proto.StatusCancelled,
	} {
		s := subagentSession(t)
		s.turnID = "turn-a"
		notify(s, "item/completed", fmt.Sprintf(
			`{"threadId":"thread-1","item":{"type":"collabAgentToolCall","id":"spawn-1","tool":"spawnAgent","status":%q,"receiverThreadIds":[]}}`, status))
		up := ofType(drain(s), proto.ToolCallUpdated)
		if len(up) != 1 || up[0].Payload.(proto.ToolCallUpdatedPayload).Status != want {
			t.Fatalf("%s: updates = %+v, want status %s", status, up, want)
		}
	}
}

func TestOtherCollabToolsAreIgnored(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	notify(s, "item/started", `{"threadId":"thread-1","item":{"type":"collabAgentToolCall","id":"w1","tool":"wait","status":"inProgress","receiverThreadIds":["child-a"]}}`)
	notify(s, "item/completed", `{"threadId":"thread-1","item":{"type":"collabAgentToolCall","id":"w1","tool":"wait","status":"completed","receiverThreadIds":["child-a"]}}`)
	if got := drain(s); len(got) != 0 {
		t.Fatalf("wait emitted %+v", got)
	}
}

// A subagent outlives the turn that spawned it. Its output keeps arriving
// after that turn ends, and even while a later turn runs; it belongs to the
// job, filed under the spawning turn, and must neither open a turn nor join
// the one running now.
func TestChildOutputIsParentedOnItsSpawn(t *testing.T) {
	s := subagentSession(t)
	s.turnID, s.serverTurnID = "turn-a", "codex-turn"
	spawn(s, "spawn-1", "trace it", "child-a")
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-turn","status":"completed"}}`)
	drain(s)

	notify(s, "item/agentMessage/delta", `{"threadId":"child-a","turnId":"ct","itemId":"m1","delta":"looking"}`)
	notify(s, "item/reasoning/summaryTextDelta", `{"threadId":"child-a","turnId":"ct","itemId":"r1","delta":"hmm"}`)
	notify(s, "item/started", `{"threadId":"child-a","turnId":"ct","item":{"type":"commandExecution","id":"c1","command":"ls -la"}}`)
	notify(s, "item/completed", `{"threadId":"child-a","turnId":"ct","item":{"type":"commandExecution","id":"c1","command":"ls -la","exitCode":0,"aggregatedOutput":"x"}}`)
	// The child's prompt and its compaction are not output.
	notify(s, "item/completed", `{"threadId":"child-a","turnId":"ct","item":{"type":"userMessage","id":"u1","clientId":"q1"}}`)
	notify(s, "item/completed", `{"threadId":"child-a","turnId":"ct","item":{"type":"contextCompaction","id":"cc"}}`)
	// A later turn of ours is running; the child's output stays with turn A.
	s.turnID, s.serverTurnID = "turn-b", "codex-turn-b"
	childSays(s, "child-a", "m2", "final_answer", "All clear\nmore")
	got := drain(s)

	if s.turnID != "turn-b" || s.serverTurnID != "codex-turn-b" {
		t.Fatalf("child output touched our turn: %q / %q", s.turnID, s.serverTurnID)
	}
	var items int
	for _, e := range got {
		var turn, parent string
		switch p := e.Payload.(type) {
		case proto.MessageChunkPayload:
			turn, parent = p.TurnID, p.ParentToolCallID
		case proto.ToolCallStartedPayload:
			turn, parent = p.TurnID, p.ParentToolCallID
		case proto.ToolCallUpdatedPayload:
			turn, parent = "turn-a", p.ParentToolCallID
		case proto.JobPayload:
			continue
		default:
			t.Fatalf("child output emitted %s: %+v", e.Type, e)
		}
		items++
		if turn != "turn-a" || parent != "spawn-1" {
			t.Fatalf("%s filed under turn %q parent %q, want turn-a / spawn-1", e.Type, turn, parent)
		}
	}
	// delta, reasoning, tool started, tool updated, unstreamed final message
	if items != 5 {
		t.Fatalf("child items = %d, want 5: %+v", items, got)
	}
	var activity []string
	for _, j := range jobs(got, proto.JobUpdated) {
		if j.JobID != "child-a" || j.ToolCallID != "spawn-1" {
			t.Fatalf("job update lost its identity: %+v", j)
		}
		activity = append(activity, j.Activity)
	}
	if strings.Join(activity, "|") != "ls -la|All clear …" {
		t.Fatalf("activity = %q", activity)
	}
}

func TestChildUsageIsTheJobs(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	drain(s)
	notify(s, "thread/tokenUsage/updated", `{"threadId":"child-a","tokenUsage":{"total":{"totalTokens":1500,"inputTokens":1000,"outputTokens":400}}}`)
	notify(s, "thread/tokenUsage/updated", `{"threadId":"child-a","tokenUsage":{"total":{"inputTokens":2000,"outputTokens":100}}}`)
	got := drain(s)
	if len(ofType(got, proto.UsageUpdated)) != 0 {
		t.Fatal("child usage reached the session meter")
	}
	up := jobs(got, proto.JobUpdated)
	if len(up) != 2 || up[0].Usage.TotalTokens != 1500 || up[1].Usage.TotalTokens != 2100 {
		t.Fatalf("usage updates = %+v", up)
	}
}

func TestChildTurnCompletionFinishesTheJob(t *testing.T) {
	for _, tc := range []struct {
		name, completion string
		status, err      string
	}{
		{"completed", `{"id":"ct","status":"completed","error":null}`, proto.JobCompleted, ""},
		{"failed", `{"id":"ct","status":"failed","error":{"message":"rate limited"}}`, proto.JobFailed, "rate limited"},
		{"interrupted", `{"id":"ct","status":"interrupted","error":null}`, proto.JobStopped, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := subagentSession(t)
			s.turnID = "turn-a"
			spawn(s, "spawn-1", "x", "child-a")
			childTurn(s, "child-a", "ct")
			childSays(s, "child-a", "m1", "final_answer", "The answer")
			childSays(s, "child-a", "m2", "commentary", "an aside")
			drain(s)
			notify(s, "turn/completed", `{"threadId":"child-a","turn":`+tc.completion+`}`)
			fin := jobs(drain(s), proto.JobFinished)
			if len(fin) != 1 {
				t.Fatalf("finished = %+v", fin)
			}
			f := fin[0]
			if f.JobID != "child-a" || f.ToolCallID != "spawn-1" || f.Status != tc.status ||
				f.Error != tc.err || f.Activity != "The answer" {
				t.Fatalf("finish = %+v", f)
			}
		})
	}
}

// A child whose messages never reached us as items still has its answer in
// the completed turn's summary, and the whole of it goes to the job's pane.
func TestChildFinalMessageFallsBackToTheTurnSummary(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	drain(s)
	notify(s, "turn/completed", `{"threadId":"child-a","turn":{"id":"ct","status":"completed","items":[
		{"type":"agentMessage","id":"m1","phase":"commentary","text":"aside"},
		{"type":"agentMessage","id":"m2","phase":"final_answer","text":"Summary answer\nwith detail"}]}}`)
	got := drain(s)
	fin := jobs(got, proto.JobFinished)
	if len(fin) != 1 || fin[0].Activity != "Summary answer …" {
		t.Fatalf("finish = %+v", fin)
	}
	chunks := ofType(got, proto.MessageChunk)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %+v, want the summary's answer once", chunks)
	}
	c := chunks[0].Payload.(proto.MessageChunkPayload)
	if c.BlockID != "m2" || c.Delta != "Summary answer\nwith detail" || c.ParentToolCallID != "spawn-1" || c.TurnID != "turn-a" {
		t.Fatalf("summary chunk = %+v", c)
	}
}

// An answer that streamed as deltas is already in the pane; its summary copy
// must not repeat it.
func TestStreamedSummaryAnswerIsNotRepeated(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	notify(s, "item/agentMessage/delta", `{"threadId":"child-a","turnId":"ct","itemId":"m2","delta":"Summary answer"}`)
	drain(s)
	notify(s, "turn/completed", `{"threadId":"child-a","turn":{"id":"ct","status":"completed","items":[
		{"type":"agentMessage","id":"m2","phase":"final_answer","text":"Summary answer"}]}}`)
	got := drain(s)
	if chunks := ofType(got, proto.MessageChunk); len(chunks) != 0 {
		t.Fatalf("streamed answer repeated: %+v", chunks)
	}
	if fin := jobs(got, proto.JobFinished); len(fin) != 1 || fin[0].Activity != "Summary answer" {
		t.Fatalf("finish = %+v", fin)
	}
}

func TestStaleChildCompletionIsIgnored(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	childTurn(s, "child-a", "ct-1")
	drain(s)

	childDone(s, "child-a", "ct-0", "completed")
	if got := drain(s); len(got) != 0 {
		t.Fatalf("a completion for another turn emitted %+v", got)
	}
	childDone(s, "child-a", "ct-1", "completed")
	childDone(s, "child-a", "ct-1", "completed")
	if fin := jobs(drain(s), proto.JobFinished); len(fin) != 1 {
		t.Fatalf("finished = %+v, want exactly one", fin)
	}
}

// The parent can hand a finished child more work; that is a new child turn,
// and the job is running again. Nothing else reopens it.
func TestNewChildTurnReopensTheJob(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	childTurn(s, "child-a", "ct-1")
	childDone(s, "child-a", "ct-1", "completed")
	drain(s)

	notify(s, "item/completed", `{"threadId":"child-a","turnId":"ct-1","item":{"type":"agentMessage","id":"late","phase":"final_answer","text":"late"}}`)
	childDone(s, "child-a", "ct-1", "completed")
	late := drain(s)
	for _, j := range append(jobs(late, proto.JobUpdated), jobs(late, proto.JobFinished)...) {
		if j.Status != "" {
			t.Fatalf("a settled child changed status on a straggler: %+v", j)
		}
	}

	childTurn(s, "child-a", "ct-2")
	up := jobs(drain(s), proto.JobUpdated)
	if len(up) != 1 || up[0].Status != proto.JobRunning || up[0].ToolCallID != "spawn-1" {
		t.Fatalf("reopen = %+v", up)
	}
	childDone(s, "child-a", "ct-2", "completed")
	if fin := jobs(drain(s), proto.JobFinished); len(fin) != 1 || fin[0].Status != proto.JobCompleted {
		t.Fatalf("second finish = %+v", fin)
	}
}

// Codex announces the child's turn before the spawn that names it completes;
// that turn id is what StopJob needs.
func TestChildTurnBeforeRegistrationIsKept(t *testing.T) {
	conn, rec := pairedConn(t, nil)
	s := subagentSession(t)
	s.conn = conn
	s.turnID = "turn-a"
	notify(s, "thread/status/changed", `{"threadId":"child-a","status":{"type":"active"}}`)
	childTurn(s, "child-a", "ct-1")
	spawn(s, "spawn-1", "x", "child-a")

	if err := s.StopJob(context.Background(), "child-a"); err != nil {
		t.Fatalf("StopJob: %v", err)
	}
	var got struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	_ = json.Unmarshal(rec.sent("turn/interrupt"), &got)
	if got.ThreadID != "child-a" || got.TurnID != "ct-1" {
		t.Fatalf("turn/interrupt %+v, want child-a / ct-1", got)
	}
}

func TestStopJobRefusesWhatIsNotRunning(t *testing.T) {
	conn, rec := pairedConn(t, nil)
	s := subagentSession(t)
	s.conn = conn
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a", "child-b")
	childTurn(s, "child-a", "ct-1")
	childDone(s, "child-a", "ct-1", "completed")

	for _, id := range []string{"nobody", "child-a", "child-b"} {
		if err := s.StopJob(context.Background(), id); err == nil {
			t.Fatalf("StopJob(%s) succeeded", id)
		}
	}
	if n := rec.count("turn/interrupt"); n != 0 {
		t.Fatalf("turn/interrupt sent %d times", n)
	}
}

func TestV2SubAgentActivityRegistersTheChild(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	for _, edge := range []string{"item/started", "item/completed"} {
		notify(s, edge, `{"threadId":"thread-1","turnId":"codex-turn","item":{"type":"subAgentActivity","id":"call_1","kind":"started","agentThreadId":"child-a","agentPath":"/root/say_hello"}}`)
	}
	notify(s, "item/started", `{"threadId":"thread-1","item":{"type":"subAgentActivity","id":"done-1","kind":"completed","agentThreadId":"child-a","agentPath":"/root/say_hello"}}`)
	got := drain(s)

	js := jobs(got, proto.JobStarted)
	if len(js) != 1 || js[0].JobID != "child-a" || js[0].ToolCallID != "call_1" || js[0].Name != "/root/say_hello" {
		t.Fatalf("jobs = %+v", js)
	}
	st := ofType(got, proto.ToolCallStarted)
	if len(st) != 1 || st[0].Payload.(proto.ToolCallStartedPayload).ToolCallID != "call_1" {
		t.Fatalf("tool calls = %+v", st)
	}
	if len(ofType(got, proto.ToolCallUpdated)) != 1 {
		t.Fatalf("updates = %+v", got)
	}
}

func TestGrandchildRunsInsideItsSpawner(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	s.turnID = ""
	drain(s)
	spawnOn(s, "child-a", "spawn-2", "deeper", "grandchild")
	got := drain(s)

	st := ofType(got, proto.ToolCallStarted)
	if len(st) != 1 {
		t.Fatalf("tool calls = %+v", st)
	}
	call := st[0].Payload.(proto.ToolCallStartedPayload)
	if call.ParentToolCallID != "spawn-1" || call.TurnID != "turn-a" {
		t.Fatalf("grandchild spawn = %+v", call)
	}
	js := jobs(got, proto.JobStarted)
	if len(js) != 1 || js[0].JobID != "grandchild" || js[0].ParentJobID != "child-a" || js[0].ToolCallID != "spawn-2" {
		t.Fatalf("grandchild job = %+v", js)
	}
	if s.turnID != "" {
		t.Fatalf("a child's spawn opened a turn: %q", s.turnID)
	}
}

// wakeSession is a session that has spawned children in turn-a, which has
// since finished.
func wakeSession(t *testing.T, reply map[string]any, children ...string) (*session, *recorder) {
	t.Helper()
	if reply == nil {
		reply = map[string]any{"turn/start": map[string]any{"turn": map[string]any{"id": "wake-turn"}}}
	}
	conn, rec := pairedConn(t, reply)
	s := subagentSession(t)
	s.conn = conn
	s.wakeDelay = time.Hour
	s.turnID, s.serverTurnID = "turn-a", "codex-turn"
	for i, c := range children {
		spawn(s, fmt.Sprintf("spawn-%d", i), "Task "+c, c)
		childTurn(s, c, "ct-"+c)
	}
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-turn","status":"completed"}}`)
	drain(s)
	return s, rec
}

func wakeText(t *testing.T, rec *recorder) string {
	t.Helper()
	params := rec.sent("turn/start")
	var p struct {
		ThreadID string `json:"threadId"`
		Input    []struct {
			Text string `json:"text"`
		} `json:"input"`
	}
	_ = json.Unmarshal(params, &p)
	if p.ThreadID != "thread-1" || len(p.Input) != 1 {
		t.Fatalf("turn/start params = %s", params)
	}
	return p.Input[0].Text
}

func TestFinishedChildrenWakeTheSessionOnce(t *testing.T) {
	s, rec := wakeSession(t, nil, "a", "b")
	childSays(s, "a", "m1", "final_answer", "A is done")
	childDone(s, "a", "ct-a", "completed")
	notify(s, "turn/completed", `{"threadId":"b","turn":{"id":"ct-b","status":"failed","error":{"message":"boom"}}}`)
	drain(s)

	s.flushWake()
	if n := rec.count("turn/start"); n != 1 {
		t.Fatalf("turn/start sent %d times, want 1", n)
	}
	want := "Subagent completed: Task a\n\nResult:\nA is done\n\nSubagent failed: Task b\n\nResult:\nboom"
	if got := wakeText(t, rec); got != want {
		t.Fatalf("wake text = %q, want %q", got, want)
	}
	started := ofType(drain(s), proto.TurnStarted)
	if len(started) != 1 {
		t.Fatalf("turns started = %+v", started)
	}
	ts := started[0].Payload.(proto.TurnStartedPayload)
	if ts.Prompt != want || ts.TurnID == "" || ts.TurnID != s.turnID || s.serverTurnID != "wake-turn" {
		t.Fatalf("wake turn = %+v, session turn %q / %q", ts, s.turnID, s.serverTurnID)
	}

	// The batch is spent.
	s.flushWake()
	if n := rec.count("turn/start"); n != 1 {
		t.Fatalf("turn/start sent %d times after a second flush", n)
	}
}

func TestWakeTimerFires(t *testing.T) {
	s, rec := wakeSession(t, nil, "a")
	s.wakeDelay = time.Millisecond
	childDone(s, "a", "ct-a", "completed")
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		started := s.serverTurnID == "wake-turn"
		s.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wake timer never started a turn")
		}
		time.Sleep(time.Millisecond)
	}
	if n := rec.count("turn/start"); n != 1 {
		t.Fatalf("turn/start sent %d times", n)
	}
}

func TestNoWakeWhenNothingIsOwed(t *testing.T) {
	for _, tc := range []struct {
		name string
		play func(s *session)
	}{
		{"our turn is running", func(s *session) {
			s.turnID = "turn-b"
			childDone(s, "a", "ct-a", "completed")
			s.turnID = ""
		}},
		{"its turn was cancelled", func(s *session) {
			// Replay with turn-a ending on the stop button.
			s.stoppedTurns = map[string]bool{"turn-a": true}
			childDone(s, "a", "ct-a", "completed")
		}},
		{"the child was stopped", func(s *session) {
			childDone(s, "a", "ct-a", "interrupted")
		}},
		{"a grandchild finished", func(s *session) {
			spawnOn(s, "a", "spawn-g", "deeper", "g")
			childDone(s, "g", "", "completed")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := wakeSession(t, nil, "a")
			tc.play(s)
			s.flushWake()
			if n := rec.count("turn/start"); n != 0 {
				t.Fatalf("turn/start sent %d times", n)
			}
			if ts := ofType(drain(s), proto.TurnStarted); len(ts) != 0 {
				t.Fatalf("turn started: %+v", ts)
			}
		})
	}
}

// The cancelled case above seeds the set by hand; this is the real path, a
// turn of ours ending interrupted.
func TestCancelledTurnSuppressesItsChildrensWake(t *testing.T) {
	conn, rec := pairedConn(t, nil)
	s := subagentSession(t)
	s.conn = conn
	s.wakeDelay = time.Hour
	s.turnID, s.serverTurnID = "turn-a", "codex-turn"
	spawn(s, "spawn-1", "x", "a")
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-turn","status":"interrupted"}}`)
	childDone(s, "a", "", "completed")
	s.flushWake()
	if n := rec.count("turn/start"); n != 0 {
		t.Fatalf("turn/start sent %d times for a cancelled turn's child", n)
	}
}

func TestWakeDroppedWhenATurnStartsFirst(t *testing.T) {
	s, rec := wakeSession(t, nil, "a")
	childDone(s, "a", "ct-a", "completed")
	s.turnID = "turn-b"
	s.flushWake()
	if n := rec.count("turn/start"); n != 0 || s.turnID != "turn-b" {
		t.Fatalf("turn/start sent %d times, turn %q", n, s.turnID)
	}
	// Dropped, not deferred: going idle later does not resurrect it.
	s.turnID = ""
	s.flushWake()
	if n := rec.count("turn/start"); n != 0 {
		t.Fatalf("a dropped batch was sent later")
	}
}

func TestWakeStartFailureClosesTheTurn(t *testing.T) {
	s, _ := wakeSession(t, map[string]any{"turn/start": errors.New("thread busy")}, "a")
	childDone(s, "a", "ct-a", "completed")
	s.flushWake()
	got := drain(s)
	started, finished := ofType(got, proto.TurnStarted), ofType(got, proto.TurnFinished)
	if len(started) != 1 || len(finished) != 1 {
		t.Fatalf("emissions = %+v", got)
	}
	f := finished[0].Payload.(proto.TurnFinishedPayload)
	if f.TurnID != started[0].Payload.(proto.TurnStartedPayload).TurnID || f.StopReason != proto.StopError || f.Error == "" {
		t.Fatalf("finish = %+v", f)
	}
	if s.turnID != "" {
		t.Fatalf("failed wake left turn %q open", s.turnID)
	}
}

// Codex v2 posts a child's "completed" activity on this thread, tagged with
// the long-finished spawning turn, and never closes it with a turn/completed.
// Opening a turn for it left the session working forever and swallowed every
// later wake.
func TestCollabBookkeepingWhileIdleOpensNoTurn(t *testing.T) {
	s, rec := wakeSession(t, nil, "a", "b")
	childSays(s, "a", "m1", "final_answer", "A is done")
	childDone(s, "a", "ct-a", "completed")
	for _, edge := range []string{"item/started", "item/completed"} {
		notify(s, edge, `{"threadId":"thread-1","turnId":"codex-turn","item":{"type":"subAgentActivity","id":"subagent-completed-ct-a","kind":"completed","agentThreadId":"a"}}`)
		notify(s, edge, `{"threadId":"thread-1","turnId":"codex-turn","item":{"type":"collabAgentToolCall","id":"w1","tool":"wait","status":"completed","receiverThreadIds":["a"]}}`)
	}
	if ts := ofType(drain(s), proto.TurnStarted); len(ts) != 0 || s.turnID != "" {
		t.Fatalf("bookkeeping opened a turn: %+v, turn %q", ts, s.turnID)
	}

	childSays(s, "b", "m2", "final_answer", "B is done")
	childDone(s, "b", "ct-b", "completed")
	s.flushWake()
	if n := rec.count("turn/start"); n != 1 {
		t.Fatalf("turn/start sent %d times, want 1", n)
	}
	want := "Subagent completed: Task a\n\nResult:\nA is done\n\nSubagent completed: Task b\n\nResult:\nB is done"
	if got := wakeText(t, rec); got != want {
		t.Fatalf("wake text = %q, want %q", got, want)
	}
}

// A spawn while idle is the harness doing work, so it opens a turn like any
// other item would; only the bookkeeping around it does not.
func TestIdleSpawnOpensATurn(t *testing.T) {
	for _, item := range []string{
		`{"type":"collabAgentToolCall","id":"s1","tool":"spawnAgent","status":"inProgress","prompt":"x","receiverThreadIds":[]}`,
		`{"type":"subAgentActivity","id":"s1","kind":"started","agentThreadId":"a","agentPath":"/root/a"}`,
	} {
		s := subagentSession(t)
		notify(s, "item/started", `{"threadId":"thread-1","turnId":"codex-turn","item":`+item+`}`)
		if ts := ofType(drain(s), proto.TurnStarted); len(ts) != 1 || s.turnID == "" {
			t.Fatalf("idle spawn %s opened %d turns, turn %q; want one", item, len(ts), s.turnID)
		}
	}
}

// A child's result answers whichever turn of ours gave it that work, not the
// one that spawned it: stopping the turn that sent it more input silences that
// input's result, and stopping the spawning turn does not silence later work.
func TestWakeFollowsTheTurnThatGaveTheWork(t *testing.T) {
	for _, tc := range []struct {
		name              string
		spawnStop, inStop string
		wakes             int
	}{
		{"the turn that sent more input was stopped", "completed", "interrupted", 0},
		{"only the spawning turn was stopped", "interrupted", "completed", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, rec := pairedConn(t, map[string]any{"turn/start": map[string]any{"turn": map[string]any{"id": "wake-turn"}}})
			s := subagentSession(t)
			s.conn = conn
			s.wakeDelay = time.Hour

			s.turnID, s.serverTurnID = "turn-a", "codex-a"
			spawn(s, "spawn-1", "x", "a")
			childTurn(s, "a", "ct-1")
			childDone(s, "a", "ct-1", "completed")
			notify(s, "turn/completed", fmt.Sprintf(`{"threadId":"thread-1","turn":{"id":"codex-a","status":%q}}`, tc.spawnStop))

			s.turnID, s.serverTurnID = "turn-b", "codex-b"
			childTurn(s, "a", "ct-2")
			notify(s, "turn/completed", fmt.Sprintf(`{"threadId":"thread-1","turn":{"id":"codex-b","status":%q}}`, tc.inStop))

			childDone(s, "a", "ct-2", "completed")
			s.flushWake()
			if n := rec.count("turn/start"); n != tc.wakes {
				t.Fatalf("turn/start sent %d times, want %d", n, tc.wakes)
			}
		})
	}
}

// An earlier message is commentary; a final answer only in the turn's
// summary is the result, and goes into the pane.
func TestSummaryAnswerBeatsEarlierCommentary(t *testing.T) {
	s := subagentSession(t)
	s.turnID = "turn-a"
	spawn(s, "spawn-1", "x", "child-a")
	childSays(s, "child-a", "m1", "commentary", "Looking around")
	drain(s)
	notify(s, "turn/completed", `{"threadId":"child-a","turn":{"id":"ct","status":"completed","items":[
		{"type":"agentMessage","id":"m1","phase":"commentary","text":"Looking around"},
		{"type":"agentMessage","id":"m2","phase":"final_answer","text":"The answer"}]}}`)
	got := drain(s)
	if fin := jobs(got, proto.JobFinished); len(fin) != 1 || fin[0].Activity != "The answer" {
		t.Fatalf("finish = %+v", fin)
	}
	chunks := ofType(got, proto.MessageChunk)
	if len(chunks) != 1 || chunks[0].Payload.(proto.MessageChunkPayload).BlockID != "m2" {
		t.Fatalf("chunks = %+v, want the summary's answer", chunks)
	}
}

// A child can finish before the spawn naming it completes; it registers
// finished, not running forever.
func TestChildFinishedBeforeRegistrationRegistersFinished(t *testing.T) {
	s, _ := stopSession(t, nil)
	s.turnID = "turn-a"
	childTurn(s, "child-a", "ct-1")
	notify(s, "turn/completed", `{"threadId":"child-a","turn":{"id":"ct-1","status":"completed","items":[
		{"type":"agentMessage","id":"m1","phase":"final_answer","text":"Done early"}]}}`)
	spawn(s, "spawn-1", "x", "child-a")
	got := drain(s)

	if st := jobs(got, proto.JobStarted); len(st) != 1 {
		t.Fatalf("started = %+v", st)
	}
	fin := jobs(got, proto.JobFinished)
	if len(fin) != 1 || fin[0].Status != proto.JobCompleted || fin[0].Activity != "Done early" {
		t.Fatalf("finish = %+v", fin)
	}
	if err := s.StopJob(context.Background(), "child-a"); err == nil {
		t.Fatal("StopJob on a finished child succeeded")
	}
}

// A spawn can complete after Stop has ended its turn, naming a child already
// running for it; that child is stopped as it registers.
func TestSpawnCompletingAfterItsStoppedTurnStopsTheChild(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID, s.serverTurnID = "turn-a", "codex-turn"
	item := func(status string, rs []string) string {
		b, _ := json.Marshal(map[string]any{
			"threadId": "thread-1", "turnId": "codex-turn",
			"item": map[string]any{
				"type": "collabAgentToolCall", "id": "spawn-1", "tool": "spawnAgent",
				"status": status, "prompt": "one", "receiverThreadIds": rs,
			},
		})
		return string(b)
	}
	notify(s, "item/started", item("inProgress", []string{}))
	childTurn(s, "sub-1", "sub-1-turn")
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-turn","status":"interrupted"}}`)
	notify(s, "item/completed", item("completed", []string{"sub-1"}))

	waitFor(t, "the late subagent interrupt", func() bool { return rec.interrupts(t)["sub-1"] == "sub-1-turn" })
	childDone(s, "sub-1", "sub-1-turn", "completed")
	if len(s.wakeQueue) != 0 {
		t.Fatalf("a stopped turn's child queued a wake: %q", s.wakeQueue)
	}
}

// A grandchild works for what its spawner works for now: spawned by a child
// codex reused in turn B, it is stopped with B, not left running for A.
func TestGrandchildOfAReusedChildIsStoppedWithItsTurn(t *testing.T) {
	s, rec := stopSession(t, nil)
	s.turnID, s.serverTurnID = "turn-a", "codex-a"
	spawn(s, "spawn-1", "one", "sub-1")
	childTurn(s, "sub-1", "sub-1-first")
	childDone(s, "sub-1", "sub-1-first", "completed")
	notify(s, "turn/completed", `{"threadId":"thread-1","turn":{"id":"codex-a","status":"completed"}}`)

	s.turnID, s.serverTurnID = "turn-b", "codex-b"
	childTurn(s, "sub-1", "sub-1-reused")
	childTurn(s, "grand", "grand-turn")
	spawnOn(s, "sub-1", "spawn-2", "deeper", "grand")
	drain(s)

	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	want := map[string]string{"thread-1": "codex-b", "sub-1": "sub-1-reused", "grand": "grand-turn"}
	if got := rec.interrupts(t); !maps.Equal(got, want) {
		t.Fatalf("interrupts = %v, want %v", got, want)
	}
}
