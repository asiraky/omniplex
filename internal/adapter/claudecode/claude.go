// Package claudecode adapts Anthropic's Claude Agent SDK to the canonical
// event model.
//
// The SDK is a TypeScript/Python library, so it is hosted in a small Node
// sidecar (see sidecar/sidecar.mjs) which this package spawns and speaks to
// over stdio. Everything that arrangement implies — locating a JS runtime,
// unpacking the bridge, finding the user's Claude Code install, process
// lifecycle — is contained in this package. Nothing outside it knows a sidecar
// exists.
package claudecode

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/proto"
)

//go:embed sidecar/sidecar.mjs sidecar/guard.mjs sidecar/package.json
var sidecarFS embed.FS

// Adapter creates Claude sessions.
type Adapter struct {
	// ClaudePath optionally pins the Claude Code executable. Empty means
	// discover it.
	ClaudePath string

	// bundledSidecar is a standalone sidecar executable compiled into this
	// build, used when the host has no JS runtime. Empty in slim builds.
	bundledSidecar string

	once      sync.Once
	unpacked  string
	unpackErr error
}

func New(claudePath string) *Adapter {
	return &Adapter{ClaudePath: claudePath, bundledSidecar: bundledSidecarPath()}
}

func (a *Adapter) ID() string { return "claude" }

func (a *Adapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{
		ID:      "claude",
		Name:    "Claude Code",
		Accent:  "oklch(0.72 0.13 48)",
		DocsURL: docsURL,
	}
}

// PermissionModes are the Agent SDK's PermissionMode values, verbatim: the id
// is what the sidecar passes as `permissionMode`. The SDK spells the manual
// mode `default` (the CLI alias `manual` is CLI-only), and it is what omniplex sends.
func (a *Adapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{
		{ID: "default", Label: "Manual", Description: "Ask before every edit, command, and network call", Default: true},
		{ID: "plan", Label: "Plan", Description: "Read and analyze only; no changes"},
		{ID: "acceptEdits", Label: "Accept edits", Description: "Auto-accept file edits; still ask for commands"},
		{ID: "auto", Label: "Auto", Description: "A classifier approves routine actions; ask on risk"},
		{ID: "dontAsk", Label: "Pre-approved only", Description: "Never prompt; deny anything not already allowed"},
		{ID: "bypassPermissions", Label: "Bypass", Description: "Skip all permission checks"},
	}
}

// Probe reports whether a Claude session could start right now. Discovery of
// the runtime and the Claude Code install is machine-level, not per-account,
// so the instance env does not change the answer today; it is accepted so a
// future credential check can be per instance.
func (a *Adapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	r, avail := a.resolve(ctx)
	if !avail.OK() {
		return avail
	}
	// An installed Claude Code is not a usable one: an account that is signed
	// out fails every session at start, and nothing else on this machine can
	// say so — the credential may be a file, a keychain entry, or a token in
	// this instance's env. The harness is the only authority, so ask it.
	st := authStatus(ctx, r.claudePath, env)
	switch {
	case st.known && !st.loggedIn:
		return adapter.Unavailable(
			"Claude is not signed in.",
			adapter.Remedy{Text: "Sign in", Command: "claude auth login", Action: adapter.RemedyLogin},
			adapter.Remedy{Text: "Or give this instance a CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY"},
		)
	case st.known:
		if st.email != "" {
			avail.Facts["account"] = st.email
		}
		if st.subscription != "" {
			avail.Facts["plan"] = st.subscription
		}
		if st.method != "" {
			avail.Facts["auth"] = st.method
		}
	}
	return avail
}

// LoginCommand starts Claude Code's own sign-in flow, which walks the user
// through the browser and takes the code back on the terminal.
func (a *Adapter) LoginCommand(ctx context.Context) ([]string, error) {
	r, avail := a.resolve(ctx)
	if !avail.OK() {
		return nil, fmt.Errorf("%s", avail.Reason)
	}
	return []string{r.claudePath, "auth", "login"}, nil
}

// claudeAuth is what `claude auth status` reports. known is false when the
// answer could not be read at all — an older CLI without the command — in
// which case nothing is claimed either way.
type claudeAuth struct {
	known        bool
	loggedIn     bool
	method       string
	email        string
	subscription string
}

func authStatus(ctx context.Context, claudePath string, env map[string]string) claudeAuth {
	out := runBrieflyEnv(ctx, adapter.MergeEnv(os.Environ(), env), claudePath, "auth", "status")
	var raw struct {
		LoggedIn         *bool  `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		Email            string `json:"email"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil || raw.LoggedIn == nil {
		return claudeAuth{}
	}
	return claudeAuth{known: true, loggedIn: *raw.LoggedIn, method: raw.AuthMethod, email: raw.Email, subscription: raw.SubscriptionType}
}

// sidecarPath unpacks the bridge next to the user's cache once per process and
// returns its directory.
func (a *Adapter) sidecarPath() (string, error) {
	a.once.Do(func() {
		// A source build should use the checked-out bridge. The root npm
		// workspace installs its SDK dependency, and Node can resolve a hoisted
		// package from any parent node_modules directory. Distribution binaries
		// fall through to the self-contained cache extraction below.
		if dir := sourceSidecarPath(); dir != "" {
			a.unpacked = dir
			return
		}
		base, err := os.UserCacheDir()
		if err != nil {
			a.unpackErr = err
			return
		}
		dir := filepath.Join(base, "omniplex", "claude-sidecar")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			a.unpackErr = err
			return
		}
		err = fs.WalkDir(sidecarFS, "sidecar", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := sidecarFS.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, filepath.Base(p)), data, 0o644)
		})
		a.unpacked, a.unpackErr = dir, err
	})
	return a.unpacked, a.unpackErr
}

