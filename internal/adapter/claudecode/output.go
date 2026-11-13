package claudecode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/proto"
)

// ---- live output of foreground Bash ----
//
// The SDK carries no output for a running Bash call: tool_progress has no
// output field, and the CLI only sends it at all in remote mode. What Claude
// Code does do is write a foreground call's stdout and stderr, as they arrive,
// to a task output file, and announce the call with a task_started naming the
// task. Tailing that file is the only live view there is (T3 Code does the
// same). Everything here is best effort: no file, or one we cannot read, means
// no live output, and the final output still arrives on the tool_result.

const (
	// defaultTailEvery is how often a running call's file is polled.
	defaultTailEvery = 250 * time.Millisecond
	// defaultTailPatience is how long to look for the file before giving up: the CLI
	// creates it as the command starts, so one that has not appeared by then
	// is not going to.
	defaultTailPatience = 10 * time.Second
	// maxTailRead caps one delta. The row shows only the last few lines, and
	// every delta is a write to the event log (#239): a burst larger than this
	// keeps only its end.
	maxTailRead = 64 << 10
)

// claudeTaskRoot is where Claude Code keeps task output files for this
// session's child, resolved from the same environment the child was given.
// It returns "" where the layout is unknown, which disables tailing.
func claudeTaskRoot(cwd string, env map[string]string) string {
	// The directory is per uid; Windows has none to name it by.
	uid := os.Getuid()
	if uid < 0 {
		return ""
	}
	lookup := func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	base := lookup("CLAUDE_CODE_TMPDIR")
	if base == "" {
		if runtime.GOOS == "darwin" {
			// Claude's macOS build ignores the per-user TMPDIR launchd sets.
			base = "/tmp"
		} else {
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				if base = lookup(key); base != "" {
					break
				}
			}
			if base == "" {
				base = "/tmp"
			}
		}
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(cwd, base)
	}
	return filepath.Join(base, fmt.Sprintf("claude-%d", uid))
}

// tail is one running call's tailer. Closing quit asks it to stop; done
// closes once its goroutine has returned, after which it emits nothing.
type tail struct {
	taskID string
	quit   chan struct{}
	done   chan struct{}
}

// followTask starts and stops tailers from the task edges handleTask has
// already turned into job rows.
func (s *session) followTask(msg map[string]json.RawMessage) {
	var m struct {
		Subtype   string `json:"subtype"`
		TaskID    string `json:"task_id"`
		ToolUseID string `json:"tool_use_id"`
		TaskType  string `json:"task_type"`
		SessionID string `json:"session_id"`
		// Older builds omit it on a foreground call, so only an explicit
		// true means backgrounded.
		IsBackgrounded *bool `json:"is_backgrounded"`
		Patch          struct {
			Status         string `json:"status"`
			IsBackgrounded *bool  `json:"is_backgrounded"`
		} `json:"patch"`
	}
	remarshal(msg, &m)

	switch m.Subtype {
	case "task_started":
		// A backgrounded shell has its own output file and job row; its
		// tool_result comes back at once, so there is no running row to feed.
		if m.TaskType != "local_bash" || (m.IsBackgrounded != nil && *m.IsBackgrounded) || m.ToolUseID == "" {
			return
		}
		session := m.SessionID
		if session == "" {
			s.mu.Lock()
			// A rewound conversation not yet settled reports a recipe; the
			// running process is its new id either way.
			session = parseConversation(s.harnessSessionID).id
			s.mu.Unlock()
		}
		s.startTail(m.ToolUseID, m.TaskID, session)

	case "task_updated":
		// Backgrounding hands the shell to its job row, whose output is read
		// through the job's own file; a terminal status means there is
		// nothing more to tail.
		backgrounded := m.Patch.IsBackgrounded != nil && *m.Patch.IsBackgrounded
		switch {
		case backgrounded, m.Patch.Status == "completed", m.Patch.Status == "failed", m.Patch.Status == "killed":
		default:
			return
		}
		s.mu.Lock()
		var toolUseID string
		for id, t := range s.tails {
			if t.taskID == m.TaskID {
				toolUseID = id
				break
			}
		}
		s.mu.Unlock()
		if toolUseID != "" {
			s.stopTail(toolUseID)
		}
	}
}

