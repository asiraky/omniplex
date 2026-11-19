package thread

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// An agent is a long-running thread with a saved brief: a versioned system
// prompt the user owns, versioned compaction instructions, and a log of every
// compaction its threads went through. It has one live thread at a time; New
// thread archives that one and starts another on the same brief.

// agentPreamble sits between Omniplex's own instructions and the agent's
// prompt in every agent thread's system prompt. It tells the model how the
// brief works, so the prompt itself can be only about the job.
const agentPreamble = `You are a long-running agent. This conversation will run for a long time, and when the context fills up it is compacted: the older turns are replaced by a summary.

Your system prompt (the text after this preamble) is your durable brief. It is the only thing that survives every compaction intact, and the user owns it. Do not keep memories, notes files or rules you write for yourself; anything worth keeping for good belongs in the prompt, and only the user puts it there.

When the user asks you to remember a rule, change your job or update your prompt, call the propose_prompt_edit tool with the change and their reason, and the user approves or rejects it. Never call it otherwise, and never propose an edit on your own initiative.

A compaction carries only in-flight state: the task in hand, what was decided in this stretch of work, and what comes next. Durable rules do not belong in the summary; they belong in the prompt.`

// AgentPreamble is the fixed text between Omniplex's instructions and an
// agent's prompt, for the agent page to show.
func (m *Manager) AgentPreamble() string { return agentPreamble }

// composeAgentInstructions is an agent thread's whole system prompt.
func composeAgentInstructions(prompt string) string {
	return omniplexInstructions + "\n\n" + agentPreamble + "\n\n" + prompt
}

// agentDir is where an agent's files live, beside the database: the
// compaction instructions its harness reads, and the raw request log.
func agentDir(st *store.Store, agentID string) string {
	return filepath.Join(st.Dir(), "agents", agentID)
}