func sourceSidecarPath() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Join(filepath.Dir(filename), "sidecar")
	if _, err := os.Stat(filepath.Join(dir, "sidecar.mjs")); err != nil {
		return ""
	}
	if moduleInstalled(dir, "@anthropic-ai", "claude-agent-sdk") {
		return dir
	}
	return ""
}

func moduleInstalled(dir string, parts ...string) bool {
	for parent := dir; ; parent = filepath.Dir(parent) {
		candidate := append([]string{parent, "node_modules"}, parts...)
		if _, err := os.Stat(filepath.Join(candidate...)); err == nil {
			return true
		}
		next := filepath.Dir(parent)
		if next == parent {
			return false
		}
	}
}

// sidecarConfig is what the bridge needs to start a session. It is passed as
// one JSON argument so the sidecar has no bespoke flag parsing.
type sidecarConfig struct {
	// Op selects what the bridge does. Empty runs a session; "models" runs the
	// one-shot listing and exits.
	Op             string `json:"op,omitempty"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	// AllowDangerouslySkipPermissions is the SDK's second opt-in: without it,
	// bypassPermissions is rejected both at start and on a later
	// setPermissionMode. It permits the mode; it does not enable it.
	AllowDangerouslySkipPermissions bool   `json:"allowDangerouslySkipPermissions,omitempty"`
	Effort                          string `json:"effort,omitempty"`
	// SessionID and Resume are mutually exclusive, as the SDK requires: one
	// names a new conversation, the other continues an existing one.
	SessionID  string `json:"sessionId,omitempty"`
	Resume     string `json:"resume,omitempty"`
	ClaudePath string `json:"claudePath,omitempty"`
}

// conversationID resolves which Claude conversation a CreateSession call names
// or resumes. omniplex names the conversation at start — the same id starts a
// session and later resumes it, so a server restart continues the conversation
// rather than starting blank. But the name is not immutable: Claude Code
// rotates its conversation id in place (/clear starts a new conversation under
// a new id inside the same process), and the rotated id — reported back
// through session.config_changed and replayed into the projection — is the
// conversation this session actually is now. Resuming the original id after a
// rotation would resurrect a cleared conversation and strand the live one.
func conversationID(o adapter.CreateOptions) string {
	if o.Resume && o.HarnessSessionID != "" {
		return o.HarnessSessionID
	}
	if o.SessionID != "" {
		return o.SessionID
	}
	return uuid.NewString()
}

func (a *Adapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	r, avail := a.resolve(ctx)
	if !avail.OK() {
		return nil, fmt.Errorf("claude is unavailable: %s", avail.Reason)
	}

	sessionID := conversationID(o)

	cfg := sidecarConfig{
		Cwd:            o.Cwd,
		Model:          o.Model,
		PermissionMode: o.Mode,
		// Always allowed as an *option* (the CLI's
		// --allow-dangerously-skip-permissions semantics), never enabled by it:
		// the SDK rejects bypassPermissions — at start or via a mid-session
		// setPermissionMode — unless this was set at launch. Bypass is a mode
		// like any other here: picking it is the whole decision, so omniplex adds no
		// gate of its own on top of the SDK's own refusals.
		AllowDangerouslySkipPermissions: true,
		Effort:                          o.Effort,
		ClaudePath:                      r.claudePath,
	}
	// One field or the other, never both — the SDK rejects the pair.
	if o.Resume {
		cfg.Resume = sessionID
	} else {
		cfg.SessionID = sessionID
	}
	blob, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	args := append(append([]string{}, r.runtimeArgs...), string(blob))
	cmd := exec.Command(r.runtime, args...)
	cmd.Dir = o.Cwd
	// The instance's overlay over the ambient environment is the entire
	// credential mechanism: CLAUDE_CONFIG_DIR, CLAUDE_CODE_OAUTH_TOKEN, or
	// ANTHROPIC_API_KEY select the account per process.
	cmd.Env = append(adapter.MergeEnv(os.Environ(), o.Env), "CLAUDE_CODE_ENTRYPOINT=sdk-ts")
	// The 1M context window is a process-start choice, not a runtime one: the
	// CLI decides it from CLAUDE_CODE_DISABLE_1M_CONTEXT when it boots, and no
	// control call changes it after. It is 1M by default on accounts that have
	// it, which is expensive and rarely wanted, so omniplex opts in explicitly: a
	// session runs 1M only when its model id carries the "[1m]" tag, and 200k
	// otherwise. That makes the tag the real switch and 200k the default.
	if !strings.Contains(o.Model, "[1m]") {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_DISABLE_1M_CONTEXT=1")
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude bridge: %w", err)
	}

	s := &session{
		host:             host,
		cmd:              cmd,
		stdin:            stdin,
		cwd:              o.Cwd,
		configDir:        claudeConfigDir(o.Cwd, o.Env),
		harnessSessionID: sessionID,
		model:            o.Model,
		effort:           o.Effort,
		events:           make(chan proto.Emission, 256),
		streams:          map[string]*stream{},
		done:             make(chan struct{}),
	}
	s.conn = jsonrpc.NewConn(stdout, stdin, s.handleRequest, s.handleNotification)

	go s.drainStderr(stderr)
	go s.watchExit()

	s.emit(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{
		HarnessSessionID: sessionID,
	}))

	return s, nil
}

// block tracks one streaming content block of the current assistant message.
type block struct {
	kind    string // text | thinking | tool_use
	blockID string
	toolID  string
	name    string
}

// stream is one message stream's state. There can be several at once: the main
// conversation, plus one per running subagent, each keyed by the Task call
// that spawned it — parallel Tasks interleave their events, and a shared block
// map would let one agent's message_start reset another's mid-flight.
type stream struct {
	messageID string
	blocks    map[int]*block
}

type session struct {
	host  adapter.HostServices
	cmd   *exec.Cmd
	conn  *jsonrpc.Conn
	stdin io.WriteCloser
	cwd   string
	// configDir is resolved from the same per-instance environment the child
	// received, so origin enrichment cannot accidentally inspect another
	// Claude account's skills.
	configDir string

	harnessSessionID string

	events chan proto.Emission
	done   chan struct{}
	closed sync.Once

	mu        sync.Mutex
	turnID    string
	streams   map[string]*stream
	sawResult bool
	model     string
	effort    string
	// fatal is the last thing the bridge said before it died. The SDK reports
	// a harness that refuses to start — an expired login above all — by
	// throwing, and that throw is the only description of what went wrong
	// anyone will ever get. Keeping it here is what lets the turn that dies
	// say why instead of "the process is gone".
	fatal string

	// usage carries both cost accounting and window occupancy; it is kept on
	// the session and re-emitted whole so a result (accounting + fallback
	// occupancy) and a context_usage message (authoritative occupancy) can
	// each update their part without clobbering the other's.
	usage proto.UsageUpdatedPayload
	// lastPromptTokens is the size of the most recent request's prompt — the
	// final assistant message's fresh + cached input — which is the occupancy
	// fallback when the harness cannot report context usage directly. It
	// deliberately excludes output: this request's output is next request's
	// input, so counting it would double-count.
	lastPromptTokens int64
}

func (s *session) Events() <-chan proto.Emission { return s.events }

func (s *session) Prompt(ctx context.Context, in adapter.PromptInput) error {
	// A bridge that has already died cannot be prompted, and pretending
	// otherwise is how a login failure turned into a mystery: the turn opened,
	// the write failed on a broken pipe (or the actor had already torn the
	// session down), and every screen downstream fell back to describing a
	// server restart. Refuse with the reason the bridge actually gave.
	select {
	case <-s.conn.Done():
		reason, kind := s.exitReason()
		return &adapter.FailureError{Kind: kind, Err: errors.New(reason)}
	default:
	}

	s.mu.Lock()
	// The actor believed the session was idle when it accepted this prompt,
	// but the harness may have started work by itself in the meantime — the
	// turn it opened is queued on the event channel and the actor has not
	// seen it yet. Overwriting that turn's id here would label the harness's
	// in-flight work with this prompt's turn and leave the open turn
	// unfinished forever. Refuse instead; the caller can retry when idle.
	if s.turnID != "" && s.turnID != in.TurnID {
		s.mu.Unlock()
		return errors.New("the harness resumed work on its own; wait for it to finish")
	}
	s.turnID = in.TurnID
	s.sawResult = false
	s.mu.Unlock()

	params := map[string]any{"text": in.Text}
	// Paths, not bytes: the sidecar reads the files and base64s them into the
	// SDK's image blocks, so a 10 MB screenshot never crosses this pipe as
	// JSON.
	if len(in.Images) > 0 {
		images := make([]map[string]any, 0, len(in.Images))
		for _, img := range in.Images {
			images = append(images, map[string]any{"path": img.Path, "mediaType": img.MediaType})
		}
		params["images"] = images
	}
	return s.conn.Notify("prompt", params)
}

func (s *session) Cancel(ctx context.Context) error {
	return s.conn.Notify("interrupt", map[string]any{})
}

// SetMode switches the permission mode mid-session via the SDK's
// setPermissionMode, which needs no restart in streaming input mode. It is a
// request, not a notification, so a mode the harness refuses (managed settings
// can disable bypass and auto) comes back as a legible error.
func (s *session) SetMode(ctx context.Context, mode string) error {
	return s.conn.Call(ctx, "setPermissionMode", map[string]any{"mode": mode}, nil)
}

// SetModel switches the model mid-session via the SDK's setModel. It is a
// request, not a notification, for the same reason as SetMode: a model the
// harness refuses — an id gone stale in the catalogue, or one a client made up
// — must come back as an error rather than be recorded as the running model
// while the session keeps using the old one. Local state is updated only once
// the harness has acknowledged the switch.
func (s *session) SetModel(ctx context.Context, model string) error {
	if err := s.conn.Call(ctx, "setModel", map[string]any{"model": model}, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.model = model
	// The cached window belonged to the old model. Clearing it stops a stale
	// value (a 1M Opus window shown for a 200k Sonnet) from persisting: the
	// next result recomputes from the new model, and the next context_usage
	// report replaces it with the harness's authoritative figure.
	s.usage.ContextWindow = 0
	s.usage.ContextLimit = 0
	s.mu.Unlock()
	return nil
}

// SetEffort switches reasoning effort mid-session via the SDK's
// applyFlagSettings, which changes effortLevel on a running streaming session
// with no restart. Like SetModel it is a request: an effort the harness
// refuses must surface as an error rather than be recorded as applied.
func (s *session) SetEffort(ctx context.Context, effort string) error {
	if err := s.conn.Call(ctx, "setEffort", map[string]any{"effort": effort}, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.effort = effort
	s.mu.Unlock()
	return nil
}

// Close tears down the bridge. Closing stdin is the primary signal: the
// sidecar watches for EOF and exits, taking Claude Code with it. The kill is
// a backstop for a wedged process.
func (s *session) Close() error {
	s.closed.Do(func() {
		close(s.done)
		_ = s.stdin.Close()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.cmd.Wait()
	})
	return nil
}

func (s *session) watchExit() {
	<-s.conn.Done()

	s.mu.Lock()
	turn, saw := s.turnID, s.sawResult
	s.turnID = ""
	s.mu.Unlock()

	if turn != "" && !saw {
		reason, kind := s.exitReason()
		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turn, StopReason: proto.StopError, Error: reason, Failure: kind,
		}))
	}
	close(s.events)
}

// exitReason explains a bridge that is no longer there, in the terms the
// person reading it can act on. The common case by far is an account that
// needs to log in again: Claude Code exits immediately, the SDK throws, and
// without this the only trace is a line in the server log.
func (s *session) exitReason() (message, failure string) {
	s.mu.Lock()
	fatal := s.fatal
	s.mu.Unlock()

	if msg, ok := loginRequired(fatal); ok {
		return msg, proto.FailureAuth
	}
	if fatal != "" {
		return "claude exited: " + briefly(fatal), ""
	}
	return "claude bridge exited", ""
}

// authNeedles are what an unauthenticated Claude Code says on its way out. The
// wording moves between releases, so this matches the several shapes it has
// taken rather than one exact string.
var authNeedles = []string{
	"/login",
	"not logged in",
	"authentication_failed",
	"invalid api key",
	"authentication_error",
	"authentication failed",
	"oauth token has expired",
	"oauth token is invalid",
	"please run claude login",
	"unauthorized",
	"status 401",
	"http 401",
}

// loginRequired reports whether a bridge failure was an authentication
// failure, and returns the message to show if so. The message names the fix,
// because "log in" is something only the human at the keyboard can do.
func loginRequired(fatal string) (string, bool) {
	low := strings.ToLower(fatal)
	for _, needle := range authNeedles {
		if strings.Contains(low, needle) {
			return "claude needs you to sign in again: run `claude` in a terminal and use /login, " +
				"or give this provider instance a valid CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY", true
		}
	}
	return "", false
}

func (s *session) drainStderr(r io.ReadCloser) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			s.host.Logf("claude bridge: %s", line)
		}
	}
}

func (s *session) emit(e proto.Emission) {
	select {
	case s.events <- e:
	case <-s.done:
	}
}

func (s *session) currentTurn() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnID
}

// ensureTurn returns the active turn id, opening a harness-initiated turn if
// none is open. The SDK can resume work without being prompted — a background
// task completing, an auto-continuation — and that work is a real turn: it
// needs an id so its events can be grouped, and a turn.started so projections
// know the session is no longer idle. A turn opened here has no prompt; that
// is what marks it as the harness's own doing.
func (s *session) ensureTurn() string {
	s.mu.Lock()
	if s.turnID != "" {
		id := s.turnID
		s.mu.Unlock()
		return id
	}
	id := uuid.NewString()
	s.turnID = id
	s.sawResult = false
	s.mu.Unlock()

	s.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: id}))
	return id
}

// handleRequest services the bridge's only inbound request: a permission
// decision, which the host routes to a human and answers from any presenter.
func (s *session) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method != "permission" {
		return nil, fmt.Errorf("unsupported request: %s", method)
	}

	var p struct {
		ToolName string          `json:"toolName"`
		Input    json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}

	// AskUserQuestion is not a permission gate — it is a question for the human,
	// and the SDK only routes it through canUseTool because that is the one
	// callback a headless host exposes. Answering it with the generic allow/deny
	// prompt is wrong twice over: the human sees "Allow Once" instead of the
	// choices, and echoing the input back unchanged leaves the tool with no
	// answers, so it parks forever and the model hangs. The tool's own input
	// schema carries an `answers` field "collected by the permission component";
	// we raise a durable elicitation for the choices and feed the selections back
	// through it. See askUserQuestion.
	if p.ToolName == "AskUserQuestion" {
		return s.askUserQuestion(ctx, p.Input)
	}

	outcome, err := s.host.RequestPermission(ctx, adapter.PermissionRequest{
		TurnID:   s.currentTurn(),
		ToolName: p.ToolName,
		Title:    toolTitle(p.ToolName, p.Input),
		RawInput: p.Input,
		Options:  proto.DefaultPermissionOptions(),
	})
	if err != nil {
		return map[string]any{"behavior": "deny", "message": "permission unavailable: " + err.Error()}, nil
	}
	if outcome.Allowed() {
		return map[string]any{"behavior": "allow", "updatedInput": p.Input}, nil
	}
	return map[string]any{"behavior": "deny", "message": "Denied by user"}, nil
}

// askQuestion is one entry of an AskUserQuestion tool call.
type askQuestion struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// askUserQuestion services an AskUserQuestion tool call as a durable elicitation:
// each question becomes an enum field the human answers from any attached device,
// and the chosen labels are returned as the tool's `answers` via updatedInput,
// which is how the SDK resolves the call. An empty answer set — the human
// declined, or the elicitation could not be raised — is the tool's own "user did
// not answer" path, so the model continues rather than hanging.
func (s *session) askUserQuestion(ctx context.Context, rawInput json.RawMessage) (any, error) {
	var parsed struct {
		Questions []askQuestion `json:"questions"`
	}
	if err := json.Unmarshal(rawInput, &parsed); err != nil {
		return nil, err
	}

	// Keep the original input object intact so updatedInput echoes every field
	// the tool expects; we add only `answers`.
	var input map[string]any
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return nil, err
	}
	allow := func(answers map[string]string) (any, error) {
		input["answers"] = answers
		return map[string]any{"behavior": "allow", "updatedInput": input}, nil
	}

	// Questions map onto index keys (q0, q1, …) rather than their text so the
	// schema stays well-formed; the answers, though, are keyed by question text,
	// which is the shape the tool reads back and is guaranteed unique.
	properties := map[string]any{}
	required := make([]string, 0, len(parsed.Questions))
	keys := make([]string, len(parsed.Questions))
	for i, q := range parsed.Questions {
		key := fmt.Sprintf("q%d", i)
		keys[i] = key
		field := map[string]any{"type": "string", "title": q.Question}
		if q.Header != "" {
			field["description"] = q.Header
		}
		if len(q.Options) > 0 {
			values := make([]string, 0, len(q.Options))
			descriptions := map[string]string{}
			for _, o := range q.Options {
				values = append(values, o.Label)
				if o.Description != "" {
					descriptions[o.Label] = o.Description
				}
			}
			field["enum"] = values
			if len(descriptions) > 0 {
				// The presenter shows these beside each option; losing them can
				// leave a terse label ("Blue") without the trade-off it stood for.
				field["x-optionDescriptions"] = descriptions
			}
			// AskUserQuestion always offers a free-text answer alongside the
			// listed choices — the tool tells the model not to add its own
			// "Other" because the presenter supplies it. Carry that through.
			field["x-allowOther"] = true
		}
		if q.MultiSelect {
			// The human may pick more than one; the presenter returns an array,
			// which we join comma-separated per the tool's answer contract.
			field["x-multiSelect"] = true
		}
		properties[key] = field
		required = append(required, key)
	}

	schema, err := json.Marshal(map[string]any{
		"type": "object", "properties": properties, "required": required,
	})
	if err != nil {
		return allow(map[string]string{})
	}

	result, err := s.host.Elicit(ctx, adapter.ElicitationRequest{
		TurnID: s.currentTurn(),
		Prompt: "The assistant needs your input to continue.",
		Schema: schema,
	})
	if err != nil || result.Action != "accept" {
		return allow(map[string]string{})
	}

	var values map[string]any
	_ = json.Unmarshal(result.Value, &values)
	answers := map[string]string{}
	for i, q := range parsed.Questions {
		// A single-select answer arrives as a string; a multi-select one as an
		// array of labels, which the tool wants joined comma-separated.
		switch v := values[keys[i]].(type) {
		case string:
			if v != "" {
				answers[q.Question] = v
			}
		case []any:
			parts := make([]string, 0, len(v))
			for _, e := range v {
				if s, ok := e.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				answers[q.Question] = strings.Join(parts, ", ")
			}
		}
	}
	return allow(answers)
}

func (s *session) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "message":
		var p struct {
			Message map[string]json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		s.handleSDKMessage(p.Message)

	case "fatal":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(params, &p)
		// "session ended" is the bridge finishing normally, not a failure.
		if p.Message != "session ended" {
			s.host.Logf("claude bridge fatal: %s", p.Message)
			s.mu.Lock()
			s.fatal = p.Message
			s.mu.Unlock()
		}
	}
}

// handleSDKMessage maps one Agent SDK message onto canonical events. This is
// the only mapping in the system, and it is the reason the sidecar stays dumb.
func (s *session) handleSDKMessage(msg map[string]json.RawMessage) {
	s.trackSessionID(msg)
	switch str(msg["type"]) {
	case "system":
		s.handleSystem(msg)
	case "stream_event":
		s.handleStreamEvent(msg)
	case "assistant":
		s.handleAssistant(msg)
	case "user":
		s.handleUser(msg)
	case "result":
		// A result carrying a parent_tool_use_id is a subagent finishing, not
		// the conversation. Letting it through would close the top-level turn
		// — and report "user's turn" — while the main agent is still working.
		if str(msg["parent_tool_use_id"]) == "" {
			s.handleResult(msg)
		}
	case "context_usage":
		s.handleContextUsage(msg)
	}
}

// trackSessionID follows the harness's own conversation id, which omniplex names at
// start but does not control afterwards: Claude Code rotates it in place when
// the user runs /clear, silently starting a new conversation under a new id in
// the same process. Every SDK message carries the id of the conversation it
// belongs to, so a rotation shows up on the first message after it. It is
// re-emitted as session.config_changed, which the projection folds into
// HarnessSessionID — the id the next resume passes back. Missing this is how a
// /clear'd session used to resurrect its cleared conversation after a server
// restart, answering from history the user had discarded and knowing nothing
// of the turns since the clear.
func (s *session) trackSessionID(msg map[string]json.RawMessage) {
	// Subagent traffic carries the parent Task's id, not the conversation's
	// identity; only top-level messages speak for the session.
	if str(msg["parent_tool_use_id"]) != "" {
		return
	}
	id := str(msg["session_id"])
	if id == "" {
		return
	}
	s.mu.Lock()
	changed := id != s.harnessSessionID
	if changed {
		s.harnessSessionID = id
	}
	s.mu.Unlock()
	if changed {
		s.emit(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{
			HarnessSessionID: id,
		}))
	}
}

// contextWindowFor is the fallback window used only when the harness cannot
// report context usage directly (an older CLI without the control method): the
// standard 200k unless the model is one of the 1M ones. The Opus 5 generation
// is 1M whether or not its id carries the "[1m]" tag (the harness reports the
// bare "claude-opus-5" mid-session), and older Opus ids run at 1M too, so the
// family name is recognised as well as the tag. When getContextUsage is
// available it supplies the real window and this is unused.
func contextWindowFor(model string) int64 {
	if strings.Contains(model, "[1m]") || strings.Contains(model, "opus") {
		return 1_000_000
	}
	return 200_000
}

func (s *session) handleSystem(msg map[string]json.RawMessage) {
	switch str(msg["subtype"]) {
	case "commands_changed":
		if host, ok := s.host.(adapter.ComposerCatalogueInvalidator); ok {
			host.ComposerCatalogueChanged()
		}
	case "init":
		var init struct {
			Model          string `json:"model"`
			PermissionMode string `json:"permissionMode"`
		}
		remarshal(msg, &init)
		s.mu.Lock()
		s.model = init.Model
		harnessID := s.harnessSessionID
		s.mu.Unlock()
		s.emit(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{
			Model: init.Model, Mode: init.PermissionMode, HarnessSessionID: harnessID,
		}))
	case "compact_boundary":
		var b struct {
			Meta struct {
				Trigger    string `json:"trigger"`
				PreTokens  int64  `json:"pre_tokens"`
				PostTokens int64  `json:"post_tokens"`
			} `json:"compact_metadata"`
		}
		remarshal(msg, &b)
		s.emit(proto.Emit(proto.ContextCompacted, proto.ContextCompactedPayload{
			Trigger:    b.Meta.Trigger,
			PreTokens:  b.Meta.PreTokens,
			PostTokens: b.Meta.PostTokens,
		}))
	}
}

// handleContextUsage folds the harness's own occupancy report — the structured
// twin of the /context command — into the usage payload. This is the correct
// source for "how full is the window": tokens in use now, measured against the
// window (the resolved auto-compaction window), with the category breakdown.
func (s *session) handleContextUsage(msg map[string]json.RawMessage) {
	var m struct {
		Usage struct {
			Model                string  `json:"model"`
			TotalTokens          int64   `json:"totalTokens"`
			MaxTokens            int64   `json:"maxTokens"`
			RawMaxTokens         int64   `json:"rawMaxTokens"`
			Percentage           float64 `json:"percentage"`
			IsAutoCompactEnabled bool    `json:"isAutoCompactEnabled"`
			AutoCompactThreshold int64   `json:"autoCompactThreshold"`
			Categories           []struct {
				Name       string `json:"name"`
				Tokens     int64  `json:"tokens"`
				IsDeferred bool   `json:"isDeferred"`
			} `json:"categories"`
		} `json:"usage"`
	}
	remarshal(msg, &m)
	u := m.Usage

	// The window occupancy is measured against. Prefer rawMaxTokens (the
	// auto-compaction window, which is what actually bounds the conversation);
	// fall back to maxTokens, then the model heuristic.
	window := u.RawMaxTokens
	if window == 0 {
		window = u.MaxTokens
	}
	if window == 0 {
		s.mu.Lock()
		window = contextWindowFor(s.model)
		s.mu.Unlock()
	}

	var cats []proto.ContextCategory
	for _, c := range u.Categories {
		// Deferred rows are out-of-window tool schemas, and the free/buffer
		// rows are the empty remainder — none of them occupy the window, so
		// they do not belong in a segmented bar of what is used.
		if c.IsDeferred || c.Tokens <= 0 || isFreeCategory(c.Name) {
			continue
		}
		cats = append(cats, proto.ContextCategory{Name: c.Name, Tokens: c.Tokens})
	}

	pct := u.Percentage
	if window > 0 {
		pct = float64(u.TotalTokens) / float64(window) * 100
	}

	s.mu.Lock()
	s.usage.ContextUsed = u.TotalTokens
	s.usage.ContextWindow = window
	s.usage.ContextLimit = u.MaxTokens
	s.usage.ContextPct = pct
	s.usage.ContextCategories = cats
	s.usage.AutoCompact = u.IsAutoCompactEnabled
	s.usage.AutoCompactThreshold = u.AutoCompactThreshold
	out := s.usage
	s.mu.Unlock()

	s.emit(proto.Emit(proto.UsageUpdated, out))
}

// isFreeCategory recognises the breakdown rows that are not occupied context —
// the empty remainder and the compaction reserve — by the labels the CLI uses.
func isFreeCategory(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "free") || strings.Contains(n, "reserved") ||
		strings.Contains(n, "buffer") || strings.Contains(n, "available")
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID string `json:"id"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
	} `json:"delta"`
}

