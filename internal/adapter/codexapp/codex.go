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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
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

	return adapter.Ready(map[string]string{
		"codex":    path,
		"codexVer": strings.TrimSpace(string(out)),
	})
}

// PermissionModes are omniplex's presets over Codex's two orthogonal axes (approval
// policy × sandbox) plus its reviewer selector. The ids are adapter-internal;
// modeSettings maps them onto the app-server protocol. Codex has no single
// "permission mode" — pretending it does would delete the modes that are the
// point, so each preset pins every axis explicitly.
func (a *Adapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{
		{ID: "untrusted", Label: "Manual", Description: "Ask before all but trusted read-only commands"},
		{ID: "read-only", Label: "Plan", Description: "Read and analyze only; ask to go further"},
		{ID: "on-request", Label: "Ask when needed", Description: "Write in the workspace; the model asks when it needs more", Default: true},
		{ID: "auto-review", Label: "Auto", Description: "A reviewer subagent approves or denies escalations"},
		{ID: "sandboxed-auto", Label: "No prompts (sandboxed)", Description: "Never ask; the sandbox contains the damage"},
		{ID: "full-access", Label: "Bypass", Description: "Never ask and no sandbox"},
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

func settingsFor(mode string) (modeSettings, error) {
	policy := func(approval, sandbox string, sandboxPolicy map[string]any, reviewer string) modeSettings {
		return modeSettings{approvalPolicy: approval, sandbox: sandbox, sandboxPolicy: sandboxPolicy, reviewer: reviewer}
	}
	readOnly := map[string]any{"type": "readOnly", "networkAccess": false}
	workspaceWrite := map[string]any{"type": "workspaceWrite", "networkAccess": false}
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

func (a *Adapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	// Resolve the mode before spawning anything: an unknown id should fail
	// legibly, not leave an orphaned app-server.
	mode, err := settingsFor(o.Mode)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(a.Bin, append([]string{"app-server"}, trustArgs(o.Cwd)...)...)
	cmd.Dir = o.Cwd
	// The instance's overlay over the ambient environment is the entire
	// credential mechanism: a per-account CODEX_HOME isolates config and login.
	cmd.Env = adapter.MergeEnv(os.Environ(), o.Env)

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
		return nil, fmt.Errorf("start %s app-server: %w", a.Bin, err)
	}

	s := &session{
		host:      host,
		cmd:       cmd,
		cwd:       o.Cwd,
		events:    make(chan proto.Emission, 256),
		streamed:  map[string]bool{},
		subagents: map[string]string{},
		done:      make(chan struct{}),
		effort:    o.Effort,
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

	method := "thread/start"
	if o.HarnessSessionID != "" {
		// Continue the existing thread so a server restart does not lose the
		// agent's context.
		method = "thread/resume"
		startParams["threadId"] = o.HarnessSessionID
	}

	var startRes struct {
		Thread struct {
			ID string `json:"id"`
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

	s.emit(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{
		HarnessSessionID: s.threadID,
	}))

	return s, nil
}

type session struct {
	host adapter.HostServices
	cmd  *exec.Cmd
	conn *jsonrpc.Conn
	cwd  string

	threadID string
	effort   string
	model    string

	events chan proto.Emission
	done   chan struct{}
	closed sync.Once

	mu sync.Mutex
	// turnID is omniplex's own turn id, echoed onto emitted events. serverTurnID is
	// codex's id for the in-flight turn — turn/interrupt needs it, and it is not
	// the same value as turnID.
	turnID       string
	serverTurnID string
	interrupting bool            // an interrupt for serverTurnID is already in flight
	streamed     map[string]bool // item ids that arrived as deltas
	// subagents maps a spawned thread's id to the turn of ours that was
	// running when it first spoke — the turn that spawned it. Lazily filled;
	// a session runs few enough subagents that it is never worth pruning.
	subagents map[string]string
	// Set while a /compact RPC is awaiting its contextCompaction item, so the
	// canonical notice can distinguish a human request from auto-compaction.
	manualCompact  bool
	lastCompaction string
}

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

	// Images lead the input, the way every chat UI orders them: codex reads
	// each path itself, so nothing is copied or re-encoded here. An
	// image-only message is legal, and sends no empty text item.
	input := make([]map[string]any, 0, len(in.Images)+1)
	for _, img := range in.Images {
		input = append(input, map[string]any{"type": "localImage", "path": img.Path})
	}
	if in.Text != "" || len(input) == 0 {
		input = append(input, map[string]any{"type": "text", "text": in.Text})
	}
	params := map[string]any{
		"threadId": s.threadID,
		"input":    input,
	}
	// effort and model are both mutable mid-session (SetEffort/SetModel), so
	// read them together under the lock.
	s.mu.Lock()
	if s.effort != "" {
		params["effort"] = s.effort
	}
	if s.model != "" {
		params["model"] = s.model
	}
	s.mu.Unlock()

	// turn/start returns immediately with the in-progress turn's id (it does not
	// block until the turn finishes). Capture that id so Cancel can address the
	// right turn — turn/interrupt requires it.
	var startRes struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := s.conn.Call(ctx, "turn/start", params, &startRes); err != nil {
		// The turn never started, so no turn/completed will ever clear this
		// id. Leaving it set would make the collision guard above reject every
		// later prompt forever. Guarded: the read loop may have cleared or
		// replaced it already.
		s.mu.Lock()
		if s.turnID == in.TurnID {
			s.turnID = ""
		}
		s.mu.Unlock()
		return err
	}
	// Only record the id if this turn is still the active one. turn/completed is
	// handled on the read-loop goroutine and may have already cleared the turn
	// (a very fast turn) between the response arriving and this line running;
	// the guard keeps a completed turn's id from being resurrected as active.
	s.mu.Lock()
	if s.turnID == in.TurnID {
		s.serverTurnID = startRes.Turn.ID
	}
	s.mu.Unlock()
	return nil
}

// Cancel interrupts the in-flight turn. codex's turn/interrupt requires both the
// thread id and the specific turn id; omitting the turn id makes it a no-op,
// which is why the stop button silently did nothing before.
//
// A second interrupt for a turn that is already being interrupted is coalesced:
// codex leaves repeated interrupts of the same turn pending indefinitely, and
// this call runs on the session actor goroutine, so a hung Call would wedge the
// whole session (a double-clicked stop button).
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	turnID := s.serverTurnID
	inFlight := s.interrupting
	if turnID != "" {
		s.interrupting = true
	}
	s.mu.Unlock()
	if turnID == "" || inFlight {
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
		if s.serverTurnID == turnID {
			s.interrupting = false
		}
		s.mu.Unlock()
	}
	return err
}

// SetMode switches the permission preset mid-thread. thread/settings/update
// applies "for subsequent turns" natively, so no restart is needed. Every axis
// is sent explicitly — including the reviewer — so switching away from a mode
// resets what that mode had set.
func (s *session) SetMode(ctx context.Context, mode string) error {
	m, err := settingsFor(mode)
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
		close(s.done)
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
	turn := s.turnID
	s.turnID = ""
	s.serverTurnID = ""
	s.interrupting = false
	s.mu.Unlock()
	if turn != "" {
		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turn, StopReason: proto.StopError, Error: "harness exited",
		}))
	}
	close(s.events)
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
		// Backfill the server id if the turn opened from a notification that
		// did not carry one; Cancel needs it.
		if s.serverTurnID == "" && serverTurn != "" {
			s.serverTurnID = serverTurn
		}
		s.mu.Unlock()
		return id
	}
	id := uuid.NewString()
	s.turnID = id
	s.serverTurnID = serverTurn
	s.mu.Unlock()

	s.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: id}))
	return id
}

