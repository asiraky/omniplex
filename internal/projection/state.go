// Package projection folds the event log into the state a UI renders. It is
// derived data: dropping every projection and rebuilding from the log must
// yield an identical result.
package projection

import (
	"encoding/json"
	"strconv"

	"github.com/asiraky/omniplex/internal/proto"
)

// Attention is the derived answer to "whose turn is it?" — the one signal a
// session list, a notifier, or any future routing system should read. It is a
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
	// AttentionNeedsPrompt: idle — the conversation is with the user.
	AttentionNeedsPrompt = "needs_prompt"
	// AttentionFailed: workspace provisioning or cleanup failed; the session
	// needs a human decision before anything else can happen.
	AttentionFailed = "failed"
	// AttentionClosed: the session is a closed transcript.
	AttentionClosed = "closed"
)

// Attention derives the session's attention state from the projection. Pending
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
	default:
		return AttentionNeedsPrompt
	}
}

// AttentionForPhase derives attention for a session with no live projection —
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
	default:
		return AttentionNeedsPrompt
	}
}

// ItemKind discriminates timeline entries.
const (
	ItemMessage = "message"
	ItemTool    = "tool"
	// ItemNotice is a system event worth a line in the timeline that is
	// neither a message nor a tool call — currently a context compaction.
	ItemNotice = "notice"
)

// Item is one entry in the session timeline, in the order it first appeared.
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

	// notice
	NoticeKind string `json:"noticeKind,omitempty"` // compaction
	Trigger    string `json:"trigger,omitempty"`    // auto | manual
	PreTokens  int64  `json:"preTokens,omitempty"`
	PostTokens int64  `json:"postTokens,omitempty"`
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
}

// PendingPermission is a permission request awaiting a human. It lives in the
// log, not in a connection, so any attached presenter can answer it.
type PendingPermission struct {
	RequestID  string                   `json:"requestId"`
	ToolCallID string                   `json:"toolCallId"`
	ToolName   string                   `json:"toolName"`
	Title      string                   `json:"title"`
	Input      json.RawMessage          `json:"input,omitempty"`
	Options    []proto.PermissionOption `json:"options"`
}

type PendingElicitation struct {
	RequestID string          `json:"requestId"`
	Prompt    string          `json:"prompt"`
	Schema    json.RawMessage `json:"schema"`
}

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

// State is the complete renderable state of a session as of Seq.
type State struct {
	SessionID string `json:"sessionId"`
	Seq       int64  `json:"seq"`
	Cwd       string `json:"cwd"`
	Harness   string `json:"harness"`
	Model     string `json:"model"`
	Mode      string `json:"mode"`
	Effort    string `json:"effort"`
	Title     string `json:"title"`
	// HarnessSessionID is the harness's own conversation id, used to resume.
	HarnessSessionID string         `json:"harnessSessionId,omitempty"`
	Phase            string         `json:"phase"` // idle | turn | closed
	Closed           bool           `json:"closed"`
	Workspace        WorkspaceState `json:"workspace"`

	Items        []Item                    `json:"items"`
	Turns        []Turn                    `json:"turns"`
	Plan         []proto.PlanEntry         `json:"plan"`
	Usage        proto.UsageUpdatedPayload `json:"usage"`
	Pending      []PendingPermission       `json:"pendingPermissions"`
	Elicitations []PendingElicitation      `json:"pendingElicitations"`
	Queued       []QueuedPrompt            `json:"queuedPrompts"`

	itemIndex map[string]int `json:"-"`
}

func New(sessionID string) *State {
	return &State{
		SessionID:    sessionID,
		Phase:        "idle",
		Items:        []Item{},
		Turns:        []Turn{},
		Plan:         []proto.PlanEntry{},
		Pending:      []PendingPermission{},
		Elicitations: []PendingElicitation{},
		Queued:       []QueuedPrompt{},
		itemIndex:    map[string]int{},
	}
}

// reindex rebuilds the id→position map, needed after a snapshot is decoded.
func (s *State) reindex() {
	s.itemIndex = make(map[string]int, len(s.Items))
	for i, it := range s.Items {
		s.itemIndex[it.ID] = i
	}
}