// writeCompactFile writes the compaction instructions the harness reads at
// each compaction and returns the file's path. A harness reads it when it
// compacts, not when it starts, so rewriting it on every compact revision is
// all a running thread needs.
func writeCompactFile(st *store.Store, agentID, body string) (string, error) {
	dir := agentDir(st, agentID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "compact.md")
	tmp := path + "." + uuid.NewString()[:8]
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// NewAgent is what creating an agent takes. Start also starts its first
// thread, with Kickoff as the first message.
type NewAgent struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Harness   string `json:"harness"`
	Instance  string `json:"instance,omitempty"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Mode      string `json:"mode,omitempty"`
	FolderID  string `json:"folderId,omitempty"`
	Workspace string `json:"workspace"`
	CompactAt int64  `json:"compactAt,omitempty"`
	Kickoff   string `json:"kickoff,omitempty"`
	RawLog    bool   `json:"rawLog,omitempty"`
	Prompt    string `json:"prompt"`
	Compact   string `json:"compact"`
	Start     bool   `json:"start"`
}

// AgentPatch is the settings save_agent changes; nil fields stay as they
// are. They apply from the next thread start: a running process keeps the
// settings it started with.
type AgentPatch struct {
	Name      *string `json:"name,omitempty"`
	Model     *string `json:"model,omitempty"`
	Effort    *string `json:"effort,omitempty"`
	Mode      *string `json:"mode,omitempty"`
	FolderID  *string `json:"folderId,omitempty"`
	Workspace *string `json:"workspace,omitempty"`
	CompactAt *int64  `json:"compactAt,omitempty"`
	Kickoff   *string `json:"kickoff,omitempty"`
	RawLog    *bool   `json:"rawLog,omitempty"`
}

// CompactionDetail is one compaction row with its bodies. For epoch 0 it
// also has the parts the thread's system prompt was composed from.
type CompactionDetail struct {
	store.Compaction
	System []SystemPart `json:"system,omitempty"`
}

// SystemPart is one part of an agent thread's system prompt: "omniplex",
// "preamble" or "prompt".
type SystemPart struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Chars int    `json:"chars"`
	Body  string `json:"body"`
}

// Restart outcomes of SaveAgentRevision.
const (
	RestartNone    = "none"
	RestartNow     = "now"
	RestartPending = "pending"
)

// errAgentArchived refuses changes to an agent that has been archived.
var errAgentArchived = errors.New("this agent has been archived")

func agentThreadRefusal(what string) error {
	return fmt.Errorf("an agent's thread cannot be %s; it belongs to its agent", what)
}

// refuseAgentThread is the error for doing what to an agent's thread, or nil
// when the thread is not one.
func (m *Manager) refuseAgentThread(ctx context.Context, threadID, what string) error {
	meta, err := m.store.Thread(ctx, threadID)
	if err != nil {
		return err
	}
	if meta.AgentID != "" {
		return agentThreadRefusal(what)
	}
	return nil
}

// Agents is every agent not archived.
func (m *Manager) Agents(ctx context.Context) ([]store.Agent, error) {
	return m.store.ListAgents(ctx)
}

// Agent is one agent, archived or not.
func (m *Manager) Agent(ctx context.Context, id string) (store.Agent, error) {
	return m.store.Agent(ctx, id)
}

// liveAgent is the agent, refused once archived.
func (m *Manager) liveAgent(ctx context.Context, id string) (store.Agent, error) {
	ag, err := m.store.Agent(ctx, id)
	if err != nil {
		return ag, err
	}
	if ag.ArchivedAt != 0 {
		return ag, errAgentArchived
	}
	return ag, nil
}

// checkAgentSettings refuses settings no thread could start from.
func (m *Manager) checkAgentSettings(ctx context.Context, ag store.Agent) error {
	if strings.TrimSpace(ag.Name) == "" {
		return errors.New("an agent needs a name")
	}
	if ag.Workspace != "local" && ag.Workspace != "managed" {
		return fmt.Errorf("unknown workspace mode %q", ag.Workspace)
	}
	if ag.CompactAt < 0 {
		return errors.New("compact-at cannot be negative")
	}
	if ag.FolderID != "" {
		if _, _, err := m.folder(ctx, ag.ProjectID, ag.FolderID); err != nil {
			return err
		}
	}
	return nil
}

// CreateAgent saves a new agent with its first prompt and compaction
// instructions, r1 and c1. With Start it also starts its first thread, whose
// id comes back; otherwise threadID is "".
func (m *Manager) CreateAgent(ctx context.Context, n NewAgent) (store.Agent, string, error) {
	ag := store.Agent{ProjectID: n.ProjectID, Name: strings.TrimSpace(n.Name), Harness: n.Harness, Instance: n.Instance, Model: n.Model, Effort: n.Effort, Mode: n.Mode, FolderID: n.FolderID, Workspace: n.Workspace, CompactAt: n.CompactAt, Kickoff: n.Kickoff, RawLog: n.RawLog}
	if ag.Workspace == "" {
		ag.Workspace = "local"
	}
	if _, err := m.store.Project(ctx, ag.ProjectID); err != nil {
		return store.Agent{}, "", err
	}
	if err := m.checkAgentSettings(ctx, ag); err != nil {
		return store.Agent{}, "", err
	}
	if _, err := m.resolveInstance(ag.Instance, ag.Harness); err != nil {
		return store.Agent{}, "", err
	}
	ag, err := m.store.CreateAgent(ctx, ag, n.Prompt, n.Compact)
	if err != nil {
		return store.Agent{}, "", err
	}
	if _, err := writeCompactFile(m.store, ag.ID, n.Compact); err != nil {
		m.logf("compaction instructions of agent %s: %v", ag.ID, err)
	}
	m.notifyAgents()
	if !n.Start {
		return ag, "", nil
	}
	threadID, err := m.startAgentThread(ctx, ag, n.Kickoff)
	if err != nil {
		// The agent is saved either way; New thread retries the start.
		return ag, "", err
	}
	ag, err = m.store.Agent(ctx, ag.ID)
	return ag, threadID, err
}

// startAgentThread creates the agent's live thread on its settings, logs its
// epoch 0 and sends the first message, if there is one. It is created through
// the ordinary project path, so provisioning, workspaces and leases are a
// project thread's. A first message that fails to send does not fail the
// start: the thread exists, and a retry would make a second one.
func (m *Manager) startAgentThread(ctx context.Context, ag store.Agent, first string) (string, error) {
	a, err := m.CreateProject(ctx, CreateProjectOptions{
		ProjectID: ag.ProjectID, Title: ag.Name, Harness: ag.Harness, Instance: ag.Instance,
		Model: ag.Model, Mode: ag.Mode, Effort: ag.Effort, AgentSettingsExplicit: true,
		Workspace: ag.Workspace, FolderID: ag.FolderID, AgentID: ag.ID,
	})
	if err != nil {
		return "", err
	}
	if _, err := m.store.AddCompaction(ctx, store.Compaction{AgentID: ag.ID, ThreadID: a.ID, Trigger: "start", PromptRev: ag.PromptRev, CompactRev: ag.CompactRev, Summary: first}); err != nil {
		m.logf("epoch 0 of %s: %v", a.ID, err)
	}
	if err := m.store.SetAgentThread(ctx, ag.ID, a.ID); err != nil {
		return a.ID, err
	}
	m.notifyAgents()
	if strings.TrimSpace(first) != "" {
		if _, err := a.Prompt(ctx, first, nil); err != nil {
			m.logf("first message of agent thread %s: %v", a.ID, err)
		}
		m.notifyList()
	}
	return a.ID, nil
}

// SaveAgent changes an agent's settings. A change of folder or workspace
// applies to its next thread; the rest to its live thread's next start.
func (m *Manager) SaveAgent(ctx context.Context, id string, p AgentPatch) (store.Agent, error) {
	ag, err := m.liveAgent(ctx, id)
	if err != nil {
		return ag, err
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&ag.Name, p.Name)
	set(&ag.Model, p.Model)
	set(&ag.Effort, p.Effort)
	set(&ag.Mode, p.Mode)
	set(&ag.FolderID, p.FolderID)
	set(&ag.Workspace, p.Workspace)
	set(&ag.Kickoff, p.Kickoff)
	if p.CompactAt != nil {
		ag.CompactAt = *p.CompactAt
	}
	if p.RawLog != nil {
		ag.RawLog = *p.RawLog
	}
	ag.Name = strings.TrimSpace(ag.Name)
	if err := m.checkAgentSettings(ctx, ag); err != nil {
		return store.Agent{}, err
	}
	if err := m.store.UpdateAgent(ctx, ag); err != nil {
		return store.Agent{}, err
	}
	m.notifyAgents()
	return m.store.Agent(ctx, id)
}

// ArchiveAgent closes the agent's live thread and takes the agent off the
// list. Its threads, revisions and log are kept.
func (m *Manager) ArchiveAgent(ctx context.Context, id string) error {
	ag, err := m.liveAgent(ctx, id)
	if err != nil {
		return err
	}
	if ag.ThreadID != "" {
		if err := m.Cleanup(ctx, ag.ThreadID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("closing the agent's thread: %w", err)
		}
	}
	if err := m.store.ArchiveAgent(ctx, id); err != nil {
		return err
	}
	m.notifyAgents()
	return nil
}

// AgentNewThread archives the agent's live thread, if it has one, with the
// ordinary close, and starts a fresh one on its current brief with this
// first message.
func (m *Manager) AgentNewThread(ctx context.Context, id, firstMessage string) (string, error) {
	ag, err := m.liveAgent(ctx, id)
	if err != nil {
		return "", err
	}
	if old := ag.ThreadID; old != "" {
		// Unlinked first, so nothing the closing thread does on its way out
		// reads as the agent's live thread.
		if err := m.store.SetAgentThread(ctx, id, ""); err != nil {
			return "", err
		}
		if err := m.Cleanup(ctx, old); err != nil && !errors.Is(err, store.ErrNotFound) {
			_ = m.store.SetAgentThread(ctx, id, old)
			return "", fmt.Errorf("closing the agent's thread: %w", err)
		}
		m.notifyAgents()
	}
	return m.startAgentThread(ctx, ag, firstMessage)
}

// AgentRevisions is the agent's revisions of one kind, newest first.
func (m *Manager) AgentRevisions(ctx context.Context, id, kind string) ([]store.AgentRevision, error) {
	if _, err := m.store.Agent(ctx, id); err != nil {
		return nil, err
	}
	return m.store.AgentRevisions(ctx, id, kind)
}

// SaveAgentRevision saves a new revision of the agent's prompt or compaction
// instructions; reverting is saving an old body again. New compaction
// instructions are rewritten to the file the harness reads at its next
// compaction, which needs no restart. A new prompt restarts the live thread
// onto it: now when it is idle, once its turn ends when busy. restart says
// which, or "none" when there was no running thread to restart; such a
// thread picks the prompt up when it next starts.
func (m *Manager) SaveAgentRevision(ctx context.Context, id, kind, body, author, reason string) (store.AgentRevision, string, error) {
	ag, err := m.liveAgent(ctx, id)
	if err != nil {
		return store.AgentRevision{}, "", err
	}
	rev, err := m.store.AddAgentRevision(ctx, id, kind, body, author, reason)
	if err != nil {
		return rev, "", err
	}
	m.notifyAgents()
	if kind == store.RevisionCompact {
		if _, err := writeCompactFile(m.store, id, body); err != nil {
			return rev, RestartNone, fmt.Errorf("saved as c%d, but the file the harness reads could not be written: %w", rev.N, err)
		}
		return rev, RestartNone, nil
	}
	if ag.ThreadID == "" {
		return rev, RestartNone, nil
	}
	pending, err := m.RestartThread(ctx, ag.ThreadID)
	switch {
	case errors.Is(err, ErrNotReady), errors.Is(err, ErrClosed), errors.Is(err, store.ErrNotFound):
		// Nothing running: provisioning, closed, or gone. The next start
		// composes the prompt from the latest revision anyway.
		return rev, RestartNone, nil
	case err != nil:
		return rev, RestartNone, fmt.Errorf("saved as r%d, but the thread could not be restarted onto it: %w", rev.N, err)
	case pending:
		return rev, RestartPending, nil
	}
	return rev, RestartNow, nil
}

// AgentLog is one of the agent's threads' compaction log, metadata only,
// and the agent's archived threads. An empty threadID is the live thread,
// whose id comes back; with no live thread the log is empty.
func (m *Manager) AgentLog(ctx context.Context, id, threadID string) (string, []store.Compaction, []store.ArchivedAgentThread, error) {
	ag, err := m.store.Agent(ctx, id)
	if err != nil {
		return "", nil, nil, err
	}
	if threadID == "" {
		threadID = ag.ThreadID
	} else if meta, err := m.store.Thread(ctx, threadID); err != nil {
		return "", nil, nil, err
	} else if meta.AgentID != id {
		return "", nil, nil, fmt.Errorf("thread %s is not this agent's: %w", threadID, store.ErrNotFound)
	}
	rows := []store.Compaction{}
	if threadID != "" {
		if rows, err = m.store.ThreadCompactions(ctx, threadID); err != nil {
			return "", nil, nil, err
		}
	}
	archived, err := m.store.ArchivedAgentThreads(ctx, id, ag.ThreadID)
	if err != nil {
		return "", nil, nil, err
	}
	if archived == nil {
		archived = []store.ArchivedAgentThread{}
	}
	return threadID, rows, archived, nil
}

// AgentCompaction is one row of the agent's log with its bodies. Epoch 0
// also lists the parts of the system prompt the thread started on, at the
// prompt revision recorded on the row.
func (m *Manager) AgentCompaction(ctx context.Context, id string, compactionID int64) (CompactionDetail, error) {
	c, err := m.store.Compaction(ctx, compactionID)
	if err != nil {
		return CompactionDetail{}, err
	}
	if c.AgentID != id {
		return CompactionDetail{}, fmt.Errorf("compaction %d is not this agent's: %w", compactionID, store.ErrNotFound)
	}
	d := CompactionDetail{Compaction: c}
	if c.Epoch != 0 {
		return d, nil
	}
	part := func(kind, name, body string) SystemPart {
		return SystemPart{Kind: kind, Name: name, Chars: len(body), Body: body}
	}
	d.System = []SystemPart{part("omniplex", "Omniplex", omniplexInstructions), part("preamble", "Agent preamble", agentPreamble)}
	revs, err := m.store.AgentRevisions(ctx, id, store.RevisionPrompt)
	if err != nil {
		return d, err
	}
	for _, r := range revs {
		if r.N == c.PromptRev {
			d.System = append(d.System, part("prompt", fmt.Sprintf("r%d", r.N), r.Body))
			break
		}
	}
	return d, nil
}

// ---- agent-list change notifications ----

// SubscribeAgents registers for changes to the agents list, any field of any
// agent as the agents frame carries it.
func (m *Manager) SubscribeAgents() (string, chan struct{}) {
	id := uuid.NewString()
	ch := make(chan struct{}, 1)
	m.listMu.Lock()
	m.agentSub[id] = ch
	m.listMu.Unlock()
	return id, ch
}

func (m *Manager) UnsubscribeAgents(id string) {
	m.listMu.Lock()
	delete(m.agentSub, id)
	m.listMu.Unlock()
}

func (m *Manager) notifyAgents() {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	for _, ch := range m.agentSub {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ---- the actor's side ----

// agentOf is the agent this thread belongs to, or false. It may be one of
// the agent's archived threads; callers that care compare ThreadID.
func (a *Actor) agentOf(ctx context.Context) (store.Agent, bool) {
	meta, err := a.store.Thread(ctx, a.ID)
	if err != nil || meta.AgentID == "" {
		return store.Agent{}, false
	}
	ag, err := a.store.Agent(ctx, meta.AgentID)
	if err != nil {
		a.logf("agent %s of %s: %v", meta.AgentID, a.ID, err)
		return store.Agent{}, false
	}
	return ag, true
}

func (a *Actor) notifyAgents() {
	a.mu.Lock()
	notify := a.onAgents
	a.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// startedOnPrompt records the prompt revision a process of the agent's live
// thread started on, which is how the agent page tells a restart still to
// come from one done. A thread the agent has just been unlinked from, or not
// yet linked to, counts as live: New thread links the new one only once it
// exists, and its process may start before that.
func (a *Actor) startedOnPrompt(ctx context.Context, x *agentExtras) {
	ag, err := a.store.Agent(ctx, x.id)
	if err != nil || (ag.ThreadID != a.ID && ag.ThreadID != "") {
		return
	}
	if err := a.store.SetAgentLivePromptRev(ctx, x.id, x.promptRev); err != nil {
		a.logf("live prompt of agent %s: %v", x.id, err)
		return
	}
	a.notifyAgents()
}

// recordCompaction adds a compaction of an agent thread to the agent's log
// and stamps the event with its epoch and row, so the transcript's notice
// and the log point at each other. Any other thread's compaction passes
// through as it came. The instructions recorded are the ones the harness
// read: the latest compact revision is what its file holds.
func (a *Actor) recordCompaction(ctx context.Context, em proto.Emission) (proto.Emission, int64) {
	var p proto.ContextCompactedPayload
	switch v := em.Payload.(type) {
	case proto.ContextCompactedPayload:
		p = v
	case *proto.ContextCompactedPayload:
		p = *v
	default:
		return em, 0
	}
	ag, ok := a.agentOf(ctx)
	if !ok {
		return em, 0
	}
	c := store.Compaction{AgentID: ag.ID, ThreadID: a.ID, Trigger: p.Trigger, PreTokens: p.PreTokens, PostTokens: p.PostTokens, DurationMs: p.DurationMs, PromptRev: ag.LivePromptRev, CompactRev: ag.CompactRev}
	if c.Trigger != "manual" {
		c.Trigger = "auto"
	}
	if c.PromptRev == 0 {
		c.PromptRev = ag.PromptRev
	}
	if rev, err := a.store.LatestAgentRevision(ctx, ag.ID, store.RevisionCompact); err == nil {
		c.Instructions = rev.Body
	}
	c, err := a.store.AddCompaction(ctx, c)
	if err != nil {
		a.logf("compaction log of %s: %v", a.ID, err)
		return em, 0
	}
	p.Epoch, p.CompactionID = c.Epoch, c.ID
	em.Payload = p
	return em, c.ID
}

// compactionLogged points the compaction's row at its notice in the thread.
func (a *Actor) compactionLogged(ctx context.Context, compactionID, seq int64) {
	if err := a.store.SetCompactionBoundary(ctx, compactionID, seq); err != nil {
		a.logf("compaction boundary of %s: %v", a.ID, err)
	}
	a.notifyAgents()
}

// fileCompactionDetail files what the harness said about a compaction on
// the agent's row for it: the thread's latest, or its epoch 0 for what the
// first request carried. A thread that is no agent's has no log to keep it
// in, and it is dropped.
func (a *Actor) fileCompactionDetail(ctx context.Context, em proto.Emission) {
	var p proto.CompactionDetailPayload
	switch v := em.Payload.(type) {
	case proto.CompactionDetailPayload:
		p = v
	case *proto.CompactionDetailPayload:
		p = *v
	default:
		return
	}
	meta, err := a.store.Thread(ctx, a.ID)
	if err != nil || meta.AgentID == "" {
		return
	}
	if err := a.store.FileCompactionDetail(ctx, a.ID, p.Start, p.Summary, p.Attachments); err != nil {
		a.logf("compaction detail of %s: %v", a.ID, err)
	}
}

// writeThroughToAgent saves a setting changed on the agent's live thread to
// the agent, so its next thread starts the way this one was left.
func (a *Actor) writeThroughToAgent(changed proto.ThreadConfigChangedPayload) {
	ctx := context.Background()
	ag, ok := a.agentOf(ctx)
	if !ok || ag.ThreadID != a.ID {
		return
	}
	if changed.Model != "" {
		ag.Model = changed.Model
	}
	if changed.Mode != "" {
		ag.Mode = changed.Mode
	}
	if changed.Effort != nil {
		ag.Effort = *changed.Effort
	}
	if err := a.store.UpdateAgent(ctx, ag); err != nil {
		a.logf("agent %s settings from %s: %v", ag.ID, a.ID, err)
		return
	}
	a.notifyAgents()
}
