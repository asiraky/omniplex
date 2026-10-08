// Package server exposes the sync protocol over WebSocket plus a small HTTP
// API. Presenters never see JSON-RPC or a harness; they see this.
package server

import (
	"encoding/json"

	"github.com/asiraky/omniplex/internal/endpoints"
	"github.com/asiraky/omniplex/internal/mcp"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// ProtocolVersion is bumped when the wire format changes incompatibly.
const ProtocolVersion = 1

// How much of the timeline travels at once. A snapshot carries the newest
// SnapshotItems top-level items — a few screenfuls past the fold — and each
// scroll-up fetch adds PageItems more. Sized for a phone on flaky 4G: big
// enough that paging is rare, small enough that opening a monster thread is
// not a multi-second download.
const (
	SnapshotItems = 100
	PageItems     = 100
)

// Client → server frames.
type clientFrame struct {
	Type string `json:"type"`

	// hello
	ProtocolVersion int    `json:"protocolVersion,omitempty"`
	ClientID        string `json:"clientId,omitempty"`

	// attach / detach
	ThreadID string `json:"threadId,omitempty"`
	AfterSeq *int64 `json:"afterSeq,omitempty"`

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
	Build     string             `json:"build,omitempty"`
	Threads   []store.ThreadMeta `json:"threads,omitempty"`
	Harnesses []thread.Harness   `json:"harnesses,omitempty"`
	Projects  []project.Project  `json:"projects,omitempty"`
	// Labels is the user's label definitions, sent on welcome and re-sent
	// whole on every change; a client treats an absent field on a labels
	// frame as "none defined".
	Labels []store.Label `json:"labels,omitempty"`
	// Quotas is every provider instance's cached usage limits, sent on
	// welcome and re-sent whole whenever a live push or a refresh changes one
	// — the whole list, because one provider moving must never blank another.
	Quotas []thread.QuotaStatus `json:"quotas,omitempty"`
	// Access travels on welcome, after the gate, so an unpaired caller
	// learns nothing about how else this machine can be reached.
	Access *endpoints.Set `json:"access,omitempty"`

	ThreadID string            `json:"threadId,omitempty"`
	Seq      int64             `json:"seq,omitempty"`
	State    *projection.State `json:"state,omitempty"`
	Event    *proto.Event      `json:"event,omitempty"`

	CommandID string          `json:"commandId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`

	// AuthFlow narrates a running authentication flow ("auth_event" frames).
	// These travel only to the connection that began the flow and are never
	// persisted: their traffic sits next to secrets.
	AuthFlow *thread.AuthFlowEvent `json:"authFlow,omitempty"`
}

// Command argument shapes.
type createArgs struct {
	Harness string `json:"harness"`
	// Instance names the provider instance to run under; empty means the
	// harness's default instance, which is today's behaviour.
	Instance  string `json:"instance"`
	ProjectID string `json:"projectId"`
	Branch    string `json:"branch"`
	Workspace string `json:"workspace"`
	// WorkspacePath attaches to a checkout that already exists rather than
	// provisioning one; empty means the usual create-a-worktree path.
	WorkspacePath string `json:"workspacePath"`
	// FolderID scopes the thread to one folder; empty is the project's only
	// folder, or the whole project when it has several.
	FolderID string `json:"folderId"`
	// BaseRef is the ref a new worktree branches from; empty defers to the
	// folder's base branch.
	BaseRef string `json:"baseRef"`
	Model   string `json:"model"`
	Mode    string `json:"mode"`
	Effort  string `json:"effort"`
	// AgentSettingsExplicit distinguishes a current UI sending "use the
	// harness default" as an empty value from an older client omitting agent
	// fields and asking the server to inherit the project profile.
	AgentSettingsExplicit bool `json:"agentSettingsExplicit"`
	// Text is the first message. It waits in the new thread's queue while the
	// workspace is prepared, so starting a thread is one round trip.
	Text string `json:"text"`
}

// deleteThreadArgs carries the user's answer to the confirmation dialog's
// checkbox. Absent — an older client — means false, which is the safe reading:
// nothing on disk is removed unless somebody asked for it.
type deleteThreadArgs struct {
	ThreadID       string `json:"threadId"`
	RemoveWorktree bool   `json:"removeWorktree"`
}

type listWorkspacesArgs struct {
	ProjectID string `json:"projectId"`
	FolderID  string `json:"folderId"`
}

// draftComposerItemsArgs names what a thread would be created with, for the
// catalogue its composer shows before it exists.
type draftComposerItemsArgs struct {
	Harness   string `json:"harness"`
	Instance  string `json:"instance"`
	ProjectID string `json:"projectId"`
	FolderID  string `json:"folderId"`
	// WorkspacePath is the existing copy the thread would attach to, if any.
	WorkspacePath string `json:"workspacePath"`
}

type saveUserConfigArgs struct {
	Config userconfig.Config `json:"config"`
}

type addFolderArgs struct {
	ProjectID string `json:"projectId"`
	thread.AddFolderOptions
}
type removeFolderArgs struct {
	ProjectID string `json:"projectId"`
	FolderID  string `json:"folderId"`
}
type saveProjectArgs struct {
	ProjectID string           `json:"projectId"`
	Name      string           `json:"name"`
	Defaults  project.Defaults `json:"defaults"`
}
type saveFolderArgs struct {
	ProjectID string         `json:"projectId"`
	Folder    project.Folder `json:"folder"`
}

// deleteProjectArgs carries only the id: deleting a project removes the
// registry entry and nothing else, so there is no "and also remove…" to ask
// about the way a thread delete has one.
type deleteProjectArgs struct {
	ProjectID string `json:"projectId"`
}

type promptArgs struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	DueAt    int64  `json:"dueAt"`
	TimeZone string `json:"timeZone"`
	ThreadID string `json:"threadId"`
	Text     string `json:"text"`
	// ImageIDs names images already uploaded to this thread, in the order
	// they were attached. The bytes are not on this path: a prompt frame is
	// stored for idempotent retry, and inlining a screenshot would put a
	// megabyte in the command log and on every reconnect that replays it.
	ImageIDs []string `json:"imageIds,omitempty"`
	// Files names artefacts uploaded to this thread that the message
	// carries. The agent is told where each one is on disk.
	Files []promptFile `json:"files,omitempty"`
}