// ---- server → client requests (approvals) ----

func (s *session) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		var p struct {
			ItemID  string `json:"itemId"`
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		return s.ask(ctx, p.ItemID, "shell", firstLine(p.Command), params)

	case "item/fileChange/requestApproval", "applyPatchApproval":
		var p struct {
			ItemID    string `json:"itemId"`
			Reason    string `json:"reason"`
			GrantRoot string `json:"grantRoot"`
		}
		_ = json.Unmarshal(params, &p)
		title := "Apply file changes"
		if p.GrantRoot != "" {
			title = "Apply file changes outside " + p.GrantRoot
		}
		return s.ask(ctx, p.ItemID, "apply_patch", title, params)

	case "item/permissions/requestApproval":
		var p struct {
			ItemID string `json:"itemId"`
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		title := "Grant additional permissions"
		if p.Reason != "" {
			title = p.Reason
		}
		return s.ask(ctx, p.ItemID, "permissions", title, params)

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
		TurnID    string              `json:"turnId"`
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
	result, err := s.host.Elicit(ctx, adapter.ElicitationRequest{
		TurnID: p.TurnID, Prompt: "Codex needs your input", Schema: schema,
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
		TurnID          string          `json:"turnId"`
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
		TurnID: p.TurnID, Prompt: p.Message, Schema: schema,
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

// ask routes an approval to a human via the host and maps the answer onto
// codex's decision vocabulary.
func (s *session) ask(ctx context.Context, itemID, tool, title string, raw json.RawMessage) (any, error) {
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
		return map[string]any{"decision": "decline"}, nil
	}

	decision := "decline"
	switch outcome.Outcome {
	case proto.OutcomeAllowOnce:
		decision = "accept"
	case proto.OutcomeAllowAlways:
		decision = "acceptForSession"
	case proto.OutcomeCancelled:
		decision = "cancel"
	}
	return map[string]any{"decision": decision}, nil
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

func (s *session) emitMessageDelta(turn string, p delta) {
	s.mu.Lock()
	s.streamed[p.ItemID] = true
	s.mu.Unlock()
	s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turn, Role: "agent", Kind: "text", BlockID: p.ItemID, Delta: p.Delta,
	}))
}

func (s *session) emitReasoningDelta(turn string, p delta) {
	s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turn, Role: "agent", Kind: "thought", BlockID: p.ItemID + ":reasoning", Delta: p.Delta,
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

// spawningTurn returns the turn a subagent thread's output belongs in: the one
// that was running the first time that thread spoke. Empty means nowhere —
// either nothing of ours was running when it first appeared, or the turn that
// spawned it has since ended and a subagent that outlives its turn must not
// spill into whatever turn is running now.
func (s *session) spawningTurn(thread, turn string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.subagents[thread]; ok {
		if owner == turn {
			return turn
		}
		return ""
	}
	if turn == "" {
		return ""
	}
	if s.subagents == nil {
		s.subagents = map[string]string{}
	}
	s.subagents[thread] = turn
	return turn
}

// handleSubagentNotification renders a spawned subagent's work inside the turn
// that spawned it. Text, reasoning and tool calls are the whole of it:
// everything else a thread reports — turn lifecycle, token usage, plan,
// compaction — is about that thread, and applying it here would relabel this
// session's turn, meter or plan with another thread's state.
//
// With no turn of ours to attach it to the output is dropped rather than
// allowed to open one. That case is exactly the stop button: interrupting this
// thread does not stop a subagent it spawned, so the subagent keeps talking
// after the turn is cancelled. Opening a turn for it made the session look like
// it had started the work over, and left a stop button that addressed another
// thread's turn — which codex ignores, so the phantom turn could not be stopped
// at all.
func (s *session) handleSubagentNotification(method, thread, turn string, params json.RawMessage) {
	turn = s.spawningTurn(thread, turn)
	if turn == "" {
		return
	}
	switch method {
	case "item/started", "item/completed":
		// A subagent compacting its own context says nothing about this
		// session's, and the notice names this session's turn — drop it.
		var p struct {
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Item.Type == "contextCompaction" {
			return
		}
		s.handleItem(method == "item/completed", turn, params)
	case "item/agentMessage/delta":
		s.emitMessageDelta(turn, parseDelta(params))
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		s.emitReasoningDelta(turn, parseDelta(params))
	}
}

func (s *session) handleNotification(method string, params json.RawMessage) {
	turn := s.currentTurn()

	if thread := s.foreignThread(params); thread != "" {
		s.handleSubagentNotification(method, thread, turn, params)
		return
	}

	switch method {
	case "skills/changed":
		if host, ok := s.host.(adapter.ComposerCatalogueInvalidator); ok {
			host.ComposerCatalogueChanged()
		}

	case "thread/compacted":
		var p struct {
			TurnID string `json:"turnId"`
		}
		_ = json.Unmarshal(params, &p)
		s.emitContextCompacted(p.TurnID)

	case "item/started", "item/completed":
		// An item starting is work running: open a harness-initiated turn if
		// the log says idle. An item completing is not — a straggling
		// completion after the turn ended must not reopen it.
		if method == "item/started" && turn == "" {
			var p struct {
				TurnID string `json:"turnId"`
			}
			_ = json.Unmarshal(params, &p)
			turn = s.ensureTurn(p.TurnID)
		}
		s.handleItem(method == "item/completed", turn, params)

	case "item/agentMessage/delta":
		p := parseDelta(params)
		// Streaming while idle is a harness-initiated turn; see ensureTurn.
		if turn == "" {
			turn = s.ensureTurn(p.TurnID)
		}
		s.emitMessageDelta(turn, p)

	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		p := parseDelta(params)
		// Streaming while idle is a harness-initiated turn; see ensureTurn.
		if turn == "" {
			turn = s.ensureTurn(p.TurnID)
		}
		s.emitReasoningDelta(turn, p)

	case "item/commandExecution/outputDelta", "command/exec/outputDelta":
		// Streamed output is dropped; the completed item carries the
		// aggregated output, which is what the timeline renders.

	case "turn/plan/updated", "item/plan/delta":
		s.handlePlan(params)

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
			Input: t.InputTokens, Output: t.OutputTokens,
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
		s.turnID = ""
		s.serverTurnID = ""
		s.interrupting = false
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

	// agentMessage
	Text  string `json:"text"`
	Phase string `json:"phase"`

	// reasoning
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`

	// commandExecution
	Command          string `json:"command"`
	Cwd              string `json:"cwd"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`

	// fileChange
	Changes []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
		Diff string `json:"diff"`
	} `json:"changes"`

	// mcpToolCall
	Server string          `json:"server"`
	Tool   string          `json:"tool"`
	Result json.RawMessage `json:"result"`

	// webSearch
	Query string `json:"query"`
}

func (s *session) handleItem(completed bool, turn string, params json.RawMessage) {
	var p struct {
		Item   codexItem `json:"item"`
		TurnID string    `json:"turnId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	it := p.Item
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
		s.emitContextCompacted(p.TurnID)

	case "userMessage":
		// Already in the log via turn.started.

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
			}))
		}

	case "reasoning":
		// Reasoning text arrives as thought deltas; nothing to do on the
		// item boundaries.

	case "commandExecution":
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindExecute,
				Title: firstLine(it.Command), Status: proto.StatusInProgress,
				RawInput: jsonOf(map[string]any{"command": it.Command, "cwd": it.Cwd}),
			}))
			return
		}
		status := proto.StatusCompleted
		if it.ExitCode != nil && *it.ExitCode != 0 {
			status = proto.StatusFailed
		}
		var content []proto.ToolContent
		if it.AggregatedOutput != "" {
			content = []proto.ToolContent{{Type: "text", Text: it.AggregatedOutput}}
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: status, Content: content,
		}))

	case "fileChange":
		var content []proto.ToolContent
		paths := make([]string, 0, len(it.Changes))
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
			content = append(content, proto.ToolContent{Type: "diff", Path: c.Path, Text: c.Diff})
		}
		title := "Edit " + strings.Join(paths, ", ")
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindEdit,
				Title: title, Status: proto.StatusInProgress,
			}))
			return
		}
		status := proto.StatusCompleted
		if it.Status == "failed" || it.Status == "declined" {
			status = proto.StatusFailed
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: status, Title: title, Content: content,
		}))

	case "mcpToolCall":
		title := it.Server + "/" + it.Tool
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindOther,
				Title: title, Status: proto.StatusInProgress,
			}))
			return
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: proto.StatusCompleted,
			Content: []proto.ToolContent{{Type: "text", Text: string(it.Result)}},
		}))

	case "webSearch":
		if !completed {
			s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
				TurnID: turn, ToolCallID: it.ID, Kind: proto.KindFetch,
				Title: "Search: " + it.Query, Status: proto.StatusInProgress,
			}))
			return
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: it.ID, Status: proto.StatusCompleted,
		}))
	}
}

