// Package adapter defines the contract every harness plugs into. Adapters emit
// canonical events and call host services. They never touch the log, the
// fanout, or a connection.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// CreateOptions configures a new harness session.
type CreateOptions struct {
	ThreadID string // omniplex thread id, caller-owned; the harness is told to use it where it can
	Cwd      string
	Model    string
	Mode     string
	Effort   string

	// Env is the provider instance's credential overlay, applied over the
	// ambient environment when the harness process spawns. It is the entire
	// multi-account mechanism: adapters never learn what an instance is, they
	// just export these variables. Nil means ambient credentials.
	Env map[string]string

	// Resume asks the harness to continue an existing conversation rather
	// than start a fresh one, so restarting the server does not amnesia the
	// agent. HarnessSessionID is the harness's own id when it differs from
	// SessionID.
	Resume           bool
	HarnessSessionID string

	// MCPServers are the MCP servers the session gets: omniplex's own tools
	// (showing a file) and the servers the user added to omniplex. The core
	// only sends user servers to an adapter that implements MCPHost, and only
	// of the kinds it says it runs. An adapter whose harness cannot take MCP
	// servers at all ignores them.
	MCPServers []MCPServer

	// ExtraDirs are folders outside Cwd the agent may read and write: the
	// project's home folder, when the session works in a repo. An adapter
	// whose harness has no such setting ignores them.
	ExtraDirs []string

	// SkillDirs are skills folders outside Cwd the harness should load: the
	// project home's .agents/skills when the session works somewhere else.
	// An adapter whose harness finds them another way ignores them.
	SkillDirs []string

	// Plugins are folders laid out as a Claude plugin (.claude-plugin/ and
	// skills/) that the session loads for itself alone: omniplex's own
	// bundled skill. Claude loads each as a session plugin; a harness without
	// plugins loads its skills/ folder as a skills root.
	Plugins []string

	// Instructions are omniplex's words to the agent: what Omniplex is, so a
	// mention of it makes sense. An adapter adds them beside its harness's own
	// prompt and never in place of it: Claude takes them as its custom system
	// prompt, Codex as developer instructions. Pi doesn't take them yet.
	Instructions string
}

// PluginSkills is the skills folder of a plugin in CreateOptions.Plugins.
func PluginSkills(plugin string) string { return filepath.Join(plugin, "skills") }

// MCPServer is one MCP server a session gets: a local process (Command) or a
// remote streamable-HTTP endpoint (URL). Tools lists the tool names it serves,
// so an adapter can pre-approve them rather than ask a human about omniplex's
// own tools.
//
// Env values and Headers can be credentials. An adapter must never put them
// on a command line, which any local user can read; it passes them through
// the harness's environment instead.
type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Tools   []string          `json:"tools,omitempty"`
	// ToolTimeout is how long the harness waits on one of this server's tool
	// calls; zero leaves the harness's default. omniplex's own tools wait on
	// a person tapping a card, which takes far longer than any default.
	ToolTimeout time.Duration `json:"-"`
}

