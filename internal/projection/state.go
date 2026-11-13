// Package projection folds the event log into the state a UI renders. It is
// derived data: dropping every projection and rebuilding from the log must
// yield an identical result.
package projection

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/asiraky/omniplex/internal/proto"
)

// Attention is the derived answer to "whose turn is it?" — the one signal a
// thread list, a notifier, or any future routing system should read. It is a
// pure function of projected state, never stored: the log stays authoritative.
//
// The user's-turn states are deliberately split three ways. "Waiting for a
// prompt", "waiting for a permission decision", and "waiting for an answer to
// a question" are all the user's turn, but anything built on top of this (a
// notification, a queue, a triage view) needs to know which kind of turn it is.
const (
	// AttentionWorking: the agent (or the workspace lifecycle) is busy; there
	// is nothing for the user to do but wait or interrupt.
	AttentionWorking = "working"
	// AttentionNeedsPermission: the harness is parked on a tool-permission
	// decision. The user's turn, and time-sensitive: the agent is blocked.
	AttentionNeedsPermission = "needs_permission"
	// AttentionNeedsAnswer: the harness asked a question (elicitation) and is
	// blocked on the reply. The user's turn.
	AttentionNeedsAnswer = "needs_answer"
	// AttentionBackground: no turn is open, but work the agent will come
	// back with is still live — a subagent finishing up, a monitor waiting on
	// its condition. Not the user's turn: the composer is sendable, but
	// "waiting for you" would be a lie while that work is running. A live
	// shell alone does not count: a dev server or a tail runs beside the
	// thread for as long as it likes, and nobody is waiting on it.
	AttentionBackground = "background"
	// AttentionNeedsPrompt: idle — the conversation is with the user.
	AttentionNeedsPrompt = "needs_prompt"
	// AttentionFailed: workspace provisioning or cleanup failed; the thread
	// needs a human decision before anything else can happen.
	AttentionFailed = "failed"
	// AttentionClosed: the thread is a closed transcript.
	AttentionClosed = "closed"
)

// Attention derives the thread's attention state from the projection. Pending
// human requests outrank the running turn on purpose: a turn that is blocked
// on a permission is the user's turn, not the agent's.
func (s *State) Attention() string {
	switch {
	case s.Closed || s.Phase == "closed":
		return AttentionClosed
	case s.Phase == "provision_failed" || s.Phase == "cleanup_failed":
		return AttentionFailed
	case len(s.Pending) > 0:
		return AttentionNeedsPermission
	case len(s.Elicitations) > 0:
		return AttentionNeedsAnswer
	case s.Phase == "turn" || s.Phase == "provisioning" || s.Phase == "cleaning":
		return AttentionWorking
	case s.LiveJobs().Working():
		return AttentionBackground
	default:
		return AttentionNeedsPrompt
	}
}

// LastTurnFailed reports whether the thread's latest turn ended in an error.
// Only the last turn counts: a newer turn starting clears it, because that
// turn is not done yet. Attention cannot carry this — a failed turn goes
// idle exactly like a finished one — so the thread list sends it alongside.
func (s *State) LastTurnFailed() bool {
	if len(s.Turns) == 0 {
		return false
	}
	t := s.Turns[len(s.Turns)-1]
	return t.Done && t.StopReason == proto.StopError
}

// JobCounts is how many jobs of each kind are live right now.
type JobCounts struct {
	Agents   int `json:"agents"`
	Shells   int `json:"shells"`
	Monitors int `json:"monitors"`
}

func (c JobCounts) Any() bool { return c.Agents+c.Shells+c.Monitors > 0 }

// Working: some live job will hand the agent something to come back with — a
// subagent's result, a monitor's event. Shells are left out: a dev server is
// still running, but no one is waiting on it to finish.
func (c JobCounts) Working() bool { return c.Agents+c.Monitors > 0 }

// LiveJobs counts the jobs that are still running, by kind. Inert jobs are
// never live: they are the harness's own housekeeping, not work anyone is
// waiting on. Liveness is a channel parallel to the turn, not part of it —
// the turn settles on its own terms, and this says what is still going.
func (s *State) LiveJobs() JobCounts {
	var c JobCounts
	for _, j := range s.Jobs {
		if proto.JobDone(j.Status) {
			continue
		}
		switch j.Kind {
		case proto.JobAgent:
			c.Agents++
		case proto.JobShell:
			c.Shells++
		case proto.JobMonitor:
			c.Monitors++
		}
	}
	return c
}

// AttentionForPhase derives attention for a thread with no live projection —
// a row in the store whose actor is not running. A dead actor cancels its
// pending permissions on the way down, so the phase alone is enough.
func AttentionForPhase(phase string) string {
	switch phase {
	case "closed":
		return AttentionClosed
	case "provision_failed", "cleanup_failed":
		return AttentionFailed
	case "turn", "creating", "provisioning", "cleaning":
		return AttentionWorking
	case "background":
		return AttentionBackground
	default:
		return AttentionNeedsPrompt
	}
}

// ItemKind discriminates timeline entries.
const (
	ItemMessage = "message"
	ItemTool    = "tool"
	// ItemNotice is a system event worth a line in the timeline that is
	// neither a message nor a tool call: a context compaction, a switch to
	// another account, or a change of model or effort mid-thread.
	ItemNotice = "notice"
	// ItemArtefact is an artefact the agent showed, at the point in the
	// conversation it showed it. Uploads get no item: they show on the
	// message that carried them.
	ItemArtefact = "artefact"
	// ItemCard is a proposal the agent raised through omniplex's own tools,
	// where it raised it. Its Status is pending until answered, then the
	// outcome's result: saved, declined or cancelled.
	ItemCard = "card"
	// ItemPlan is the agent's task list, where it set it. A Claude TodoWrite
	// call is an item of its own, and the newest marks the earlier ones
	// Superseded. A codex plan is one item per turn, revised in place.
	ItemPlan = "plan"
)