func (s *session) handleStreamEvent(msg map[string]json.RawMessage) {
	var wrapper struct {
		Event streamEvent `json:"event"`
	}
	remarshal(msg, &wrapper)
	ev := wrapper.Event
	// Non-empty when this stream belongs to a subagent: the id of the Task
	// call that spawned it, carried on every SDK message the subagent emits.
	parent := str(msg["parent_tool_use_id"])

	switch ev.Type {
	case "message_start":
		s.mu.Lock()
		s.streams[parent] = &stream{messageID: ev.Message.ID, blocks: map[int]*block{}}
		s.mu.Unlock()

	case "content_block_start":
		// Output is starting. If no turn is open — the harness resumed work
		// by itself, without a prompt — this opens one, so the events below
		// never carry an empty turn id.
		turn := s.ensureTurn()
		s.mu.Lock()
		st := s.streams[parent]
		if st == nil {
			st = &stream{blocks: map[int]*block{}}
			s.streams[parent] = st
		}
		b := &block{kind: ev.ContentBlock.Type}
		switch ev.ContentBlock.Type {
		case "text", "thinking":
			b.blockID = fmt.Sprintf("%s:%d", st.messageID, ev.Index)
		case "tool_use":
			b.toolID, b.name = ev.ContentBlock.ID, ev.ContentBlock.Name
		}
		st.blocks[ev.Index] = b
		s.mu.Unlock()

		if ev.ContentBlock.Type == "tool_use" {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID:           turn,
				ToolCallID:       ev.ContentBlock.ID,
				Kind:             toolKind(ev.ContentBlock.Name),
				Title:            ev.ContentBlock.Name,
				Status:           proto.StatusPending,
				ParentToolCallID: parent,
			}))
		}

	case "content_block_delta":
		s.mu.Lock()
		var b *block
		if st := s.streams[parent]; st != nil {
			b = st.blocks[ev.Index]
		}
		s.mu.Unlock()
		if b == nil {
			return
		}
		turn := s.ensureTurn()
		switch ev.Delta.Type {
		case "text_delta":
			s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
				TurnID: turn, Role: "agent", Kind: "text", BlockID: b.blockID, Delta: ev.Delta.Text,
				ParentToolCallID: parent,
			}))
		case "thinking_delta":
			s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
				TurnID: turn, Role: "agent", Kind: "thought", BlockID: b.blockID, Delta: ev.Delta.Thinking,
				ParentToolCallID: parent,
			}))
		}
	}
}

