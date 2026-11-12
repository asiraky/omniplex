package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/proto"
)

var _ adapter.JobStopper = (*session)(nil)

// Codex runs a spawned subagent (spawn_agent in the collaboration tools, or
// the v2 multi-agent tools) as a thread of its own, multiplexed over this
// session's connection and tagged with that thread's id. Each such thread is
// a job: its output goes into the log parented on the spawn tool call, so it
// renders in the job's own pane rather than the main transcript, and it keeps
// coming after the turn that spawned it has ended: a subagent outlives its
// turn. Interrupting this thread does not stop it, so Cancel interrupts the
// children working for the stopped turn alongside it, and StopJob stops one.
//
// Nothing a child thread reports about itself — its turns, token meter, plan,
// compaction — is ever applied to this session's state: that would close or
// relabel this session's turn with another thread's.

// defaultWakeDelay is how long a finished child waits for siblings before the
// wake turn goes out, so children finishing together cost one turn.
const defaultWakeDelay = time.Second

// labelRunes caps a job's name: it is a one-line label, not the prompt.
const labelRunes = 80

// child is one subagent thread. The identity fields are fixed at
// registration; the rest is guarded by s.mu.
type child struct {
	jobID       string // the child's codex thread id
	toolCallID  string // the spawn item its output is parented on
	parentJobID string // the spawning child's job id; empty for a direct child
	label       string
	// spawnTurn is the turn of ours its output is filed under: the one that
	// spawned it, even after that turn has ended.
	spawnTurn string

	// workTurn is the turn of ours its current work was asked for: the
	// spawning turn, or the one that was running when it last started a turn
	// (codex reuses a child across our turns; send_input hands it more).
	// Stopping that turn stops the child, and its result from waking the
	// session.
	workTurn string
	// serverTurn is the child's running codex turn, which turn/interrupt
	// needs; empty between turns. finishedTurn is the one that last
	// completed, so a straggling item from it cannot mark the child running
	// again. interrupting is set while an interrupt for serverTurn is in
	// flight.
	serverTurn   string
	finishedTurn string
	interrupting bool
	settled      bool
	lastMessage  string
	finalMessage string
}

// pendingChild is what a thread did before the spawn naming it completed:
// the turn it started, and that turn's completion if it got that far.
type pendingChild struct {
	turn string
	done json.RawMessage
}

func (c *child) toolCall() string {
	if c == nil {
		return ""
	}
	return c.toolCallID
}

func (c *child) row() proto.JobPayload {
	return proto.JobPayload{JobID: c.jobID, ToolCallID: c.toolCallID, ParentJobID: c.parentJobID}
}

// subagentLabel is the first non-empty of: the spawn prompt's first line, the
// agent path, the thread id.
func subagentLabel(prompt, path, thread string) string {
	label := strings.TrimSpace(prompt)
	if i := strings.IndexByte(label, '\n'); i >= 0 {
		label = strings.TrimSpace(label[:i])
	}
	if r := []rune(label); len(r) > labelRunes {
		label = string(r[:labelRunes]) + "…"
	}
	if label == "" {
		label = path
	}
	if label == "" {
		label = thread
	}
	return label
}

// spawnsAgent reports whether a collaboration item is a spawn: v1's
// spawnAgent call, or v2's "started" activity.
func spawnsAgent(it codexItem) bool {
	switch it.Type {
	case "collabAgentToolCall":
		return it.Tool == "spawnAgent"
	case "subAgentActivity":
		return it.Kind == "started"
	}
	return false
}

// collabBookkeeping reports whether an item is collaboration other than a
// spawn (wait, sendInput, v2's "completed" activity), which is not work.
func collabBookkeeping(it codexItem) bool {
	return (it.Type == "collabAgentToolCall" || it.Type == "subAgentActivity") && !spawnsAgent(it)
}