// Item is one entry in the thread timeline, in the order it first appeared.
type Item struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	TurnID string `json:"turnId,omitempty"`
	// ParentID links work done inside a subagent to the Task/Agent tool call
	// that spawned it. The transcript folds such items away; the subagents
	// surface groups by it.
	ParentID string `json:"parentId,omitempty"`
	// ReceivedAt is when the item's first event landed, in millis. It is
	// display metadata: the timeline's order is the log's order regardless.
	ReceivedAt int64 `json:"receivedAt,omitempty"`

	// message
	Role        string `json:"role,omitempty"`
	ContentKind string `json:"contentKind,omitempty"` // text | thought
	Text        string `json:"text,omitempty"`
	// Images a user message carried. References, not bytes: a presenter reads
	// each one back from the attachment endpoint.
	Images []proto.PromptImage `json:"images,omitempty"`

	// tool
	ToolKind string              `json:"toolKind,omitempty"`
	Title    string              `json:"title,omitempty"`
	Status   string              `json:"status,omitempty"`
	Input    json.RawMessage     `json:"input,omitempty"`
	Content  []proto.ToolContent `json:"content,omitempty"`
	// Tail is the last lines a running call has printed, folded from its
	// output deltas and bounded (see appendTail). It is cleared the moment the
	// call finishes: from then on Content holds the output, capped to a preview
	// when it is long (see Truncated).
	Tail string `json:"tail,omitempty"`
	// Truncated: some of Content was cut to a preview or dropped (see
	// capText), or Input was capped on the way to a client. The event log
	// still has it whole.
	Truncated bool `json:"truncated,omitempty"`
	// ChildrenBefore is how many of this item's child items a wire copy left
	// out (see WireCopy). Zero on the actor's own state.
	ChildrenBefore int `json:"childrenBefore,omitempty"`

	// notice
	NoticeKind string `json:"noticeKind,omitempty"` // compaction | account | model | notify
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	// From is what the turn before ran on. The log names models by whatever
	// id it was handed, so an alias and the id it resolves to can differ
	// here and still be one model; a presenter that knows the catalogue can
	// tell, and leaves such a line out.
	From       *TurnSettings `json:"from,omitempty"`
	Trigger    string        `json:"trigger,omitempty"` // auto | manual
	PreTokens  int64         `json:"preTokens,omitempty"`
	PostTokens int64         `json:"postTokens,omitempty"`
	// Level is a notify notice's severity, warning or error; Text is the
	// extension's message.
	Level string `json:"level,omitempty"`

	// artefact: Title is its name. Enough to draw the tile without the
	// artefact list.
	ArtefactID string `json:"artefactId,omitempty"`
	MediaType  string `json:"mediaType,omitempty"`
	Size       int64  `json:"size,omitempty"`

	// card: what was proposed, and once answered what became of it. Status
	// is the outcome's result.
	Card    json.RawMessage `json:"card,omitempty"`
	Outcome json.RawMessage `json:"outcome,omitempty"`

	// plan: the whole list as it stands, never a delta. Superseded is set on
	// a TodoWrite list once a later one exists.
	Steps       []proto.PlanStep `json:"steps,omitempty"`
	Explanation string           `json:"explanation,omitempty"`
	Superseded  bool             `json:"superseded,omitempty"`
}

// settle moves a tool call to a terminal status. The tail goes with it: it
// only ever stands for output still arriving.
func (it *Item) settle(status string) {
	it.Status = status
	it.Tail = ""
}

// ExtensionStatus is one status line a harness extension keeps in the thread
// header, under the extension's own key.
type ExtensionStatus struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Artefact is a file or folder the thread has shown, as it was the last time
// it was shown. The file itself is live on disk and may have moved on.
type Artefact struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Dir        bool   `json:"dir,omitempty"`
	MediaType  string `json:"mediaType"`
	Size       int64  `json:"size"`
	Entry      string `json:"entry"`
	Files      int    `json:"files"`
	ModifiedAt int64  `json:"modifiedAt"`
	Source     string `json:"source"`
	Note       string `json:"note,omitempty"`
	TurnID     string `json:"turnId,omitempty"`
	ShownAt    int64  `json:"shownAt"`
}

// Job is work running beside the conversation: a subagent, a background
// shell, a monitor. It is the projection of the job.* events, merged field by
// field so a usage tick never blanks a name.
type Job struct {
	ID          string `json:"id"`
	ToolCallID  string `json:"toolCallId,omitempty"`
	ParentJobID string `json:"parentJobId,omitempty"`
	// Depth is how many parents are above this job: 0 at the top level.
	Depth        int            `json:"depth"`
	Kind         string         `json:"kind"`
	TaskType     string         `json:"taskType,omitempty"`
	Name         string         `json:"name,omitempty"`
	Role         string         `json:"role,omitempty"`
	WorkflowName string         `json:"workflowName,omitempty"`
	Status       string         `json:"status"`
	Activity     string         `json:"activity,omitempty"`
	Usage        proto.JobUsage `json:"usage"`
	Error        string         `json:"error,omitempty"`
	OutputFile   string         `json:"outputFile,omitempty"`
	Backgrounded bool           `json:"backgrounded,omitempty"`
	Hidden       bool           `json:"hidden,omitempty"`
	TurnID       string         `json:"turnId,omitempty"`
	StartedAt    int64          `json:"startedAt,omitempty"`
	FinishedAt   int64          `json:"finishedAt,omitempty"`
}

// Turn records the lifecycle of one prompt/response cycle.
type Turn struct {
	ID         string              `json:"id"`
	Prompt     string              `json:"prompt"`
	Images     []proto.PromptImage `json:"images,omitempty"`
	StopReason string              `json:"stopReason,omitempty"`
	Error      string              `json:"error,omitempty"`
	// Failure classifies Error, so a presenter branches on a kind rather than
	// on the wording of a message.
	Failure string `json:"failure,omitempty"`
	Done    bool   `json:"done"`
	// Recovery is set when the server started this turn itself to continue
	// work a restart interrupted.
	Recovery *proto.TurnRecovery `json:"recovery,omitempty"`
	// Diff is what the turn changed on disk, and arrives after the turn is
	// done. A turn that changed nothing never gets one.
	Diff *proto.TurnDiffPayload `json:"diff,omitempty"`
	// When the turn started and finished, from the event log's own clock.
	// The UI folds a finished turn behind "Worked for 34s", and that label
	// is measured here rather than in any presenter.
	StartedAt  int64 `json:"startedAt,omitempty"`
	FinishedAt int64 `json:"finishedAt,omitempty"`
}

// QueuedPrompt is a prompt waiting for the running turn to finish. It lives in
// the log so it survives a restart and every presenter can see and remove it.
type QueuedPrompt struct {
	QueueID  string              `json:"queueId"`
	Prompt   string              `json:"prompt"`
	Images   []proto.PromptImage `json:"images,omitempty"`
	QueuedAt int64               `json:"queuedAt,omitempty"`
	// Sent means the harness holds it and reads it at its next step; it
	// cannot be taken back.
	Sent bool `json:"sent,omitempty"`
	// Held means it waits for a human to resume the queue rather than for
	// the thread to go idle.
	Held bool `json:"held,omitempty"`
}

// PendingPermission is a permission request awaiting a human. It lives in the
// log, not in a connection, so any attached presenter can answer it.
type PendingPermission struct {
	RequestID  string                   `json:"requestId"`
	TurnID     string                   `json:"turnId,omitempty"`
	ToolCallID string                   `json:"toolCallId"`
	ToolName   string                   `json:"toolName"`
	Title      string                   `json:"title"`
	Input      json.RawMessage          `json:"input,omitempty"`
	Options    []proto.PermissionOption `json:"options"`
}

type PendingElicitation struct {
	RequestID string          `json:"requestId"`
	TurnID    string          `json:"turnId,omitempty"`
	Prompt    string          `json:"prompt"`
	Schema    json.RawMessage `json:"schema"`
	// Card is set on a proposal the agent raised through omniplex's tools.
	// Omniplex holds it, not the harness, so it does not hold the turn up.
	Card json.RawMessage `json:"card,omitempty"`
}

// IsCard reports whether this is a proposal omniplex holds rather than a
// question the harness is blocked on.
func (p PendingElicitation) IsCard() bool { return len(p.Card) > 0 }

