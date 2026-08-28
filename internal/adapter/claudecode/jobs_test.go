package claudecode

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

// drainJobs empties the event channel and returns every job row, in order.
func drainJobs(t *testing.T, s *session) []struct {
	Type string
	proto.JobPayload
} {
	t.Helper()
	var out []struct {
		Type string
		proto.JobPayload
	}
	for {
		select {
		case e := <-s.events:
			if p, ok := e.Payload.(proto.JobPayload); ok {
				out = append(out, struct {
					Type string
					proto.JobPayload
				}{e.Type, p})
			}
		default:
			return out
		}
	}
}

func system(t *testing.T, subtype string, fields map[string]any) map[string]any {
	t.Helper()
	m := map[string]any{"type": "system", "subtype": subtype, "session_id": "conv"}
	for k, v := range fields {
		m[k] = v
	}
	return m
}

// The SDK's task edges become job rows, each carrying the linkage bundle, and
// a subagent's own result lands on the job's usage rather than the session's.
func TestTaskEdgesBecomeJobRows(t *testing.T) {
	s := newTestSession()

	s.handleSDKMessage(rawSDK(t, system(t, "task_started", map[string]any{
		"task_id": "t1", "tool_use_id": "tu1", "description": "Explore the repo",
		"subagent_type": "Explore", "task_type": "local_agent",
	})))
	s.handleSDKMessage(rawSDK(t, system(t, "task_progress", map[string]any{
		"task_id": "t1", "description": "Explore the repo", "last_tool_name": "Grep",
		"usage": map[string]any{"total_tokens": 1200, "tool_uses": 3, "duration_ms": 900},
	})))
	s.handleSDKMessage(rawSDK(t, system(t, "task_updated", map[string]any{
		"task_id": "t1", "patch": map[string]any{"is_backgrounded": true, "status": "running"},
	})))
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "result", "subtype": "success", "parent_tool_use_id": "tu1", "session_id": "conv",
		"total_cost_usd": 0.05, "usage": map[string]any{"input_tokens": 700, "output_tokens": 300},
	}))
	s.handleSDKMessage(rawSDK(t, system(t, "task_notification", map[string]any{
		"task_id": "t1", "tool_use_id": "tu1", "status": "completed", "output_file": "/tmp/t1.out", "summary": "done",
	})))

	rows := drainJobs(t, s)
	if len(rows) != 5 {
		t.Fatalf("got %d job rows, want 5: %+v", len(rows), rows)
	}
	for i, r := range rows {
		if r.JobID != "t1" || r.ToolCallID != "tu1" {
			t.Fatalf("row %d lost its linkage: %+v", i, r)
		}
	}
	start := rows[0]
	if start.Type != proto.JobStarted || start.Kind != proto.JobAgent || start.Role != "Explore" || start.Name != "Explore the repo" || start.Status != proto.JobRunning {
		t.Fatalf("start row = %+v", start)
	}
	if p := rows[1]; p.Type != proto.JobUpdated || p.Activity != "Explore the repo" || p.Usage == nil || p.Usage.TotalTokens != 1200 || p.Usage.ToolUses != 3 {
		t.Fatalf("progress row = %+v", p)
	}
	if u := rows[2]; u.Type != proto.JobUpdated || u.Backgrounded == nil || !*u.Backgrounded || u.Status != proto.JobRunning {
		t.Fatalf("updated row = %+v", u)
	}
	if r := rows[3]; r.Type != proto.JobUpdated || r.Usage == nil || r.Usage.TotalTokens != 1000 || r.Usage.Cost != 0.05 {
		t.Fatalf("subagent result row = %+v", r)
	}
	if n := rows[4]; n.Type != proto.JobFinished || n.Status != proto.JobCompleted || n.OutputFile != "/tmp/t1.out" {
		t.Fatalf("notification row = %+v", n)
	}

	// No turn was open, so a subagent result must not have opened or closed
	// one, nor touched the session's own usage.
	if s.usage.Cost != 0 {
		t.Fatalf("subagent cost leaked into session usage: %+v", s.usage)
	}
}

// task_updated statuses map onto job statuses and the right event type.
func TestTaskUpdatedStatusMapping(t *testing.T) {
	cases := []struct {
		patch, typ, status string
	}{
		{"completed", proto.JobFinished, proto.JobCompleted},
		{"failed", proto.JobFinished, proto.JobFailed},
		{"killed", proto.JobFinished, proto.JobStopped},
		{"paused", proto.JobUpdated, proto.JobPaused},
		{"pending", proto.JobUpdated, proto.JobRunning},
	}
	for _, c := range cases {
		s := newTestSession()
		s.handleSDKMessage(rawSDK(t, system(t, "task_updated", map[string]any{
			"task_id": "t", "patch": map[string]any{"status": c.patch, "error": "boom"},
		})))
		rows := drainJobs(t, s)
		if len(rows) != 1 || rows[0].Type != c.typ || rows[0].Status != c.status {
			t.Fatalf("%s: rows = %+v", c.patch, rows)
		}
		if c.patch == "failed" && rows[0].Error != "boom" {
			t.Fatalf("failed row lost its error: %+v", rows[0])
		}
	}
}

