// Package piapp adapts Pi (`pi --mode rpc`, JSON lines over stdio) to the
// canonical event model.
//
// Pi's RPC mode is typed commands with an optional echoed id, not JSON-RPC, so
// this package carries its own message framing and takes only the line reader
// from internal/jsonrpc. The TUI is never scraped. Verified against
// @earendil-works/pi-coding-agent 0.84.4.
package piapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/jsonrpc"
	"github.com/asiraky/omniplex/internal/procgroup"
	"github.com/asiraky/omniplex/internal/proto"
)

// Adapter creates pi sessions.
type Adapter struct {
	Bin string

	// bridgeOverride replaces the resolved `node authbridge.mjs <pkgRoot>`
	// argv in tests, so auth flows can be exercised against a fake bridge
	// without a Pi install.
	bridgeOverride []string
}

func New(bin string) *Adapter {
	if bin == "" {
		bin = "pi"
	}
	return &Adapter{Bin: bin}
}

func (a *Adapter) ID() string { return "pi" }

func (a *Adapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{
		ID:      "pi",
		Name:    "Pi",
		Accent:  "oklch(0.72 0.15 55)",
		DocsURL: "https://github.com/earendil-works/pi",
	}
}

// ConfigFields declares how an instance of Pi is configured. PI_CODING_AGENT_DIR
// is the whole mechanism: it holds auth.json and settings, so pointing a second
// instance at its own directory is what makes it a second account. The user's
// configuration in it is shared from ~/.pi/agent (see SharedEntries).
func (a *Adapter) ConfigFields() []adapter.ConfigField {
	return []adapter.ConfigField{
		{
			Env:         "PI_CODING_AGENT_DIR",
			Label:       "Pi agent directory",
			Description: "Directory holding this account's Pi credentials and conversations. Settings, models, AGENTS.md, skills, prompts and extensions are shared from ~/.pi/agent. Leave empty on the default instance to use the machine's own login.",
			Placeholder: "~/.pi/agent",
			Kind:        adapter.FieldPath,
			Isolates:    true,
		},
	}
}

// PermissionModes is a single entry because Pi has no permission system: every
// tool runs without asking, always. Presenting that as a mode — rather than an
// empty list — lets a UI say so before the first shell command runs.
func (a *Adapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{
		{
			ID:          "full-access",
			Label:       "Full access",
			Description: "Pi runs read, edit, and shell tools without asking. It has no approval prompts.",
			Default:     true,
			Level:       adapter.LevelAll,
		},
	}
}