// Questions counts the elicitations a harness is blocked on: cards are left
// out, since they hold nothing up.
func (s *State) Questions() int {
	n := 0
	for _, p := range s.Elicitations {
		if !p.IsCard() {
			n++
		}
	}
	return n
}

// QueueHeld says queued prompts must wait: the last turn stopped at a usage
// limit, and each would only hit it again. The next turn to start — the
// server's continuation, or anything a human sends — lifts it, and so does
// moving the thread to another account: the limit was the old one's.
// Mirrored by web/src/components/transcript/useTranscriptModel.ts.
func (s *State) QueueHeld() bool {
	if len(s.Turns) == 0 {
		return false
	}
	last := s.Turns[len(s.Turns)-1]
	if !last.Done || last.Failure != proto.FailureLimit {
		return false
	}
	for i := len(s.Items) - 1; i >= 0 && s.Items[i].ReceivedAt >= last.FinishedAt; i-- {
		if it := s.Items[i]; it.Kind == ItemNotice && it.NoticeKind == "account" {
			return false
		}
	}
	return true
}

// HeldByLimit says whether a held queue holds q: it was queued before the
// limit stopped the turn. One queued since is a prompt sent by hand that had
// to wait on a settings change, and it is what lifts the hold.
func (s *State) HeldByLimit(q QueuedPrompt) bool {
	return s.QueueHeld() && q.QueuedAt <= s.Turns[len(s.Turns)-1].FinishedAt
}

// CardID is the timeline item id of the card a request raised.
func CardID(requestID string) string { return "card:" + requestID }

// TurnPlanID is the timeline item id of a turn's plan (codex), revised in
// place as the turn goes.
func TurnPlanID(turnID string) string { return "turn-plan:" + turnID }

const todoPlanPrefix = "todo:"

// TodoPlanID is the timeline item id of one TodoWrite call's list.
func TodoPlanID(toolCallID string) string { return todoPlanPrefix + toolCallID }

// isTodoPlan reports whether an item is a TodoWrite list: the kind of plan a
// newer one supersedes.
func isTodoPlan(it Item) bool { return it.Kind == ItemPlan && strings.HasPrefix(it.ID, todoPlanPrefix) }

type WorkspaceState struct {
	Phase              string         `json:"phase"`
	ProjectID          string         `json:"projectId,omitempty"`
	ProjectRoot        string         `json:"projectRoot,omitempty"`
	Mode               string         `json:"mode,omitempty"`
	Branch             string         `json:"branch,omitempty"`
	BaseRef            string         `json:"baseRef,omitempty"`
	Hook               string         `json:"hook,omitempty"`
	Command            string         `json:"command,omitempty"`
	Output             string         `json:"output,omitempty"`
	Error              string         `json:"error,omitempty"`
	ExitCode           int            `json:"exitCode,omitempty"`
	StartedAt          int64          `json:"startedAt,omitempty"`
	DurationMs         int64          `json:"durationMs,omitempty"`
	Resources          map[string]any `json:"resources,omitempty"`
	DeleteAfterCleanup bool           `json:"deleteAfterCleanup,omitempty"`
}

// State is the complete renderable state of a thread as of Seq.
type State struct {
	Scheduled []proto.ScheduledPrompt `json:"scheduledPrompts,omitempty"`
	ThreadID  string                  `json:"threadId"`
	Seq       int64                   `json:"seq"`
	Cwd       string                  `json:"cwd"`
	Harness   string                  `json:"harness"`
	Model     string                  `json:"model"`
	Mode      string                  `json:"mode"`
	Effort    string                  `json:"effort"`
	// ServiceTier is a harness service-tier id every turn runs at, or "" for
	// standard speed.
	ServiceTier string `json:"serviceTier"`
	Title       string `json:"title"`
	// HarnessSessionID is the harness's own conversation id, used to resume.
	HarnessSessionID string         `json:"harnessSessionId,omitempty"`
	Phase            string         `json:"phase"` // idle | turn | closed
	Closed           bool           `json:"closed"`
	Workspace        WorkspaceState `json:"workspace"`

	Items []Item `json:"items"`
	// ItemsBefore is how many items were trimmed from the front of Items when
	// this state was windowed for the wire (see WireCopy). Zero on the actor's
	// own state and on stored snapshots: only copies sent to a client carry a
	// window, and the count doubles as the client's cursor for paging older
	// items back in.
	ItemsBefore int    `json:"itemsBefore,omitempty"`
	Turns       []Turn `json:"turns"`
	Jobs        []Job  `json:"jobs"`
	// JobsBefore is how many jobs a wire copy left out of Jobs (see
	// WireCopy). Zero on the actor's own state and on stored snapshots.
	JobsBefore   int                       `json:"jobsBefore,omitempty"`
	Usage        proto.UsageUpdatedPayload `json:"usage"`
	Pending      []PendingPermission       `json:"pendingPermissions"`
	Elicitations []PendingElicitation      `json:"pendingElicitations"`
	Queued       []QueuedPrompt            `json:"queuedPrompts"`
	// Artefacts is never windowed: it is small, and the list has to be whole
	// for the artefacts surface to be.
	Artefacts []Artefact `json:"artefacts"`
	// RanWith is the model and effort the latest turn started on, which the
	// next turn is measured against to mark a switch. Nil until a turn has
	// started, and on snapshots stored before it existed.
	RanWith *TurnSettings `json:"ranWith,omitempty"`
	// ExtensionStatuses are the status lines harness extensions keep in the
	// thread header, in the order their keys first appeared. Never windowed.
	ExtensionStatuses []ExtensionStatus `json:"extensionStatuses"`

	itemIndex map[string]int `json:"-"`
	jobIndex  map[string]int `json:"-"`
	// recapped is set by FromSnapshot when the stored snapshot held tool
	// content over the cap: the row on disk is still the big one and wants
	// rewriting.
	recapped bool
}

// TurnSettings is the model and effort a turn runs on.
type TurnSettings struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func (s *State) settings() TurnSettings { return TurnSettings{Model: s.Model, Effort: s.Effort} }

func New(threadID string) *State {
	return &State{
		ThreadID:          threadID,
		Phase:             "idle",
		Items:             []Item{},
		Turns:             []Turn{},
		Jobs:              []Job{},
		Pending:           []PendingPermission{},
		Elicitations:      []PendingElicitation{},
		Queued:            []QueuedPrompt{},
		Artefacts:         []Artefact{},
		ExtensionStatuses: []ExtensionStatus{},
		itemIndex:         map[string]int{},
		jobIndex:          map[string]int{},
	}
}

// reindex rebuilds the id→position maps, needed after a snapshot is decoded.
func (s *State) reindex() {
	s.itemIndex = make(map[string]int, len(s.Items))
	for i, it := range s.Items {
		s.itemIndex[it.ID] = i
	}
	s.reindexJobs()
}

func (s *State) reindexJobs() {
	s.jobIndex = make(map[string]int, len(s.Jobs))
	for i, j := range s.Jobs {
		s.jobIndex[j.ID] = i
	}
}

// Recapped reports whether FromSnapshot had to cap tool content the stored
// snapshot held whole, so the stored snapshot is worth replacing.
func (s *State) Recapped() bool { return s.recapped }