// background_tasks_changed is the authoritative live set: a task that drops
// out without a terminal row of its own was reaped, and is finished as stopped.
func TestBackgroundSetReapsVanishedTasks(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(rawSDK(t, system(t, "task_started", map[string]any{
		"task_id": "sh1", "tool_use_id": "tu9", "description": "npm test", "task_type": "local_bash",
	})))
	s.handleSDKMessage(rawSDK(t, system(t, "background_tasks_changed", map[string]any{
		"tasks": []map[string]any{{"task_id": "sh1", "task_type": "local_bash", "description": "npm test"}},
	})))
	s.handleSDKMessage(rawSDK(t, system(t, "background_tasks_changed", map[string]any{
		"tasks": []map[string]any{},
	})))

	rows := drainJobs(t, s)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Kind != proto.JobShell {
		t.Fatalf("a local_bash task is a shell: %+v", rows[0])
	}
	if r := rows[1]; r.Type != proto.JobFinished || r.Status != proto.JobStopped || r.ToolCallID != "tu9" {
		t.Fatalf("vanished task was not stopped: %+v", r)
	}

	// The set is now empty and the job done; an identical empty set is silent.
	s.handleSDKMessage(rawSDK(t, system(t, "background_tasks_changed", map[string]any{"tasks": []map[string]any{}})))
	if rows := drainJobs(t, s); len(rows) != 0 {
		t.Fatalf("a settled set emitted again: %+v", rows)
	}
}

// A task killed through task_updated and then reported by task_notification
// gets both rows — the notification is what names the output file — but the
// background set does not stop it a third time.
func TestNotificationAfterKillStillLandsOnce(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(rawSDK(t, system(t, "task_started", map[string]any{"task_id": "t", "task_type": "local_bash"})))
	s.handleSDKMessage(rawSDK(t, system(t, "background_tasks_changed", map[string]any{
		"tasks": []map[string]any{{"task_id": "t", "task_type": "local_bash"}},
	})))
	s.handleSDKMessage(rawSDK(t, system(t, "task_updated", map[string]any{"task_id": "t", "patch": map[string]any{"status": "killed"}})))
	s.handleSDKMessage(rawSDK(t, system(t, "task_notification", map[string]any{
		"task_id": "t", "status": "stopped", "output_file": "/tmp/t.out",
	})))
	s.handleSDKMessage(rawSDK(t, system(t, "background_tasks_changed", map[string]any{"tasks": []map[string]any{}})))

	rows := drainJobs(t, s)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[1].Type != proto.JobFinished || rows[1].Status != proto.JobStopped {
		t.Fatalf("kill row = %+v", rows[1])
	}
	if rows[2].Type != proto.JobFinished || rows[2].Status != proto.JobStopped || rows[2].OutputFile != "/tmp/t.out" {
		t.Fatalf("notification row = %+v", rows[2])
	}
}

func TestAgentToolsAreAgentKind(t *testing.T) {
	for _, n := range []string{"Task", "Agent"} {
		if toolKind(n) != proto.KindAgent {
			t.Fatalf("%s kind = %s", n, toolKind(n))
		}
	}
}

// A background Bash reports its task id and output file only in its tool
// result, while the shell is still running; that is what makes a live tail
// possible.
func TestBackgroundShellResultLinksOutputFile(t *testing.T) {
	s := newTestSession()
	s.handleSDKMessage(rawSDK(t, system(t, "task_started", map[string]any{
		"task_id": "bg1", "task_type": "local_bash", "description": "sleep",
	})))
	s.handleSDKMessage(rawSDK(t, map[string]any{
		"type": "user", "session_id": "conv",
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": "tu9",
			"content": "Command running in background with ID: bg1. Output is being written to: /tmp/x/bg1.output. You will be notified when it completes.",
		}}},
	}))
	rows := drainJobs(t, s)
	last := rows[len(rows)-1]
	if last.Type != proto.JobUpdated || last.JobID != "bg1" || last.ToolCallID != "tu9" || last.OutputFile != "/tmp/x/bg1.output" || last.Kind != proto.JobShell {
		t.Fatalf("shell link row = %+v", last)
	}
}
