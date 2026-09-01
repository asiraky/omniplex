// Package piapp adapts Pi (`pi --mode rpc`, JSON lines over stdio) to the
// canonical event model.
//
// Pi's RPC mode is typed commands with an optional echoed id, not JSON-RPC, so
// this package carries its own line framing instead of internal/jsonrpc. The
// TUI is never scraped. Verified against @earendil-works/pi-coding-agent
// 0.84.4.
package piapp

import (
	"bufio"
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
// instance at its own directory is what makes it a second account.
func (a *Adapter) ConfigFields() []adapter.ConfigField {
	return []adapter.ConfigField{
		{
			Env:         "PI_CODING_AGENT_DIR",
			Label:       "Pi agent directory",
			Description: "Directory holding this account's Pi credentials and settings. Leave empty on the default instance to use the machine's own login.",
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
		"pi":    path,
		"piVer": strings.TrimSpace(string(out)),
		"auth":  authSummary(env),
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
		sid = o.SessionID
	}
	args := []string{"--mode", "rpc", "--session-id", sid}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--thinking", o.Effort)
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
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s --mode rpc: %w", a.Bin, err)
	}

	s := &session{
		host:    host,
		cmd:     cmd,
		stdin:   stdin,
		events:  make(chan proto.Emission, 256),
		done:    make(chan struct{}),
		rpcDone: make(chan struct{}),
		pending: map[string]chan rpcResponse{},
	}

	go s.readLoop(stdout)
	go s.drainStderr(stderr)
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
	s.emit(proto.Emit(proto.SessionConfigChanged, proto.SessionConfigChangedPayload{
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
	stdin io.WriteCloser

	events chan proto.Emission
	done   chan struct{}
	// rpcDone closes when the read loop ends — the process is gone or its
	// stdout broke — so a call waiting for a response that will never come
	// unblocks.
	rpcDone chan struct{}
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
	turnID  string
	// msgSeq counts assistant messages within the session so block ids stay
	// unique across a turn with several messages: pi's contentIndex restarts
	// at zero on each one.
	msgSeq int
	// lastStop and lastErr are the final assistant message's verdict, carried
	// from message_end to agent_settled — the turn boundary — where they
	// become the turn's stop reason.
	lastStop string
	lastErr  string
	// Running totals for usage.updated. Pi reports usage per assistant
	// message; the meter wants the session's cumulative spend.
	totIn, totOut, totCacheR, totCacheW int64
	totCost                             float64
	contextWindow                       int64
	// stderrTail keeps the last few stderr lines so an unexplained exit can at
	// least quote the harness's dying words.
	stderrTail []string
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
	id := uuid.NewString()
	cmd["id"] = id
	ch := make(chan rpcResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	if err := s.send(cmd); err != nil {
		return err
	}
	select {
	case res := <-ch:
		if !res.Success {
			return fmt.Errorf("%s: %s", res.Command, res.Error)
		}
		if out != nil && len(res.Data) > 0 {
			return json.Unmarshal(res.Data, out)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.rpcDone:
		return errors.New("pi exited before responding")
	}
}

func (s *session) readLoop(r io.Reader) {
	defer close(s.rpcDone)
	sc := bufio.NewScanner(r)
	// A single message_end can carry a whole assistant message, and a tool
	// result can carry a file; the default 64K token limit is far too small.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			continue
		}
		if head.Type == "response" {
			var res rpcResponse
			if err := json.Unmarshal(line, &res); err != nil {
				continue
			}
			s.mu.Lock()
			ch := s.pending[head.ID]
			s.mu.Unlock()
			if ch != nil {
				ch <- res
			}
			continue
		}
		s.handleEvent(head.Type, append([]byte(nil), line...))
	}
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

	// Pi takes image bytes inline, so each attachment is read from the host
	// path and base64'd. Failing the prompt beats silently dropping a
	// screenshot the human is about to ask a question about.
	images := make([]map[string]any, 0, len(in.Images))
	for _, img := range in.Images {
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
	cmd := map[string]any{"type": "prompt", "message": in.Text}
	if len(images) > 0 {
		cmd["images"] = images
	}
	if err := s.call(ctx, cmd, nil); err != nil {
		// The prompt was refused, so no agent_settled will ever clear this
		// turn id; leaving it set would make the collision guard above reject
		// every later prompt forever.
		s.clearTurn(in.TurnID)
		return s.classify(err)
	}
	return nil
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
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	idle := s.turnID == ""
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
		close(s.done)
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.cmd.Wait()
	})
	return nil
}

func (s *session) watchExit() {
	<-s.rpcDone
	s.mu.Lock()
	turn := s.turnID
	s.turnID = ""
	tail := strings.Join(s.stderrTail, "\n")
	s.mu.Unlock()
	if turn != "" {
		msg := "harness exited"
		if tail != "" {
			msg = "harness exited: " + tail
		}
		s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
			TurnID: turn, StopReason: proto.StopError, Error: msg,
			Failure: failureKind(tail),
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
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.host.Logf("pi stderr: %s", line)
		s.mu.Lock()
		s.stderrTail = append(s.stderrTail, line)
		if len(s.stderrTail) > 5 {
			s.stderrTail = s.stderrTail[1:]
		}
		s.mu.Unlock()
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
		s.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
			TurnID: s.ensureTurn(), ToolCallID: p.ToolCallID,
			Kind: toolKind(p.ToolName), Title: toolTitle(p.ToolName, p.Args),
			Status: proto.StatusInProgress, RawInput: p.Args,
		}))

	case "tool_execution_update":
		// Streamed partial results are dropped; tool_execution_end carries the
		// aggregate, which is what the timeline renders. Same choice as codex's
		// outputDelta.

	case "tool_execution_end":
		var p struct {
			ToolCallID string `json:"toolCallId"`
			IsError    bool   `json:"isError"`
			Result     struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return
		}
		status := proto.StatusCompleted
		if p.IsError {
			status = proto.StatusFailed
		}
		var content []proto.ToolContent
		for _, c := range p.Result.Content {
			if c.Type == "text" && c.Text != "" {
				content = append(content, proto.ToolContent{Type: "text", Text: c.Text})
			}
		}
		s.emit(proto.Emit(proto.ToolCallUpdated, proto.ToolCallUpdatedPayload{
			ToolCallID: p.ToolCallID, Status: status, Content: content,
		}))

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
		// contentIndex restarts per message, so the block id is scoped by the
		// message counter to keep two messages' first blocks apart.
		BlockID: fmt.Sprintf("m%d.b%d", seq, ev.ContentIndex),
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
	s.turnID = ""
	s.lastStop, s.lastErr = "", ""
	s.mu.Unlock()
	// A settle with no open turn is a straggler (an abort raced the settle);
	// emitting an empty turn id would confuse every projection downstream.
	if turn == "" {
		return
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
	var p struct {
		ID          string   `json:"id"`
		Method      string   `json:"method"`
		Title       string   `json:"title"`
		Message     string   `json:"message"`
		Placeholder string   `json:"placeholder"`
		Prefill     string   `json:"prefill"`
		Options     []string `json:"options"`
		NotifyType  string   `json:"notifyType"`
	}
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
		// read loop: a dialog would freeze every event until answered.
		go s.elicitUIRequest(p.ID, p.Method, p.Title, p.Message, p.Placeholder, p.Prefill, p.Options)
	}
}

func (s *session) elicitUIRequest(id, method, title, message, placeholder, prefill string, options []string) {
	prompt := title
	if message != "" {
		prompt = strings.TrimSpace(title + "\n" + message)
	}

	field := map[string]any{"type": "string", "title": title}
	switch method {
	case "select":
		field["enum"] = options
	case "confirm":
		field["enum"] = []string{"Yes", "No"}
	case "input":
		if placeholder != "" {
			field["description"] = placeholder
		}
	case "editor":
		if prefill != "" {
			field["default"] = prefill
		}
	}
	schema, _ := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"value": field},
		"required":   []string{"value"},
	})

	respond := func(body map[string]any) {
		body["type"] = "extension_ui_response"
		body["id"] = id
		_ = s.send(body)
	}

	result, err := s.host.Elicit(context.Background(), adapter.ElicitationRequest{
		TurnID: s.currentTurn(), Prompt: prompt, Schema: schema,
	})
	if err != nil || result.Action != "accept" {
		respond(map[string]any{"cancelled": true})
		return
	}
	var values struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(result.Value, &values)
	if method == "confirm" {
		respond(map[string]any{"confirmed": values.Value == "Yes"})
		return
	}
	respond(map[string]any{"value": values.Value})
}

// ---- tool presentation ----

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