// FromSnapshot decodes a stored snapshot back into usable state.
func FromSnapshot(blob json.RawMessage) (*State, error) {
	var s State
	if err := json.Unmarshal(blob, &s); err != nil {
		return nil, err
	}
	if s.Artefacts == nil {
		s.Artefacts = []Artefact{}
	}
	if s.ExtensionStatuses == nil {
		s.ExtensionStatuses = []ExtensionStatus{}
	}
	// A snapshot from before tool content was capped holds it whole. Capping
	// here shrinks it the first time it loads, and the next snapshot written
	// persists the small form.
	for i := range s.Items {
		if capItem(&s.Items[i]) {
			s.recapped = true
		}
	}
	s.reindex()
	return &s, nil
}

// ItemByID returns the timeline item with that id.
func (s *State) ItemByID(id string) (Item, bool) {
	if s.itemIndex == nil {
		s.reindex()
	}
	i, ok := s.itemIndex[id]
	if !ok {
		return Item{}, false
	}
	return s.Items[i], true
}

// job finds a job by id through jobIndex. A long orchestrator thread runs
// thousands of jobs and folds a job.updated for every usage tick, so a scan
// here made replay quadratic.
func (s *State) job(id string) *Job {
	if s.jobIndex == nil {
		s.reindexJobs()
	}
	if i, ok := s.jobIndex[id]; ok {
		return &s.Jobs[i]
	}
	return nil
}

// applyJob folds one job event. Every row carries the linkage bundle, so a
// job whose start was never seen is created from whatever row arrives first.
func (s *State) applyJob(ev proto.Event, finished bool) {
	var p proto.JobPayload
	decode(ev.Payload, &p)
	if p.JobID == "" {
		return
	}
	j := s.job(p.JobID)
	if j == nil {
		s.Jobs = append(s.Jobs, Job{ID: p.JobID, Status: proto.JobRunning, StartedAt: ev.Timestamp})
		s.jobIndex[p.JobID] = len(s.Jobs) - 1
		j = &s.Jobs[len(s.Jobs)-1]
		// The turn that spawned it, for the transcript to pin its card to.
		for i := range s.Turns {
			if !s.Turns[i].Done {
				j.TurnID = s.Turns[i].ID
			}
		}
	}
	if p.ToolCallID != "" {
		j.ToolCallID = p.ToolCallID
	}
	if p.ParentJobID != "" {
		j.ParentJobID = p.ParentJobID
		if parent := s.job(p.ParentJobID); parent != nil {
			j.Depth = parent.Depth + 1
		}
	}
	if p.Kind != "" {
		j.Kind = p.Kind
	}
	if j.Kind == "" {
		j.Kind = proto.ClassifyJob(p.TaskType)
	}
	if p.TaskType != "" {
		j.TaskType = p.TaskType
	}
	if p.Name != "" {
		j.Name = p.Name
	}
	if p.Role != "" {
		j.Role = p.Role
	}
	if p.WorkflowName != "" {
		j.WorkflowName = p.WorkflowName
	}
	if p.Activity != "" {
		j.Activity = p.Activity
	}
	if p.Usage != nil {
		// Usage is a running total: a row that reports less than we have
		// already seen is a partial (a cost tick without tokens), not a reset.
		if p.Usage.TotalTokens > 0 {
			j.Usage.TotalTokens = p.Usage.TotalTokens
		}
		if p.Usage.ToolUses > 0 {
			j.Usage.ToolUses = p.Usage.ToolUses
		}
		if p.Usage.DurationMs > 0 {
			j.Usage.DurationMs = p.Usage.DurationMs
		}
		if p.Usage.Cost > 0 {
			j.Usage.Cost = p.Usage.Cost
		}
	}
	if p.Error != "" {
		j.Error = p.Error
	}
	if p.OutputFile != "" {
		j.OutputFile = p.OutputFile
	}
	if p.Backgrounded != nil {
		j.Backgrounded = *p.Backgrounded
	}
	if p.Hidden {
		j.Hidden = true
	}
	if p.Status != "" {
		// A finished job reported running again was reopened (a Codex
		// subagent taking a new turn): its elapsed clock runs again.
		if !finished && !proto.JobDone(p.Status) {
			j.FinishedAt = 0
		}
		j.Status = p.Status
	}
	if finished {
		if !proto.JobDone(j.Status) {
			j.Status = proto.JobCompleted
		}
		if j.FinishedAt == 0 {
			j.FinishedAt = ev.Timestamp
		}
		// The spawning tool call, if it was still waiting on this job, is
		// settled by it: a stopped agent's Task row must not spin forever.
		// So is the job's own work left mid-flight (a stopped subagent's
		// command): its parent's turn finishing spared it for the job, and
		// the job is over. A real completion arriving later still lands.
		if j.ToolCallID != "" {
			if i, ok := s.itemIndex[j.ToolCallID]; ok {
				it := &s.Items[i]
				if proto.ToolRunning(it.Status) {
					switch j.Status {
					case proto.JobFailed:
						it.settle(proto.StatusFailed)
					case proto.JobCompleted:
						it.settle(proto.StatusCompleted)
					default:
						it.settle(proto.StatusCancelled)
					}
				}
			}
			// A live grandchild's spawn call is still its job's to settle.
			live := s.liveJobCalls()
			for i := range s.Items {
				it := &s.Items[i]
				if it.Kind == ItemTool && it.ParentID == j.ToolCallID && !live[it.ID] &&
					proto.ToolRunning(it.Status) {
					it.settle(proto.StatusCancelled)
				}
			}
		}
	} else if proto.JobDone(j.Status) && j.FinishedAt == 0 {
		j.FinishedAt = ev.Timestamp
	}
}

// liveJobCalls is the tool calls standing for jobs still running.
func (s *State) liveJobCalls() map[string]bool {
	live := map[string]bool{}
	for _, j := range s.Jobs {
		if j.ToolCallID != "" && !proto.JobDone(j.Status) {
			live[j.ToolCallID] = true
		}
	}
	return live
}

// markSettings puts a line in the timeline where a turn starts on a model or
// effort other than the last turn's, so every reply reads under the settings
// that produced it. It is drawn at the turn rather than at the switch: a
// harness may take a switch made mid-turn only at the next one, picking
// through the menu is one change rather than five, and switching away and
// back again is no change at all. Anchored to the sequence like a compaction
// so replays land the same item.
func (s *State) markSettings(ev proto.Event) {
	now := s.settings()
	if was := s.RanWith; was != nil && *was != now {
		s.upsert("model:"+strconv.FormatInt(ev.Seq, 10), func(it *Item) {
			it.Kind = ItemNotice
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.NoticeKind = "model"
			it.Model, it.Effort = now.Model, now.Effort
			it.From = was
		})
	}
	s.RanWith = &now
}

func (s *State) upsert(id string, mut func(*Item)) {
	if s.itemIndex == nil {
		s.reindex()
	}
	if i, ok := s.itemIndex[id]; ok {
		mut(&s.Items[i])
		return
	}
	it := Item{ID: id}
	mut(&it)
	s.Items = append(s.Items, it)
	s.itemIndex[id] = len(s.Items) - 1
}