// MCPServerStatus is how a live session reports one of its MCP servers.
type MCPServerStatus struct {
	Name string `json:"name"`
	// Status is connected, needs_auth, failed, pending or disabled.
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// MCPHost is implemented by adapters whose harness can take MCP servers from
// omniplex. It is how a new harness opts in: implement it, honour
// CreateOptions.MCPServers, and the user's servers reach it with no change to
// the core or the UI.
type MCPHost interface {
	// MCPTransports lists the kinds of server the harness runs: "stdio" (a
	// local process) and "http" (a remote streamable-HTTP endpoint).
	MCPTransports() []string
	// ConfiguredMCPServers lists the servers the harness's own config already
	// defines, under a provider instance's environment overlay (nil means
	// ambient), so the user can copy them into omniplex. It only reads, and a
	// harness config that is not there is an empty list, not an error.
	// Values in Env and Headers come back as they are in the file; the core
	// never sends them to a client.
	ConfiguredMCPServers(ctx context.Context, env map[string]string) ([]ConfiguredMCPServer, error)
	// ProjectMCPServers lists the servers the harness loads in a session
	// whose working directory is dir, beyond ConfiguredMCPServers: the
	// repo's own config and any user config keyed to that folder. Same
	// rules as ConfiguredMCPServers otherwise.
	ProjectMCPServers(ctx context.Context, env map[string]string, dir string) ([]ConfiguredMCPServer, error)
}

// ConfiguredMCPServer is a server found in a harness's own config.
type ConfiguredMCPServer struct {
	MCPServer
	// Origin says where it was found, for a person: "User settings",
	// "Plugin cloudflare", "config.toml".
	Origin string `json:"origin"`
}

// MCPControl is implemented by sessions whose harness can report on its MCP
// servers and reconnect one. Reconnect takes the server's current definition,
// so a fresh token reaches the harness without restarting the session; an
// adapter that cannot swap the definition returns ErrMCPUnsupported.
type MCPControl interface {
	MCPStatus(ctx context.Context) ([]MCPServerStatus, error)
	ReconnectMCP(ctx context.Context, server MCPServer) error
}

// ErrMCPUnsupported is returned by an MCPControl that cannot do what was asked.
var ErrMCPUnsupported = errors.New("this agent cannot do that with its MCP servers")

// PromptInput is one user turn.
type PromptInput struct {
	TurnID string
	// QueueID is set on a Steer instead of TurnID: the prompt's identity is
	// the queue entry it came from, and the harness is expected to hand it
	// back with that id when it reads it.
	QueueID string
	Text    string
	// Images the human attached, already stored on this host. Each carries the
	// path the harness reads the bytes from; a harness that cannot take images
	// simply ignores them.
	Images []proto.PromptImage
}

// Adapter creates harness sessions.
//
// An adapter is wholly responsible for whatever its harness needs to run —
// binaries, runtimes, sidecars, credentials — and reports that through Probe.
// The core never learns what any particular harness requires; it asks whether
// an adapter is ready and renders the answer.
type Adapter interface {
	ID() string
	Meta() HarnessMeta
	// Models is the built-in fallback list, used only until (or unless) a live
	// ListModels answer arrives. It is deliberately small: the real list comes
	// from the harness.
	Models() []ModelMeta
	// PermissionModes returns the permission presets this harness offers, most
	// permissive last. The id is opaque to the server and the UI; only the
	// adapter interprets it.
	PermissionModes() []PermissionModeMeta
	// ListModels asks the harness which models it offers right now, under the
	// given instance's environment overlay (nil means ambient). It spawns the
	// harness, so it is slow and may fail; callers cache the answer and fall
	// back to Models. An adapter that cannot ask returns an error rather than
	// a guess.
	ListModels(ctx context.Context, env map[string]string) ([]ModelMeta, error)
	// Probe reports whether this harness can start right now, under the given
	// provider instance's environment overlay (nil means ambient). It must be
	// cheap, must not mutate anything, and must never block for long: it runs
	// at startup and whenever a UI asks to re-check. It runs per instance, so
	// two accounts can report independent health.
	Probe(ctx context.Context, env map[string]string) Availability
	CreateSession(ctx context.Context, host HostServices, o CreateOptions) (Session, error)
}

// HarnessMeta is everything a UI needs to present a harness. It lives here so
// that adding a harness requires no change to the server or to any client:
// presentation details travel with the adapter.
type HarnessMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Accent is a CSS colour a UI may use to distinguish this harness.
	Accent string `json:"accent"`
	// DocsURL points at the harness's own documentation, for install hints.
	DocsURL string `json:"docsUrl,omitempty"`
}

// ModelMeta is a selectable model, as the harness itself describes it.
//
// Everything but Group is the harness's own answer: omniplex does not know what
// models exist, what they are called, or which one is the default. Group is
// the adapter's one presentation call — which of its models a UI should fold
// away as superseded — because no harness reports that today.
type ModelMeta struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Version names the generation behind the label ("Opus 5 with 1M
	// context", "5.6"), so a row can say which Opus it is.
	Version string `json:"version,omitempty"`
	// Description is the harness's one-line summary of what the model is for.
	Description string `json:"description,omitempty"`
	// Resolves is the concrete model an alias stands for, so a UI can say what
	// "Default" actually runs.
	Resolves string `json:"resolves,omitempty"`
	// Group is "" for a current model and GroupLegacy for a superseded one a
	// UI should collapse. Any other value is a group name a UI renders
	// verbatim.
	Group string `json:"group,omitempty"`
	// Default marks the model the harness itself would pick. Exactly one row
	// should carry it; a UI preselects that row rather than inventing a
	// "Default" entry of its own.
	Default bool `json:"default,omitempty"`
	// Efforts are the reasoning levels this model accepts, most modest first.
	// They are per model — one harness offers "ultra" on its newest models
	// only — so an effort control reads them rather than assuming a fixed set.
	Efforts []string `json:"efforts,omitempty"`
	// Supports1M marks a model the harness offers a 1M-context alias for, so a
	// UI can offer the larger window rather than guessing from the name.
	Supports1M bool `json:"supports1m,omitempty"`
}

