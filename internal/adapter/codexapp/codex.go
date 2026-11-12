// Package codexapp adapts `codex app-server` (JSON-RPC over stdio) to the
// canonical event model.
//
// Flow: initialize → initialized → thread/start → turn/start, with streaming
// item/* notifications and server→client approval requests. Verified against
// codex-cli 0.147.0.
package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/procgroup"
	"github.com/asiraky/omniplex/internal/proto"
)

// Adapter creates codex sessions.
type Adapter struct{ Bin string }

func New(bin string) *Adapter {
	if bin == "" {
		bin = "codex"
	}
	return &Adapter{Bin: bin}
}

func (a *Adapter) ID() string { return "codex" }

// ConfigFields declares how an instance of Codex is configured. CODEX_HOME is
// the whole mechanism: it holds the login, so pointing a second instance at
// its own directory is what makes it a second account. The user's
// configuration in it is shared from ~/.codex (see SharedEntries).
func (a *Adapter) ConfigFields() []adapter.ConfigField {
	return []adapter.ConfigField{
		{
			Env:         "CODEX_HOME",
			Label:       "Codex home",
			Description: "Directory holding this account's Codex login and conversations. config.toml, AGENTS.md, skills, agents, rules and plugins are shared from ~/.codex. Leave empty on the default instance to use the machine's own login.",
			Placeholder: "~/.codex",
			Kind:        adapter.FieldPath,
			Isolates:    true,
		},
	}
}

func (a *Adapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{
		ID:      "codex",
		Name:    "Codex",
		Accent:  "oklch(0.76 0.13 165)",
		DocsURL: "https://developers.openai.com/codex",
	}
}

// Probe reports whether a Codex session could start right now. Codex needs
// only its own CLI: it exposes app-server as a documented programmatic
// interface, so there is no sidecar and no runtime to find. The instance's
// env overlay is applied to the probe run so a per-account CODEX_HOME is
// checked, not the ambient one.
func (a *Adapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	path, err := exec.LookPath(a.Bin)
	if err != nil {
		return adapter.Unavailable(
			"The Codex CLI was not found on this machine.",
			adapter.Remedy{Text: "Install Codex", URL: "https://developers.openai.com/codex"},
			adapter.Remedy{Text: "Or install it with npm", Command: "npm i -g @openai/codex"},
		)
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	probe := exec.CommandContext(probeCtx, path, "--version")
	probe.Env = adapter.MergeEnv(os.Environ(), env)
	out, err := probe.Output()
	if err != nil {
		return adapter.Unavailable(
			"The Codex CLI was found at "+path+" but did not run.",
			adapter.Remedy{Text: "Check the install with: codex doctor", Command: "codex doctor"},
		)
	}

	// Installed is not usable: a signed-out Codex fails every session at
	// start. `codex login status` is the only authority on that, and an older
	// CLI that cannot answer claims nothing either way.
	if signedOut(ctx, path, env) {
		return adapter.Unavailable(
			"Codex is not signed in.",
			adapter.Remedy{Text: "Sign in", Command: "codex login", Action: adapter.RemedyLogin},
		)
	}

	return adapter.Ready(map[string]string{
		"codex":    path,
		"codexVer": strings.TrimSpace(string(out)),
	})
}

// signedOut reports whether `codex login status` positively says nobody is
// signed in. Any other answer, including a failure to run it, is not taken as
// signed out: the probe must not block a session on a question the CLI could
// not answer.
func signedOut(ctx context.Context, path string, env map[string]string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "login", "status")
	cmd.Env = adapter.MergeEnv(os.Environ(), env)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "not logged in")
}

// LoginCommand starts Codex's own sign-in flow, which opens the browser and
// waits for the callback on this machine.
func (a *Adapter) LoginCommand(ctx context.Context, overlay map[string]string) ([]string, []string, error) {
	path, err := exec.LookPath(a.Bin)
	if err != nil {
		return nil, nil, fmt.Errorf("the Codex CLI was not found on this machine")
	}
	return []string{path, "login"}, adapter.MergeEnv(os.Environ(), overlay), nil
}

// PermissionModes are omniplex's presets over Codex's two orthogonal axes (approval
// policy × sandbox) plus its reviewer selector. The ids are adapter-internal;
// modeSettings maps them onto the app-server protocol. Codex has no single
// "permission mode" — pretending it does would delete the modes that are the
// point, so each preset pins every axis explicitly.
func (a *Adapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{
		{ID: "untrusted", Label: "Manual", Description: "Ask before all but trusted read-only commands", Level: adapter.LevelAsk},
		{ID: "read-only", Label: "Plan", Description: "Read and analyze only; ask to go further"},
		{ID: "on-request", Label: "Ask when needed", Description: "Write in the workspace; the model asks when it needs more", Default: true, Level: adapter.LevelEdits},
		{ID: "auto-review", Label: "Auto", Description: "A reviewer subagent approves or denies escalations"},
		{ID: "sandboxed-auto", Label: "No prompts (sandboxed)", Description: "Never ask; the sandbox contains the damage"},
		{ID: "full-access", Label: "Bypass", Description: "Never ask and no sandbox", Level: adapter.LevelAll},
	}
}

// modeSettings resolves a preset id to the protocol values. The zero id is
// today's default, so behaviour is unchanged when no mode is chosen.
type modeSettings struct {
	approvalPolicy string
	sandbox        string         // flat SandboxMode string, for thread/start
	sandboxPolicy  map[string]any // rich SandboxPolicy object, for thread/settings/update
	reviewer       string         // ApprovalsReviewer
}

func settingsFor(mode string, writableRoots []string) (modeSettings, error) {
	policy := func(approval, sandbox string, sandboxPolicy map[string]any, reviewer string) modeSettings {
		return modeSettings{approvalPolicy: approval, sandbox: sandbox, sandboxPolicy: sandboxPolicy, reviewer: reviewer}
	}
	readOnly := map[string]any{"type": "readOnly", "networkAccess": false}
	workspaceWrite := map[string]any{"type": "workspaceWrite", "networkAccess": false}
	if len(writableRoots) > 0 {
		workspaceWrite["writableRoots"] = writableRoots
	}
	fullAccess := map[string]any{"type": "dangerFullAccess"}

	switch mode {
	case "untrusted":
		return policy("untrusted", "read-only", readOnly, "user"), nil
	case "read-only":
		return policy("on-request", "read-only", readOnly, "user"), nil
	case "", "on-request":
		// on-request means the server asks us before anything outside the
		// sandbox, which is the whole point of routing permissions to a human.
		return policy("on-request", "workspace-write", workspaceWrite, "user"), nil
	case "auto-review":
		return policy("on-request", "workspace-write", workspaceWrite, "auto_review"), nil
	case "sandboxed-auto":
		return policy("never", "workspace-write", workspaceWrite, "user"), nil
	case "full-access":
		return policy("never", "danger-full-access", fullAccess, "user"), nil
	default:
		return modeSettings{}, fmt.Errorf("codex does not have a %q permission mode", mode)
	}
}

// trustArgs marks the session's own directory trusted for this app-server run.
//
// Codex disables a repository's project-local `.codex` — its agent
// definitions, hooks and exec policies — until the folder is trusted, and
// answers with nothing but a line on stderr. The CLI can prompt a human and
// write the answer to config.toml; app-server cannot, so an untrusted
// directory silently loses everything the repo configured and the model runs
// with a stripped-down toolkit it was never told about. A managed worktree is
// a brand-new path every session, which is exactly the case the CLI's
// remembered answer does not cover.
//
// Adding a directory to omniplex and starting a session in it is the consent
// codex is asking for, so the grant is made here — per run, via -c, never
// written to the user's config.toml. Trust is inherited by subpaths, so
// naming the cwd covers a worktree beneath it without enumerating them.
func trustArgs(cwd string) []string {
	if cwd == "" {
		return nil
	}
	// The path is quoted as a TOML basic string: a repo path may contain
	// characters that a bare dotted key cannot carry.
	return []string{"-c", fmt.Sprintf("projects.%s.trust_level=%q", strconv.Quote(cwd), "trusted")}
}