// Apply folds one event. Applying the same event twice is a no-op for every
// type except message.chunk, which is why callers must discard seq <= Seq.
func (s *State) Apply(ev proto.Event) {
	if ev.Seq <= s.Seq {
		return
	}
	s.Seq = ev.Seq

	switch ev.Type {
	case proto.ThreadCreated:
		var p proto.ThreadCreatedPayload
		decode(ev.Payload, &p)
		s.Cwd, s.Harness, s.Model, s.Mode, s.Effort, s.Title = p.Cwd, p.Harness, p.Model, p.Mode, p.Effort, p.Title
		s.ServiceTier = p.ServiceTier

	case proto.ThreadConfigChanged:
		var p proto.ThreadConfigChangedPayload
		decode(ev.Payload, &p)
		// An event carrying a harness session id is the harness reporting its
		// own settings as it (re)starts or takes a prompt, not anyone
		// switching: it names the bare model id for the alias that was
		// picked. One that lands while a model switch is waiting for its turn
		// can predate that switch (Claude reports a turn's settings seconds
		// after the turn starts), so it leaves the pick alone; otherwise it
		// moves the baseline's model with it.
		report := p.HarnessSessionID != ""
		pending := s.RanWith != nil && s.RanWith.Model != s.Model
		if (p.ReplaceSettings || p.Model != "") && !(report && pending) {
			s.Model = p.Model
		}
		if p.ReplaceSettings || p.Mode != "" {
			s.Mode = p.Mode
		}
		// Unlike the fields around it, an empty effort is a choice — "let the
		// harness decide" — so the event says whether it touched effort at
		// all rather than inferring it from emptiness.
		if p.Effort != nil {
			s.Effort = *p.Effort
		}
		// The same goes for the service tier: "" turns Fast off.
		if p.ServiceTier != nil {
			s.ServiceTier = *p.ServiceTier
		}
		if p.Title != "" {
			s.Title = p.Title
		}
		if p.HarnessSessionID != "" {
			s.HarnessSessionID = p.HarnessSessionID
		}
		if report && s.RanWith != nil && !pending {
			ran := *s.RanWith
			ran.Model = s.Model
			s.RanWith = &ran
		}

	case proto.ThreadClosed:
		s.Closed = true
		s.Phase = "closed"
		// No process will run for this thread again, so nothing is keeping
		// its extensions' statuses. A thread can close with no actor live to
		// clear them first, so the close itself is what clears them.
		s.ExtensionStatuses = []ExtensionStatus{}

	case proto.WorkspaceRequested:
		var p proto.WorkspaceRequestedPayload
		decode(ev.Payload, &p)
		s.Phase = "provisioning"
		s.Workspace = WorkspaceState{Phase: "provisioning", ProjectID: p.ProjectID, ProjectRoot: p.ProjectRoot, Mode: p.Mode, Branch: p.Branch, BaseRef: p.BaseRef, StartedAt: ev.Timestamp}

	case proto.WorkspaceHookStarted:
		var p proto.WorkspaceHookStartedPayload
		decode(ev.Payload, &p)
		s.Workspace.Hook, s.Workspace.Command = p.Hook, p.Command
		if p.Hook == "deprovision" {
			s.Phase, s.Workspace.Phase = "cleaning", "cleaning"
		}

	case proto.WorkspaceHookOutput:
		var p proto.WorkspaceHookOutputPayload
		decode(ev.Payload, &p)
		if p.Stream == "stderr" {
			s.Workspace.Output += "[stderr] "
		}
		s.Workspace.Output += p.Chunk

	case proto.WorkspaceHookFinished:
		var p proto.WorkspaceHookFinishedPayload
		decode(ev.Payload, &p)
		s.Workspace.ExitCode, s.Workspace.DurationMs = p.ExitCode, p.DurationMs

	case proto.WorkspaceReady:
		var p proto.WorkspaceReadyPayload
		decode(ev.Payload, &p)
		s.Cwd, s.Workspace.Branch, s.Workspace.Resources = p.Cwd, p.Branch, p.Resources
		s.Phase, s.Workspace.Phase, s.Workspace.Error = "idle", "ready", ""

	case proto.WorkspaceFailed:
		var p proto.WorkspaceFailedPayload
		decode(ev.Payload, &p)
		s.Phase, s.Workspace.Phase, s.Workspace.Error, s.Workspace.ExitCode = "provision_failed", "provision_failed", p.Error, p.ExitCode

	case proto.WorkspaceCleanupStarted:
		var p struct {
			Purge bool `json:"purge"`
		}
		decode(ev.Payload, &p)
		s.Phase, s.Workspace.Phase, s.Workspace.DeleteAfterCleanup = "cleaning", "cleaning", p.Purge

	case proto.WorkspaceCleanupFailed:
		var p proto.WorkspaceFailedPayload
		decode(ev.Payload, &p)
		s.Phase, s.Workspace.Phase, s.Workspace.Error, s.Workspace.ExitCode = "cleanup_failed", "cleanup_failed", p.Error, p.ExitCode

	case proto.WorkspaceCleanupFinished, proto.WorkspaceReleased:
		s.Workspace.Phase = "released"

	case proto.TurnStarted:
		var p proto.TurnStartedPayload
		decode(ev.Payload, &p)
		s.Phase = "turn"
		if p.QueueID != "" {
			// A turn the harness started from a prompt it was holding names
			// the queue entry but not what it carried: the echo it is built
			// from has the text, never the images. The entry does.
			if q := s.queued(p.QueueID); q != nil {
				if p.Prompt == "" {
					p.Prompt = q.Prompt
				}
				if len(p.Images) == 0 {
					p.Images = q.Images
				}
			}
			s.removeQueued(p.QueueID)
			for i := range s.Scheduled {
				if s.Scheduled[i].ID == p.QueueID {
					s.Scheduled[i].Status = "sent"
					s.Scheduled[i].TurnID = p.TurnID
					s.Scheduled[i].Revision++
				}
			}
		}
		s.Turns = append(s.Turns, Turn{ID: p.TurnID, Prompt: p.Prompt, Images: p.Images, Recovery: p.Recovery, StartedAt: ev.Timestamp})
		if s.Title == "" {
			s.Title = truncate(p.Prompt, 60)
			if s.Title == "" && len(p.Images) > 0 {
				s.Title = proto.AttachmentTitle(p.Images)
			}
		}
		s.markSettings(ev)
		// The prompt itself is a timeline item so the UI shows what was asked.
		// A harness-initiated turn has no prompt — nobody asked anything — so
		// there is no item to add.
		if p.Prompt != "" || len(p.Images) > 0 {
			s.upsert("prompt:"+p.TurnID, func(it *Item) {
				it.Kind = ItemMessage
				if it.ReceivedAt == 0 {
					it.ReceivedAt = ev.Timestamp
				}
				it.Role = "user"
				it.ContentKind = "text"
				it.Text = p.Prompt
				it.Images = p.Images
				it.TurnID = p.TurnID
			})
		}

	case proto.PromptScheduled:
		var p proto.ScheduledPrompt
		decode(ev.Payload, &p)
		found := false
		for i := range s.Scheduled {
			if s.Scheduled[i].ID == p.ID {
				s.Scheduled[i] = p
				found = true
				break
			}
		}
		if !found {
			s.Scheduled = append(s.Scheduled, p)
		}

	case proto.PromptQueued:
		var p proto.PromptQueuedPayload
		decode(ev.Payload, &p)
		s.Queued = append(s.Queued, QueuedPrompt{QueueID: p.QueueID, Prompt: p.Prompt, Images: p.Images, QueuedAt: ev.Timestamp, Held: p.Held})

	case proto.PromptSent:
		// The harness took a queued prompt mid-turn. One it already read, or
		// started a turn with, has left the queue, and there is nothing to mark.
		// One the harness has is no longer the server's to hold.
		var p proto.PromptSentPayload
		decode(ev.Payload, &p)
		if q := s.queued(p.QueueID); q != nil {
			q.Sent, q.Held = true, false
		}

	case proto.PromptEdited:
		var p proto.PromptEditedPayload
		decode(ev.Payload, &p)
		if q := s.queued(p.QueueID); q != nil {
			q.Prompt, q.Images = p.Prompt, p.Images
		}

	case proto.PromptHeld:
		// A held prompt is the server's to send, never the harness's. A
		// restart took with it the process that might have had a sent one,
		// and a refused start holds only what the harness was never handed.
		var p proto.PromptHeldPayload
		decode(ev.Payload, &p)
		for _, id := range p.QueueIDs {
			if q := s.queued(id); q != nil {
				q.Held, q.Sent = true, false
			}
		}

	case proto.PromptReleased:
		var p proto.PromptReleasedPayload
		decode(ev.Payload, &p)
		for _, id := range p.QueueIDs {
			if q := s.queued(id); q != nil {
				q.Held = false
			}
		}

	case proto.PromptInjected:
		// The harness read a prompt it was holding into the running turn.
		// It leaves the queue and joins the transcript where it was read —
		// after the work that was already done — as the user message it is.
		var p proto.PromptInjectedPayload
		decode(ev.Payload, &p)
		q := s.queued(p.QueueID)
		if q == nil {
			return
		}
		prompt, images := q.Prompt, q.Images
		s.removeQueued(p.QueueID)
		s.upsert("prompt:"+p.QueueID, func(it *Item) {
			it.Kind = ItemMessage
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.Role = "user"
			it.ContentKind = "text"
			it.Text = prompt
			it.Images = images
			it.TurnID = p.TurnID
		})

	case proto.PromptDequeued:
		var p proto.PromptDequeuedPayload
		decode(ev.Payload, &p)
		s.removeQueued(p.QueueID)

	case proto.TurnDiff:
		var p proto.TurnDiffPayload
		decode(ev.Payload, &p)
		// The diff is hung off the turn it measured. A turn id that is not in
		// the log is nothing to fold: the event describes a turn this
		// projection has never seen.
		for i := range s.Turns {
			if s.Turns[i].ID == p.TurnID {
				diff := p
				s.Turns[i].Diff = &diff
			}
		}

	case proto.TurnFinished:
		var p proto.TurnFinishedPayload
		decode(ev.Payload, &p)
		if p.StopReason == proto.StopError {
			for i := range s.Scheduled {
				if s.Scheduled[i].TurnID == p.TurnID {
					s.Scheduled[i].Status = "failed"
					s.Scheduled[i].Error = p.Error
					s.Scheduled[i].Revision++
				}
			}
		}
		// Only the finish of the turn that is actually open may take the
		// thread idle. A stale finish — an adapter closing a turn the log
		// never opened, or a duplicate for a turn already superseded — must
		// not report "user's turn" while different work is running. This is
		// the projection's twin of the actor's turnActive guard; without it
		// the two disagree and the UI goes idle while the busy guard holds.
		// Mirrored in web/src/apply.ts.
		open := ""
		for i := range s.Turns {
			if !s.Turns[i].Done {
				open = s.Turns[i].ID
			}
		}
		match := open == "" || p.TurnID == open
		s.settleRequests(p.TurnID)
		for i := range s.Turns {
			if s.Turns[i].ID == p.TurnID {
				s.Turns[i].StopReason = p.StopReason
				s.Turns[i].Error = p.Error
				s.Turns[i].Failure = p.Failure
				s.Turns[i].Done = true
				s.Turns[i].FinishedAt = ev.Timestamp
			}
		}
		if match {
			s.Phase = "idle"
			// Any tool of this turn left mid-flight was interrupted, not
			// broken: it is cancelled, never failed. A call that is standing
			// for a live job keeps its status — it outlived its turn on
			// purpose, and the job's own finish will settle it — but not its
			// tail: nothing streams a call's own output past the end of its
			// turn (the Claude adapter stops its tailers before it emits the
			// finish; a Codex subagent's output goes to its own work), so the
			// tail would sit there frozen. A live job's own work (a subagent's
			// running command) is left alone entirely: it is still running.
			// Tools of other turns are left alone too: a stray finish must
			// not touch unrelated running work.
			live := s.liveJobCalls()
			for i := range s.Items {
				it := &s.Items[i]
				if it.Kind != ItemTool || (it.TurnID != p.TurnID && it.TurnID != "") ||
					(it.ParentID != "" && live[it.ParentID]) {
					continue
				}
				if live[it.ID] {
					it.Tail = ""
				} else if proto.ToolRunning(it.Status) {
					it.settle(proto.StatusCancelled)
				}
			}
		}

	case proto.MessageChunk:
		var p proto.MessageChunkPayload
		decode(ev.Payload, &p)
		// Streaming while idle means a turn is running that the log did not
		// announce. Trusting the activity over the phase keeps a lifecycle
		// desync from freezing every attached UI. A subagent's own output
		// (parented to its job's call) is not: it streams on after the
		// parent's turn ended, and the job surfaces carry it. Mirrored in
		// web/src/apply.ts.
		if s.Phase == "idle" && p.ParentToolCallID == "" {
			s.Phase = "turn"
		}
		s.upsert(p.BlockID, func(it *Item) {
			it.Kind = ItemMessage
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.Role = p.Role
			it.ContentKind = p.Kind
			it.TurnID = p.TurnID
			if p.ParentToolCallID != "" {
				it.ParentID = p.ParentToolCallID
			}
			it.Text += p.Delta
		})

	case proto.ToolCallStarted:
		var p proto.ToolCallStartedPayload
		decode(ev.Payload, &p)
		// Same defence as message.chunk: a tool starting is a turn running,
		// unless it is a subagent's.
		if s.Phase == "idle" && p.ParentToolCallID == "" {
			s.Phase = "turn"
		}
		s.upsert(p.ToolCallID, func(it *Item) {
			it.Kind = ItemTool
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.TurnID = p.TurnID
			it.ToolKind = p.Kind
			it.Title = p.Title
			it.Status = p.Status
			if proto.ToolDone(it.Status) {
				it.Tail = ""
			}
			it.Input = p.RawInput
			if p.ParentToolCallID != "" {
				it.ParentID = p.ParentToolCallID
			}
		})

	case proto.ToolCallUpdated:
		var p proto.ToolCallUpdatedPayload
		decode(ev.Payload, &p)
		// Same defence as message.chunk, but only for a tool going active. A
		// completion is not: a background tool's result straggling in after
		// the turn ended must not reopen "working" with nothing left to close
		// it. A subagent's tool is not the parent's turn either.
		if s.Phase == "idle" && p.Status == proto.StatusInProgress && p.ParentToolCallID == "" {
			s.Phase = "turn"
		}
		s.upsert(p.ToolCallID, func(it *Item) {
			it.Kind = ItemTool
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			if p.Status != "" {
				it.Status = p.Status
			}
			if p.Title != "" {
				it.Title = p.Title
			}
			if p.ParentToolCallID != "" {
				it.ParentID = p.ParentToolCallID
			}
			if len(p.RawInput) > 0 {
				it.Input = p.RawInput
			}
			if len(p.Content) > 0 {
				appendToolContent(it, p.Content)
			}
			// Output lands in the tail only while the call is running, judged
			// after this update's own status: a call that finished has its
			// output in Content, so its tail is dropped, and a delta straggling
			// in after the finish is dropped with it. Judging by the status
			// before the update would be equally true to the events, but a
			// coalesced in_progress-then-delta could then fold differently from
			// the two apart on a call being reopened (see merge in
			// internal/thread/coalesce.go).
			if proto.ToolDone(it.Status) {
				it.Tail = ""
			} else if p.OutputDelta != "" {
				it.Tail = appendTail(it.Tail, p.OutputDelta)
			}
		})

	case proto.JobStarted, proto.JobUpdated:
		s.applyJob(ev, false)

	case proto.JobFinished:
		s.applyJob(ev, true)

	case proto.PlanUpdated:
		var p proto.PlanUpdatedPayload
		decode(ev.Payload, &p)
		// A plan anchored to nothing — the old shape, logged before plans
		// had a turn — has nowhere in the transcript to go.
		if p.TurnID == "" && p.ToolCallID == "" {
			return
		}
		// A TodoWrite call is a list of its own, keyed by the call; a codex
		// plan is the turn's, revised in place. Each update carries every
		// step, so steps are replaced outright. Codex gives its explanation
		// only on the turn's first update, so a later one that omits it keeps
		// it. Mirrored in web/src/apply.ts.
		id := TurnPlanID(p.TurnID)
		if p.ToolCallID != "" {
			id = TodoPlanID(p.ToolCallID)
			if _, ok := s.ItemByID(id); !ok {
				for i := range s.Items {
					if isTodoPlan(s.Items[i]) {
						s.Items[i].Superseded = true
					}
				}
			}
		}
		s.upsert(id, func(it *Item) {
			it.Kind = ItemPlan
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.TurnID = p.TurnID
			it.Steps = p.Steps
			if p.Explanation != "" {
				it.Explanation = p.Explanation
			}
		})

	case proto.UsageUpdated:
		var p proto.UsageUpdatedPayload
		decode(ev.Payload, &p)
		s.Usage = p

	case proto.ContextCompacted:
		var p proto.ContextCompactedPayload
		decode(ev.Payload, &p)
		// Anchored to the event's sequence so a replay lands the same item and
		// two boundaries never collide. It carries no turn id: compaction is
		// the harness's own housekeeping, so it stands on its own line rather
		// than folding into a turn.
		s.upsert("compact:"+strconv.FormatInt(ev.Seq, 10), func(it *Item) {
			it.Kind = ItemNotice
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.NoticeKind = "compaction"
			it.Trigger = p.Trigger
			it.PreTokens = p.PreTokens
			it.PostTokens = p.PostTokens
		})

	case proto.ArtefactShown:
		var p proto.ArtefactShownPayload
		decode(ev.Payload, &p)
		s.applyArtefact(p, ev)

	case proto.ExtensionNotified:
		var p proto.ExtensionNotifiedPayload
		decode(ev.Payload, &p)
		// Info is a toast and nothing more; a warning or an error is worth a
		// line the user can find later. Turnless like a compaction: the
		// extension speaks for itself, not as part of the agent's turn.
		if p.Level != proto.NotifyWarning && p.Level != proto.NotifyError {
			break
		}
		s.upsert("notify:"+strconv.FormatInt(ev.Seq, 10), func(it *Item) {
			it.Kind = ItemNotice
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.NoticeKind = "notify"
			it.Level = p.Level
			it.Text = p.Message
		})

	case proto.ExtensionStatus:
		var p proto.ExtensionStatusPayload
		decode(ev.Payload, &p)
		s.applyExtensionStatus(p)

	case proto.ThreadAccountChanged:
		var p proto.ThreadAccountChangedPayload
		decode(ev.Payload, &p)
		// A line in the timeline where the account changed, anchored to the
		// sequence like a compaction so replays land the same item.
		s.upsert("account:"+strconv.FormatInt(ev.Seq, 10), func(it *Item) {
			it.Kind = ItemNotice
			if it.ReceivedAt == 0 {
				it.ReceivedAt = ev.Timestamp
			}
			it.NoticeKind = "account"
			it.Title = p.ToName
			if it.Title == "" {
				it.Title = p.To
			}
		})

	case proto.PermissionRequested:
		var p proto.PermissionRequestedPayload
		decode(ev.Payload, &p)
		s.Pending = append(s.Pending, PendingPermission{
			RequestID:  p.RequestID,
			TurnID:     p.TurnID,
			ToolCallID: p.ToolCallID,
			ToolName:   p.ToolName,
			Title:      p.Title,
			Input:      p.RawInput,
			Options:    p.Options,
		})

	case proto.PermissionResolved:
		var p proto.PermissionResolvedPayload
		decode(ev.Payload, &p)
		out := s.Pending[:0]
		for _, pend := range s.Pending {
			if pend.RequestID != p.RequestID {
				out = append(out, pend)
			}
		}
		s.Pending = out

	case proto.ElicitationRequested:
		var p proto.ElicitationRequestedPayload
		decode(ev.Payload, &p)
		s.Elicitations = append(s.Elicitations, PendingElicitation{
			RequestID: p.RequestID, TurnID: p.TurnID, Prompt: p.Prompt, Schema: p.Schema, Card: p.Card,
		})
		if len(p.Card) > 0 {
			s.upsert(CardID(p.RequestID), func(it *Item) {
				it.Kind = ItemCard
				if it.ReceivedAt == 0 {
					it.ReceivedAt = ev.Timestamp
				}
				it.TurnID = p.TurnID
				it.Card = p.Card
				it.Status = CardPending
			})
		}

	case proto.ElicitationResolved:
		var p proto.ElicitationResolvedPayload
		decode(ev.Payload, &p)
		out := s.Elicitations[:0]
		for _, pending := range s.Elicitations {
			if pending.RequestID != p.RequestID {
				out = append(out, pending)
			}
		}
		s.Elicitations = out
		if s.itemIndex == nil {
			s.reindex()
		}
		if i, ok := s.itemIndex[CardID(p.RequestID)]; ok && s.Items[i].Kind == ItemCard {
			s.Items[i].Status = cardResult(p)
			if len(p.Value) > 0 {
				s.Items[i].Outcome = p.Value
			}
		}
	}
}