// Probe reports whether a pi session could start right now. Pi itself needs
// only its own CLI; the auth bridge additionally needs Node, but that is a
// settings-page concern, not a can-a-session-start one, so its absence is not
// reported here.
func (a *Adapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	path, ok := a.findPi()
	if !ok {
		return adapter.Unavailable(
			"The Pi CLI was not found on this machine.",
			adapter.Remedy{Text: "Install Pi", URL: "https://github.com/earendil-works/pi"},
			adapter.Remedy{Text: "Or install it with npm", Command: "npm i -g @earendil-works/pi-coding-agent"},
		)
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	probe := exec.CommandContext(probeCtx, path, "--version")
	probe.Env = withBinDir(path, adapter.MergeEnv(os.Environ(), env))
	out, err := probe.Output()
	if err != nil {
		return adapter.Unavailable(
			"The Pi CLI was found at "+path+" but did not run.",
			adapter.Remedy{Text: "Reinstall it with npm", Command: "npm i -g @earendil-works/pi-coding-agent"},
		)
	}

	return adapter.Ready(map[string]string{
		"pi":                path,
		adapter.FactVersion: adapter.VersionNumber(string(out)),
		"auth":              authSummary(env),
	})
}

// authSummary is a stable, non-secret description of which providers this
// instance holds credentials for — "anthropic (oauth), openrouter (api_key)" —
// read straight from auth.json. It exists so the availability facts change
// when the account does, which is what lets a cached probe answer be
// invalidated by comparing facts. Only provider ids and credential types are
// retained; key material is decoded into nothing.
func authSummary(env map[string]string) string {
	data, err := os.ReadFile(filepath.Join(agentDir(env), "auth.json"))
	if err != nil {
		return "none"
	}
	var creds map[string]struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &creds); err != nil || len(creds) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(creds))
	for id, c := range creds {
		parts = append(parts, fmt.Sprintf("%s (%s)", id, c.Type))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// agentDir resolves the Pi agent directory the way Pi itself does: the
// instance overlay wins, then the ambient environment, then ~/.pi/agent.
func agentDir(env map[string]string) string {
	if dir := env["PI_CODING_AGENT_DIR"]; dir != "" {
		return dir
	}
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

func (a *Adapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	if o.Mode != "" && o.Mode != "full-access" {
		return nil, fmt.Errorf("pi does not have a %q permission mode; it always runs with full access", o.Mode)
	}

	// --session-id is create-or-resume: pi opens the session file with this id
	// if one exists under the cwd's project, and otherwise creates a new
	// session carrying it. Passing our own id on the first run and the
	// harness's on later ones makes resume deterministic without a separate
	// code path. (Pi does not persist an empty session, so a resume before the
	// first real message simply creates afresh — same id, nothing lost.)
	sid := o.HarnessSessionID
	if sid == "" {
		sid = o.ThreadID
	}
	// --approve trusts the workspace's project-local resources. Pi never
	// prompts for trust in rpc mode; without this it falls back to
	// defaultProjectTrust, which ignores .pi/settings.json, project
	// extensions, and project skills — so a repo's own skills would be
	// missing from the composer with nothing to say why. Omniplex only ever
	// opens workspaces the operator asked it to open, and the harness already
	// runs with full access to them, so withholding trust from the skills
	// while granting it to the shell buys nothing.
	args := []string{"--mode", "rpc", "--approve", "--session-id", sid}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--thinking", o.Effort)
	}
	// Read once at start: pi does not rescan, so a skill added mid-session
	// arrives with the next one.
	for _, d := range o.SkillDirs {
		args = append(args, "--skill", d)
	}
	for _, p := range o.Plugins {
		args = append(args, "--skill", adapter.PluginSkills(p))
	}

	bin, ok := a.findPi()
	if !ok {
		return nil, fmt.Errorf("the Pi CLI (%s) was not found on this machine", a.Bin)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = o.Cwd
	// The instance's overlay over the ambient environment is the entire
	// credential mechanism: a per-account PI_CODING_AGENT_DIR isolates
	// auth.json and settings.
	cmd.Env = withBinDir(bin, adapter.MergeEnv(os.Environ(), o.Env))

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
	// The whole tree, not just pi: shells, dev servers and browsers the agent
	// starts must end with the session.
	tree := procgroup.Attach(cmd, "pi-"+sid)
	if err := cmd.Start(); err != nil {
		tree.Kill()
		return nil, fmt.Errorf("start %s --mode rpc: %w", a.Bin, err)
	}

	s := &session{
		host:      host,
		cmd:       cmd,
		proc:      adapter.WatchProcess(cmd, stderr, host.Logf, "pi"),
		tree:      tree,
		stdin:     stdin,
		events:    make(chan proto.Emission, 256),
		done:      make(chan struct{}),
		rpcDone:   make(chan struct{}),
		pending:   map[string]chan rpcResponse{},
		dialogs:   map[string]dialog{},
		editPaths: map[string]string{},
	}

	go s.readLoop(stdout)
	go s.watchExit()

	// get_state is the handshake: it proves the process actually reached RPC
	// mode (a bad flag dies before answering), and it reports the session id
	// pi settled on plus the running model's context window for the usage
	// meter.
	var state struct {
		SessionID string `json:"sessionId"`
		Model     *struct {
			Provider      string `json:"provider"`
			ID            string `json:"id"`
			ContextWindow int64  `json:"contextWindow"`
		} `json:"model"`
	}
	if err := s.call(ctx, map[string]any{"type": "get_state"}, &state); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("pi get_state: %w", s.classify(err))
	}
	if state.Model != nil {
		s.mu.Lock()
		s.contextWindow = state.Model.ContextWindow
		s.mu.Unlock()
	}
	s.emit(proto.Emit(proto.ThreadConfigChanged, proto.ThreadConfigChangedPayload{
		HarnessSessionID: state.SessionID,
	}))

	return s, nil
}

// rpcResponse is the id-correlated half of pi's stdout stream.
type rpcResponse struct {
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type session struct {
	host  adapter.HostServices
	cmd   *exec.Cmd
	proc  *adapter.Process
	tree  procgroup.Group
	stdin io.WriteCloser

	events chan proto.Emission
	done   chan struct{}
	// rpcDone closes when the read loop ends — the process is gone or its
	// stdout broke — so a call waiting for a response that will never come
	// unblocks. readErr is what broke it, if anything; set before the close.
	rpcDone chan struct{}
	readErr error
	closed  sync.Once

	// emitMu orders every send on events before the close in watchExit. The
	// sends and the close happen on unrelated goroutines (the caller's, the
	// read loop's, an elicitation's), and a channel close concurrent with a
	// send is a race even when it loses it.
	emitMu       sync.RWMutex
	eventsClosed bool

	writeMu sync.Mutex // serialises stdin lines

	mu      sync.Mutex
	pending map[string]chan rpcResponse
	// dialogs holds every extension dialog still waiting on a human, keyed
	// by pi's request id. Cancelling one withdraws its card and answers pi
	// cancelled; each removes itself when its Elicit returns.
	dialogs map[string]dialog
	turnID  string
	// editPaths holds the file each running edit call names, by item id.
	// tool_execution_end carries the result but not the args, and the diff
	// it becomes needs the path.
	editPaths map[string]string
	// msgSeq counts assistant messages within this process so block ids stay
	// unique across a turn with several messages: pi's contentIndex restarts
	// at zero on each one. It restarts on respawn, so it is only unique
	// alongside the turn id.
	msgSeq int
	// lastStop and lastErr are the final assistant message's verdict, carried
	// from message_end to agent_settled — the turn boundary — where they
	// become the turn's stop reason.
	lastStop string
	lastErr  string
	// aborted is the turn Cancel aborted. Pi reports a run aborted mid-tool
	// (an open extension dialog, say) as an error, and the user pressed Stop.
	aborted string
	// Running totals for usage.updated. Pi reports usage per assistant
	// message; the meter wants the session's cumulative spend.
	totIn, totOut, totCacheR, totCacheW int64
	totCost                             float64
	contextWindow                       int64
}

func (s *session) Events() <-chan proto.Emission { return s.events }

// send writes one command line. Pi's framing is strict LF-delimited JSON, and
// encoding/json never emits a raw newline, so one Marshal is one frame.
func (s *session) send(cmd map[string]any) error {
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

// call sends a command with a correlation id and waits for its response,
// decoding data into out when non-nil.
func (s *session) call(ctx context.Context, cmd map[string]any, out any) error {
	ch, release, err := s.request(cmd)
	if err != nil {
		return err
	}
	defer release()
	select {
	case res := <-ch:
		if err := res.err(); err != nil {
			return err
		}
		if out != nil && len(res.Data) > 0 {
			return json.Unmarshal(res.Data, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.rpcDone:
		return s.died(errors.New("pi exited before responding"))
	}
}

// request is the sending half of call: it registers a correlation id, writes
// the command, and hands back the channel its response will arrive on. The
// caller must call release once it stops waiting, or the pending entry leaks.
// A write failure is returned here, so a dead pipe is still reported
// synchronously even when the response is awaited elsewhere.
func (s *session) request(cmd map[string]any) (<-chan rpcResponse, func(), error) {
	id := uuid.NewString()
	cmd["id"] = id
	ch := make(chan rpcResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}
	if err := s.send(cmd); err != nil {
		release()
		return nil, nil, s.died(err)
	}
	return ch, release, nil
}

// err is the response's refusal as an error, or nil when it succeeded.
func (r rpcResponse) err() error {
	if r.Success {
		return nil
	}
	return fmt.Errorf("%s: %s", r.Command, r.Error)
}

// died swaps a command's error for the reason pi is gone, when it is.
func (s *session) died(err error) error {
	return s.proc.Explain(err, s.rpcDone, func() error { return s.readErr })
}

// maxLine is the longest event readLoop takes. A message_end can carry a
// whole assistant message and a tool result a file, so it is generous; what
// goes over it in practice is agent_end and turn_end, which repeat every
// message of the run, screenshots and all, and which nothing here reads.
const maxLine = 16 << 20

func (s *session) readLoop(r io.Reader) {
	defer close(s.rpcDone)
	s.readErr = jsonrpc.ReadLines(r, maxLine, s.handleLine, func(head []byte, size int) {
		s.host.Logf("pi: skipped a %d-byte event over the %d-byte limit: %s", size, maxLine, jsonrpc.Brief(head))
	})
}

func (s *session) handleLine(line []byte) {
	var head struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		s.host.Logf("pi: skipped a line that is not JSON: %s", jsonrpc.Brief(line))
		return
	}
	if head.Type == "response" {
		var res rpcResponse
		if err := json.Unmarshal(line, &res); err != nil {
			s.host.Logf("pi: skipped a response that does not parse: %v: %s", err, jsonrpc.Brief(line))
			return
		}
		s.mu.Lock()
		ch := s.pending[head.ID]
		s.mu.Unlock()
		if ch != nil {
			ch <- res
		}
		return
	}
	s.handleEvent(head.Type, append([]byte(nil), line...))
}

func (s *session) Prompt(ctx context.Context, in adapter.PromptInput) error {
	s.mu.Lock()
	// The actor believed the session was idle when it accepted this prompt,
	// but pi may have started work by itself in the meantime (an overflow
	// retry, a queued follow-up) — see ensureTurn. Overwriting that turn's id
	// would label the in-flight work with this prompt's turn and leave the
	// open turn unfinished forever. Refuse instead; mirrors codex and claude.
	if s.turnID != "" && s.turnID != in.TurnID {
		s.mu.Unlock()
		return errors.New("the harness resumed work on its own; wait for it to finish")
	}
	s.turnID = in.TurnID
	s.mu.Unlock()

	// Pi takes image bytes inline, so each image is read from the host path
	// and base64'd. Failing the prompt beats silently dropping a screenshot
	// the human is about to ask a question about. Pi has no document input,
	// so a PDF's path goes in the text and the agent reads it itself.
	attached, documents := adapter.SplitAttachments(in.Images)
	images := make([]map[string]any, 0, len(attached))
	for _, img := range attached {
		data, err := os.ReadFile(img.Path)
		if err != nil {
			s.clearTurn(in.TurnID)
			return fmt.Errorf("read attached image: %w", err)
		}
		images = append(images, map[string]any{
			"type":     "image",
			"data":     base64.StdEncoding.EncodeToString(data),
			"mimeType": img.MediaType,
		})
	}
	cmd := map[string]any{"type": "prompt", "message": adapter.WithDocumentPaths(in.Text, documents)}
	if len(images) > 0 {
		cmd["images"] = images
	}
	// Pi acks a prompt only after its preflight: extension commands and input
	// handlers, the auth check, and possibly a full context compaction. That
	// can take minutes, and an extension that opens a dialog during preflight
	// needs the thread actor to answer it, so the actor calling Prompt must not
	// wait for the ack. Only the write happens here; the ack is awaited on its
	// own goroutine and a refusal closes the turn there.
	ack, release, err := s.request(cmd)
	if err != nil {
		s.clearTurn(in.TurnID)
		return s.classify(err)
	}
	go s.awaitPromptAck(in.TurnID, ack, release)
	return nil
}

// awaitPromptAck waits out pi's prompt preflight and, if pi refuses the prompt,
// finishes the turn with the refusal. It is bounded by the session rather than
// the caller's context: the caller has long since returned. If pi exits first,
// watchExit closes the turn instead.
func (s *session) awaitPromptAck(turnID string, ack <-chan rpcResponse, release func()) {
	defer release()
	var res rpcResponse
	select {
	case res = <-ack:
	case <-s.rpcDone:
		return
	case <-s.done:
		return
	}
	refusal := res.err()
	if refusal == nil {
		return
	}
	// The prompt was refused, so no agent_settled will ever clear this turn
	// id; leaving it set would make the collision guard in Prompt reject every
	// later prompt forever. But only a turn that is still ours: if it already
	// settled (an abort during preflight) or another turn has since opened,
	// closing it here would finish work this prompt never started.
	s.mu.Lock()
	ours := s.turnID == turnID
	if ours {
		s.turnID = ""
	}
	s.mu.Unlock()
	if !ours {
		return
	}
	refusal = s.classify(refusal)
	s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: turnID, StopReason: proto.StopError, Error: refusal.Error(),
		Failure: adapter.FailureOf(refusal),
	}))
}

// clearTurn resets the active turn id if it is still ours. Guarded because the
// read loop may have finished or replaced the turn in the meantime.
func (s *session) clearTurn(turnID string) {
	s.mu.Lock()
	if s.turnID == turnID {
		s.turnID = ""
	}
	s.mu.Unlock()
}

// Cancel aborts the in-flight run. Pi's abort succeeds even when idle, so
// there is no coalescing to do, but skipping the call when no turn is open
// keeps a stray stop press from aborting a run that starts a moment later.
// Open extension dialogs are withdrawn with it: stop means stop.
func (s *session) Cancel(ctx context.Context) error {
	s.cancelDialogs()
	s.mu.Lock()
	idle := s.turnID == ""
	s.aborted = s.turnID
	s.mu.Unlock()
	if idle {
		return nil
	}
	return s.call(ctx, map[string]any{"type": "abort"}, nil)
}

// SetModel switches models mid-session. Model ids are "provider/modelId",
// split at the first slash because pi's provider ids never contain one while
// its model ids may.
func (s *session) SetModel(ctx context.Context, model string) error {
	provider, modelID, ok := strings.Cut(model, "/")
	if !ok {
		return fmt.Errorf("pi model ids look like provider/model, got %q", model)
	}
	var res struct {
		ContextWindow int64 `json:"contextWindow"`
	}
	if err := s.call(ctx, map[string]any{"type": "set_model", "provider": provider, "modelId": modelID}, &res); err != nil {
		return s.classify(err)
	}
	// The usage meter measures occupancy against the running model's window,
	// so it must follow the switch.
	s.mu.Lock()
	s.contextWindow = res.ContextWindow
	s.mu.Unlock()
	return nil
}

// SetEffort maps omniplex's effort onto pi's thinking level; the ids are pi's
// own ("off" … "max"), straight from the model catalogue.
func (s *session) SetEffort(ctx context.Context, effort string) error {
	return s.call(ctx, map[string]any{"type": "set_thinking_level", "level": effort}, nil)
}

func (s *session) Close() error {
	s.closed.Do(func() {
		s.cancelDialogs()
		close(s.done)
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		s.tree.Kill()
		_ = s.proc.Wait()
	})
	return nil
}

func (s *session) watchExit() {
	<-s.rpcDone
	// Nobody is left to read a dialog's answer, and the read loop has
	// registered its last one, so this catches them all.
	s.cancelDialogs()
	s.mu.Lock()
	turn := s.turnID
	s.turnID = ""
	s.mu.Unlock()
	if turn != "" {
		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turn, StopReason: proto.StopError, Error: s.proc.ExitReason(s.readErr),
			Failure: failureKind(s.proc.Tail()),
		}))
	}
	s.emitMu.Lock()
	s.eventsClosed = true
	close(s.events)
	s.emitMu.Unlock()
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
// none is open. Pi continues on its own after an overflow compaction and runs
// queued follow-ups, and that work is a real turn: without a turn.started the
// session reads as idle while it is working. Mirrors codex's ensureTurn.
func (s *session) ensureTurn() string {
	s.mu.Lock()
	if s.turnID != "" {
		id := s.turnID
		s.mu.Unlock()
		return id
	}
	id := uuid.NewString()
	s.turnID = id
	s.mu.Unlock()
	s.emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: id}))
	return id
}