// FromSnapshot decodes a stored snapshot back into usable state.
func FromSnapshot(blob json.RawMessage) (*State, error) {
	var s State
	if err := json.Unmarshal(blob, &s); err != nil {
		return nil, err
	}
	s.reindex()
	return &s, nil
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
	case proto.SessionCreated:
		var p proto.SessionCreatedPayload
		decode(ev.Payload, &p)
		s.Cwd, s.Harness, s.Model, s.Mode, s.Effort, s.Title = p.Cwd, p.Harness, p.Model, p.Mode, p.Effort, p.Title

	case proto.SessionConfigChanged:
		var p proto.SessionConfigChangedPayload
		decode(ev.Payload, &p)
		if p.Model != "" {
			s.Model = p.Model
		}
		if p.Mode != "" {
			s.Mode = p.Mode
		}
		// Unlike the fields around it, an empty effort is a choice — "let the
		// harness decide" — so the event says whether it touched effort at
		// all rather than inferring it from emptiness.
		if p.Effort != nil {
			s.Effort = *p.Effort
		}
		if p.Title != "" {
			s.Title = p.Title
		}
		if p.HarnessSessionID != "" {
			s.HarnessSessionID = p.HarnessSessionID
		}

	case proto.SessionClosed:
		s.Closed = true
		s.Phase = "closed"

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
		s.Turns = append(s.Turns, Turn{ID: p.TurnID, Prompt: p.Prompt, Images: p.Images, Recovery: p.Recovery, StartedAt: ev.Timestamp})
		if p.QueueID != "" {
			s.removeQueued(p.QueueID)
		}
		if s.Title == "" {
			s.Title = truncate(p.Prompt, 60)
			if s.Title == "" && len(p.Images) > 0 {
				s.Title = proto.ImageTitle(len(p.Images))
			}
		}
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

	case proto.PromptQueued:
		var p proto.PromptQueuedPayload
		decode(ev.Payload, &p)
		s.Queued = append(s.Queued, QueuedPrompt{QueueID: p.QueueID, Prompt: p.Prompt, Images: p.Images, QueuedAt: ev.Timestamp})

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
		// Only the finish of the turn that is actually open may take the
		// session idle. A stale finish — an adapter closing a turn the log
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
			// Any tool of this turn left mid-flight is no longer running. Tools
			// of other turns are left alone: a stray finish must not paint
			// unrelated running work as failed.
			for i := range s.Items {
				if s.Items[i].Kind == ItemTool &&
					(s.Items[i].Status == proto.StatusInProgress || s.Items[i].Status == proto.StatusPending) &&
					(s.Items[i].TurnID == p.TurnID || s.Items[i].TurnID == "") {
					s.Items[i].Status = proto.StatusFailed
				}
			}
		}

	case proto.MessageChunk:
		var p proto.MessageChunkPayload
		decode(ev.Payload, &p)
		// Streaming while idle means a turn is running that the log did not
		// announce. Trusting the activity over the phase keeps a lifecycle
		// desync from freezing every attached UI. Mirrored in web/src/apply.ts.
		if s.Phase == "idle" {
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
		// Same defence as message.chunk: a tool starting is a turn running.
		if s.Phase == "idle" {
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
		// it.
		if s.Phase == "idle" && p.Status == proto.StatusInProgress {
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
				it.Content = append(it.Content, p.Content...)
			}
		})

	case proto.PlanUpdated:
		var p proto.PlanUpdatedPayload
		decode(ev.Payload, &p)
		s.Plan = p.Entries

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

	case proto.PermissionRequested:
		var p proto.PermissionRequestedPayload
		decode(ev.Payload, &p)
		s.Pending = append(s.Pending, PendingPermission{
			RequestID:  p.RequestID,
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
			RequestID: p.RequestID, Prompt: p.Prompt, Schema: p.Schema,
		})

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
	}
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

// Clone returns a deep copy safe to hand outside the actor goroutine. A
// shallow copy would share the item and content slices with the live state,
// which the actor keeps mutating.
func (s *State) Clone() *State {
	out := *s
	out.itemIndex = nil

	out.Items = make([]Item, len(s.Items))
	for i, it := range s.Items {
		cp := it
		if len(it.Content) > 0 {
			cp.Content = append([]proto.ToolContent(nil), it.Content...)
		}
		if len(it.Input) > 0 {
			cp.Input = append(json.RawMessage(nil), it.Input...)
		}
		out.Items[i] = cp
	}

	out.Turns = make([]Turn, len(s.Turns))
	copy(out.Turns, s.Turns)
	out.Plan = make([]proto.PlanEntry, len(s.Plan))
	copy(out.Plan, s.Plan)

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
	}

	out.Queued = make([]QueuedPrompt, len(s.Queued))
	copy(out.Queued, s.Queued)

	return &out
}
