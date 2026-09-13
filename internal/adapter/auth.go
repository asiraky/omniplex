package adapter

import "context"

// This file is the structured-authentication capability. The original
// abstraction — Authenticator, an argv that a terminal runs — assumed every
// harness signs in through its own interactive CLI. That holds for Codex and
// Claude, but not for a harness like Pi that authenticates per model provider
// with API keys, OAuth, and device codes, none of which has a CLI flow to
// point a terminal at. AuthFlows models the interaction itself: the adapter
// runs the harness's native flow and narrates it through AuthInteraction,
// and any UI — settings, an in-session recovery card, a phone — renders the
// same events.
//
// Credential ownership stays with the harness. An adapter implementing
// AuthFlows stores nothing in omniplex: it drives the harness's own login and
// the harness's own storage. Secrets travel only through AuthInteraction
// prompt answers, which the host must never log, persist, or echo to another
// client.

// Auth method kinds. Kind is a presentation hint: every flow speaks the same
// event protocol, but a UI may introduce a "paste your key" method
// differently from a browser OAuth dance.
const (
	// AuthKindOAuth signs in through a browser: the flow emits a URL (and
	// possibly a device code) and waits for the harness to see the result.
	AuthKindOAuth = "oauth"
	// AuthKindSecret asks for a value the user already holds — an API key —
	// through a secret prompt.
	AuthKindSecret = "secret"
	// AuthKindTerminal is the fallback: no structured flow, the harness's own
	// CLI runs in an embedded terminal. The server implements it with
	// Authenticator.LoginCommand; BeginAuth is never called for it.
	AuthKindTerminal = "terminal"
)

// AuthMethod is one way an instance can authenticate, as the adapter's
// harness describes it. For a multi-provider harness the list is per model
// provider ("OpenRouter API key", "ChatGPT subscription"), and it is a live
// answer — what Pi offers depends on its installed version — so it is asked
// per instance, not baked into HarnessMeta.
type AuthMethod struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
	// Hint suggests the expected input for secret methods ("sk-or-..."),
	// rendered as a placeholder.
	Hint string `json:"hint,omitempty"`
	// Subscription marks a method that signs into a paid plan rather than
	// metered API billing, so a UI can group them first.
	Subscription bool `json:"subscription,omitempty"`
}

// Auth status states.
const (
	AuthConnected    = "connected"
	AuthDisconnected = "disconnected"
)

// Credential sources. Ambient credentials are described, never read: the UI
// may say "from environment" but no value ever leaves the adapter.
const (
	SourceNative      = "native"      // the harness's own credential storage
	SourceEnvironment = "environment" // an ambient or overlay environment variable
)

// AuthStatus is one method's current answer: whether the harness holds a
// working credential for it, and where that credential lives. Detail is
// prose for a UI ("expires in 2 days", an account email); it must never
// contain a secret.
type AuthStatus struct {
	MethodID string `json:"methodId"`
	State    string `json:"state"`
	Account  string `json:"account,omitempty"`
	Source   string `json:"source,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Auth event types, mirrored by the wire protocol. The vocabulary is
// deliberately the union of what real flows need: a URL to open, a device
// code to display, progress prose.
const (
	AuthEventInfo       = "info"
	AuthEventURL        = "auth_url"
	AuthEventDeviceCode = "device_code"
	AuthEventProgress   = "progress"
)

// AuthEvent is one display-only notification from a running flow. Fields are
// populated per Type; unused ones stay empty.
type AuthEvent struct {
	Type         string `json:"type"`
	Message      string `json:"message,omitempty"`
	URL          string `json:"url,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	UserCode     string `json:"userCode,omitempty"`
	// VerificationURI accompanies UserCode for device-code flows.
	VerificationURI string `json:"verificationUri,omitempty"`
}

// AuthPrompt asks the user for one value mid-flow. Secret marks input that
// must use the dedicated non-persisted transport: masked in the UI, absent
// from events, logs, and command results. Options, when non-empty, turns the
// prompt into a choice and the answer is the chosen option's ID.
type AuthPrompt struct {
	Message     string             `json:"message"`
	Placeholder string             `json:"placeholder,omitempty"`
	Secret      bool               `json:"secret,omitempty"`
	Options     []AuthPromptOption `json:"options,omitempty"`
}

type AuthPromptOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// AuthInteraction is how a running flow talks to whoever started it. The
// host implements it; the adapter calls it. Prompt blocks until the user
// answers or the context is cancelled. Answers to Secret prompts are handed
// to the adapter and forgotten.
type AuthInteraction interface {
	Notify(ev AuthEvent)
	Prompt(ctx context.Context, p AuthPrompt) (string, error)
}

// AuthFlows is the structured-authentication capability. Optional, like every
// adapter capability: a harness whose only sign-in is its own CLI implements
// Authenticator instead, and the server synthesises a single terminal-kind
// method for it. An adapter may implement both — structured methods plus
// LoginCommand as the explicit fallback.
//
// Every method takes the instance's env overlay so two accounts stay
// isolated, exactly as Probe and ListModels do.
type AuthFlows interface {
	// AuthMethods lists how this instance could authenticate right now. It
	// may spawn the harness, so callers treat it like ListModels: slow, may
	// fail, worth caching briefly.
	AuthMethods(ctx context.Context, env map[string]string) ([]AuthMethod, error)
	// AuthStatuses reports per-method credential state. Same cost profile as
	// AuthMethods.
	AuthStatuses(ctx context.Context, env map[string]string) ([]AuthStatus, error)
	// BeginAuth runs one method's flow to completion, narrating through ia.
	// It returns nil only when the harness ended up holding a working
	// credential. Cancelling ctx abandons the flow.
	BeginAuth(ctx context.Context, env map[string]string, methodID string, ia AuthInteraction) error
	// Logout disconnects one method's credential in the harness's own
	// storage. An adapter that cannot revoke a method returns an error that
	// says so.
	Logout(ctx context.Context, env map[string]string, methodID string) error
}

// ConfigField is one driver-specific setting an instance can carry, described
// declaratively so the client renders every driver's form with the same
// generic code — the same reason ModelMeta and PermissionModeMeta travel as
// data. Each field materialises as one environment variable in the
// instance's overlay; the adapter's existing env handling does the rest.
type ConfigField struct {
	// Env is the environment variable the field sets.
	Env         string `json:"env"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	// Kind is text | path | secret. Secret values go to the secret store and
	// come back redacted.
	Kind string `json:"kind"`
	// Isolates marks the field that gives an instance its own credential/
	// config home (CODEX_HOME, CLAUDE_CONFIG_DIR, PI_CODING_AGENT_DIR). The
	// server defaults it for new explicit instances so two accounts never
	// share state, and leaves it empty on the default instance so ambient
	// behaviour is untouched.
	Isolates bool `json:"isolates,omitempty"`
}

// Field kinds.
const (
	FieldText   = "text"
	FieldPath   = "path"
	FieldSecret = "secret"
)

// Configurer is implemented by adapters whose instances have driver-specific
// settings. Optional; an adapter without it offers only a display name and
// free-form environment variables.
type Configurer interface {
	ConfigFields() []ConfigField
}

// ModelSettings is implemented by adapters that hold per-model configuration
// the harness itself reads — routing preferences, per-model overrides — and
// that a user would otherwise have to hand-edit a config file to change.
//
// The values are opaque JSON to Omniplex: it is the harness's schema, the
// harness's file, and the harness's business what the keys mean. Omniplex
// only offers a place to put them, so the setting lives beside the account it
// applies to instead of in a text editor.
type ModelSettings interface {
	// ModelSettingsSchema describes the box a UI should draw. A zero Prefix
	// means every model takes the setting.
	ModelSettingsSchema() ModelSettingsSchema
	// ModelSettings reads the values currently stored, keyed by the model id
	// as it appears in a listing. Models with nothing set are absent.
	ModelSettings(env map[string]string) (map[string]string, error)
	// SetModelSetting stores one model's value, or clears it when value is
	// empty. It must leave every other key in the harness's config verbatim:
	// a user's hand-written settings are not ours to rewrite.
	SetModelSetting(env map[string]string, modelID, value string) error
}

// ModelSettingsSchema tells a UI what the per-model setting is and which
// models it applies to.
type ModelSettingsSchema struct {
	// Label names the setting, e.g. "Provider routing".
	Label string `json:"label"`
	// Description says what it does, in one or two sentences.
	Description string `json:"description,omitempty"`
	// Placeholder is an example value.
	Placeholder string `json:"placeholder,omitempty"`
	// DocsURL points at whoever defines the schema.
	DocsURL string `json:"docsUrl,omitempty"`
	// Prefix restricts the setting to model ids starting with it, because a
	// setting can be specific to one of a harness's providers.
	Prefix string `json:"prefix,omitempty"`
}