// endedTurn is ours for codex's turn id serverTurn when that is the turn
// that just completed, else serverTurn itself: a spawn that straggles in
// after its turn ended names it by codex's id, and stopping that turn must
// still reach the child.
func (s *session) endedTurn(serverTurn string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if serverTurn != "" && serverTurn == s.lastServerTurn && s.lastTurn != "" {
		return s.lastTurn
	}
	return serverTurn
}

// handleSpawnItem renders a spawn as an agent tool call and registers the
// threads it names. v1 names its children only on completion; v2 announces
// each in a subAgentActivity "started" item. Every other collaboration item
// (wait, sendInput, the v2 "completed" activity) is ignored: the child's own
// turn/completed is the authoritative end.
func (s *session) handleSpawnItem(completed bool, turn string, owner *child, it codexItem) {
	if !spawnsAgent(it) {
		return
	}
	var label string
	var receivers []string
	if it.Type == "collabAgentToolCall" {
		label = subagentLabel(it.Prompt, "", "")
		if completed {
			receivers = it.ReceiverThreadIDs
		}
	} else {
		label = subagentLabel("", it.AgentPath, "")
		if it.AgentThreadID != "" {
			receivers = []string{it.AgentThreadID}
		}
	}
	parent := owner.toolCall()

	if !completed {
		title := label
		if title == "" {
			title = "Subagent"
		}
		s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
			TurnID: turn, ToolCallID: it.ID, Kind: proto.KindAgent,
			Title: title, Status: proto.StatusInProgress, ParentToolCallID: parent,
		}))
	} else {
		status := proto.StatusCompleted
		switch it.Status {
		case "failed":
			status = proto.StatusFailed
		case "interrupted":
			status = proto.StatusCancelled
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: status, ParentToolCallID: parent,
		}))
	}

	for i, thread := range receivers {
		// The spawn's tool call is the transcript item a job links to; a
		// spawn that names several children can only give it to one, and
		// the rest get ids of their own that still lead back to it.
		toolCallID := it.ID
		if i > 0 {
			toolCallID = it.ID + ":" + thread
		}
		name := label
		if name == "" {
			name = thread
		}
		s.registerChild(thread, toolCallID, name, turn, owner)
	}
}

// registerChild makes a thread a job. A thread already known is left alone:
// v2 announces a child on both edges of its activity item.
func (s *session) registerChild(thread, toolCallID, label, turn string, owner *child) {
	s.mu.Lock()
	if thread == "" || thread == s.threadID || s.children[thread] != nil {
		s.mu.Unlock()
		return
	}
	if s.children == nil {
		s.children = map[string]*child{}
	}
	c := &child{jobID: thread, toolCallID: toolCallID, label: label, spawnTurn: turn, workTurn: turn}
	if owner != nil {
		// A grandchild works for whatever its spawner is working for now,
		// which is not the turn its output is filed under once codex has
		// reused the spawner.
		c.parentJobID = owner.jobID
		c.workTurn = owner.workTurn
	}
	var done json.RawMessage
	if p := s.pending[thread]; p != nil {
		c.serverTurn, done = p.turn, p.done
		delete(s.pending, thread)
	}
	s.children[thread] = c
	// A child that started working before the spawn naming it arrived, for
	// a turn already stopped, missed Cancel; it is stopped now, unless it
	// has already finished.
	var late string
	if done == nil {
		late = s.lateInterruptLocked(c)
	}
	row := c.row()
	s.mu.Unlock()

	row.Kind = proto.JobAgent
	row.Name = label
	row.Status = proto.JobRunning
	s.emit(proto.Emit(proto.JobStarted, row))
	if done != nil {
		s.childTurnCompleted(c, done)
		return
	}
	s.interruptLate(c, late)
}

// lateInterruptLocked claims c for interrupting if its running turn is work
// for a stopped turn of ours, and returns that turn; empty otherwise. s.mu
// must be held.
func (s *session) lateInterruptLocked(c *child) string {
	if c.serverTurn == "" || c.interrupting || !s.stoppedTurns[c.workTurn] {
		return ""
	}
	c.interrupting = true
	return c.serverTurn
}