type promptFile struct {
	ArtefactID string `json:"artefactId"`
}

type skillArgs struct {
	ThreadID    string `json:"threadId"`
	ProjectID   string `json:"projectId"`
	Dir         string `json:"dir"`
	Path        string `json:"path"`
	Content     string `json:"content"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"` // set_skill_mode
	On          bool   `json:"on"`   // set_claude_sync, set_claude_bundled, set_claude_builtin, set_codex_bundled
	// Installing: what was pasted, the staging dir a fetch made, and what to
	// take out of it.
	Source string   `json:"source"`
	ID     string   `json:"id"`
	Skill  string   `json:"skill"`
	Skills []string `json:"skills"`
	// Destination is the folder create_skill and install_staged write
	// into, one of list_skills' destinations; "" is the personal library.
	Destination string `json:"destination"`
	// Committing the library, and applying an update.
	Names   []string `json:"names"`
	Message string   `json:"message"`
	Dirs    []string `json:"dirs"`
}

type threadArgs struct {
	ThreadID   string `json:"threadId"`
	Comparison string `json:"comparison,omitempty"`
}

type fileDiffArgs struct {
	ThreadID   string `json:"threadId"`
	Path       string `json:"path"`
	Comparison string `json:"comparison,omitempty"`
	Base       string `json:"base,omitempty"`
	Head       string `json:"head,omitempty"`
}

type fileTreeArgs struct {
	ThreadID string `json:"threadId"`
	// IncludeIgnored turns the .gitignore filter off for the listing.
	IncludeIgnored bool `json:"includeIgnored"`
}

type readFileArgs struct {
	ThreadID string `json:"threadId"`
	Path     string `json:"path"`
}

type jobArgs struct {
	ThreadID string `json:"threadId"`
	JobID    string `json:"jobId"`
}

// jobOutputArgs reads a job's output file from Offset; the reply's offset is
// where to read from next, so a client polls a growing file in small chunks.
type jobOutputArgs struct {
	ThreadID string `json:"threadId"`
	JobID    string `json:"jobId"`
	Offset   int64  `json:"offset"`
}

type setModeArgs struct {
	ThreadID string `json:"threadId"`
	Mode     string `json:"mode"`
}

type switchAccountArgs struct {
	ThreadID string `json:"threadId"`
	Instance string `json:"instance"`
}

type setModelArgs struct {
	ThreadID string `json:"threadId"`
	Model    string `json:"model"`
}