// sessionArgs turns on what a session needs that codex leaves off.
//
// update_plan has been opt-in since codex 0.152; without it the model never
// plans, and turn/plan/updated — recorded as plan.updated — never arrives.
//
// Codex waits one second for optional MCP servers before the first turn and
// runs it without any still starting. An npx or uvx server routinely takes
// longer, so it would be missing from the turn the user just sent. Ten seconds
// covers them; a server that is ready sooner ends the wait sooner.
func sessionArgs() []string {
	return []string{
		"-c", "tools.update_plan.enabled=true",
		"-c", "mcp_optional_startup_grace_ms=10000",
	}
}

// writableRootsArgs lets a workspace-write sandbox write to folders outside
// the cwd: the project's home folder, when the session works in a repo.
func writableRootsArgs(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = strconv.Quote(d)
	}
	return []string{"-c", "sandbox_workspace_write.writable_roots=[" + strings.Join(quoted, ",") + "]"}
}

func (a *Adapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	// Resolve the mode before spawning anything: an unknown id should fail
	// legibly, not leave an orphaned app-server.
	mode, err := settingsFor(o.Mode, o.ExtraDirs)
	if err != nil {
		return nil, err
	}

	args := append([]string{"app-server"}, sessionArgs()...)
	args = append(args, trustArgs(o.Cwd)...)
	var native []string
	if len(o.MCPServers) > 0 {
		all, err := nativeServers(o.Env)
		if err != nil {
			return nil, err
		}
		// The cwd is trusted, so codex also loads the repo's own
		// .codex/config.toml layers, and a server there shadows like one in
		// the user's config.
		project, err := projectServers(o.Env, o.Cwd)
		if err != nil {
			return nil, err
		}
		if all == nil {
			all = map[string]codexServerTOML{}
		}
		for name, s := range project {
			all[name] = s
		}
		native = sortedNames(all)
	}
	mcp, secrets, refused, mcpKeys := mcpConfig(o.MCPServers, native)
	for _, r := range refused {
		host.Logf("codex: MCP server left out: %s", r)
	}
	args = append(args, mcp...)
	args = append(args, writableRootsArgs(o.ExtraDirs)...)
	cmd := exec.Command(a.Bin, args...)
	cmd.Dir = o.Cwd
	// The instance's overlay over the ambient environment is the entire
	// credential mechanism: a per-account CODEX_HOME isolates config and login.
	// MCP header and env values ride along under their generated names.
	overlay := make(map[string]string, len(o.Env)+len(secrets))
	for k, v := range o.Env {
		overlay[k] = v
	}
	for k, v := range secrets {
		overlay[k] = v
	}
	cmd.Env = adapter.MergeEnv(os.Environ(), overlay)

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
	// The whole tree, not just app-server: shells, dev servers and browsers
	// the agent starts must end with the session.
	tree := procgroup.Attach(cmd, "codex-"+o.ThreadID)
	if err := cmd.Start(); err != nil {
		tree.Kill()
		return nil, fmt.Errorf("start %s app-server: %w", a.Bin, err)
	}

	mcpOurs := map[string]bool{}
	for _, m := range o.MCPServers {
		mcpOurs[m.Name] = true
	}
	s := &session{
		mcpKeys:   mcpKeys,
		mcpOurs:   mcpOurs,
		host:      host,
		cmd:       cmd,
		tree:      tree,
		cwd:       o.Cwd,
		events:    make(chan proto.Emission, 256),
		streamed:  map[string]bool{},
		done:      make(chan struct{}),
		effort:    o.Effort,
		extraDirs: o.ExtraDirs,
	}
	s.conn = jsonrpc.NewConn(stdout, stdin, s.handleRequest, s.handleNotification)

	go s.drainStderr(stderr)
	go s.watchExit()

	var initRes struct {
		CodexHome string `json:"codexHome"`
	}
	if err := s.conn.Call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "omniplex", "version": "0.1.0"},
		"capabilities": map[string]any{},
	}, &initRes); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	if err := s.conn.Notify("initialized", map[string]any{}); err != nil {
		_ = s.Close()
		return nil, err
	}
	// Codex does not look for skills in its writable roots. The list is
	// process-wide, which is this session's alone: every session runs its own
	// app-server. A codex too old for the method runs without them.
	// A plugin's skills come the same way: codex has no session plugins.
	extraRoots := append([]string{}, o.SkillDirs...)
	for _, p := range o.Plugins {
		extraRoots = append(extraRoots, adapter.PluginSkills(p))
	}
	if len(extraRoots) > 0 {
		if err := s.conn.Call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": extraRoots}, nil); err != nil {
			host.Logf("codex: project skills left out: %v", err)
		}
	}

	startParams := map[string]any{
		"cwd":            o.Cwd,
		"approvalPolicy": mode.approvalPolicy,
		"sandbox":        mode.sandbox,
	}
	if mode.reviewer != "user" {
		startParams["approvalsReviewer"] = mode.reviewer
	}
	if o.Model != "" {
		startParams["model"] = o.Model
	}
	// Developer instructions sit beside Codex's own base prompt;
	// baseInstructions would replace it.
	if o.Instructions != "" {
		startParams["developerInstructions"] = o.Instructions
	}

	method := "thread/start"
	if o.HarnessSessionID != "" {
		// Continue the existing thread so a server restart does not lose the
		// agent's context.
		method = "thread/resume"
		startParams["threadId"] = o.HarnessSessionID
	}

	var startRes struct {
		Thread struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"thread"`
	}
	if err := s.conn.Call(ctx, method, startParams, &startRes); err != nil {
		if o.HarnessSessionID == "" {
			_ = s.Close()
			return nil, fmt.Errorf("codex %s: %w", method, err)
		}
		// The thread is gone (archived, or a different codex home). Fall back
		// to a fresh one rather than leaving the session unusable.
		host.Logf("codex thread/resume failed, starting a new thread: %v", err)
		delete(startParams, "threadId")
		if err := s.conn.Call(ctx, "thread/start", startParams, &startRes); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("codex thread/start: %w", err)
		}
	}
	s.threadID = startRes.Thread.ID
	// The thread's resolved model is recorded even when the caller named none:
	// it is what the account-usage aggregation prices against, and a session
	// that never said must not report a blank forever. The CLI's answer is
	// authoritative for the process, so it wins over the request's own guess.
	if startRes.Thread.Model != "" {
		s.mu.Lock()
		s.model = startRes.Thread.Model
		s.mu.Unlock()
	}
	s.emit(proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{
		HarnessSessionID: s.threadID,
		Model:            s.model,
	}))

	// The account's usage limits are readable the moment the app-server is
	// up, and the Limits page is most wanted right after a session starts —
	// before any turn has run. Off the hot path: a slow read must not delay
	// the first prompt.
	go s.pushQuota()

	return s, nil
}

type session struct {
	host adapter.HostServices
	cmd  *exec.Cmd
	tree procgroup.Group
	conn *jsonrpc.Conn
	cwd  string
	// extraDirs stay writable across a permission change.
	extraDirs []string

	threadID string
	effort   string
	model    string

	// mcpKeys maps each config key omniplex gave an MCP server back to its
	// name; mcpOurs holds those names.
	mcpKeys map[string]string
	mcpOurs map[string]bool

	events chan proto.Emission
	done   chan struct{}
	closed sync.Once
	// emitMu orders every send on events before the close in watchExit: the
	// sends come from other goroutines (CreateSession's, the quota read's),
	// and a close concurrent with a send is a race even when it loses it.
	emitMu       sync.RWMutex
	eventsClosed bool

	mu sync.Mutex
	// turnID is omniplex's own turn id, echoed onto emitted events. serverTurnID is
	// codex's id for the in-flight turn — turn/interrupt needs it, and it is not
	// the same value as turnID.
	turnID       string
	serverTurnID string
	// runningTurnID is the id turn/started gave the open turn, which is the
	// one turn/interrupt accepts. For most turns it is serverTurnID. A review
	// is the exception: review/start answers with one turn, its items and its
	// completion name that one, and codex runs — and will only interrupt — a
	// second, named nowhere but turn/started.
	runningTurnID string
	interrupting  bool            // an interrupt for the open turn is already in flight
	streamed      map[string]bool // item ids that arrived as deltas
	// lastServerTurn is codex's id for the turn that most recently completed.
	// A notification that straggles in after its turn ended still names it,
	// and recording it as the next turn's id would aim the stop button at a
	// finished turn and drop the real turn's completion as stale.
	lastServerTurn string
	// lastTurn is ours for that same turn: a spawn that completes after its
	// turn ended names it by codex's id, and its child works for ours.
	lastTurn string
	// children are the subagent threads codex spawned from this one, or from
	// each other, by thread id; each is a job. See subagents.go. Lazily
	// filled; a session runs few enough subagents that it is never worth
	// pruning, and the same goes for the other per-thread and per-turn maps.
	children map[string]*child
	// pending holds what a thread not yet known to be a child did before the
	// spawn item naming it completed: codex can start, and even finish, the
	// child's turn first, and stopping or settling it needs that turn.
	pendingChildren map[string]*pendingChild
	// unknownThreads is the threads already logged as dropped, so a stray
	// thread's stream logs once rather than per delta.
	unknownThreads map[string]bool
	// stoppedTurns are the turns of ours Stop was pressed on. A child working
	// for one is interrupted with it, and again if it starts a turn after the
	// interrupt went out; and its result never wakes the session, since the
	// human stopped that work.
	stoppedTurns map[string]bool
	// wakeQueue and wakeTimer batch finished children into one wake turn.
	// wakeDelay overrides defaultWakeDelay (tests).
	wakeQueue []string
	wakeTimer *time.Timer
	wakeDelay time.Duration
	// Set while a /compact RPC is awaiting its contextCompaction item, so the
	// canonical notice can distinguish a human request from auto-compaction.
	manualCompact bool
	compaction    compactionCount
	// decodeWarned holds each notification method and decode error already
	// logged, so a drifted codex that repeats a bad shape logs it once.
	decodeWarned map[string]bool
}

// compactionCount tracks the compactions one codex turn has reported, so the
// two signals for the same compaction become one notice. A turn can compact
// more than once, so the turn id alone cannot be the key.
type compactionCount struct {
	turn    string
	items   int // contextCompaction items completed
	notices int // deprecated thread/compacted notifications
	emitted int // notices sent for this turn
}

// compactionSignal is which of codex's two reports of a compaction arrived.
type compactionSignal int

const (
	compactionItem   compactionSignal = iota // a contextCompaction item
	compactionNotice                         // a thread/compacted notification
)

func (s *session) Events() <-chan proto.Emission { return s.events }

func (s *session) Prompt(ctx context.Context, in adapter.PromptInput) error {
	s.mu.Lock()
	// The actor believed the session was idle when it accepted this prompt,
	// but codex may have started work by itself in the meantime — see
	// ensureTurn. Overwriting that turn's id would label the in-flight work
	// with this prompt's turn and leave the open turn unfinished forever.
	// Refuse instead; the caller can retry when idle. Mirrors the claude
	// adapter's guard.
	if s.turnID != "" && s.turnID != in.TurnID {
		s.mu.Unlock()
		return errors.New("the harness resumed work on its own; wait for it to finish")
	}
	s.turnID = in.TurnID
	s.mu.Unlock()

	_, err := s.startTurn(ctx, in.TurnID, in.Text, in.Images)
	return err
}

// startTurn sends turn/start for the turn of ours already claimed as id.
// turn/start returns at once with codex's id for the turn (it does not block
// until the turn finishes); recording it is what lets Cancel address the turn,
// as turn/interrupt requires. A failed call releases the claim instead: no
// turn/completed will ever close a turn that never started, and a claim left
// set would make Prompt's collision guard refuse every later prompt. Either
// happens only if id is still the running turn — the read loop may have
// closed it (a very fast turn) or replaced it meanwhile — and ours reports
// whether it was.
func (s *session) startTurn(ctx context.Context, id, text string, images []proto.PromptImage) (ours bool, err error) {
	// effort and model are both mutable mid-session (SetEffort/SetModel), so
	// read them together under the lock.
	s.mu.Lock()
	params := map[string]any{
		"threadId": s.threadID,
		"input":    promptInput(text, images),
	}
	if s.effort != "" {
		params["effort"] = s.effort
	}
	if s.model != "" {
		params["model"] = s.model
	}
	s.mu.Unlock()

	var res struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	err = s.conn.Call(ctx, "turn/start", params, &res)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnID != id {
		return false, err
	}
	if err != nil {
		s.turnID = ""
		s.serverTurnID, s.runningTurnID = "", ""
	} else {
		s.serverTurnID = res.Turn.ID
	}
	return true, err
}

// Steer hands codex a prompt while a turn is running. turn/start on a busy
// thread does not open a second turn: codex holds the input and folds it into
// the running one after the current command — or, if the model had already
// answered, extends the turn with it. The clientUserMessageId comes back as
// the clientId on the userMessage item codex emits when it reads it, which is
// how handleItem names the queue entry. An interrupt discards anything unread.
func (s *session) Steer(ctx context.Context, in adapter.PromptInput) error {
	if in.QueueID == "" {
		return errors.New("a steered prompt needs a queue id")
	}
	s.mu.Lock()
	running := s.turnID != ""
	s.mu.Unlock()
	if !running {
		return errors.New("no turn is running to steer")
	}
	params := map[string]any{
		"threadId":            s.threadID,
		"clientUserMessageId": in.QueueID,
		"input":               promptInput(in.Text, in.Images),
	}
	var res json.RawMessage
	return s.conn.Call(ctx, "turn/start", params, &res)
}

// promptInput is a turn's input items. Images lead, the way every chat UI
// orders them: codex reads each path itself, so nothing is copied or
// re-encoded here. An image-only message is legal, and sends no empty text
// item. Codex has no input item for a PDF, so a PDF's path goes in the text
// and the agent reads it itself.
func promptInput(text string, attachments []proto.PromptImage) []map[string]any {
	images, documents := adapter.SplitAttachments(attachments)
	text = adapter.WithDocumentPaths(text, documents)
	input := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		input = append(input, map[string]any{"type": "localImage", "path": img.Path})
	}
	if text != "" || len(input) == 0 {
		input = append(input, map[string]any{"type": "text", "text": text})
	}
	return input
}

// Cancel interrupts the in-flight turn. codex's turn/interrupt requires both the
// thread id and the specific turn id; omitting the turn id makes it a no-op,
// which is why the stop button silently did nothing before.
//
// Interrupting this thread does not stop a subagent it spawned: each runs as
// its own codex thread, and would keep working, and spending, with nobody
// reading its output. So every subagent of the stopped turn that has a turn
// running is interrupted alongside it. A subagent's refusal is logged rather
// than returned: the stop the user asked for is this thread's, and that one
// either went out or failed on its own.
//
// A second interrupt for a turn that is already being interrupted is coalesced:
// codex leaves repeated interrupts of the same turn pending indefinitely, so each
// extra press of the stop button would only add a Call that never returns, and a
// caller blocked on it, until ctx gives up.
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	turnID := s.interruptTargetLocked()
	parent := turnID != "" && !s.interrupting
	if parent {
		s.interrupting = true
	}
	if s.turnID != "" {
		s.markStoppedLocked(s.turnID)
	}
	children := s.runningChildrenLocked(s.turnID)
	s.mu.Unlock()

	var wg sync.WaitGroup
	for c, turn := range children {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.interruptChild(ctx, c, turn)
		}()
	}
	defer wg.Wait()

	if !parent {
		return nil
	}
	err := s.conn.Call(ctx, "turn/interrupt", map[string]any{
		"threadId": s.threadID,
		"turnId":   turnID,
	}, nil)
	if err != nil {
		// An interrupt codex refused will never be answered by a
		// turn/completed, so nothing else clears the flag. Leaving it set
		// would coalesce — and so silently swallow — every later press of the
		// stop button for the life of the session.
		s.mu.Lock()
		if s.interruptTargetLocked() == turnID {
			s.interrupting = false
		}
		s.mu.Unlock()
	}
	return err
}

// interruptTargetLocked is codex's id for the open turn as turn/interrupt
// wants it, or "" if none is known. s.mu must be held.
func (s *session) interruptTargetLocked() string {
	if s.runningTurnID != "" {
		return s.runningTurnID
	}
	return s.serverTurnID
}

// lateInterruptTimeout bounds an interrupt sent off the read loop, after the
// Stop that asked for it has returned: nobody else is waiting on it.
const lateInterruptTimeout = 10 * time.Second

// noteRunningTurn records the id turn/started gave turn. Stop pressed before
// it arrived went out under the id the turn was started with, which a review
// refuses; this is the review's to stop, so it is interrupted now.
func (s *session) noteRunningTurn(turn, running string) {
	s.mu.Lock()
	if running == "" || s.turnID != turn || running == s.lastServerTurn {
		s.mu.Unlock()
		return
	}
	missed := s.stoppedTurns[turn] && running != s.interruptTargetLocked()
	s.runningTurnID = running
	s.mu.Unlock()
	if !missed {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lateInterruptTimeout)
		defer cancel()
		if err := s.conn.Call(ctx, "turn/interrupt", map[string]any{"threadId": s.threadID, "turnId": running}, nil); err != nil {
			s.host.Logf("codex: late interrupt of turn %s: %v", running, err)
		}
	}()
}

// markStoppedLocked records turn as stopped. s.mu must be held.
func (s *session) markStoppedLocked(turn string) {
	if s.stoppedTurns == nil {
		s.stoppedTurns = map[string]bool{}
	}
	s.stoppedTurns[turn] = true
}

// runningChildrenLocked claims, for interrupting, every child whose current
// work is owner's and that has a turn running, and returns each with that
// turn. A child already being interrupted is left out, for the same reason
// Cancel coalesces its own: codex never answers a repeated interrupt. s.mu
// must be held.
func (s *session) runningChildrenLocked(owner string) map[*child]string {
	if owner == "" {
		return nil
	}
	var out map[*child]string
	for _, c := range s.children {
		if c.workTurn != owner || c.serverTurn == "" || c.interrupting {
			continue
		}
		c.interrupting = true
		if out == nil {
			out = map[*child]string{}
		}
		out[c] = c.serverTurn
	}
	return out
}

// interruptChild stops one child's running turn, already claimed by setting
// its interrupting flag. On failure the claim is released so a later stop can
// try again, as Cancel does for its own turn.
func (s *session) interruptChild(ctx context.Context, c *child, turn string) error {
	err := s.conn.Call(ctx, "turn/interrupt", map[string]any{
		"threadId": c.jobID,
		"turnId":   turn,
	}, nil)
	if err == nil {
		return nil
	}
	s.host.Logf("codex: interrupting subagent %s turn %s: %v", c.jobID, turn, err)
	s.mu.Lock()
	if c.serverTurn == turn {
		c.interrupting = false
	}
	s.mu.Unlock()
	return err
}

// SetMode switches the permission preset mid-thread. thread/settings/update
// applies "for subsequent turns" natively, so no restart is needed. Every axis
// is sent explicitly — including the reviewer — so switching away from a mode
// resets what that mode had set.
func (s *session) SetMode(ctx context.Context, mode string) error {
	m, err := settingsFor(mode, s.extraDirs)
	if err != nil {
		return err
	}
	return s.conn.Call(ctx, "thread/settings/update", map[string]any{
		"threadId":          s.threadID,
		"approvalPolicy":    m.approvalPolicy,
		"sandboxPolicy":     m.sandboxPolicy,
		"approvalsReviewer": m.reviewer,
	}, nil)
}

// SetModel records the model for subsequent turns. turn/start accepts a
// per-turn model override, which is how the change takes effect without
// restarting the thread.
func (s *session) SetModel(ctx context.Context, model string) error {
	s.mu.Lock()
	s.model = model
	s.mu.Unlock()
	return nil
}

// SetEffort records the reasoning effort for subsequent turns. Like the model,
// turn/start carries a per-turn effort override, so the change takes effect on
// the next prompt without restarting the thread.
func (s *session) SetEffort(ctx context.Context, effort string) error {
	s.mu.Lock()
	s.effort = effort
	s.mu.Unlock()
	return nil
}

func (s *session) Close() error {
	s.closed.Do(func() {
		s.mu.Lock()
		if s.wakeTimer != nil {
			s.wakeTimer.Stop()
			s.wakeTimer = nil
		}
		s.wakeQueue = nil
		s.mu.Unlock()
		close(s.done)
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		s.tree.Kill()
		_ = s.cmd.Wait()
	})
	return nil
}

func (s *session) watchExit() {
	<-s.conn.Done()
	s.mu.Lock()
	turn := s.turnID
	s.turnID = ""
	s.serverTurnID, s.runningTurnID = "", ""
	s.interrupting = false
	s.mu.Unlock()
	if turn != "" {
		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turn, StopReason: proto.StopError, Error: "harness exited",
		}))
	}
	s.emitMu.Lock()
	s.eventsClosed = true
	close(s.events)
	s.emitMu.Unlock()
}

func (s *session) drainStderr(r io.ReadCloser) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			s.host.Logf("codex stderr: %s", line)
		}
	}
}

func (s *session) emit(e proto.Emission) {
	s.emitMu.RLock()
	defer s.emitMu.RUnlock()
	if s.eventsClosed {
		return
	}
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
// none is open. Codex can produce work omniplex never prompted — a queued follow-up,
// an auto-continuation — and that work is a real turn: without a turn.started
// the session list and restart recovery both read the session as idle while
// it is working. Mirrors the claude adapter's ensureTurn.
// serverTurn is the codex-side turn id from the triggering notification, when
// it carries one; recording it is what lets Cancel interrupt a turn omniplex never
// started. Empty is fine — the turn still opens, it just cannot be interrupted.
func (s *session) ensureTurn(serverTurn string) string {
	s.mu.Lock()
	if s.turnID != "" {
		id := s.turnID
		s.backfillServerTurnLocked(id, serverTurn)
		s.mu.Unlock()
		return id
	}
	id := uuid.NewString()
	s.turnID = id
	if serverTurn != s.lastServerTurn {
		s.serverTurnID = serverTurn
	}
	s.mu.Unlock()

	s.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: id}))
	return id
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

// workingTurn is the turn a notification that shows work running belongs to:
// the open one, or a harness-initiated one opened for it (ensureTurn). Either
// way it records codex's id for the turn if nothing has yet. That is the only
// source for a /compact turn — thread/compact/start answers with nothing — and
// a turn whose id is never learned cannot be stopped.
func (s *session) workingTurn(turn, serverTurn string) string {
	if turn == "" {
		return s.ensureTurn(serverTurn)
	}
	s.mu.Lock()
	s.backfillServerTurnLocked(turn, serverTurn)
	s.mu.Unlock()
	return turn
}

// backfillServerTurnLocked records serverTurn as codex's id for turn, if turn
// is still the open one and no id is known yet. An id already recorded is not
// replaced: it came from turn/start or turn/started, which name the turn
// outright, while an item names whichever turn it belongs to — and the turn
// that just completed is refused outright, since a straggler from it is the
// one item that can arrive after the next turn has opened. s.mu must be held.
func (s *session) backfillServerTurnLocked(turn, serverTurn string) {
	if serverTurn == "" || s.turnID != turn || s.serverTurnID != "" || serverTurn == s.lastServerTurn {
		return
	}
	s.serverTurnID = serverTurn
}

// ---- server → client requests (approvals) ----

func (s *session) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "item/commandExecution/requestApproval":
		var p struct {
			ItemID  string `json:"itemId"`
			Command string `json:"command"`
		}
		_ = json.Unmarshal(params, &p)
		return decisionResponse(s.ask(ctx, p.ItemID, "shell", firstLine(p.Command), params)), nil

	case "item/fileChange/requestApproval":
		var p struct {
			ItemID    string `json:"itemId"`
			GrantRoot string `json:"grantRoot"`
		}
		_ = json.Unmarshal(params, &p)
		title := "Apply file changes"
		if p.GrantRoot != "" {
			title = "Apply file changes outside " + p.GrantRoot
		}
		return decisionResponse(s.ask(ctx, p.ItemID, "apply_patch", title, params)), nil

	case "item/permissions/requestApproval":
		var p struct {
			ItemID      string          `json:"itemId"`
			Reason      string          `json:"reason"`
			Permissions json.RawMessage `json:"permissions"`
		}
		_ = json.Unmarshal(params, &p)
		title := "Grant additional permissions"
		if p.Reason != "" {
			title = p.Reason
		}
		return permissionsResponse(s.ask(ctx, p.ItemID, "permissions", title, params), p.Permissions), nil

	case "item/tool/requestUserInput":
		return s.requestUserInput(ctx, params)

	case "mcpServer/elicitation/request":
		return s.requestMCPElicitation(ctx, params)

	default:
		return nil, fmt.Errorf("unsupported request: %s", method)
	}
}

type userInputQuestion struct {
	ID       string `json:"id"`
	Header   string `json:"header"`
	Question string `json:"question"`
	IsSecret bool   `json:"isSecret"`
	Options  []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

func (s *session) requestUserInput(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Questions []userInputQuestion `json:"questions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	properties := map[string]any{}
	required := make([]string, 0, len(p.Questions))
	for _, q := range p.Questions {
		field := map[string]any{"type": "string", "title": q.Question, "description": q.Header}
		if q.IsSecret {
			field["format"] = "password"
		}
		if len(q.Options) > 0 {
			values := make([]string, 0, len(q.Options))
			descriptions := map[string]string{}
			for _, option := range q.Options {
				values = append(values, option.Label)
				descriptions[option.Label] = option.Description
			}
			field["enum"] = values
			field["x-optionDescriptions"] = descriptions
		}
		properties[q.ID] = field
		required = append(required, q.ID)
	}
	schema := jsonOf(map[string]any{"type": "object", "properties": properties, "required": required})
	// Omniplex's turn, not codex's: the projection settles a turn's pending
	// requests by it when the turn ends.
	result, err := s.host.Elicit(ctx, adapter.ElicitationRequest{
		TurnID: s.currentTurn(), Prompt: "Codex needs your input", Schema: schema,
	})
	if err != nil || result.Action != "accept" {
		return map[string]any{"answers": map[string]any{}}, nil
	}
	var values map[string]any
	_ = json.Unmarshal(result.Value, &values)
	answers := map[string]any{}
	for _, q := range p.Questions {
		answers[q.ID] = map[string]any{"answers": stringValues(values[q.ID])}
	}
	return map[string]any{"answers": answers}, nil
}