// settleRequests drops the permissions and questions a finished turn was
// blocked on. The harness has stopped waiting on them once its turn is over,
// whether or not it said so, and a card left up would answer nothing. A
// request that names no turn is left alone: nothing says this finish ended
// it. Cards are omniplex's to hold and outlive the turn. Mirrored in
// web/src/apply.ts.
func (s *State) settleRequests(turnID string) {
	if turnID == "" {
		return
	}
	perms := s.Pending[:0]
	for _, p := range s.Pending {
		if p.TurnID != turnID {
			perms = append(perms, p)
		}
	}
	s.Pending = perms
	elicits := s.Elicitations[:0]
	for _, p := range s.Elicitations {
		if p.IsCard() || p.TurnID != turnID {
			elicits = append(elicits, p)
		}
	}
	s.Elicitations = elicits
}

// Card statuses: pending until answered, then the outcome's result.
const (
	CardPending   = "pending"
	CardSaved     = "saved"
	CardDeclined  = "declined"
	CardCancelled = "cancelled"
)

// cardResult is the status an answered card lands on: the outcome's own
// result, else what the action implies.
func cardResult(p proto.ElicitationResolvedPayload) string {
	var o struct {
		Result string `json:"result"`
	}
	if len(p.Value) > 0 && json.Unmarshal(p.Value, &o) == nil && o.Result != "" {
		return o.Result
	}
	switch p.Action {
	case "accept":
		return CardSaved
	case "decline":
		return CardDeclined
	}
	return CardCancelled
}