// ---- event translation ----

// piUsage is the usage block pi attaches to messages and updates.
type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
	Cost        struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// contextTokens is pi's own occupancy formula: the provider-reported total
// when present, otherwise the sum of the parts.
func (u piUsage) contextTokens() int64 {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

func (s *session) handleEvent(typ string, raw json.RawMessage) {
	switch typ {
	case "agent_start":
		s.ensureTurn()

	case "message_start":
		var p struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Message.Role == "assistant" {
			s.mu.Lock()
			s.msgSeq++
			s.mu.Unlock()
		}

	case "message_update":
		s.handleMessageUpdate(raw)

	case "message_end":
		s.handleMessageEnd(raw)

	case "tool_execution_start":
		var p struct {
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Args       json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return
		}
		turn := s.ensureTurn()
		if p.ToolName == "edit" {
			if path := editPath(p.Args); path != "" {
				s.mu.Lock()
				s.editPaths[toolItemID(turn, p.ToolCallID)] = path
				s.mu.Unlock()
			}
		}
		s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
			TurnID: turn, ToolCallID: toolItemID(turn, p.ToolCallID),
			Kind: toolKind(p.ToolName), Title: toolTitle(p.ToolName, p.Args),
			Status: proto.StatusInProgress, RawInput: p.Args,
		}))

	case "tool_execution_update":
		// Streamed partial results are dropped; tool_execution_end carries the
		// aggregate, which is what the timeline renders.

	case "tool_execution_end":
		s.handleToolEnd(raw)

	case "compaction_end":
		var p struct {
			Reason  string `json:"reason"` // manual | threshold | overflow
			Aborted bool   `json:"aborted"`
			Result  *struct {
				TokensBefore         int64 `json:"tokensBefore"`
				EstimatedTokensAfter int64 `json:"estimatedTokensAfter"`
			} `json:"result"`
		}
		_ = json.Unmarshal(raw, &p)
		// A null result is an aborted or failed compaction: nothing changed,
		// so there is no boundary to record.
		if p.Aborted || p.Result == nil {
			return
		}
		trigger := "auto"
		if p.Reason == "manual" {
			trigger = "manual"
		}
		s.emit(proto.Emit(proto.ContextCompacted, proto.ContextCompactedPayload{
			Trigger: trigger, PreTokens: p.Result.TokensBefore, PostTokens: p.Result.EstimatedTokensAfter,
		}))

	case "agent_settled":
		// The turn boundary. agent_end is deliberately not it: pi may still
		// auto-retry, compact-and-retry, or run a queued follow-up after
		// agent_end, and closing the turn there would strand that work.
		s.finishTurn()

	case "extension_ui_request":
		s.handleUIRequest(raw)

	case "auto_retry_start":
		var p struct {
			Attempt      int    `json:"attempt"`
			MaxAttempts  int    `json:"maxAttempts"`
			ErrorMessage string `json:"errorMessage"`
		}
		_ = json.Unmarshal(raw, &p)
		s.host.Logf("pi auto-retry %d/%d after: %s", p.Attempt, p.MaxAttempts, p.ErrorMessage)

	case "extension_error":
		var p struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &p)
		s.host.Logf("pi extension error: %s", p.Error)
	}
}