// pathElement reports whether a harness-supplied id is safe to use as one
// element of a path. The ids come from SDK frames, and one carrying a
// separator or ".." must not point the tailer at another file.
func pathElement(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

// startTail begins tailing one call's output file. A call already being
// tailed is left alone.
func (s *session) startTail(toolUseID, taskID, sessionID string) {
	if s.taskRoot == "" || !pathElement(taskID) || !pathElement(sessionID) {
		return
	}
	every, patience := s.tailEvery, s.tailPatience
	if every <= 0 {
		every = defaultTailEvery
	}
	if patience <= 0 {
		patience = defaultTailPatience
	}
	t := &tail{taskID: taskID, quit: make(chan struct{}), done: make(chan struct{})}
	s.mu.Lock()
	if s.tails == nil {
		s.tails = map[string]*tail{}
	}
	if s.tails[toolUseID] != nil {
		s.mu.Unlock()
		return
	}
	s.tails[toolUseID] = t
	s.mu.Unlock()
	go s.runTail(t, toolUseID, s.taskRoot, sessionID, every, patience)
}

// stopTail stops one call's tailer and waits for it, so nothing it read can
// land after the caller's next event — the call's completion, above all.
func (s *session) stopTail(toolUseID string) {
	s.mu.Lock()
	t := s.tails[toolUseID]
	delete(s.tails, toolUseID)
	s.mu.Unlock()
	if t != nil {
		close(t.quit)
		<-t.done
	}
}

// stopTails stops every tailer: the turn has ended, or the session has.
func (s *session) stopTails() {
	s.mu.Lock()
	tails := s.tails
	s.tails = nil
	s.mu.Unlock()
	for _, t := range tails {
		close(t.quit)
	}
	for _, t := range tails {
		<-t.done
	}
}

// runTail polls one call's output file and emits what it gained each time.
// It never takes s.mu — stopTail's callers may hold locks of their own while
// waiting for it — and a send blocked on a full channel gives way to quit. One
// queued on eventsMu behind another emit does not: it waits for that emit, so
// a stop on the goroutine that drains events must close done first (Close).
func (s *session) runTail(t *tail, toolUseID, root, sessionID string, every, patience time.Duration) {
	defer close(t.done)
	tick := time.NewTicker(every)
	defer tick.Stop()
	deadline := time.Now().Add(patience)
	var path string
	var r tailReader
	for {
		select {
		case <-t.quit:
			return
		case <-s.done:
			return
		case <-tick.C:
		}
		if path == "" {
			if path = findTaskOutput(root, sessionID, t.taskID); path == "" {
				if time.Now().After(deadline) {
					// The CLI keeps its task files where it decides to; this
					// line is the only sign it has moved them.
					s.host.Logf("claude: no output file for task %s under %s; its output will not stream", t.taskID, root)
					return
				}
				continue
			}
		}
		text := r.read(path)
		if text == "" {
			continue
		}
		s.emitTail(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: toolUseID, OutputDelta: text,
		}), t.quit)
	}
}

// emitTail is emit, except that it also gives up when the tailer is stopped.
// A stop can come from the goroutine that drains events (Cancel and Close are
// called from the actor); waiting there on a full channel would deadlock.
func (s *session) emitTail(e proto.Emission, quit <-chan struct{}) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventsClosed {
		return
	}
	select {
	case s.events <- e:
	case <-s.done:
	case <-quit:
	}
}

// findTaskOutput finds a task's output file under root. The directory between
// root and the session is the CLI's sanitised form of the cwd; listing it
// avoids re-deriving a sanitising rule that is the CLI's to change.
func findTaskOutput(root, sessionID, taskID string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), sessionID, "tasks", taskID+".output")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// tailReader turns a growing file into deltas: each read returns only what
// was written since the last, as valid UTF-8.
type tailReader struct {
	off int64
	// held is an incomplete UTF-8 sequence at the end of the last read,
	// prepended to the next so a rune split across writes is emitted whole.
	held []byte
}

func (r *tailReader) read(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	if size < r.off {
		// Truncated or replaced: what is there now is all new.
		r.off, r.held = 0, nil
	}
	if size == r.off {
		return ""
	}
	start, skipped := r.off, false
	if size-start > maxTailRead {
		start, skipped = size-maxTailRead, true
		r.held = nil
	}
	buf := make([]byte, size-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && err != io.EOF {
		return ""
	}
	r.off = start + int64(n)
	data := append(r.held, buf[:n]...)
	r.held = nil
	if skipped {
		// The cut can land inside a rune; drop its tail rather than emit
		// replacement characters for it.
		for i := 0; i < utf8.UTFMax-1 && len(data) > 0 && !utf8.RuneStart(data[0]); i++ {
			data = data[1:]
		}
	}
	// Hold back a sequence the writer has not finished.
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				r.held = append([]byte(nil), data[i:]...)
				data = data[:i]
			}
			break
		}
	}
	text := strings.ToValidUTF8(string(data), "�")
	if skipped {
		// The row shows a tail of lines; starting on a fresh one keeps the
		// skipped-into partial line from joining the line before it.
		text = "\n" + text
	}
	return text
}