func (s *State) queued(queueID string) *QueuedPrompt {
	for i := range s.Queued {
		if s.Queued[i].QueueID == queueID {
			return &s.Queued[i]
		}
	}
	return nil
}

func (s *State) removeQueued(queueID string) {
	kept := s.Queued[:0]
	for _, q := range s.Queued {
		if q.QueueID != queueID {
			kept = append(kept, q)
		}
	}
	s.Queued = kept
}

func decode(raw json.RawMessage, v any) {
	if len(raw) == 0 {
		return
	}
	_ = json.Unmarshal(raw, v)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ArtefactByID finds an artefact.
func (s *State) ArtefactByID(id string) (Artefact, bool) {
	for _, a := range s.Artefacts {
		if a.ID == id {
			return a, true
		}
	}
	return Artefact{}, false
}

// ArtefactByPath finds the artefact for a path, so showing a file again
// revises it rather than adding a second one.
func (s *State) ArtefactByPath(p string) (Artefact, bool) {
	for _, a := range s.Artefacts {
		if a.Path == p {
			return a, true
		}
	}
	return Artefact{}, false
}

func (s *State) applyArtefact(p proto.ArtefactShownPayload, ev proto.Event) {
	a := Artefact{
		ID: p.ArtefactID, Name: p.Name, Path: p.Path, Dir: p.Dir, MediaType: p.MediaType, Size: p.Size,
		Entry: p.Entry, Files: p.Files, ModifiedAt: p.ModifiedAt, Source: p.Source, Note: p.Note,
		TurnID: p.TurnID, ShownAt: ev.Timestamp,
	}
	found := false
	for i := range s.Artefacts {
		if s.Artefacts[i].ID == p.ArtefactID {
			s.Artefacts[i] = a
			found = true
			break
		}
	}
	if !found {
		s.Artefacts = append(s.Artefacts, a)
	}
	if p.Source == proto.ArtefactFromUpload {
		return
	}
	s.upsert("artefact:"+p.ArtefactID+"@"+strconv.FormatInt(ev.Seq, 10), func(it *Item) {
		it.Kind = ItemArtefact
		it.TurnID = p.TurnID
		if it.ReceivedAt == 0 {
			it.ReceivedAt = ev.Timestamp
		}
		it.Title = p.Name
		it.ArtefactID = p.ArtefactID
		it.MediaType = p.MediaType
		it.Size = p.Size
	})
}

// applyExtensionStatus sets, updates in place, or (on empty text) removes one
// key, so a chip that changes text does not jump along the header.
func (s *State) applyExtensionStatus(p proto.ExtensionStatusPayload) {
	for i := range s.ExtensionStatuses {
		if s.ExtensionStatuses[i].Key != p.Key {
			continue
		}
		if p.Text == "" {
			s.ExtensionStatuses = append(s.ExtensionStatuses[:i], s.ExtensionStatuses[i+1:]...)
		} else {
			s.ExtensionStatuses[i].Text = p.Text
		}
		return
	}
	if p.Text != "" {
		s.ExtensionStatuses = append(s.ExtensionStatuses, ExtensionStatus{Key: p.Key, Text: p.Text})
	}
}

// Clone returns a deep copy safe to hand outside the actor goroutine. A
// shallow copy would share the item and content slices with the live state,
// which the actor keeps mutating.
func (s *State) Clone() *State {
	out := s.cloneRest()
	out.Items = make([]Item, len(s.Items))
	for i, it := range s.Items {
		out.Items[i] = copyItem(it)
	}
	out.Jobs = make([]Job, len(s.Jobs))
	copy(out.Jobs, s.Jobs)
	return out
}

// copyItem deep-copies one item, so the copy shares no bytes with the live
// state.
func copyItem(it Item) Item {
	if len(it.Content) > 0 {
		it.Content = append([]proto.ToolContent(nil), it.Content...)
	}
	if len(it.Input) > 0 {
		it.Input = append(json.RawMessage(nil), it.Input...)
	}
	if len(it.Card) > 0 {
		it.Card = append(json.RawMessage(nil), it.Card...)
	}
	if len(it.Outcome) > 0 {
		it.Outcome = append(json.RawMessage(nil), it.Outcome...)
	}
	if len(it.Steps) > 0 {
		it.Steps = append([]proto.PlanStep(nil), it.Steps...)
	}
	return it
}

// cloneRest deep-copies everything but Items and Jobs, which Clone copies
// whole and WireCopy windows.
func (s *State) cloneRest() *State {
	out := *s
	out.itemIndex = nil
	out.jobIndex = nil
	out.Items = nil
	out.Jobs = nil

	out.Turns = make([]Turn, len(s.Turns))
	copy(out.Turns, s.Turns)
	out.Artefacts = append([]Artefact{}, s.Artefacts...)
	out.ExtensionStatuses = append([]ExtensionStatus{}, s.ExtensionStatuses...)

	out.Pending = make([]PendingPermission, len(s.Pending))
	for i, p := range s.Pending {
		cp := p
		cp.Options = append([]proto.PermissionOption(nil), p.Options...)
		if len(p.Input) > 0 {
			cp.Input = append(json.RawMessage(nil), p.Input...)
		}
		out.Pending[i] = cp
	}
	out.Elicitations = make([]PendingElicitation, len(s.Elicitations))
	for i, pending := range s.Elicitations {
		out.Elicitations[i] = pending
		out.Elicitations[i].Schema = append(json.RawMessage(nil), pending.Schema...)
		if len(pending.Card) > 0 {
			out.Elicitations[i].Card = append(json.RawMessage(nil), pending.Card...)
		}
	}

	out.Scheduled = append([]proto.ScheduledPrompt(nil), s.Scheduled...)
	out.Queued = make([]QueuedPrompt, len(s.Queued))
	copy(out.Queued, s.Queued)

	return &out
}