// handleToolEnd turns pi's tool result into the call's final update. Text
// stays text; an image (read on a PNG) is stored by the host and travels as a
// URL, never as base64; an edit's unified patch becomes a diff the timeline
// can render in place of "Successfully replaced 1 block(s)".
func (s *session) handleToolEnd(raw json.RawMessage) {
	var p struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		IsError    bool   `json:"isError"`
		Result     struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Data string `json:"data"`
			} `json:"content"`
			Details json.RawMessage `json:"details"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	// The tool ends inside the turn that started it (pi settles after its
	// tools finish), so the current turn rebuilds the same item id.
	itemID := toolItemID(s.currentTurn(), p.ToolCallID)
	s.mu.Lock()
	path := s.editPaths[itemID]
	delete(s.editPaths, itemID)
	s.mu.Unlock()

	status := proto.StatusCompleted
	if p.IsError {
		status = proto.StatusFailed
	}
	var content []proto.ToolContent
	for _, c := range p.Result.Content {
		switch {
		case c.Type == "text" && c.Text != "":
			content = append(content, proto.ToolContent{Type: "text", Text: c.Text})
		case c.Type == "image" && c.Data != "":
			content = append(content, adapter.ToolImage(s.host, c.Data))
		}
	}
	// Pi's patch is jsdiff's createTwoFilesPatch with file headers only:
	// ---/+++ then @@ hunks, which the web's parsePatch reads as it is.
	if p.ToolName == "edit" && !p.IsError && path != "" {
		var d struct {
			Patch string `json:"patch"`
		}
		_ = json.Unmarshal(p.Result.Details, &d)
		if d.Patch != "" && len(d.Patch) <= adapter.MaxToolDiff {
			content = append(content, proto.ToolContent{Type: "diff", Path: path, Text: d.Patch})
		}
	}
	s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
		ToolCallID: itemID, Status: status, Content: content,
	}))
}

// editPath is the file an edit call names. Pi still accepts file_path from
// models that use the older argument name.
func editPath(args json.RawMessage) string {
	var a struct {
		Path string `json:"path"`
		File string `json:"file_path"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Path != "" {
		return a.Path
	}
	return a.File
}

