// Package server exposes the sync protocol over WebSocket plus a small HTTP
// API. Presenters never see JSON-RPC or a harness; they see this.
package server

import (
	"encoding/json"

	"github.com/asiraky/omniplex/internal/endpoints"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// ProtocolVersion is bumped when the wire format changes incompatibly.
const ProtocolVersion = 1

// Client → server frames.
type clientFrame struct {
	Type string `json:"type"`

	// hello
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
	ClientID        string `json:"clientId,omitempty"`

	// attach / detach
	SessionID string `json:"sessionId,omitempty"`
	AfterSeq  *int64 `json:"afterSeq,omitempty"`

	// command
	CommandID string          `json:"commandId,omitempty"`
	Command   string          `json:"command,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
}

// Server → client frames.
type serverFrame struct {
	Type string `json:"type"`

	ServerID string `json:"serverId,omitempty"`
	// Build identifies the UI bundle this server holds. A client running a
	// different one is stale and reloads itself.
	Build     string              `json:"build,omitempty"`
	Sessions  []store.SessionMeta `json:"sessions,omitempty"`
	Harnesses []session.Harness   `json:"harnesses,omitempty"`
	Projects  []project.Project   `json:"projects,omitempty"`
	// Labels is the user's label definitions, sent on welcome and re-sent
	// whole on every change; a client treats an absent field on a labels
	// frame as "none defined".
	Labels []store.Label `json:"labels,omitempty"`
	Cwd    string        `json:"cwd,omitempty"`
	// Access travels on welcome, after the gate, so an unpaired caller
	// learns nothing about how else this machine can be reached.
	Access *endpoints.Set `json:"access,omitempty"`

	SessionID string            `json:"sessionId,omitempty"`
	Seq       int64             `json:"seq,omitempty"`
	State     *projection.State `json:"state,omitempty"`
	Event     *proto.Event      `json:"event,omitempty"`

	CommandID string          `json:"commandId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Command argument shapes.
type createArgs struct {
	Harness string `json:"harness"`
	// Instance names the provider instance to run under; empty means the
	// harness's default instance, which is today's behaviour.
	Instance  string `json:"instance"`
	Cwd       string `json:"cwd"`
	ProjectID string `json:"projectId"`
	Branch    string `json:"branch"`
	Workspace string `json:"workspace"`
	// WorkspacePath attaches to a checkout that already exists rather than
	// provisioning one; empty means the usual create-a-worktree path.
	WorkspacePath string `json:"workspacePath"`
	// BaseRef is the ref a new worktree branches from; empty defers to the
	// project's default base branch.
	BaseRef string `json:"baseRef"`
	Model   string `json:"model"`
	Mode    string `json:"mode"`
	Effort  string `json:"effort"`
	// AgentSettingsExplicit distinguishes a current UI sending "use the
	// harness default" as an empty value from an older client omitting agent
	// fields and asking the server to inherit the project profile.
	AgentSettingsExplicit bool `json:"agentSettingsExplicit"`
}

// deleteSessionArgs carries the user's answer to the confirmation dialog's
// checkbox. Absent — an older client — means false, which is the safe reading:
// nothing on disk is removed unless somebody asked for it.
type deleteSessionArgs struct {
	SessionID      string `json:"sessionId"`
	RemoveWorktree bool   `json:"removeWorktree"`
}

type listWorkspacesArgs struct {
	ProjectID string `json:"projectId"`
}

type saveUserConfigArgs struct {
	Config userconfig.Config `json:"config"`
}

type addProjectArgs struct {
	Root string `json:"root"`
}
type saveProjectArgs struct {
	ProjectID string         `json:"projectId"`
	Config    project.Config `json:"config"`
}

// deleteProjectArgs carries only the id: deleting a project removes the
// registry entry and nothing else, so there is no "and also remove…" to ask
// about the way a session delete has one.
type deleteProjectArgs struct {
	ProjectID string `json:"projectId"`
}

type promptArgs struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
	// ImageIDs names images already uploaded to this session, in the order
	// they were attached. The bytes are not on this path: a prompt frame is
	// stored for idempotent retry, and inlining a screenshot would put a
	// megabyte in the command log and on every reconnect that replays it.
	ImageIDs []string `json:"imageIds,omitempty"`
}

type sessionArgs struct {
	SessionID string `json:"sessionId"`
}

// summarizeArgs asks for a fresh summary of one session. There is no "use the
// cached one" flag: the command is only sent when a client wants a new answer,
// and the client holds the last one it was given.
type summarizeArgs struct {
	SessionID string `json:"sessionId"`
}

type fileDiffArgs struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
}

type fileTreeArgs struct {
	SessionID string `json:"sessionId"`
	// IncludeIgnored turns the .gitignore filter off for the listing.
	IncludeIgnored bool `json:"includeIgnored"`
}

type readFileArgs struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
}

type jobArgs struct {
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
}

// jobOutputArgs reads a job's output file from Offset; the reply's offset is
// where to read from next, so a client polls a growing file in small chunks.
type jobOutputArgs struct {
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Offset    int64  `json:"offset"`
}

type setModeArgs struct {
	SessionID string `json:"sessionId"`
	Mode      string `json:"mode"`
}

type setModelArgs struct {
	SessionID string `json:"sessionId"`
	Model     string `json:"model"`
}

type setEffortArgs struct {
	SessionID string `json:"sessionId"`
	Effort    string `json:"effort"`
}

type createLabelArgs struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// saveLabelArgs is the whole definition, restated: rename, recolour and
// reorder all travel through the one shape.
type saveLabelArgs struct {
	LabelID  string `json:"labelId"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Position int    `json:"position"`
}

type deleteLabelArgs struct {
	LabelID string `json:"labelId"`
}

// setSessionLabelArgs files a session under a label; an empty labelId clears
// it. One label per session — a status, not a tag set — so this is the whole
// assignment surface.
type setSessionLabelArgs struct {
	SessionID string `json:"sessionId"`
	LabelID   string `json:"labelId"`
}

type runComposerActionArgs struct {
	SessionID  string `json:"sessionId"`
	Action     string `json:"action"`
	Args       string `json:"args"`
	Invocation string `json:"invocation"`
}

type resolveArgs struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Outcome   string `json:"outcome"`
	OptionID  string `json:"optionId"`
}

type resolveElicitationArgs struct {
	SessionID string          `json:"sessionId"`
	RequestID string          `json:"requestId"`
	Action    string          `json:"action"`
	Value     json.RawMessage `json:"value"`
}