func (s *session) requestMCPElicitation(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Message         string          `json:"message"`
		Mode            string          `json:"mode"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
		URL             string          `json:"url"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	schema := p.RequestedSchema
	if p.Mode == "url" {
		schema = jsonOf(map[string]any{
			"type": "object", "properties": map[string]any{}, "x-url": p.URL,
		})
	}
	result, err := s.host.Elicit(ctx, adapter.ElicitationRequest{
		TurnID: s.currentTurn(), Prompt: p.Message, Schema: schema,
	})
	if err != nil {
		return map[string]any{"action": "cancel"}, nil
	}
	action := result.Action
	if action != "accept" && action != "decline" {
		action = "cancel"
	}
	response := map[string]any{"action": action}
	if action == "accept" && len(result.Value) > 0 {
		var content any
		if json.Unmarshal(result.Value, &content) == nil {
			response["content"] = content
		}
	}
	return response, nil
}

func stringValues(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	case nil:
		return []string{}
	default:
		return []string{fmt.Sprint(v)}
	}
}

// ask routes an approval to a human via the host and returns the outcome
// (proto.Outcome*). A host error reads as a rejection.
func (s *session) ask(ctx context.Context, itemID, tool, title string, raw json.RawMessage) string {
	outcome, err := s.host.RequestPermission(ctx, adapter.PermissionRequest{
		TurnID:     s.currentTurn(),
		ToolCallID: itemID,
		ToolName:   tool,
		Title:      title,
		RawInput:   raw,
		Options: []proto.PermissionOption{
			{OptionID: "accept", Name: "Allow once", Kind: proto.OutcomeAllowOnce},
			{OptionID: "acceptForSession", Name: "Allow for session", Kind: proto.OutcomeAllowAlways},
			{OptionID: "decline", Name: "Reject", Kind: proto.OutcomeRejectOnce},
		},
	})
	if err != nil {
		return proto.OutcomeRejectOnce
	}
	return outcome.Outcome
}