type setEffortArgs struct {
	ThreadID string `json:"threadId"`
	Effort   string `json:"effort"`
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

// setThreadLabelArgs files a thread under a label; an empty labelId clears
// it. One label per thread — a status, not a tag set — so this is the whole
// assignment surface.
type setThreadLabelArgs struct {
	ThreadID string `json:"threadId"`
	LabelID  string `json:"labelId"`
}

// markViewedArgs records how far the user has actually read: seq is the head
// the client had rendered when it reported, not the server's — events landing
// mid-report stay unread.
type markViewedArgs struct {
	ThreadID string `json:"threadId"`
	Seq      int64  `json:"seq"`
}

type runComposerActionArgs struct {
	ThreadID   string `json:"threadId"`
	Action     string `json:"action"`
	Args       string `json:"args"`
	Invocation string `json:"invocation"`
}

type resolveArgs struct {
	ThreadID  string `json:"threadId"`
	RequestID string `json:"requestId"`
	Outcome   string `json:"outcome"`
	OptionID  string `json:"optionId"`
}

type resolveElicitationArgs struct {
	ThreadID  string          `json:"threadId"`
	RequestID string          `json:"requestId"`
	Action    string          `json:"action"`
	Value     json.RawMessage `json:"value"`
}

// usageReportArgs asks for the historical usage aggregate over one range.
// The range ids are the usage package's: 24h | 7d | 30d | 90d.
type usageReportArgs struct {
	Range string `json:"range"`
}

// quotaRefreshArgs re-reads one provider instance's usage limits. An empty
// instance refreshes every instance; each one's outcome — snapshot or error —
// travels in its own status, so one provider failing never blanks another.
type quotaRefreshArgs struct {
	Instance string `json:"instance"`
}

// saveProviderInstanceArgs carries a full instance spec. A sensitive env value
// travels here exactly once, on its way to the secret store; it is never
// echoed back, and the command's args are not persisted anywhere.
type saveProviderInstanceArgs struct {
	Spec provider.Spec `json:"spec"`
}

type instanceArgs struct {
	InstanceID string `json:"instanceId"`
}

// authBeginArgs starts one of three kinds of sign-in: a provider instance's
// method, an MCP server's OAuth (MCPServer, in project MCPProject or
// everywhere, with the browser's Origin, which decides where the
// authorization server sends it back), or a CLI account.
type authBeginArgs struct {
	InstanceID string `json:"instanceId"`
	MethodID   string `json:"methodId"`
	MCPServer  string `json:"mcpServer"`
	MCPProject string `json:"mcpProject"`
	Origin     string `json:"origin"`
	CLI        string `json:"cli"`
	Account    string `json:"account"`
}

// authRespondArgs answers one prompt of a running flow. Value may be a secret;
// this command bypasses the command ledger entirely so it is never written to
// disk, and its ack carries no payload.
type authRespondArgs struct {
	FlowID   string `json:"flowId"`
	PromptID string `json:"promptId"`
	Value    string `json:"value"`
}

type authCancelArgs struct {
	FlowID string `json:"flowId"`
}

// modelSettingArgs stores one model's harness-level setting for an instance.
// The value is the harness's own config JSON, not a credential — it is
// deliberately readable back, so the box a user pasted into shows what is
// actually in effect.
type modelSettingArgs struct {
	InstanceID string `json:"instanceId"`
	ModelID    string `json:"modelId"`
	Value      string `json:"value"`
}

type logoutArgs struct {
	InstanceID string `json:"instanceId"`
	MethodID   string `json:"methodId"`
}

// saveMCPServerArgs carries a server with its env and header values. They
// travel here on their way to the secret store and are never echoed back.
type saveMCPServerArgs struct {
	Server       mcp.Draft `json:"server"`
	PreviousName string    `json:"previousName"`
}

// listConnectionsArgs scopes the listing to one project; empty lists all.
type listConnectionsArgs struct {
	ProjectID string `json:"projectId"`
}

// mcpServerArgs names a server: Project is its project, or empty for one
// that goes everywhere.
type mcpServerArgs struct {
	Name    string `json:"name"`
	Project string `json:"project"`
}

type setMCPServerOffArgs struct {
	Name    string   `json:"name"`
	Project string   `json:"project"`
	Off     []string `json:"off"`
}

// setMCPServerProjectOffArgs keeps a server that goes everywhere out of one
// project, or lets it back in.
type setMCPServerProjectOffArgs struct {
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
	Off       bool   `json:"off"`
}

type parseMCPServerArgs struct {
	Text string `json:"text"`
}

type addFoundServerArgs struct {
	Harness string `json:"harness"`
	Name    string `json:"name"`
	// Where is the server's URL or command, which tells apart same-named
	// servers in two instances' configs.
	Where string `json:"where"`
	// Project is set for a server found in that project's folders, which
	// is added to the project.
	Project string `json:"project"`
}

type saveCLIArgs struct {
	CLI        mcp.CLI `json:"cli"`
	PreviousID string  `json:"previousId"`
}

type cliArgs struct {
	ID      string `json:"id"`
	Account string `json:"account"`
}

// threadMCPArgs names a thread and one of its servers. Project is what the
// client believes the server's project is; the thread's own project decides.
type threadMCPArgs struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
	Project  string `json:"project"`
}