// New app-servers emit a contextCompaction item; older ones emit the
// thread/compacted notification, and transitional versions may emit both.
func (s *session) emitContextCompacted(turnID string) {
	s.mu.Lock()
	if turnID != "" && turnID == s.lastCompaction {
		s.mu.Unlock()
		return
	}
	manual := s.manualCompact
	s.manualCompact = false
	s.lastCompaction = turnID
	s.mu.Unlock()
	trigger := "auto"
	if manual {
		trigger = "manual"
	}
	s.emit(proto.Emit(proto.ContextCompacted, proto.ContextCompactedPayload{Trigger: trigger}))
}

func (s *session) handlePlan(params json.RawMessage) {
	var p struct {
		Plan []struct {
			Step   string `json:"step"`
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"plan"`
		Item struct {
			Plan []struct {
				Step   string `json:"step"`
				Text   string `json:"text"`
				Status string `json:"status"`
			} `json:"plan"`
		} `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	steps := p.Plan
	if len(steps) == 0 {
		steps = p.Item.Plan
	}
	entries := make([]proto.PlanEntry, 0, len(steps))
	for _, st := range steps {
		text := st.Step
		if text == "" {
			text = st.Text
		}
		entries = append(entries, proto.PlanEntry{Content: text, Status: st.Status})
	}
	if len(entries) > 0 {
		s.emit(proto.Emit(proto.PlanUpdated, proto.PlanUpdatedPayload{Entries: entries}))
	}
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