// decisionResponse maps an outcome onto the {decision} reply that command and
// file change approvals take.
func decisionResponse(outcome string) map[string]any {
	decision := "decline"
	switch outcome {
	case proto.OutcomeAllowOnce:
		decision = "accept"
	case proto.OutcomeAllowAlways:
		decision = "acceptForSession"
	case proto.OutcomeCancelled:
		decision = "cancel"
	}
	return map[string]any{"decision": decision}
}

// permissionsResponse builds a PermissionsRequestApprovalResponse. Codex grants
// exactly the profile we send back, so allowing echoes the requested profile
// and denying (or cancelling, which this reply has no word for) sends an
// empty one.
func permissionsResponse(outcome string, requested json.RawMessage) map[string]any {
	granted, scope := json.RawMessage(`{}`), "turn"
	switch outcome {
	case proto.OutcomeAllowAlways:
		scope = "session"
		fallthrough
	case proto.OutcomeAllowOnce:
		if len(requested) > 0 && string(requested) != "null" {
			granted = requested
		}
	}
	return map[string]any{"permissions": granted, "scope": scope}
}

// ---- server → client notifications ----

// delta is the shape every streaming notification shares.
type delta struct {
	ItemID string `json:"itemId"`
	TurnID string `json:"turnId"`
	Delta  string `json:"delta"`
}

