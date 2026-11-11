package claudecode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

// The recorded SDK frames of a foreground Bash call (T3 Code fixture, SDK
// 0.2.111), verbatim but for the tool_result's stdout, which is trimmed.
const (
	tailSession = "95b68ca2-f0b8-4902-839b-3586d4ba3f3d"
	tailTask    = "bmjeipgqr"
	tailTool    = "toolu_01CJnQSvsvh3iZtmvSWQVVvg"

	frameTaskStarted      = `{"type":"system","subtype":"task_started","task_id":"bmjeipgqr","tool_use_id":"toolu_01CJnQSvsvh3iZtmvSWQVVvg","description":"for i in 1 2 3 4; do printf '\\033[32mstream line %s\\033[0m\\n' $i; sleep 1; done; printf 'progress 50%%\\rprogress 100%%\\n'","is_backgrounded":false,"task_type":"local_bash","uuid":"6d2c30a6-ae01-4f1e-b461-a788611fbb27","session_id":"95b68ca2-f0b8-4902-839b-3586d4ba3f3d"}`
	frameTaskNotification = `{"type":"system","subtype":"task_notification","task_id":"bmjeipgqr","tool_use_id":"toolu_01CJnQSvsvh3iZtmvSWQVVvg","status":"completed","output_file":"","summary":"...","uuid":"bc93586f-9042-46d7-bb2d-bef6306dae41","session_id":"95b68ca2-f0b8-4902-839b-3586d4ba3f3d"}`
	frameToolResult       = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01CJnQSvsvh3iZtmvSWQVVvg","type":"tool_result","content":"\u001b[32mstream line 1\u001b[0m\n\u001b[32mstream line 2\u001b[0m\n\u001b[32mstream line 3\u001b[0m\n\u001b[32mstream line 4\u001b[0m\nprogress 50%\rprogress 100%","is_error":false}]},"parent_tool_use_id":null,"session_id":"95b68ca2-f0b8-4902-839b-3586d4ba3f3d","uuid":"1e330b27-e5ec-4efd-a1e8-f3210856a354","timestamp":"2026-10-03T04:28:29.316Z","tool_use_result":{"stdout":"\u001b[32mstream line 1\u001b[0m\n...","stderr":"","interrupted":false,"isImage":false,"noOutputExpected":false}}`
)

func frame(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// newTailSession is a test session whose task files live under a temp root
// and are polled fast.
func newTailSession(t *testing.T) *session {
	t.Helper()
	s := newTestSession()
	s.host = &fakeHost{}
	s.taskRoot = t.TempDir()
	s.tailEvery = 2 * time.Millisecond
	s.tailPatience = 5 * time.Second
	t.Cleanup(s.stopTails)
	return s
}

// taskOutput creates the task's output file where the CLI would, under a
// project directory the tailer has to find on its own.
func taskOutput(t *testing.T, root, session, task string) string {
	t.Helper()
	dir := filepath.Join(root, "-home-someone-project", session, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, task+".output")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// nextDelta waits for the next live output delta, skipping the job rows and
// anything else the handlers emit around it.
func nextDelta(t *testing.T, s *session) proto.ToolCallUpdatedPayload {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-s.events:
			if p, ok := e.Payload.(proto.ToolCallUpdatedPayload); ok && p.OutputDelta != "" {
				return p
			}
		case <-deadline:
			t.Fatal("no output delta arrived")
		}
	}
}

func runningTail(t *testing.T, s *session, toolUseID string) *tail {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	tl := s.tails[toolUseID]
	if tl == nil {
		t.Fatalf("no tailer for %s", toolUseID)
	}
	return tl
}

func exited(tl *tail) bool {
	select {
	case <-tl.done:
		return true
	default:
		return false
	}
}

// Each delta is only what the command printed since the last one, in order,
// and nothing else rides on it.
func TestTailEmitsOnlyNewOutput(t *testing.T) {
	s := newTailSession(t)
	path := taskOutput(t, s.taskRoot, tailSession, tailTask)
	s.handleSDKMessage(frame(t, frameTaskStarted))

	var got strings.Builder
	for i := 1; i <= 4; i++ {
		line := fmt.Sprintf("\x1b[32mstream line %d\x1b[0m\n", i)
		appendFile(t, path, line)
		d := nextDelta(t, s)
		if d.ToolCallID != tailTool || d.Status != "" || len(d.Content) != 0 {
			t.Fatalf("delta %d carries more than output: %+v", i, d)
		}
		if d.OutputDelta != line {
			t.Fatalf("delta %d = %q, want only the new %q", i, d.OutputDelta, line)
		}
		got.WriteString(d.OutputDelta)
	}
	data, _ := os.ReadFile(path)
	if got.String() != string(data) {
		t.Fatalf("deltas concatenate to %q, file holds %q", got.String(), data)
	}
}

// A rune the command wrote half of in one poll and finished in the next is
// emitted whole, once it is complete.
func TestTailHoldsBackASplitRune(t *testing.T) {
	s := newTailSession(t)
	path := taskOutput(t, s.taskRoot, tailSession, tailTask)
	s.handleSDKMessage(frame(t, frameTaskStarted))

	euro := "€" // three bytes
	appendFile(t, path, "cost "+euro[:2])
	if d := nextDelta(t, s); d.OutputDelta != "cost " {
		t.Fatalf("first delta = %q, want the complete text before the split rune", d.OutputDelta)
	}
	appendFile(t, path, euro[2:]+"5\n")
	d := nextDelta(t, s)
	if d.OutputDelta != euro+"5\n" {
		t.Fatalf("second delta = %q, want the rune whole", d.OutputDelta)
	}
}

// The call's tool_result stops its tailer before the completion goes out, so
// no live output can land after the call has finished.
func TestToolResultStopsTailBeforeCompletion(t *testing.T) {
	s := newTailSession(t)
	path := taskOutput(t, s.taskRoot, tailSession, tailTask)
	s.handleSDKMessage(frame(t, frameTaskStarted))
	tl := runningTail(t, s, tailTool)

	appendFile(t, path, "stream line 1\n")
	nextDelta(t, s)

	s.handleSDKMessage(frame(t, frameTaskNotification))
	s.handleSDKMessage(frame(t, frameToolResult))
	if !exited(tl) {
		t.Fatal("the tailer was still running when the completion was emitted")
	}

	// Whatever the tailer sent before it stopped precedes the completion; the
	// completion is the last tool update.
	appendFile(t, path, "stream line 2\n")
	completed := false
	for {
		select {
		case e := <-s.events:
			p, ok := e.Payload.(proto.ToolCallUpdatedPayload)
			if !ok {
				continue
			}
			if completed {
				t.Fatalf("tool update after completion: %+v", p)
			}
			completed = p.Status == proto.StatusCompleted
			continue
		default:
		}
		break
	}
	if !completed {
		t.Fatal("the tool_result did not complete the call")
	}
}

// Only a foreground shell is tailed: a backgrounded one has its own job row
// and output file, and other task kinds write no output file of this shape.
func TestTaskOutputTailsOnlyForegroundShells(t *testing.T) {
	cases := map[string]string{
		"backgrounded": strings.Replace(frameTaskStarted, `"is_backgrounded":false`, `"is_backgrounded":true`, 1),
		"agent":        strings.Replace(frameTaskStarted, `"task_type":"local_bash"`, `"task_type":"local_agent"`, 1),
		"escaping id":  strings.Replace(frameTaskStarted, `"task_id":"bmjeipgqr"`, `"task_id":"../../x"`, 1),
	}
	for name, raw := range cases {
		s := newTailSession(t)
		s.handleSDKMessage(frame(t, raw))
		s.mu.Lock()
		n := len(s.tails)
		s.mu.Unlock()
		if n != 0 {
			t.Fatalf("%s: started a tailer", name)
		}
	}

	// Older builds omit is_backgrounded on a foreground call.
	s := newTailSession(t)
	s.handleSDKMessage(frame(t, strings.Replace(frameTaskStarted, `"is_backgrounded":false,`, ``, 1)))
	runningTail(t, s, tailTool)
}

// Backgrounding a running call hands it to its job row, and a terminal patch
// ends it; either stops the tail.
func TestTaskUpdatedStopsTail(t *testing.T) {
	for _, patch := range []map[string]any{{"is_backgrounded": true}, {"status": "killed"}} {
		s := newTailSession(t)
		taskOutput(t, s.taskRoot, tailSession, tailTask)
		s.handleSDKMessage(frame(t, frameTaskStarted))
		tl := runningTail(t, s, tailTool)
		s.handleSDKMessage(rawSDK(t, system(t, "task_updated", map[string]any{"task_id": tailTask, "patch": patch})))
		if !exited(tl) {
			t.Fatalf("patch %v left the tailer running", patch)
		}
	}
}

// Turn end stops every tailer, the ones whose tool_result never came too.
func TestTurnEndStopsAllTails(t *testing.T) {
	s := newTailSession(t)
	s.turnID = "turn-1"
	s.handleSDKMessage(frame(t, frameTaskStarted))
	s.handleSDKMessage(rawSDK(t, system(t, "task_started", map[string]any{
		"task_id": "other", "tool_use_id": "tu2", "task_type": "local_bash", "session_id": tailSession,
	})))
	a, b := runningTail(t, s, tailTool), runningTail(t, s, "tu2")

	s.handleSDKMessage(rawSDK(t, map[string]any{"type": "result", "subtype": "success", "session_id": tailSession}))
	if !exited(a) || !exited(b) {
		t.Fatal("a tailer outlived the turn")
	}
}

type nopGroup struct{}

func (nopGroup) Kill() {}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestCloseStopsAllTails(t *testing.T) {
	s := newTailSession(t)
	s.cmd, s.tree, s.stdin = &exec.Cmd{}, nopGroup{}, nopWriteCloser{io.Discard}
	s.handleSDKMessage(frame(t, frameTaskStarted))
	tl := runningTail(t, s, tailTool)
	_ = s.Close()
	if !exited(tl) {
		t.Fatal("a tailer outlived the session")
	}
}

// The actor closes the session from the goroutine that drains its events. A
// tailer queued behind an emit stuck on the full channel must not hold Close
// up forever.
func TestCloseIsNotHeldByATailerBehindAFullChannel(t *testing.T) {
	s := newTailSession(t)
	s.cmd, s.tree, s.stdin = &exec.Cmd{}, nopGroup{}, nopWriteCloser{io.Discard}
	path := taskOutput(t, s.taskRoot, tailSession, tailTask)
	s.handleSDKMessage(frame(t, frameTaskStarted))
	tl := runningTail(t, s, tailTool)

	for len(s.events) < cap(s.events) {
		s.events <- proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "turn-1"})
	}
	stuck := make(chan struct{})
	go func() {
		defer close(stuck)
		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "turn-1"}))
	}()
	waitFor(t, func() bool {
		if s.eventsMu.TryLock() {
			s.eventsMu.Unlock()
			return false
		}
		return true
	})
	appendFile(t, path, "queued behind the stuck emit\n")
	time.Sleep(20 * s.tailEvery)

	closed := make(chan struct{})
	go func() {
		_ = s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited on a tailer that could not exit")
	}
	if !exited(tl) {
		t.Fatal("a tailer outlived the session")
	}
	<-stuck
}

func TestBridgeExitStopsAllTails(t *testing.T) {
	s, pw := newBridgeSession(t)
	s.taskRoot, s.tailEvery = t.TempDir(), time.Millisecond
	s.handleSDKMessage(frame(t, frameTaskStarted))
	tl := runningTail(t, s, tailTool)
	_ = pw.Close()
	waitFor(t, func() bool { return exited(tl) })
}

// A file that never appears is given up on rather than looked for forever.
func TestTailGivesUpWithoutAFile(t *testing.T) {
	s := newTailSession(t)
	s.tailPatience = 10 * time.Millisecond
	s.handleSDKMessage(frame(t, frameTaskStarted))
	tl := runningTail(t, s, tailTool)
	waitFor(t, func() bool { return exited(tl) })
}

// A file that shrank was replaced: what it holds now is all new.
func TestTailReaderResetsOnShrink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.output")
	var r tailReader
	if err := os.WriteFile(path, []byte("a long first line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.read(path)
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.read(path); got != "new\n" {
		t.Fatalf("after shrink = %q, want the whole new content", got)
	}
	appendFile(t, path, "more\n")
	if got := r.read(path); got != "more\n" {
		t.Fatalf("after shrink and growth = %q", got)
	}
}

// A burst far larger than the row can show is cut to its end, starting on a
// fresh line, so one poll cannot put megabytes into the event log.
func TestTailReaderBoundsABurst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.output")
	var r tailReader
	// Multibyte throughout, so the cut lands inside a rune.
	burst := strings.Repeat("ünïcødé line\n", 20000) + "the end\n"
	if err := os.WriteFile(path, []byte(burst), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.read(path)
	if !strings.HasPrefix(got, "\n") {
		t.Fatalf("a cut delta must start on a fresh line: %q…", got[:20])
	}
	if len(got) > maxTailRead+1 || len(got) < maxTailRead/2 {
		t.Fatalf("cut delta is %d bytes for a %d byte burst", len(got), len(burst))
	}
	if !strings.HasSuffix(got, "the end\n") || !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("cut delta lost the end or a rune: …%q", got[len(got)-20:])
	}
	// The skipped bytes are skipped for good, not read next time.
	appendFile(t, path, "after\n")
	if got := r.read(path); got != "after\n" {
		t.Fatalf("after a burst = %q", got)
	}
}

// Invalid bytes the command printed come out as valid UTF-8 rather than
// being held back as if a rune were unfinished.
func TestTailReaderEmitsValidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.output")
	var r tailReader
	if err := os.WriteFile(path, []byte("bad \xff\xfe byte\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.read(path)
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "bad ") || !strings.HasSuffix(got, " byte\n") {
		t.Fatalf("got %q", got)
	}
}

// The root is resolved from the child's environment the way Claude Code
// resolves it: its own override first, then the platform's temp dir.
func TestTaskOutputRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no task root on windows")
	}
	for _, key := range []string{"CLAUDE_CODE_TMPDIR", "TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, "")
	}
	uid := fmt.Sprintf("claude-%d", os.Getuid())
	// macOS ignores TMPDIR and its kin.
	tmp := func(dir string) string {
		if runtime.GOOS == "darwin" {
			return "/tmp"
		}
		return dir
	}
	cases := []struct {
		name    string
		ambient map[string]string
		overlay map[string]string
		want    string
	}{
		{"override wins", nil, map[string]string{"CLAUDE_CODE_TMPDIR": "/ct", "TMPDIR": "/td"}, "/ct"},
		{"relative override", nil, map[string]string{"CLAUDE_CODE_TMPDIR": "scratch"}, "/work/scratch"},
		{"ambient override", map[string]string{"CLAUDE_CODE_TMPDIR": "/amb"}, nil, "/amb"},
		{"overlay clears ambient", map[string]string{"CLAUDE_CODE_TMPDIR": "/amb"}, map[string]string{"CLAUDE_CODE_TMPDIR": "", "TMPDIR": "/td"}, tmp("/td")},
		{"relative TMPDIR", nil, map[string]string{"TMPDIR": "rel"}, tmp("/work/rel")},
		{"TMP before TEMP", map[string]string{"TEMP": "/te"}, map[string]string{"TMP": "/tm"}, tmp("/tm")},
		{"TEMP last", map[string]string{"TEMP": "/te"}, nil, tmp("/te")},
		{"none", nil, nil, "/tmp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.ambient {
				t.Setenv(k, v)
			}
			if got, want := claudeTaskRoot("/work", c.overlay), filepath.Join(c.want, uid); got != want {
				t.Fatalf("root = %q, want %q", got, want)
			}
		})
	}
}
