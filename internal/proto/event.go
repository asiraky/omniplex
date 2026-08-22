// Package proto defines the canonical event vocabulary that every harness
// adapter normalises into. It is the boundary of the system: adapters emit
// these, the log stores them, projections fold them, and UIs render them.
package proto

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event types. Lifecycle, content, and human interaction.
const (
	SessionCreated       = "session.created"
	SessionConfigChanged = "session.config_changed"
	SessionClosed        = "session.closed"

	TurnStarted  = "turn.started"
	TurnFinished = "turn.finished"
	// TurnDiff reports what one turn changed on disk. It arrives after
	// turn.finished, because it is measured by snapshotting the checkout once
	// the harness has stopped writing to it.
	TurnDiff = "turn.diff"

	MessageChunk    = "message.chunk"
	ToolCallStarted = "tool_call.started"
	ToolCallUpdated = "tool_call.updated"
	PlanUpdated     = "plan.updated"
	UsageUpdated    = "usage.updated"
	// ContextCompacted marks a point where the harness compressed the
	// conversation to reclaim context window. It carries the token counts
	// either side of the boundary so the transcript can show what happened.
	ContextCompacted = "context.compacted"

	PermissionRequested  = "permission.requested"
	PermissionResolved   = "permission.resolved"
	ElicitationRequested = "elicitation.requested"
	ElicitationResolved  = "elicitation.resolved"

	WorkspaceRequested       = "workspace.requested"
	WorkspaceHookStarted     = "workspace.hook_started"
	WorkspaceHookOutput      = "workspace.hook_output"
	WorkspaceHookFinished    = "workspace.hook_finished"
	WorkspaceReady           = "workspace.ready"
	WorkspaceFailed          = "workspace.failed"
	WorkspaceCleanupStarted  = "workspace.cleanup_started"
	WorkspaceCleanupFinished = "workspace.cleanup_finished"
	WorkspaceCleanupFailed   = "workspace.cleanup_failed"
	WorkspaceReleased        = "workspace.released"
)

// Stop reasons for turn.finished.
const (
	StopEndTurn   = "end_turn"
	StopMaxTokens = "max_tokens"
	StopRefusal   = "refusal"
	StopCancelled = "cancelled"
	StopError     = "error"
)