func (s *session) handleAssistant(msg map[string]json.RawMessage) {
	var m struct {
		Message struct {
			ID      string `json:"id"`
			Content []struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
			Usage struct {
				InputTokens              int64 `json:"input_tokens"`
				CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
				CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	remarshal(msg, &m)

	// The prompt of the latest request is the conversation so far, so its
	// input (fresh + cached) is the context in use — the occupancy fallback
	// when the harness cannot report it directly. Output is excluded on
	// purpose: this request's output is the next request's input.
	if prompt := m.Message.Usage.InputTokens + m.Message.Usage.CacheReadInputTokens + m.Message.Usage.CacheCreationInputTokens; prompt > 0 {
		s.mu.Lock()
		s.lastPromptTokens = prompt
		s.mu.Unlock()
	}

	parent := str(msg["parent_tool_use_id"])
	// Text normally arrives as stream deltas and this whole message is a
	// recap. Not always: a built-in command answered by the harness itself —
	// /usage, /context, /cost — is a synthetic message with no stream events
	// at all, so its text exists nowhere but here. Anything not streamed is
	// emitted now, otherwise the command runs and the transcript shows nothing.
	s.mu.Lock()
	st := s.streams[parent]
	streamed := st != nil && st.messageID == m.Message.ID
	s.mu.Unlock()
	for i, c := range m.Message.Content {
		if c.Type == "text" && !streamed && c.Text != "" {
			s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
				TurnID: s.ensureTurn(), Role: "agent", Kind: "text",
				BlockID: fmt.Sprintf("%s:%d", m.Message.ID, i), Delta: c.Text,
				ParentToolCallID: parent,
			}))
			continue
		}
		if c.Type != "tool_use" {
			continue
		}
		// A tool going active is a turn running. Resumed work does not always
		// lead with streaming text (the only other place a harness-initiated
		// turn is opened), so open one here too — otherwise the session reads
		// idle while the harness is invoking tools.
		s.ensureTurn()
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID:       c.ID,
			Status:           proto.StatusInProgress,
			Title:            toolTitle(c.Name, c.Input),
			RawInput:         c.Input,
			ParentToolCallID: parent,
		}))
	}
}