func (s *session) handleMessageUpdate(raw json.RawMessage) {
	var p struct {
		AssistantMessageEvent struct {
			Type         string `json:"type"`
			ContentIndex int    `json:"contentIndex"`
			Delta        string `json:"delta"`
		} `json:"assistantMessageEvent"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	ev := p.AssistantMessageEvent
	var kind string
	switch ev.Type {
	case "text_delta":
		kind = "text"
	case "thinking_delta":
		kind = "thought"
	default:
		// Block boundaries carry no text, and toolcall deltas are rendered
		// from the tool_execution_* events instead.
		return
	}
	if ev.Delta == "" {
		return
	}
	turn := s.ensureTurn()
	s.mu.Lock()
	seq := s.msgSeq
	s.mu.Unlock()
	s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turn, Role: "agent", Kind: kind,
		// Block ids are folded session-wide, so they must never repeat.
		// contentIndex restarts per message, hence the message counter; the
		// counter restarts with the process, hence the turn id. Without it a
		// respawned pi reuses an earlier turn's ids and its reply is appended
		// to that old message instead of appearing in its own turn.
		BlockID: fmt.Sprintf("%s:m%d.b%d", turn, seq, ev.ContentIndex),
		Delta:   ev.Delta,
	}))
}

func (s *session) handleMessageEnd(raw json.RawMessage) {
	var p struct {
		Message struct {
			Role         string  `json:"role"`
			StopReason   string  `json:"stopReason"`
			ErrorMessage string  `json:"errorMessage"`
			Usage        piUsage `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	m := p.Message
	if m.Role != "assistant" {
		return
	}
	s.mu.Lock()
	// The last assistant message's verdict becomes the turn's verdict at
	// agent_settled; anything earlier ("toolUse", a retried error) is
	// intermediate.
	s.lastStop = m.StopReason
	s.lastErr = m.ErrorMessage
	s.totIn += m.Usage.Input
	s.totOut += m.Usage.Output
	s.totCacheR += m.Usage.CacheRead
	s.totCacheW += m.Usage.CacheWrite
	s.totCost += m.Usage.Cost.Total
	in, out, cr, cw, cost := s.totIn, s.totOut, s.totCacheR, s.totCacheW, s.totCost
	window := s.contextWindow
	s.mu.Unlock()

	// Occupancy comes from this message alone — a request's usage is the
	// whole context it was sent with — while the token columns are the
	// session's cumulative spend.
	used := m.Usage.contextTokens()
	var pct float64
	if used > 0 && window > 0 {
		pct = float64(used) / float64(window) * 100
	}
	s.emit(proto.Emit(proto.UsageUpdated, proto.UsageUpdatedPayload{
		Input: in, Output: out, CacheRead: cr, CacheWrite: cw, Cost: cost,
		ContextPct: pct, ContextUsed: used, ContextWindow: window,
	}))
}

func (s *session) finishTurn() {
	s.mu.Lock()
	turn := s.turnID
	stop, errMsg := s.lastStop, s.lastErr
	aborted := turn != "" && s.aborted == turn
	s.turnID = ""
	s.lastStop, s.lastErr, s.aborted = "", "", ""
	// An edit that never ended (pi died mid-call) has nothing left to claim
	// its path: the next turn builds different item ids.
	clear(s.editPaths)
	s.mu.Unlock()
	// A settle with no open turn is a straggler (an abort raced the settle);
	// emitting an empty turn id would confuse every projection downstream.
	if turn == "" {
		return
	}
	// Pi is done with the turn, so it is done with its dialogs, whether or
	// not it said so: one raised as Stop was pressed lands after Cancel's
	// sweep, and nothing else would take it down.
	s.cancelTurnDialogs(turn)

	if aborted && stop == "error" {
		stop = "aborted"
	}
	reason, failure := proto.StopEndTurn, ""
	switch stop {
	case "aborted":
		reason = proto.StopCancelled
	case "length":
		reason = proto.StopMaxTokens
	case "error":
		reason = proto.StopError
		failure = failureKind(errMsg)
	default:
		// "stop", "toolUse", or a message-less turn all mean pi finished of
		// its own accord.
		errMsg = ""
	}
	s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: turn, StopReason: reason, Error: errMsg, Failure: failure,
	}))
}