func parseDelta(params json.RawMessage) delta {
	var p delta
	_ = json.Unmarshal(params, &p)
	return p
}

// parent, on these, is the spawn tool call of the subagent the output came
// from (handleItem's owner); empty for this session's own thread.
func (s *session) emitMessageDelta(turn, parent string, p delta) {
	s.mu.Lock()
	s.streamed[p.ItemID] = true
	s.mu.Unlock()
	s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turn, Role: "agent", Kind: "text", BlockID: p.ItemID, Delta: p.Delta,
		ParentToolCallID: parent,
	}))
}

// emitOutputDelta streams what a running command has printed onto its tool
// call. It carries no status, so the actor can merge a burst of them into one
// write and an idle session is not read as working again. The completed item
// still carries the aggregated output as content.
func (s *session) emitOutputDelta(p delta) {
	if p.ItemID == "" || p.Delta == "" {
		return
	}
	s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: p.ItemID, OutputDelta: p.Delta,
	}))
}

func (s *session) emitReasoningDelta(turn, parent string, p delta) {
	s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turn, Role: "agent", Kind: "thought", BlockID: p.ItemID + ":reasoning", Delta: p.Delta,
		ParentToolCallID: parent,
	}))
}

// foreignThread reports whether a notification is about a codex thread other
// than this session's. Codex runs a spawned subagent (the collaboration
// spawn_agent tool) as its own thread and multiplexes that thread's
// notifications over this same connection, tagged with its own threadId.
//
// A notification carrying no threadId is connection-scoped, so it is ours.
func (s *session) foreignThread(params json.RawMessage) string {
	if s.threadID == "" {
		return ""
	}
	var p struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &p)
	if p.ThreadID == s.threadID {
		return ""
	}
	return p.ThreadID
}

