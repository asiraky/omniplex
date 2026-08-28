// Package adapter defines the contract every harness plugs into. Adapters emit
// canonical events and call host services. They never touch the log, the
// fanout, or a connection.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/asiraky/omniplex/internal/proto"
)

// CreateOptions configures a new harness session.
type CreateOptions struct {
	SessionID string // caller-owned identity; the harness is told to use it where it can
	Cwd       string
	Model     string
	Mode      string
	Effort    string

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
}

// PromptInput is one user turn.
type PromptInput struct {
	TurnID string
	Text   string
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
}

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

// SummaryRequest is one transcript to compress, plus the instructions that say
// how. System is the operator's editable prompt; Transcript is the rendered
// session. They are kept apart rather than concatenated so an adapter can put
// each where its harness expects it — a system prompt slot and a user turn —
// instead of every adapter re-inventing the same delimiter.
type SummaryRequest struct {
	System     string
	Transcript string
}

// Summarizer is an optional adapter capability: answer one question about a
// transcript, cheaply, without starting a session.
//
// It is on the Adapter rather than on Session on purpose. A summary is most
// wanted for a session that finished days ago, and resuming a closed
// conversation just to ask it what it did would restart a harness, restore its
// context, and bill for it. This spawns a short-lived process against the
// harness's fastest model and throws it away.
//
// The env overlay is the session's own provider instance, so the summary is
// billed to the account that did the work. An adapter whose harness cannot do
// this simply does not implement it, and the host says so.
type Summarizer interface {
	Summarize(ctx context.Context, env map[string]string, req SummaryRequest) (SummaryResult, error)
}

// SummaryResult is the answer plus the model that gave it. The model is
// reported rather than configured because the adapter chooses it, and a
// summary that names its author can be judged; one that appears from nowhere
// cannot.
type SummaryResult struct {
	Text  string
	Model string
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