// GroupLegacy marks a model kept for continuity rather than offered first.
const GroupLegacy = "legacy"

// PermissionModeMeta is one permission preset a harness offers. Like
// ModelMeta, it travels from the adapter to the UI as opaque data: the server
// never interprets the id, and a harness with a different permission shape
// (one enum, two axes, whatever) maps its own ids in its own adapter.
type PermissionModeMeta struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Default marks the mode selected when the user has expressed no
	// preference. It matches what an empty CreateOptions.Mode does.
	Default bool `json:"default,omitempty"`
	// Level places the mode on the scale every harness shares, so a person can
	// pick how much the agent may do without learning each harness's names.
	// Empty for the modes that fit none of them; those stay under Advanced.
	Level string `json:"level,omitempty"`
}

// The permission levels, least trusting first.
const (
	LevelAsk   = "ask"   // ask before changing anything
	LevelEdits = "edits" // edit files, ask before commands
	LevelAll   = "all"   // do everything without asking
)

// Availability states.
const (
	StateReady       = "ready"
	StateUnavailable = "unavailable"
)

// Remedy is one actionable step a user can take to make a harness available.
type Remedy struct {
	Text string `json:"text"`
	// URL is optional; a UI may render Text as a link to it.
	URL string `json:"url,omitempty"`
	// Command is optional; a shell command the user could run.
	Command string `json:"command,omitempty"`
	// Action is optional; names something the server can do on the user's
	// behalf. The only value today is RemedyLogin.
	Action string `json:"action,omitempty"`
}

// Availability is an adapter's self-report. An unavailable adapter is still
// registered and still listed — it simply cannot start a session, and says
// why in terms its own harness understands.
type Availability struct {
	State  string   `json:"state"`
	Reason string   `json:"reason,omitempty"`
	Remedy []Remedy `json:"remedy,omitempty"`
	// Facts are diagnostic key/values (resolved paths, versions). Displayed
	// verbatim; never interpreted by the core.
	Facts map[string]string `json:"facts,omitempty"`
}

// Authenticator is implemented by an adapter whose harness signs in
// interactively. The server runs the command in a terminal the user can see
// and type into — the login is the harness's own flow, not omniplex's.
type Authenticator interface {
	// LoginCommand is the argv that starts the harness's sign-in flow under the
	// instance's environment. Unavailable when the harness itself cannot be
	// found, in which case the error says why.
	LoginCommand(ctx context.Context) ([]string, error)
}

// RemedyLogin marks a remedy the server can carry out itself: the UI offers a
// sign-in that runs the adapter's LoginCommand in a terminal.
const RemedyLogin = "login"

func Ready(facts map[string]string) Availability {
	return Availability{State: StateReady, Facts: facts}
}

func Unavailable(reason string, remedy ...Remedy) Availability {
	return Availability{State: StateUnavailable, Reason: reason, Remedy: remedy}
}

func (a Availability) OK() bool { return a.State == StateReady }

// MergeEnv applies an instance's overlay onto a base environment, replacing
// any variable the overlay names. Overlay keys are applied in sorted order so
// the result is deterministic. This is the whole credential mechanism: no
// value is ever handed to an SDK directly.
func MergeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(overlay))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, shadowed := overlay[name]; shadowed {
				continue
			}
		}
		out = append(out, entry)
	}
	names := make([]string, 0, len(overlay))
	for name := range overlay {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, name+"="+overlay[name])
	}
	return out
}

// Session is one live harness process.
type Session interface {
	Prompt(ctx context.Context, in PromptInput) error
	Cancel(ctx context.Context) error
	// Events is closed when the harness is disposed.
	Events() <-chan proto.Emission
	Close() error
}

// ModeSwitcher is implemented by sessions whose harness can change permission
// mode mid-conversation. The mode is one of the adapter's own
// PermissionModes ids. A harness that cannot switch simply does not implement
// this, and the host reports that legibly instead of silently ignoring it.
// Steerer is a Session that can take a prompt while a turn is running. The
// harness owns the prompt from the moment Steer returns nil: it reads it at
// its next model call and says so with prompt.injected naming the queue id,
// or — when the turn ends before that — starts a turn with it and names the
// queue id on turn.started. Either way the actor never sends it again. Cancel
// discards any steer the harness has not read yet.
type Steerer interface {
	Steer(ctx context.Context, in PromptInput) error
}