func (s *session) handleNotification(method string, params json.RawMessage) {
	// Codex settled a request it asked us — its turn ended, or it was
	// interrupted — and no longer wants an answer. Release the handler still
	// waiting on a human; the host withdraws the prompt. This runs before
	// subagent routing: request ids belong to the connection, not a thread,
	// and a subagent's resolution would otherwise be dropped there.
	// Mirrors t3code@13c7de296c.
	if method == "serverRequest/resolved" {
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(params, &p) == nil && len(p.RequestID) > 0 {
			s.conn.CancelInbound(p.RequestID)
		}
		return
	}

	turn := s.currentTurn()

	if thread := s.foreignThread(params); thread != "" {
		s.handleChildNotification(method, thread, params)
		return
	}

	switch method {
	case "skills/changed":
		if host, ok := s.host.(adapter.ComposerCatalogueInvalidator); ok {
			host.ComposerCatalogueChanged()
		}

	case "account/rateLimits/updated":
		// A sparse live update of the account's allowance — the structured
		// twin of what the CLI's /status prints, pushed as usage moves. It
		// belongs to the account, not the transcript: it goes to the host's
		// quota cache and never becomes a canonical event.
		if snap, ok := parseCodexUpdate(params); ok {
			s.reportQuota(snap)
		}

	case "thread/compacted":
		var p struct {
			TurnID string `json:"turnId"`
		}
		_ = json.Unmarshal(params, &p)
		s.emitContextCompacted(p.TurnID, compactionNotice)

	case "turn/started":
		// Codex names the turn outright here, which is the one place a
		// /compact turn's id arrives before its items do. A turn starting
		// while the log says idle is harness-initiated work, as with an item.
		var p struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		s.noteRunningTurn(s.workingTurn(turn, p.Turn.ID), p.Turn.ID)

	case "item/started", "item/completed":
		// An item starting is work running: open a harness-initiated turn if
		// the log says idle. An item completing is not — a straggling
		// completion after the turn ended must not reopen it, nor lend the
		// open turn its id.
		//
		// Collaboration bookkeeping is not work either: codex posts a
		// subagent's v2 "completed" activity on this thread, tagged with the
		// turn that spawned it, after that turn has ended. No turn/completed
		// follows, so a turn opened for it would read as working forever and
		// hold off the wake the child's result is owed; nor does the turn it
		// names say anything about the one open now. A spawn is work.
		if method == "item/started" {
			var p struct {
				TurnID string    `json:"turnId"`
				Item   codexItem `json:"item"`
			}
			_ = json.Unmarshal(params, &p)
			if !collabBookkeeping(p.Item) {
				turn = s.workingTurn(turn, p.TurnID)
			}
		}
		s.handleItem(method, turn, nil, params)

	case "item/agentMessage/delta":
		p := parseDelta(params)
		// Streaming while idle is a harness-initiated turn; see workingTurn.
		turn = s.workingTurn(turn, p.TurnID)
		s.emitMessageDelta(turn, "", p)

	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		p := parseDelta(params)
		// Streaming while idle is a harness-initiated turn; see workingTurn.
		turn = s.workingTurn(turn, p.TurnID)
		s.emitReasoningDelta(turn, "", p)

	case "item/commandExecution/outputDelta":
		s.emitOutputDelta(parseDelta(params))

	case "command/exec/outputDelta":
		// Output of a command/exec RPC — a client-initiated command, keyed by
		// processId rather than a thread item. Omniplex never sends one, and it
		// has no tool call to stream onto.

	case "turn/plan/updated":
		// item/plan/delta is deliberately absent: it streams the text of a
		// plan-mode item, never a step list, and goes unhandled like any
		// other method this switch does not name.
		s.handlePlan(method, turn, params)

	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Total struct {
					InputTokens           int64 `json:"inputTokens"`
					OutputTokens          int64 `json:"outputTokens"`
					CachedInputTokens     int64 `json:"cachedInputTokens"`
					CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
				} `json:"total"`
				// Last is the most recent request. Its total is the current
				// active context; cached input is already included in input.
				Last struct {
					TotalTokens int64 `json:"totalTokens"`
				} `json:"last"`
				ModelContextWindow int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		_ = json.Unmarshal(params, &p)
		t := p.TokenUsage.Total
		// Codex's input includes the cached and cache-written share; the
		// canonical input is the uncached remainder, as the usage scanner
		// reads it from the rollout. Passing it through counted cached
		// tokens twice wherever input and cache reads are summed.
		input := max(0, t.InputTokens-t.CachedInputTokens-t.CacheWriteInputTokens)
		var pct float64
		window := p.TokenUsage.ModelContextWindow
		used := p.TokenUsage.Last.TotalTokens
		if used > 0 && window > 0 {
			// Unclamped, matching the claude path: an over-limit reading is a
			// real signal, and the meter renders the raw ratio rather than a
			// value pinned at 100.
			pct = float64(used) / float64(window) * 100
		}
		s.emit(proto.Emit(proto.UsageUpdated, proto.UsageUpdatedPayload{
			Input: input, Output: t.OutputTokens,
			CacheRead: t.CachedInputTokens, CacheWrite: t.CacheWriteInputTokens,
			ContextPct: pct, ContextUsed: used, ContextWindow: window,
		}))

	case "turn/completed", "turn/failed":
		var p struct {
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)

		// A completion naming a different codex turn than the one we know is
		// running is stale — a duplicate of an earlier turn's completion. Using
		// s.turnID here would relabel it as the current turn and close a turn
		// that is still working. When either id is unknown the guard stands
		// down: a composer-driven or harness-initiated turn may have no
		// recorded server id, and refusing to close those would leak them.
		s.mu.Lock()
		known := s.serverTurnID
		s.mu.Unlock()
		if p.Turn.ID != "" && known != "" && p.Turn.ID != known {
			return
		}

		stop, errMsg := proto.StopEndTurn, ""
		switch {
		case p.Turn.Error != nil:
			stop, errMsg = proto.StopError, p.Turn.Error.Message
		case p.Turn.Status == "cancelled" || p.Turn.Status == "interrupted":
			stop = proto.StopCancelled
		case p.Turn.Status == "failed":
			stop = proto.StopError
		}

		s.mu.Lock()
		done := s.turnID
		// lastTurn and lastServerTurn name one turn or lastTurn is empty: a
		// completion naming no codex id leaves lastServerTurn the turn before.
		s.lastTurn = done
		if finished := p.Turn.ID; finished != "" {
			s.lastServerTurn = finished
		} else if known != "" {
			s.lastServerTurn = known
		} else {
			s.lastTurn = ""
		}
		s.turnID = ""
		s.serverTurnID, s.runningTurnID = "", ""
		s.interrupting = false
		// A turn that ended cancelled was stopped, whether or not the stop
		// came through Cancel.
		if done != "" && stop == proto.StopCancelled {
			s.markStoppedLocked(done)
		}
		s.mu.Unlock()

		// A completion for a turn that was never started — a compact or review
		// already closed by the composer path, a duplicate turn/completed —
		// has nothing to finish. Emitting a turn.finished with an empty id
		// would take the projection idle while the actor's busy guard holds a
		// different turn, splitting the two forever. Mirrors the claude
		// adapter's guard on a promptless result.
		if done == "" {
			return
		}

		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: done, StopReason: stop, Error: errMsg,
		}))

		// A finished turn is when the allowance moved, so the quota cache
		// re-reads now — the notification the CLI pushes on its own covers the
		// same ground, but a CLI that does not push still lands here.
		go s.pushQuota()

	case "error":
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(params, &p)
		s.host.Logf("codex error: %s", p.Message)
	}
}