// handleUIRequest answers a pi extension's UI call. Dialog methods block the
// extension until extension_ui_response arrives, so each is routed through the
// host's elicitation and answered even on failure — an unanswered request
// wedges the extension. Fire-and-forget methods (notify, setStatus, …) expect
// no response.
func (s *session) handleUIRequest(raw json.RawMessage) {
	var p uiRequest
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}

	switch p.Method {
	case "notify":
		s.host.Logf("pi extension notify (%s): %s", p.NotifyType, p.Message)
		return
	case "setStatus", "setWidget", "setTitle", "set_editor_text":
		// Ephemeral TUI chrome with no equivalent surface here.
		return
	case "select", "confirm", "input", "editor":
		// Handled below. Elicit blocks on a human, so it must not run on the
		// read loop: a dialog would freeze every event until answered. It is
		// registered here, not in the goroutine, so a Cancel, exit or turn
		// end that follows this event always finds it, under the turn that
		// was open when pi raised it.
		var ctx context.Context
		var cancel context.CancelFunc
		if p.Timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), time.Duration(p.Timeout*float64(time.Millisecond)))
		} else {
			ctx, cancel = context.WithCancel(context.Background())
		}
		s.mu.Lock()
		turn := s.turnID
		s.dialogs[p.ID] = dialog{turn: turn, cancel: cancel}
		s.mu.Unlock()
		go func() {
			defer func() {
				cancel()
				s.mu.Lock()
				delete(s.dialogs, p.ID)
				s.mu.Unlock()
			}()
			s.elicitUIRequest(ctx, turn, p)
		}()
	}
}