type ModeSwitcher interface {
	SetMode(ctx context.Context, mode string) error
}

// ModelSwitcher is implemented by sessions whose harness can change model
// mid-conversation. The model is one of the adapter's own Models ids. A
// harness that cannot switch simply does not implement this, and the host
// reports that legibly instead of silently ignoring it.
type ModelSwitcher interface {
	SetModel(ctx context.Context, model string) error
}

// ConversationMover is implemented by drivers whose conversation lives in the
// account's own storage, so moving a session to another account of the same
// driver means moving that storage with it: the next resume under the new
// account's env reads the conversation from where that account keeps it.
// from and to are the two accounts' credential overlays; the harness process
// must already be stopped. A driver that does not implement this cannot switch
// accounts mid-session, and the host says so.
type ConversationMover interface {
	MoveConversation(from, to map[string]string, cwd, harnessSessionID string) error
}

// EffortSwitcher is implemented by sessions whose harness can change reasoning
// effort mid-conversation. The effort is one of the running model's own
// Efforts ids. A harness that cannot switch simply does not implement this,
// and the host reports that legibly instead of silently ignoring it.
type EffortSwitcher interface {
	SetEffort(ctx context.Context, effort string) error
}

// JobStopper is implemented by sessions whose harness can stop one running
// job — a subagent, a background shell — by id, without interrupting the
// turn. A harness that cannot simply does not implement this, and the host
// reports that legibly instead of silently ignoring it.
type JobStopper interface {
	StopJob(ctx context.Context, jobID string) error
}

// ComposerItem is one provider-native token the composer can discover. The
// core deliberately does not interpret Trigger, InsertText, or Action: they
// are the adapter's normalized presentation and routing contract.
type ComposerItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"`    // command | skill
	Trigger     string   `json:"trigger"` // /, $, or another provider-native token
	InsertText  string   `json:"insertText"`
	ArgsHint    string   `json:"argsHint,omitempty"`
	Origin      string   `json:"origin,omitempty"`
	Behavior    string   `json:"behavior"` // prompt | client-action | adapter-action
	Action      string   `json:"action,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	// Inline marks a token the harness acts on wherever it sits in a prompt.
	// Without it the token only means something as the first thing in one, so
	// a composer offers it there and nowhere else.
	Inline bool `json:"inline,omitempty"`
}

const (
	ComposerPrompt        = "prompt"
	ComposerClientAction  = "client-action"
	ComposerAdapterAction = "adapter-action"
)

// ComposerCataloguer is an optional live-session capability. Discovery lives
// here, beside the provider process whose cwd, credentials, and installed
// version determine the real answer.
type ComposerCataloguer interface {
	ComposerItems(ctx context.Context) ([]ComposerItem, error)
}

// DraftCataloguer is an optional adapter capability: what a session started in
// cwd under env would be able to invoke, answered without starting one, for a
// thread that does not exist yet. It must be cheap — it runs while someone is
// typing — so it reads what is on disk rather than spawning the harness, and
// the live session's ComposerItems replaces its answer once there is one.
// Every entry is ComposerPrompt: there is no thread for an action to act on.
type DraftCataloguer interface {
	DraftComposerItems(ctx context.Context, env map[string]string, cwd string) ([]ComposerItem, error)
}

// ComposerActionRunner handles catalogue entries that map to a provider RPC
// rather than prompt text. Action is opaque outside the adapter.
type ComposerActionInput struct {
	TurnID string
	Action string
	Args   string
}

type ComposerActionRunner interface {
	RunComposerAction(ctx context.Context, in ComposerActionInput) (any, error)
}

// PermissionRequest is what an adapter asks a human, via the host.
type PermissionRequest struct {
	TurnID     string
	ToolCallID string
	ToolName   string
	Title      string
	RawInput   json.RawMessage
	Options    []proto.PermissionOption
}

// PermissionOutcome is the human's answer, routed back from any presenter.
type PermissionOutcome struct {
	Outcome  string // proto.Outcome*
	OptionID string
}

type ElicitationRequest struct {
	TurnID string
	Prompt string
	Schema json.RawMessage
}

type ElicitationResult struct {
	Action string
	Value  json.RawMessage
}

// Allowed reports whether the outcome permits the tool to run.
func (o PermissionOutcome) Allowed() bool {
	return o.Outcome == proto.OutcomeAllowOnce || o.Outcome == proto.OutcomeAllowAlways
}

// HostServices are capabilities the adapter must not implement itself.
// RequestPermission blocks until a permission.resolved event is appended — by
// any presenter — which is what makes permissions fungible across devices.
type HostServices interface {
	RequestPermission(ctx context.Context, req PermissionRequest) (PermissionOutcome, error)
	Elicit(ctx context.Context, req ElicitationRequest) (ElicitationResult, error)
	Logf(format string, args ...any)
}

// ComposerCatalogueInvalidator is an optional host service used by adapters
// whose native runtime watches skills or commands. It is ephemeral UI state,
// not a canonical transcript event.
type ComposerCatalogueInvalidator interface {
	ComposerCatalogueChanged()
}

// ---- Account-level usage limits ----
//
// Quota is separate from the per-session usage.updated payload on purpose:
// that event is context occupancy and turn accounting for one conversation,
// while quota answers the account question — “can I keep working?” — for the
// provider instance the session ran under. Nothing about quota is session
// state: it is cached per instance, merged from whatever source last spoke,
// and presented on its own page.

// Quota window kinds. Kinds drive presentation (icons, wording) without any
// provider-specific vocabulary leaking into the core.
const (
	QuotaSession = "session"
	QuotaWeekly  = "weekly"
	QuotaMonthly = "monthly"
	QuotaCredits = "credits"
)

// QuotaWindow is one provider-reported allowance bucket, already normalised:
// a percentage used, when it resets, and how wide the window is. ID is stable
// across reads and live updates, which is what lets a sparse update land on
// the row an earlier read drew.
type QuotaWindow struct {
	CheckedAt int64  `json:"checkedAt,omitempty"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	// UsedPercent is 0–100 as the provider reports it. Not clamped: an
	// over-limit reading is a real signal, exactly like context occupancy.
	// A pointer because a sparse live update carries only the fields it knows:
	// nil means “unchanged”, not “zero”.
	UsedPercent *float64 `json:"usedPercent,omitempty"`
	// ResetsAt is epoch milliseconds; zero means the provider did not say.
	ResetsAt   int64 `json:"resetsAt,omitempty"`
	WindowMins int   `json:"windowDurationMins,omitempty"`
	// Count is how many reset credits remain, for credits windows. Nil for
	// utilisation windows.
	Count *int `json:"count,omitempty"`
}