// codexItem is the subset of the ThreadItem union this prototype renders.
type codexItem struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Status string `json:"status"`

	// userMessage: the clientUserMessageId the turn/start that carried it
	// was given — the queue id, for a prompt steered into a running turn.
	ClientID string `json:"clientId"`

	// agentMessage
	Text  string `json:"text"`
	Phase string `json:"phase"`

	// reasoning
	Summary []string `json:"summary"`

	// commandExecution
	Command          string `json:"command"`
	Cwd              string `json:"cwd"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`

	// fileChange
	Changes []codexChange `json:"changes"`

	// mcpToolCall; Tool is also collabAgentToolCall's ("spawnAgent", "wait", …)
	Server string         `json:"server"`
	Tool   string         `json:"tool"`
	Result *mcpToolResult `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`

	// webSearch
	Query  string `json:"query"`
	Action *struct {
		Type    string   `json:"type"`
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		URL     string   `json:"url"`
		Pattern string   `json:"pattern"`
	} `json:"action"`

	// collabAgentToolCall
	Prompt            string   `json:"prompt"`
	ReceiverThreadIDs []string `json:"receiverThreadIds"`

	// subAgentActivity
	Kind          string `json:"kind"`
	AgentThreadID string `json:"agentThreadId"`
	AgentPath     string `json:"agentPath"`
}

// mcpToolResult is an MCP CallToolResult as codex relays it.
type mcpToolResult struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
		URI      string `json:"uri"`
		Resource *struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"resource"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
}

// codexChange is one file of a fileChange item. Kind is an object, not a
// string; an update with a move_path renames Path to MovePath.
type codexChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string  `json:"type"` // add | delete | update
		MovePath *string `json:"move_path"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

// op names what the change does to its file: add, delete, update or move.
func (c codexChange) op() string {
	if c.Kind.Type == "update" && c.Kind.MovePath != nil && *c.Kind.MovePath != "" {
		return "move"
	}
	return c.Kind.Type
}

// fileChangeCall renders a patch as one tool row: a title naming every file,
// and a diff per file. A patch that only renames is a move and one that only
// deletes is a delete; anything else is an edit.
func fileChangeCall(changes []codexChange) (kind, title string, content []proto.ToolContent) {
	ops := map[string]bool{}
	names := make([]string, 0, len(changes))
	for _, c := range changes {
		op := c.op()
		ops[op] = true
		if op != "move" {
			names = append(names, c.Path)
			content = append(content, proto.ToolContent{Type: "diff", Path: c.Path, Text: c.Diff})
			continue
		}
		to := *c.Kind.MovePath
		names = append(names, c.Path+" → "+to)
		// Codex marks a rename by appending this line to the diff; the
		// content says it in its own words instead.
		diff := strings.TrimSuffix(c.Diff, "\n\nMoved to: "+to)
		content = append(content, proto.ToolContent{Type: "diff", Path: to, Text: "moved from " + c.Path + "\n" + diff})
	}
	kind, verb := proto.KindEdit, "Edit"
	if len(ops) == 1 {
		switch {
		case ops["move"]:
			kind, verb = proto.KindMove, "Move"
		case ops["delete"]:
			kind, verb = proto.KindDelete, "Delete"
		case ops["add"]:
			verb = "Add"
		}
	}
	return kind, verb + " " + strings.Join(names, ", "), content
}

// decodeFailed logs a notification that would not decode, once per method
// and error. Dropping it silently is how every fileChange item went missing.
func (s *session) decodeFailed(method string, err error) {
	key := method + "\n" + err.Error()
	s.mu.Lock()
	seen := s.decodeWarned[key]
	if !seen {
		if s.decodeWarned == nil {
			s.decodeWarned = map[string]bool{}
		}
		s.decodeWarned[key] = true
	}
	s.mu.Unlock()
	if !seen {
		s.host.Logf("codex: dropped %s that failed to decode: %v", method, err)
	}
}

// toolTitle is the one-line title of a tool-like item; empty for the rest.
func toolTitle(it codexItem) string {
	switch it.Type {
	case "commandExecution":
		return firstLine(it.Command)
	case "fileChange":
		_, title, _ := fileChangeCall(it.Changes)
		return title
	case "mcpToolCall":
		return it.Server + "/" + it.Tool
	case "webSearch":
		return webSearchTitle(it)
	}
	return ""
}

// handleItem renders one item. owner is the subagent whose thread it came
// from, nil for this session's own thread.
func (s *session) handleItem(method, turn string, owner *child, params json.RawMessage) {
	var p struct {
		Item   codexItem `json:"item"`
		TurnID string    `json:"turnId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		s.decodeFailed(method, err)
		return
	}
	completed := method == "item/completed"
	it := p.Item
	parent := owner.toolCall()
	// open reports whether omniplex has this turn open; a completion arriving with
	// no open turn is a straggler from after turn/completed. Labelling such
	// events with codex's own turn id keeps the timeline coherent, but see the
	// agentMessage case for the one event that must not go out at all.
	open := turn != ""
	if turn == "" {
		turn = p.TurnID
	}

	switch it.Type {
	case "contextCompaction":
		if !completed {
			return
		}
		s.emitContextCompacted(p.TurnID, compactionItem)

	case "userMessage":
		// The turn's own prompt is already in the log via turn.started. A
		// message carrying a client id is a steer codex has just read into
		// the running turn (Steer); say so, named by its queue entry.
		if completed && open && it.ClientID != "" {
			s.emit(proto.Emit(proto.PromptInjected, proto.PromptInjectedPayload{QueueID: it.ClientID, TurnID: turn}))
		}

	case "agentMessage":
		// Deltas normally carry the text. Backfill only when none arrived,
		// so a message that did stream is not duplicated.
		if !completed || it.Text == "" {
			return
		}
		// No open turn means this completion straggled in after turn/completed.
		// The projection reads any chunk while idle as a running turn (the
		// activity defence), and nothing would ever close it — drop the
		// backfill rather than reopen the session as working forever.
		if !open {
			s.host.Logf("codex: dropping agentMessage %s that completed after its turn", it.ID)
			return
		}
		s.mu.Lock()
		streamed := s.streamed[it.ID]
		s.mu.Unlock()
		if !streamed {
			s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
				TurnID: turn, Role: "agent", Kind: "text", BlockID: it.ID, Delta: it.Text,
				ParentToolCallID: parent,
			}))
		}

	case "reasoning":
		// Reasoning text arrives as thought deltas; nothing to do on the
		// item boundaries.

	case "commandExecution":
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindExecute,
				Title: toolTitle(it), Status: proto.StatusInProgress,
				RawInput:         jsonOf(map[string]any{"command": it.Command, "cwd": it.Cwd}),
				ParentToolCallID: parent,
			}))
			return
		}
		status := itemStatus(it.Status)
		if status == proto.StatusCompleted && it.ExitCode != nil && *it.ExitCode != 0 {
			status = proto.StatusFailed
		}
		var content []proto.ToolContent
		if it.AggregatedOutput != "" {
			content = []proto.ToolContent{{Type: "text", Text: it.AggregatedOutput}}
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: status, Content: content, ParentToolCallID: parent,
		}))

	case "fileChange":
		kind, title, content := fileChangeCall(it.Changes)
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: kind,
				Title: title, Status: proto.StatusInProgress, ParentToolCallID: parent,
			}))
			return
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: itemStatus(it.Status), Title: title, Content: content,
			ParentToolCallID: parent,
		}))

	case "mcpToolCall":
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindOther,
				Title: toolTitle(it), Status: proto.StatusInProgress, ParentToolCallID: parent,
			}))
			return
		}
		status, content := mcpOutcome(it)
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: status, Content: content, ParentToolCallID: parent,
		}))

	case "webSearch":
		// The query is often empty when the search starts and filled in by
		// the time it completes, so the completion re-titles the card.
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindFetch,
				Title: webSearchTitle(it), Status: proto.StatusInProgress, ParentToolCallID: parent,
			}))
			return
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: proto.StatusCompleted, Title: webSearchTitle(it),
			ParentToolCallID: parent,
		}))

	case "collabAgentToolCall", "subAgentActivity":
		if !open {
			turn = s.endedTurn(p.TurnID)
		}
		s.handleSpawnItem(completed, turn, owner, it)
	}
}