// uiRequest is pi's extension_ui_request.
type uiRequest struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"`
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Placeholder string   `json:"placeholder"`
	Prefill     string   `json:"prefill"`
	Options     []string `json:"options"`
	NotifyType  string   `json:"notifyType"`
	// Timeout is in milliseconds. On expiry pi resolves the dialog to its
	// default by itself and tells us nothing, so the card must go too.
	Timeout float64 `json:"timeout"`
}

// dialog is an extension dialog waiting on a human, and the turn it was
// raised in; empty when pi raised it outside one.
type dialog struct {
	turn   string
	cancel context.CancelFunc
}

// cancelDialogs withdraws every open extension dialog. The host drops each
// card when its ctx ends, and elicitUIRequest answers pi cancelled.
func (s *session) cancelDialogs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.dialogs {
		d.cancel()
	}
}

// cancelTurnDialogs withdraws the open extension dialogs raised during turn.
func (s *session) cancelTurnDialogs(turn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.dialogs {
		if d.turn == turn {
			d.cancel()
		}
	}
}

func (s *session) elicitUIRequest(ctx context.Context, turn string, p uiRequest) {
	prompt := p.Title
	if p.Message != "" {
		prompt = strings.TrimSpace(p.Title + "\n" + p.Message)
	}

	field := map[string]any{"type": "string", "title": p.Title}
	switch p.Method {
	case "select":
		field["enum"] = p.Options
	case "confirm":
		field["enum"] = []string{"Yes", "No"}
	case "input":
		if p.Placeholder != "" {
			field["description"] = p.Placeholder
		}
	case "editor":
		if p.Prefill != "" {
			field["default"] = p.Prefill
		}
	}
	schema, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"value": field},
		"required":   []string{"value"},
	})

	respond := func(body map[string]any) {
		body["type"] = "extension_ui_response"
		body["id"] = p.ID
		_ = s.send(body)
	}

	result, err := s.host.Elicit(ctx, adapter.ElicitationRequest{
		TurnID: turn, Prompt: prompt, Schema: schema,
	})
	if err != nil || result.Action != "accept" {
		respond(map[string]any{"cancelled": true})
		return
	}
	var values struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(result.Value, &values)
	if p.Method == "confirm" {
		respond(map[string]any{"confirmed": values.Value == "Yes"})
		return
	}
	respond(map[string]any{"value": values.Value})
}