// interruptLate interrupts a child lateInterruptLocked claimed. It runs off
// this goroutine: that is the read loop, and the response cannot be read
// while it waits for one.
func (s *session) interruptLate(c *child, turn string) {
	if turn == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lateInterruptTimeout)
		defer cancel()
		_ = s.interruptChild(ctx, c, turn)
	}()
}

// handleChildNotification applies one notification from a thread other than
// this session's.
func (s *session) handleChildNotification(method, thread string, params json.RawMessage) {
	s.mu.Lock()
	c := s.children[thread]
	if c == nil {
		s.unregisteredLocked(method, thread, params)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	switch method {
	case "turn/started":
		var p struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		s.childTurnStarted(c, p.Turn.ID)

	case "turn/completed", "turn/failed":
		s.childTurnCompleted(c, params)

	case "item/started", "item/completed":
		var p struct {
			Item   codexItem `json:"item"`
			TurnID string    `json:"turnId"`
		}
		_ = json.Unmarshal(params, &p)
		completed := method == "item/completed"
		// An item naming a turn the child was not known to be running is
		// that turn starting, heard about late.
		s.mu.Lock()
		started := c.serverTurn == "" && p.TurnID != "" && p.TurnID != c.finishedTurn
		s.mu.Unlock()
		if started {
			s.childTurnStarted(c, p.TurnID)
		}
		switch p.Item.Type {
		case "contextCompaction", "userMessage":
			// The child compacting its own context says nothing about this
			// session's, and its user message is the spawn prompt, already
			// on the spawn's tool call.
			return
		}
		s.handleItem(method, c.spawnTurn, c, params)
		s.childActivity(c, completed, p.Item)

	case "item/agentMessage/delta":
		s.emitMessageDelta(c.spawnTurn, c.toolCallID, parseDelta(params))

	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		s.emitReasoningDelta(c.spawnTurn, c.toolCallID, parseDelta(params))

	case "item/commandExecution/outputDelta":
		s.emitOutputDelta(parseDelta(params))

	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Total struct {
					TotalTokens  int64 `json:"totalTokens"`
					InputTokens  int64 `json:"inputTokens"`
					OutputTokens int64 `json:"outputTokens"`
				} `json:"total"`
			} `json:"tokenUsage"`
		}
		_ = json.Unmarshal(params, &p)
		t := p.TokenUsage.Total
		total := t.TotalTokens
		if total == 0 {
			total = t.InputTokens + t.OutputTokens
		}
		if total == 0 {
			return
		}
		row := c.row()
		row.Usage = &proto.JobUsage{TotalTokens: total}
		s.emit(proto.Emit(proto.JobUpdated, row))
	}
}

// unregisteredLocked handles a thread not (yet) known as a child. Its turn
// starting and finishing are kept for registration; the rest is dropped.
// Caller holds s.mu.
func (s *session) unregisteredLocked(method, thread string, params json.RawMessage) {
	switch method {
	case "turn/started":
		var p struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Turn.ID != "" {
			if s.pending == nil {
				s.pending = map[string]*pendingChild{}
			}
			s.pending[thread] = &pendingChild{turn: p.Turn.ID}
		}
		return
	case "turn/completed", "turn/failed":
		if s.pending == nil {
			s.pending = map[string]*pendingChild{}
		}
		p := s.pending[thread]
		if p == nil {
			p = &pendingChild{}
			s.pending[thread] = p
		}
		p.done = params
		return
	}
	// Status and MCP startup chatter precede every child's registration;
	// only real output from a thread nobody spawned is worth a line.
	if method == "thread/status/changed" || strings.HasPrefix(method, "mcpServer/") {
		return
	}
	if s.unknownThreads[thread] {
		return
	}
	if s.unknownThreads == nil {
		s.unknownThreads = map[string]bool{}
	}
	s.unknownThreads[thread] = true
	s.host.Logf("codex: dropping output from unknown thread %s (first: %s)", thread, method)
}