// Tool call kinds and statuses.
const (
	KindRead    = "read"
	KindEdit    = "edit"
	KindDelete  = "delete"
	KindMove    = "move"
	KindSearch  = "search"
	KindExecute = "execute"
	KindThink   = "think"
	KindFetch   = "fetch"
	KindOther   = "other"

	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Permission outcomes.
const (
	OutcomeAllowOnce    = "allow_once"
	OutcomeAllowAlways  = "allow_always"
	OutcomeRejectOnce   = "reject_once"
	OutcomeRejectAlways = "reject_always"
	OutcomeCancelled    = "cancelled"
)

// Event is one durable fact about a session. seq is a per-session monotonic
// integer assigned at append time; there is no global sequence.
type Event struct {
	SessionID string          `json:"sessionId"`
	Seq       int64           `json:"seq"`
	Timestamp int64           `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// Emission is an event before it has been sequenced. Adapters emit these; the
// session actor stamps seq and timestamp at append.
type Emission struct {
	Type    string
	Payload any
}

func Emit(typ string, payload any) Emission { return Emission{Type: typ, Payload: payload} }

// NowMillis is the timestamp unit used throughout.
func NowMillis() int64 { return time.Now().UnixMilli() }

// ---- Payloads ----

type SessionCreatedPayload struct {
	Cwd     string `json:"cwd"`
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Title   string `json:"title,omitempty"`
}

type WorkspaceRequestedPayload struct {
	ProjectID   string `json:"projectId,omitempty"`
	ProjectRoot string `json:"projectRoot"`
	Mode        string `json:"mode"`
	Branch      string `json:"branch,omitempty"`
	BaseRef     string `json:"baseRef,omitempty"`
}

type WorkspaceHookStartedPayload struct {
	RunID   string `json:"runId"`
	Hook    string `json:"hook"`
	Command string `json:"command"`
}

type WorkspaceHookOutputPayload struct {
	RunID  string `json:"runId"`
	Hook   string `json:"hook"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
}

type WorkspaceHookFinishedPayload struct {
	RunID      string `json:"runId"`
	Hook       string `json:"hook"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

type WorkspaceReadyPayload struct {
	Cwd       string         `json:"cwd"`
	Branch    string         `json:"branch,omitempty"`
	Resources map[string]any `json:"resources,omitempty"`
}

type WorkspaceFailedPayload struct {
	Hook     string `json:"hook"`
	Error    string `json:"error"`
	ExitCode int    `json:"exitCode,omitempty"`
}

type SessionConfigChangedPayload struct {
	Model string `json:"model,omitempty"`
	Mode  string `json:"mode,omitempty"`
	// Effort is a pointer because "" is a value it can be set to, not just
	// the absence of one: an empty effort hands the choice back to the
	// harness, and a plain string with omitempty cannot say that — the field
	// vanishes and a projection reads it as "this event did not touch
	// effort", leaving the old level in place forever.
	Effort *string `json:"effort,omitempty"`
	Title  string  `json:"title,omitempty"`
	// HarnessSessionID is the harness's own identifier for this conversation,
	// which is what a restart needs in order to resume with context intact.
	HarnessSessionID string `json:"harnessSessionId,omitempty"`
}

type SessionClosedPayload struct {
	Reason string `json:"reason"`
}

// PromptImage is one image a human attached to a prompt.
//
// The bytes stay in the attachment store: this is the reference a presenter
// resolves back to a picture through the attachment endpoint, so replaying a
// long transcript on a phone costs the same as it did before images existed.
type PromptImage struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	// Path is where the bytes are on the host running the harness. It is
	// deliberately not serialised: it is how the adapter reaches the file, and
	// nothing a client is told.
	Path string `json:"-"`
}

// ImageTitle names a prompt that was nothing but pictures, so a session sent
// from a phone with one screenshot and no words still reads as something in
// the sidebar.
func ImageTitle(n int) string {
	if n == 1 {
		return "1 image"
	}
	return fmt.Sprintf("%d images", n)
}

type TurnStartedPayload struct {
	TurnID string `json:"turnId"`
	Prompt string `json:"prompt"`
	// Images the prompt carried, in the order they were attached.
	Images []PromptImage `json:"images,omitempty"`
	// Recovery is set only on a turn the server started by itself, to finish
	// work a restart interrupted. It is absent on every human prompt.
	Recovery *TurnRecovery `json:"recovery,omitempty"`
}

// TurnRecovery describes a turn the server started to continue interrupted
// work. Attempt counts consecutive recoveries so a session that dies on every
// resume stops rather than restarting itself forever.
type TurnRecovery struct {
	ResumeOf string `json:"resumeOf"`
	Attempt  int    `json:"attempt"`
}

// ChangedFile is one path a change set touched, aggregated over the whole set:
// a file edited five times appears once.
type ChangedFile struct {
	Path string `json:"path"`
	// OldPath is set for a rename, and is the name the file had before it.
	OldPath string `json:"oldPath,omitempty"`
	// Status is one of added, modified, deleted, renamed, copied.
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Binary files have no line counts, so the UI must not print +0 / -0.
	Binary bool `json:"binary,omitempty"`
	// Untracked marks a file Git has never seen, which needs --no-index to diff.
	Untracked bool `json:"untracked,omitempty"`
}

// TurnDiffPayload is what one turn changed, measured between the snapshots
// bracketing it rather than read out of the tool calls: a formatter or a codemod
// changes files without ever going through a tool we parse.
type TurnDiffPayload struct {
	TurnID    string        `json:"turnId"`
	Files     []ChangedFile `json:"files"`
	Additions int           `json:"additions"`
	Deletions int           `json:"deletions"`
	Truncated bool          `json:"truncated,omitempty"`
	// Error explains a turn whose changes could not be measured. An empty list
	// and a failed snapshot are otherwise indistinguishable.
	Error string `json:"error,omitempty"`
}

type TurnFinishedPayload struct {
	TurnID     string `json:"turnId"`
	StopReason string `json:"stopReason"`
	Error      string `json:"error,omitempty"`
}

// MessageChunkPayload carries a delta of assistant (or replayed user) content.
// BlockID groups deltas belonging to one content block so a projection can
// append without needing an index space shared across harnesses.
type MessageChunkPayload struct {
	TurnID  string `json:"turnId"`
	Role    string `json:"role"` // user | agent
	Kind    string `json:"kind"` // text | thought
	BlockID string `json:"blockId"`
	Delta   string `json:"delta"`
	// ParentToolCallID names the tool call (a Task/Agent spawn) this chunk was
	// produced inside, when the harness reports one. Empty means the top-level
	// conversation.
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"` // text | diff
	Text string `json:"text,omitempty"`
	Path string `json:"path,omitempty"`
	Old  string `json:"oldText,omitempty"`
	New  string `json:"newText,omitempty"`
}

type ToolCallStartedPayload struct {
	TurnID     string          `json:"turnId"`
	ToolCallID string          `json:"toolCallId"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Status     string          `json:"status"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	// ParentToolCallID links a call made by a subagent to the Task/Agent call
	// that spawned it. Empty means the top-level conversation.
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
}

type ToolCallUpdatedPayload struct {
	ToolCallID       string          `json:"toolCallId"`
	Status           string          `json:"status,omitempty"`
	Title            string          `json:"title,omitempty"`
	Content          []ToolContent   `json:"content,omitempty"`
	RawInput         json.RawMessage `json:"rawInput,omitempty"`
	ParentToolCallID string          `json:"parentToolCallId,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

type PlanUpdatedPayload struct {
	Entries []PlanEntry `json:"entries"`
}

type UsageUpdatedPayload struct {
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	// ContextPct is how full the context window is, and is deliberately NOT
	// clamped to 100: an over-limit reading is a real signal (compaction is
	// imminent or overdue), and clamping it in the adapter is what let a
	// broken occupancy calculation masquerade as a full window.
	ContextPct float64 `json:"contextPct,omitempty"`
	// Raw context readings behind ContextPct, so the UI can say
	// "12k / 200k tokens" and scale with the model's window. ContextWindow is
	// the window occupancy is measured against — the resolved auto-compaction
	// window, which on a 1M model may be the 200k compaction boundary.
	ContextUsed   int64 `json:"contextUsed,omitempty"`
	ContextWindow int64 `json:"contextWindow,omitempty"`
	// ContextLimit is the model's full context window, which ContextWindow may
	// be smaller than when auto-compaction runs against a tighter boundary.
	// When it exceeds ContextWindow, the difference is the room past the
	// compaction threshold, and the UI draws that threshold as a marker.
	ContextLimit int64 `json:"contextLimit,omitempty"`
	// AutoCompact reports whether the harness will compact automatically as the
	// window fills. When false the window is a hard limit rather than a
	// compaction boundary, and the UI must not promise a compaction.
	AutoCompact bool `json:"autoCompact,omitempty"`
	// AutoCompactThreshold is the token count at which auto-compaction triggers,
	// when the harness reports it — the marker the UI draws on the bar.
	AutoCompactThreshold int64 `json:"autoCompactThreshold,omitempty"`
	// ContextCategories is the per-category breakdown of what occupies the
	// window (system prompt, tools, messages, …), for a segmented bar. It is
	// present only when the harness reports it.
	ContextCategories []ContextCategory `json:"contextCategories,omitempty"`
}

// ContextCategory is one row of the context-window occupancy breakdown.
type ContextCategory struct {
	Name   string `json:"name"`
	Tokens int64  `json:"tokens"`
}

// ContextCompactedPayload reports one compaction boundary. Trigger is "auto"
// when the harness compacted on its own to stay under the window, or "manual"
// when a human asked for it.
type ContextCompactedPayload struct {
	Trigger    string `json:"trigger,omitempty"`
	PreTokens  int64  `json:"preTokens,omitempty"`
	PostTokens int64  `json:"postTokens,omitempty"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once | allow_always | reject_once | reject_always
}

type PermissionRequestedPayload struct {
	RequestID  string             `json:"requestId"`
	TurnID     string             `json:"turnId"`
	ToolCallID string             `json:"toolCallId"`
	ToolName   string             `json:"toolName"`
	Title      string             `json:"title"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	Options    []PermissionOption `json:"options"`
}

type PermissionResolvedPayload struct {
	RequestID string `json:"requestId"`
	Outcome   string `json:"outcome"`
	OptionID  string `json:"optionId,omitempty"`
}

type ElicitationRequestedPayload struct {
	RequestID string          `json:"requestId"`
	TurnID    string          `json:"turnId,omitempty"`
	Prompt    string          `json:"prompt"`
	Schema    json.RawMessage `json:"schema"`
}

type ElicitationResolvedPayload struct {
	RequestID string          `json:"requestId"`
	Action    string          `json:"action"` // accept | decline | cancel
	Value     json.RawMessage `json:"value,omitempty"`
}

// DefaultPermissionOptions is the option set offered when a harness does not
// supply its own.
func DefaultPermissionOptions() []PermissionOption {
	return []PermissionOption{
		{OptionID: "allow_once", Name: "Allow once", Kind: OutcomeAllowOnce},
		{OptionID: "allow_always", Name: "Always allow this tool", Kind: OutcomeAllowAlways},
		{OptionID: "reject_once", Name: "Reject", Kind: OutcomeRejectOnce},
	}
}