// ---- tool presentation ----

// toolItemID scopes pi's tool call id by turn. Tool items are folded
// session-wide like message blocks, and pi's ids are not unique across turns:
// its Mistral provider derives one from the call's index ("toolcall:0") when
// the API omits it, so every such turn would overwrite the same item.
func toolItemID(turn, toolCallID string) string {
	return turn + ":" + toolCallID
}

// toolKind maps pi's built-in tool names onto the canonical kinds. Unknown
// names — extension tools, MCP servers — are KindOther, not a guess.
func toolKind(name string) string {
	switch name {
	case "read":
		return proto.KindRead
	case "edit", "write", "multi_edit", "multiedit":
		return proto.KindEdit
	case "bash", "powershell":
		return proto.KindExecute
	case "grep", "glob", "find", "ls", "rg":
		return proto.KindSearch
	default:
		return proto.KindOther
	}
}

// toolTitle picks the one argument worth putting in a row title: the command
// for a shell, the path for file tools, the pattern for search. Falling back
// to the bare tool name beats dumping raw JSON at a human.
func toolTitle(name string, args json.RawMessage) string {
	var a struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		File    string `json:"file_path"`
		Pattern string `json:"pattern"`
	}
	_ = json.Unmarshal(args, &a)
	switch {
	case a.Command != "":
		return firstLine(a.Command)
	case a.Path != "":
		return name + " " + a.Path
	case a.File != "":
		return name + " " + a.File
	case a.Pattern != "":
		return name + ": " + a.Pattern
	default:
		return name
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// ---- failure classification ----

// authNeedles match the error text pi actually produces when a model has no
// usable credentials: AuthError's `No API key found for "..."`, models.js's
// "No API key for <provider/id>" and "Provider is not configured: ...", the
// auth check's "credentials_not_configured" reason, plus generic provider
// rejections for an expired or revoked key.
var authNeedles = []string{
	"no api key",
	"provider is not configured",
	"credentials_not_configured",
	"invalid api key",
	"authentication",
	"unauthorized",
	"oauth token",
	"status 401",
	" 401 ",
}

// failureKind classifies an error message for turn.finished, so a sign-in
// problem is offered a login rather than a retry.
func failureKind(msg string) string {
	lower := strings.ToLower(msg)
	for _, needle := range authNeedles {
		if strings.Contains(lower, needle) {
			return proto.FailureAuth
		}
	}
	return ""
}

// classify wraps an error whose text says the harness needs credentials, so
// the caller records the turn with the right failure kind.
func (s *session) classify(err error) error {
	if err == nil {
		return nil
	}
	if kind := failureKind(err.Error()); kind != "" {
		return &adapter.FailureError{Kind: kind, Err: err}
	}
	return err
}