func (s *session) handleUser(msg map[string]json.RawMessage) {
	var m struct {
		Message struct {
			Content []struct {
				Type      string          `json:"type"`
				ToolUseID string          `json:"tool_use_id"`
				IsError   bool            `json:"is_error"`
				Content   json.RawMessage `json:"content"`
			} `json:"content"`
		} `json:"message"`
	}
	remarshal(msg, &m)

	for _, c := range m.Message.Content {
		if c.Type != "tool_result" {
			continue
		}
		status := proto.StatusCompleted
		if c.IsError {
			status = proto.StatusFailed
		}
		var content []proto.ToolContent
		if text := flattenContent(c.Content); text != "" {
			content = []proto.ToolContent{{Type: "text", Text: text}}
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: c.ToolUseID, Status: status, Content: content,
			ParentToolCallID: str(msg["parent_tool_use_id"]),
		}))
	}
}

func (s *session) handleResult(msg map[string]json.RawMessage) {
	var r struct {
		IsError    bool   `json:"is_error"`
		StopReason string `json:"stop_reason"`
		// Result is the harness's own last word on the turn. On a failure it
		// is the whole explanation — "Not logged in · Please run /login" is a
		// result, not a crash — and dropping it left the turn saying only that
		// something went wrong.
		Result string `json:"result"`
		// TerminalReason distinguishes a turn that failed talking to the API
		// from one that failed on its own terms.
		TerminalReason string `json:"terminal_reason"`
		// Errors is where the SDK's error-shaped results carry their
		// diagnostics: that variant has no result field at all, so a turn that
		// failed before it could start — an expired login among them — says so
		// only here.
		Errors       []string `json:"errors"`
		TotalCostUSD float64  `json:"total_cost_usd"`
		Usage        struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	remarshal(msg, &r)

	// The result's usage is a cost-accounting total summed over every request
	// in the turn, so it is right for cost and token accounting but wrong for
	// occupancy — summing prompts that each already contain the whole
	// conversation overcounts by roughly O(N²) in tool calls. Occupancy comes
	// from the harness's context_usage report (handleContextUsage); until that
	// arrives — or on a CLI too old to send it — we fall back to the size of
	// the final request's prompt, which is the actual context in use.
	s.mu.Lock()
	s.usage.Input = r.Usage.InputTokens
	s.usage.Output = r.Usage.OutputTokens
	s.usage.CacheRead = r.Usage.CacheReadInputTokens
	s.usage.CacheWrite = r.Usage.CacheCreationInputTokens
	s.usage.Cost = r.TotalCostUSD
	// Refresh the occupancy fallback every turn, not just until the first
	// context_usage report: that report is emitted right after this and
	// overrides these values, but if it ever fails to arrive (an older CLI, a
	// dropped control request) the meter must still track the latest turn
	// rather than freeze on a stale reading. Reuse the last window the harness
	// reported so occupancy does not swing back to the heuristic each turn.
	used := s.lastPromptTokens
	window := s.usage.ContextWindow
	if window == 0 {
		window = contextWindowFor(s.model)
	}
	s.usage.ContextUsed = used
	s.usage.ContextWindow = window
	if used > 0 && window > 0 {
		s.usage.ContextPct = float64(used) / float64(window) * 100
	} else {
		s.usage.ContextPct = 0
	}
	out := s.usage
	s.mu.Unlock()

	s.emit(proto.Emit(proto.UsageUpdated, out))

	stop := r.StopReason
	switch {
	case r.IsError:
		stop = proto.StopError
	case stop == "":
		stop = proto.StopEndTurn
	}

	// A failed turn must say why. This is the path a login failure actually
	// takes — the harness does not crash, it answers with an error result —
	// and an error with no message left the only recourse a "continue where
	// it left off" button, whose prompt announces a server restart that never
	// happened.
	var failure, failureKind string
	if stop == proto.StopError {
		failure, failureKind = resultFailure(r.Result, strings.Join(r.Errors, "\n"), r.TerminalReason)
	}

	s.mu.Lock()
	turn := s.turnID
	s.sawResult = true
	s.turnID = ""
	s.mu.Unlock()

	// A result for a turn that was never started — no prompt, and no output
	// that would have opened one — has nothing to finish. Emitting it anyway
	// would put an unmatched turn.finished in the log.
	if turn == "" {
		return
	}

	s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: turn, StopReason: stop, Error: failure, Failure: failureKind,
	}))
}