// childActivity reports what a child is doing now: the tool it started, or
// the message it just finished.
func (s *session) childActivity(c *child, completed bool, it codexItem) {
	var activity string
	switch it.Type {
	case "commandExecution", "fileChange", "mcpToolCall", "webSearch":
		if completed {
			return
		}
		activity = toolTitle(it)
	case "agentMessage":
		if !completed || it.Text == "" {
			return
		}
		s.mu.Lock()
		c.lastMessage = it.Text
		if it.Phase == "final_answer" {
			c.finalMessage = it.Text
		}
		s.mu.Unlock()
		activity = firstLine(it.Text)
	default:
		return
	}
	if activity == "" {
		return
	}
	row := c.row()
	row.Activity = activity
	s.emit(proto.Emit(proto.JobUpdated, row))
}

// childTurnStarted records the child's running turn, and the turn of ours
// that asked for it: a child's later work comes from whichever of our turns
// sent it input, which is the one running now. One that starts a turn while
// we are idle is still working for the turn it last did, so a child of a
// stopped turn that starts one after the interrupt went out — it was between
// turns when Stop was pressed — is stopped as it starts, until a new turn of
// ours hands it work. A settled child that starts another turn is running
// again.
func (s *session) childTurnStarted(c *child, turn string) {
	s.mu.Lock()
	if turn == "" || turn == c.serverTurn {
		s.mu.Unlock()
		return
	}
	c.serverTurn = turn
	c.interrupting = false
	if s.turnID != "" {
		c.workTurn = s.turnID
	}
	late := s.lateInterruptLocked(c)
	reopened := c.settled
	if reopened {
		c.settled = false
		c.lastMessage, c.finalMessage = "", ""
	}
	s.mu.Unlock()
	if reopened {
		row := c.row()
		row.Status = proto.JobRunning
		s.emit(proto.Emit(proto.JobUpdated, row))
	}
	s.interruptLate(c, late)
}