// QuotaSnapshot is one provider account's usage limits at a moment. A
// snapshot with no windows and a non-empty Unavailable is a legible negative
// answer (an API-key session has no plan limits) rather than a failure.
type QuotaSnapshot struct {
	// Full distinguishes complete reads from sparse notifications inside the server.
	Full        bool          `json:"-"`
	CheckedAt   int64         `json:"checkedAt"` // epoch ms
	Plan        string        `json:"plan,omitempty"`
	AccountID   string        `json:"accountId,omitempty"`
	Windows     []QuotaWindow `json:"windows,omitempty"`
	Unavailable string        `json:"unavailable,omitempty"`
}

// QuotaReader is an optional adapter capability: read the account's usage
// limits out-of-band, without a live session, by asking the harness once.
// It runs under the provider instance's environment overlay for the same
// reason a session does — the quota belongs to the account the overlay
// selects.
type QuotaReader interface {
	ReadQuota(ctx context.Context, env map[string]string) (QuotaSnapshot, error)
}

// SessionQuota is the optional live-session counterpart: ask the running
// harness process, which is cheaper than spawning one and always reads the
// account the process is authenticated as.
type SessionQuota interface {
	Quota(ctx context.Context) (QuotaSnapshot, error)
}

// QuotaReporter is an optional HostServices extension. A harness that pushes
// live usage-limit updates — Claude's rate-limit events, Codex's
// account/rateLimits notifications — reports them through it; the host caches
// them per provider instance, merging sparse windows by id.
type QuotaReporter interface {
	ReportQuota(snap QuotaSnapshot)
}

// FailureError is an error that already knows how it should be presented. An
// adapter that refuses a prompt because the harness needs a login knows that
// much at the point of refusal; without somewhere to put it, the classification
// is lost and the turn recorded by the caller looks like any other failure —
// which is how a sign-in problem came to be offered a "continue" button.
//
// Kind is one of the proto.Failure* constants.
type FailureError struct {
	Kind string
	Err  error
}

func (e *FailureError) Error() string { return e.Err.Error() }
func (e *FailureError) Unwrap() error { return e.Err }

// FailureOf reports how err wants to be classified, or "" if it has no opinion.
func FailureOf(err error) string {
	var fe *FailureError
	if errors.As(err, &fe) {
		return fe.Kind
	}
	return ""
}