// resultFailure turns the harness's last word on a failed turn into something
// worth showing. An authentication failure gets the instruction that fixes it;
// anything else is passed through as the harness phrased it.
func resultFailure(result, diagnostics, terminalReason string) (message, failure string) {
	for _, said := range []string{result, diagnostics} {
		if msg, ok := loginRequired(said); ok {
			return msg, proto.FailureAuth
		}
	}
	for _, said := range []string{result, diagnostics} {
		if text := briefly(said); text != "" {
			return text, ""
		}
	}
	if terminalReason != "" {
		return "the turn failed: " + terminalReason, ""
	}
	return "the turn failed and the harness did not say why", ""
}

// ---- helpers ----

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func remarshal(msg map[string]json.RawMessage, out any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, out)
}

func toolKind(name string) string {
	switch name {
	case "Read", "NotebookRead":
		return proto.KindRead
	case "Edit", "Write", "NotebookEdit", "MultiEdit":
		return proto.KindEdit
	case "Bash", "BashOutput", "KillShell":
		return proto.KindExecute
	case "Grep", "Glob", "Search":
		return proto.KindSearch
	case "WebFetch", "WebSearch":
		return proto.KindFetch
	case "Task", "Agent":
		return proto.KindThink
	default:
		return proto.KindOther
	}
}

func toolTitle(name string, input json.RawMessage) string {
	var in map[string]any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch name {
	case "Bash":
		if c := pick("command"); c != "" {
			return firstLine(c)
		}
	case "Read", "Write", "Edit", "NotebookEdit":
		if p := pick("file_path", "path", "notebook_path"); p != "" {
			return name + " " + short(p)
		}
	case "Grep", "Glob":
		if p := pick("pattern"); p != "" {
			return name + " " + p
		}
	case "WebFetch", "WebSearch":
		if u := pick("url", "query"); u != "" {
			return name + " " + u
		}
	case "Task":
		if d := pick("description"); d != "" {
			return "Task: " + d
		}
	}
	return name
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// briefly reduces a thrown stack to something a one-line error can carry: the
// message, without the frames under it.
func briefly(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func short(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return string(raw)
}