// childTurnCompleted finishes the job, and queues a wake when nothing of
// ours is running to read the result.
func (s *session) childTurnCompleted(c *child, params json.RawMessage) {
	var p struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Items []codexItem `json:"items"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(params, &p)

	row := c.row()
	// Interrupted first: a stopped child must never read as a failure, which
	// would wake the session to report the stop the human just made.
	switch {
	case p.Turn.Status == "interrupted" || p.Turn.Status == "cancelled":
		row.Status = proto.JobStopped
	case p.Turn.Error != nil:
		row.Status, row.Error = proto.JobFailed, p.Turn.Error.Message
	case p.Turn.Status == "failed":
		row.Status = proto.JobFailed
	default:
		row.Status = proto.JobCompleted
	}

	s.mu.Lock()
	// A completion for a turn other than the child's running one is stale —
	// a duplicate of one already applied — and so is any after it settled.
	if c.settled || (p.Turn.ID != "" && c.serverTurn != "" && p.Turn.ID != c.serverTurn) {
		s.mu.Unlock()
		return
	}
	// A final answer only in the turn's summary never reached the job's pane
	// as an item; it goes out as one below, unless its deltas already did.
	// It beats an earlier message, which was only commentary; the summary's
	// last message stands in for an answer only when nothing else was seen.
	final := c.finalMessage
	var summary *codexItem
	if final == "" {
		it := turnFinalMessage(p.Turn.Items)
		if it != nil && (it.Phase == "final_answer" || c.lastMessage == "") {
			final = it.Text
			if !s.streamed[it.ID] {
				summary = it
			}
		} else {
			final = c.lastMessage
		}
	}
	c.settled = true
	c.finishedTurn = c.serverTurn
	if p.Turn.ID != "" {
		c.finishedTurn = p.Turn.ID
	}
	c.serverTurn = ""
	c.interrupting = false
	c.lastMessage, c.finalMessage = "", ""
	// Codex does not wake a finished turn for a child's result, so the result
	// would sit unread. Only direct children wake us (a grandchild reports to
	// the child that spawned it), only for work the human did not stop, and
	// only when idle: a running turn of ours receives the result natively.
	if c.parentJobID == "" &&
		(row.Status == proto.JobCompleted || row.Status == proto.JobFailed) &&
		!s.stoppedTurns[c.workTurn] && s.turnID == "" {
		entry := fmt.Sprintf("Subagent %s: %s", row.Status, c.label)
		// A child that failed before answering still owes the session why.
		result := final
		if result == "" {
			result = row.Error
		}
		if result != "" {
			entry += "\n\nResult:\n" + result
		}
		s.queueWakeLocked(entry)
	}
	s.mu.Unlock()

	if summary != nil {
		s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
			TurnID: c.spawnTurn, Role: "agent", Kind: "text", BlockID: summary.ID, Delta: summary.Text,
			ParentToolCallID: c.toolCallID,
		}))
	}
	if final != "" {
		row.Activity = firstLine(final)
	}
	s.emit(proto.Emit(proto.JobFinished, row))
}

// turnFinalMessage is the final answer among a completed turn's summary
// items, else its last message; nil when it has none.
func turnFinalMessage(items []codexItem) *codexItem {
	var last *codexItem
	for i := range items {
		it := &items[i]
		if it.Type != "agentMessage" || it.Text == "" {
			continue
		}
		if it.Phase == "final_answer" {
			return it
		}
		last = it
	}
	return last
}

// queueWakeLocked adds a finished child to the pending wake; the first entry
// arms the timer the rest ride on. Caller holds s.mu.
func (s *session) queueWakeLocked(entry string) {
	s.wakeQueue = append(s.wakeQueue, entry)
	if s.wakeTimer != nil {
		return
	}
	d := s.wakeDelay
	if d == 0 {
		d = defaultWakeDelay
	}
	s.wakeTimer = time.AfterFunc(d, s.flushWake)
}

// flushWake starts one turn carrying every queued child's result. It runs
// off the read loop (the timer's goroutine), so the turn/start round trip
// cannot deadlock the connection.
func (s *session) flushWake() {
	s.mu.Lock()
	entries := s.wakeQueue
	s.wakeQueue = nil
	if s.wakeTimer != nil {
		s.wakeTimer.Stop()
		s.wakeTimer = nil
	}
	select {
	case <-s.done:
		s.mu.Unlock()
		return
	default:
	}
	// A turn that started since the entries were queued gets the results
	// from codex itself. The check and the claim are one critical section,
	// the same as Prompt's guard, so a prompt cannot slip between them.
	if len(entries) == 0 || s.turnID != "" {
		s.mu.Unlock()
		return
	}
	id := uuid.NewString()
	s.turnID = id
	s.serverTurnID, s.runningTurnID = "", ""
	s.interrupting = false
	s.mu.Unlock()
	text := strings.Join(entries, "\n\n")

	s.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: id, Prompt: text}))

	ours, err := s.startTurn(context.Background(), id, text, nil)
	if err != nil {
		s.host.Logf("codex: waking for subagent results: %v", err)
		// Nobody waits on the wake to report its failure, so it closes its
		// own turn; one the read loop already closed is not its to close.
		if ours {
			s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
				TurnID: id, StopReason: proto.StopError, Error: err.Error(),
			}))
		}
	}
}

// StopJob interrupts one subagent's running turn. Its job finishes through
// the child's own turn/completed, as stopped, which never wakes the session.
// A second stop while the first is in flight is coalesced, as Cancel's is.
func (s *session) StopJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	c := s.children[jobID]
	if c == nil {
		s.mu.Unlock()
		return fmt.Errorf("no codex subagent %q", jobID)
	}
	turn := c.serverTurn
	if c.settled || turn == "" {
		s.mu.Unlock()
		return fmt.Errorf("codex subagent %q is not running", jobID)
	}
	if c.interrupting {
		s.mu.Unlock()
		return nil
	}
	c.interrupting = true
	s.mu.Unlock()
	return s.interruptChild(ctx, c, turn)
}