// itemStatus maps a completed item's CommandExecutionStatus or
// PatchApplyStatus onto the canonical one. A declined call never ran: it is
// neither done nor broken.
func itemStatus(status string) string {
	switch status {
	case "declined":
		return proto.StatusDeclined
	case "failed":
		return proto.StatusFailed
	}
	return proto.StatusCompleted
}

// mcpOutcome is a completed MCP call's status and what the card shows. A
// failure carries its error and no result; printing the absent result is what
// rendered "null" under a green tick.
func mcpOutcome(it codexItem) (string, []proto.ToolContent) {
	if it.Status == "failed" || it.Error != nil {
		msg := "MCP call failed"
		if it.Error != nil && it.Error.Message != "" {
			msg = it.Error.Message
		}
		return proto.StatusFailed, []proto.ToolContent{{Type: "text", Text: msg}}
	}
	if it.Result == nil {
		return proto.StatusCompleted, nil
	}
	var content []proto.ToolContent
	for _, c := range it.Result.Content {
		var text string
		switch c.Type {
		case "text":
			text = c.Text
		case "image", "audio":
			// The bytes stay off the wire: base64 is unreadable as text and
			// heavy on a phone. A placeholder says what was there (#287
			// turns images into thumbnails).
			text = fmt.Sprintf("[%s: %s, %s]", c.Type, c.MimeType, base64Size(c.Data))
		case "resource_link":
			text = c.URI
		case "resource":
			if c.Resource != nil {
				text = c.Resource.Text
				if text == "" {
					text = "[resource: " + c.Resource.URI + "]"
				}
			}
		}
		if text != "" {
			content = append(content, proto.ToolContent{Type: "text", Text: text})
		}
	}
	// Structured output is often the data itself behind a one-line text
	// summary, so it is kept unless a text block already carries it — MCP
	// asks servers to mirror it as text, and printing it twice is noise.
	if sc := it.Result.StructuredContent; len(sc) > 0 && string(sc) != "null" && !textCarries(content, sc) {
		content = append(content, proto.ToolContent{Type: "text", Text: string(sc)})
	}
	return proto.StatusCompleted, content
}

// textCarries reports whether a text block holds the same JSON as raw.
func textCarries(content []proto.ToolContent, raw json.RawMessage) bool {
	var want any
	if json.Unmarshal(raw, &want) != nil {
		return false
	}
	for _, c := range content {
		var got any
		if json.Unmarshal([]byte(c.Text), &got) == nil && reflect.DeepEqual(got, want) {
			return true
		}
	}
	return false
}

// base64Size is the decoded size of base64 data, for a human: "42 KB".
func base64Size(data string) string {
	n := len(strings.TrimRight(data, "=")) * 3 / 4
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%d KB", (n+512)/1024)
}

// webSearchTitle names a web search by what it looked for. Codex fills the
// item's query for a plain search; the action carries the rest.
func webSearchTitle(it codexItem) string {
	if it.Query != "" {
		return "Search: " + it.Query
	}
	if a := it.Action; a != nil {
		switch {
		case a.Query != "":
			return "Search: " + a.Query
		case len(a.Queries) > 0:
			return "Search: " + strings.Join(a.Queries, ", ")
		case a.Type == "openPage" && a.URL != "":
			return "Open " + a.URL
		case a.Type == "findInPage" && a.Pattern != "" && a.URL != "":
			return "Find \"" + a.Pattern + "\" in " + a.URL
		case a.Type == "findInPage" && a.Pattern != "":
			return "Find \"" + a.Pattern + "\""
		}
	}
	return "Web search"
}

// New app-servers emit a contextCompaction item; older ones emit the
// thread/compacted notification, and transitional versions may emit both.
// Within a turn, the nth compaction is announced by whichever signal reports
// it first, so both signals count but neither repeats the other.
func (s *session) emitContextCompacted(turnID string, signal compactionSignal) {
	s.mu.Lock()
	if turnID != "" {
		c := &s.compaction
		if c.turn != turnID {
			*c = compactionCount{turn: turnID}
		}
		if signal == compactionItem {
			c.items++
		} else {
			c.notices++
		}
		if max(c.items, c.notices) <= c.emitted {
			s.mu.Unlock()
			return
		}
		c.emitted++
	}
	manual := s.manualCompact
	s.manualCompact = false
	s.mu.Unlock()
	trigger := "auto"
	if manual {
		trigger = "manual"
	}
	s.emit(proto.Emit(proto.ContextCompacted, proto.ContextCompactedPayload{Trigger: trigger}))
}

// handlePlan records codex's update_plan list: the whole plan as it stands,
// sent again on every revision. It is stamped with the omniplex turn, not
// codex's turnId — the projection keys plans by the turns it knows.
func (s *session) handlePlan(method, turn string, params json.RawMessage) {
	var p struct {
		TurnID      string `json:"turnId"`
		Explanation string `json:"explanation"` // null decodes to ""
		Plan        []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		s.decodeFailed(method, err)
		return
	}
	if len(p.Plan) == 0 {
		return
	}
	steps := make([]proto.PlanStep, 0, len(p.Plan))
	for _, st := range p.Plan {
		steps = append(steps, proto.PlanStep{Text: st.Step, Status: planStatus(st.Status)})
	}
	// Planning while idle is a harness-initiated turn; see ensureTurn.
	if turn == "" {
		turn = s.ensureTurn(p.TurnID)
	}
	s.emit(proto.Emit(proto.PlanUpdated, proto.PlanUpdatedPayload{
		TurnID: turn, Explanation: p.Explanation, Steps: steps,
	}))
}

// planStatus maps codex's step status onto the canonical one. Anything it
// does not recognise reads as pending: a step not yet known to be running.
func planStatus(s string) string {
	switch s {
	case "inProgress":
		return proto.PlanRunning
	case "completed":
		return proto.PlanCompleted
	}
	return proto.PlanPending
}

func jsonOf(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
