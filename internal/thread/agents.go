package thread

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/adapter"
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

// writeCompactFile writes the compaction instructions the harness reads and
// returns the file's path. When it reads them is the harness's
// (adapter.CompactReads): Claude at each compaction, so rewriting the file is
// all its running thread needs; Codex once at start, so its thread takes a
// new revision only from its next process.
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
// are. See SaveAgent for when each applies.
type AgentPatch = store.AgentPatch

// AgentStart is the thread an agent was started on, and the error its first
// message failed with, if it did. The thread exists either way: a retry
// would make a second one.
type AgentStart struct {
	ThreadID    string
	PromptError string
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

// lockAgent serialises what changes an agent and its live thread together:
// starting, replacing and closing the thread, and saving settings and
// revisions. It returns the unlock. Nothing the thread's actor does on its
// loop takes it, so holding it across a call to the actor cannot deadlock.
func (m *Manager) lockAgent(id string) func() {
	v, _ := m.agentLocks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// compactUse is how the agent's harness uses its compaction instructions.
func (m *Manager) compactUse(harness string) adapter.CompactInstructionsUse {
	ad, ok := m.drivers[harness]
	if !ok {
		return adapter.CompactInstructionsUse{}
	}
	return adapter.CompactInstructionsOf(ad)
}

// checkCompactLength refuses compaction instructions longer than the harness
// takes. Claude would cut them off silently, and the log would claim the
// whole text was sent.
func (m *Manager) checkCompactLength(harness, body string) error {
	use := m.compactUse(harness)
	if use.MaxChars == 0 {
		return nil
	}
	if n := len(utf16.Encode([]rune(body))); n > use.MaxChars {
		name := harness
		if ad, ok := m.drivers[harness]; ok {
			name = ad.Meta().Name
		}
		return fmt.Errorf("%s takes at most %d characters of compaction instructions, and these are %d; shorten them", name, use.MaxChars, n)
	}
	return nil
}

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
// instructions, r1 and c1. With Start it also starts its first thread, which
// comes back with its first message's error, if that failed to send;
// otherwise the start is empty. Instructions longer than the harness takes
// are refused.
func (m *Manager) CreateAgent(ctx context.Context, n NewAgent) (store.Agent, AgentStart, error) {
	ag := store.Agent{ProjectID: n.ProjectID, Name: strings.TrimSpace(n.Name), Harness: n.Harness, Instance: n.Instance, Model: n.Model, Effort: n.Effort, Mode: n.Mode, FolderID: n.FolderID, Workspace: n.Workspace, CompactAt: n.CompactAt, Kickoff: n.Kickoff, RawLog: n.RawLog}
	if ag.Workspace == "" {
		ag.Workspace = "local"
	}
	if _, err := m.store.Project(ctx, ag.ProjectID); err != nil {
		return store.Agent{}, AgentStart{}, err
	}
	if err := m.checkAgentSettings(ctx, ag); err != nil {
		return store.Agent{}, AgentStart{}, err
	}
	if _, err := m.resolveInstance(ag.Instance, ag.Harness); err != nil {
		return store.Agent{}, AgentStart{}, err
	}
	if err := m.checkCompactLength(ag.Harness, n.Compact); err != nil {
		return store.Agent{}, AgentStart{}, err
	}
	ag, err := m.store.CreateAgent(ctx, ag, n.Prompt, n.Compact)
	if err != nil {
		return store.Agent{}, AgentStart{}, err
	}
	defer m.lockAgent(ag.ID)()
	if _, err := writeCompactFile(m.store, ag.ID, n.Compact); err != nil {
		m.logf("compaction instructions of agent %s: %v", ag.ID, err)
	}
	m.notifyAgents()
	if !n.Start {
		return ag, AgentStart{}, nil
	}
	start, err := m.startAgentThread(ctx, ag, n.Kickoff)
	if err != nil {
		// The agent is saved either way; New thread retries the start.
		return ag, start, err
	}
	ag, err = m.store.Agent(ctx, ag.ID)
	return ag, start, err
}

// startAgentThread creates the agent's live thread on its settings, logs its
// epoch 0 and sends the first message, if there is one. It is created through
// the ordinary project path, so provisioning, workspaces and leases are a
// project thread's. A first message that fails to send does not fail the
// start, since the thread exists and a retry would make a second one; its
// error comes back as the start's PromptError. The caller holds the agent's
// lock.
func (m *Manager) startAgentThread(ctx context.Context, ag store.Agent, first string) (AgentStart, error) {
	a, err := m.CreateProject(ctx, CreateProjectOptions{
		ProjectID: ag.ProjectID, Title: ag.Name, Harness: ag.Harness, Instance: ag.Instance,
		Model: ag.Model, Mode: ag.Mode, Effort: ag.Effort, AgentSettingsExplicit: true,
		Workspace: ag.Workspace, FolderID: ag.FolderID, AgentID: ag.ID,
	})
	if err != nil {
		return AgentStart{}, err
	}
	start := AgentStart{ThreadID: a.ID}
	compactRev := ag.CompactRev
	if m.compactUse(ag.Harness).Reads == adapter.CompactReadsNever {
		compactRev = 0
	}
	if _, err := m.store.AddCompaction(ctx, store.Compaction{AgentID: ag.ID, ThreadID: a.ID, Trigger: "start", PromptRev: ag.PromptRev, CompactRev: compactRev, Summary: first}); err != nil {
		m.logf("epoch 0 of %s: %v", a.ID, err)
	}
	if err := m.store.SetAgentThread(ctx, ag.ID, a.ID); err != nil {
		return start, err
	}
	m.notifyAgents()
	if strings.TrimSpace(first) != "" {
		if _, err := a.Prompt(ctx, first, nil); err != nil {
			m.logf("first message of agent thread %s: %v", a.ID, err)
			start.PromptError = err.Error()
		}
		m.notifyList()
	}
	return start, nil
}

// SaveAgent changes an agent's settings. A change of folder or workspace
// applies to its next thread. Model, effort and permission mode apply to its
// live thread too, the way the composer changes them: a running harness is
// switched now, and a thread with no process running has them recorded for
// its next start. A new name retitles the live thread.
func (m *Manager) SaveAgent(ctx context.Context, id string, p AgentPatch) (store.Agent, error) {
	defer m.lockAgent(id)()
	old, err := m.liveAgent(ctx, id)
	if err != nil {
		return old, err
	}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		p.Name = &name
	}
	ag := p.Apply(old)
	if err := m.checkAgentSettings(ctx, ag); err != nil {
		return store.Agent{}, err
	}
	if err := m.store.PatchAgent(ctx, id, p); err != nil {
		return store.Agent{}, err
	}
	m.notifyAgents()
	applyErr := m.applyToLiveThread(ctx, old, ag)
	saved, err := m.store.Agent(ctx, id)
	if err == nil && applyErr != nil {
		err = fmt.Errorf("saved, but the agent's thread could not take the change: %w", applyErr)
	}
	return saved, err
}

// applyToLiveThread hands the live thread what changed from old to ag that
// it can take now: its title, and its model, effort and mode. A model or
// mode cleared back to the harness's default cannot be switched to mid-thread
// and waits for the next thread.
func (m *Manager) applyToLiveThread(ctx context.Context, old, ag store.Agent) error {
	if ag.ThreadID == "" {
		return nil
	}
	if ag.Name != old.Name {
		// Straight to the store: RenameThread refuses an agent's thread, as
		// the user renames the agent rather than its thread.
		if err := m.store.RenameThread(ctx, ag.ThreadID, normaliseTitle(ag.Name)); err != nil && !errors.Is(err, store.ErrNotFound) {
			m.logf("title of agent thread %s: %v", ag.ThreadID, err)
		}
		m.notifyList()
	}
	var s agentSettings
	if ag.Model != old.Model && ag.Model != "" {
		s.model = &ag.Model
	}
	if ag.Mode != old.Mode && ag.Mode != "" {
		s.mode = &ag.Mode
	}
	if ag.Effort != old.Effort {
		s.effort = &ag.Effort
	}
	if s == (agentSettings{}) {
		return nil
	}
	a, err := m.View(ctx, ag.ThreadID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	err = a.adoptAgentSettings(ctx, s)
	if errors.Is(err, ErrClosed) || errors.Is(err, ErrNotReady) {
		// Closed, or provisioning: its next thread starts on the agent's.
		return nil
	}
	return err
}

// ArchiveAgent closes the agent's live thread and takes the agent off the
// list. Its threads, revisions and log are kept.
func (m *Manager) ArchiveAgent(ctx context.Context, id string) error {
	defer m.lockAgent(id)()
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
func (m *Manager) AgentNewThread(ctx context.Context, id, firstMessage string) (AgentStart, error) {
	defer m.lockAgent(id)()
	ag, err := m.liveAgent(ctx, id)
	if err != nil {
		return AgentStart{}, err
	}
	if old := ag.ThreadID; old != "" {
		// Unlinked first, so nothing the closing thread does on its way out
		// reads as the agent's live thread.
		if err := m.store.SetAgentThread(ctx, id, ""); err != nil {
			return AgentStart{}, err
		}
		if err := m.Cleanup(ctx, old); err != nil && !errors.Is(err, store.ErrNotFound) {
			_ = m.store.SetAgentThread(ctx, id, old)
			return AgentStart{}, fmt.Errorf("closing the agent's thread: %w", err)
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
// instructions; reverting is saving an old body again. base, when above 0,
// is the revision the edit started from, and the save is refused if another
// has been saved since (store.RevisionMovedError).
//
// A new prompt restarts the live thread onto it: now when it is idle, once
// its turn ends when busy. New compaction instructions are rewritten to the
// file the harness reads, and what that takes depends on when it reads it: a
// harness that reads it at each compaction needs nothing more; one that reads
// it at start is restarted like a prompt; one that never reads it keeps them
// for nothing. restart says which: "now", "pending", or "none" when there was
// nothing to restart. A thread with no process running picks the revision up
// when it next starts.
func (m *Manager) SaveAgentRevision(ctx context.Context, id, kind, body, author, reason string, base int) (store.AgentRevision, string, error) {
	defer m.lockAgent(id)()
	ag, err := m.liveAgent(ctx, id)
	if err != nil {
		return store.AgentRevision{}, "", err
	}
	if kind == store.RevisionCompact {
		if err := m.checkCompactLength(ag.Harness, body); err != nil {
			return store.AgentRevision{}, "", err
		}
	}
	rev, err := m.store.AddAgentRevision(ctx, id, kind, body, author, reason, base)
	if err != nil {
		return rev, "", err
	}
	m.notifyAgents()
	letter := "r"
	if kind == store.RevisionCompact {
		letter = "c"
		if _, err := writeCompactFile(m.store, id, body); err != nil {
			return rev, RestartNone, fmt.Errorf("saved as c%d, but the file the harness reads could not be written: %w", rev.N, err)
		}
		if m.compactUse(ag.Harness).Reads != adapter.CompactReadsAtStart {
			return rev, RestartNone, nil
		}
	}
	if ag.ThreadID == "" {
		return rev, RestartNone, nil
	}
	pending, err := m.restartLive(ctx, ag.ThreadID)
	switch {
	case errors.Is(err, ErrNotReady), errors.Is(err, ErrClosed), errors.Is(err, store.ErrNotFound):
		// Nothing running: provisioning, closed, or gone. The next start
		// reads the latest revisions anyway.
		return rev, RestartNone, nil
	case err != nil:
		return rev, RestartNone, fmt.Errorf("saved as %s%d, but the thread could not be restarted onto it: %w", letter, rev.N, err)
	case pending:
		return rev, RestartPending, nil
	}
	return rev, RestartNow, nil
}

// CheckAgentCompact refuses compaction instructions longer than the agent's
// harness takes, for a caller about to propose them.
func (m *Manager) CheckAgentCompact(ctx context.Context, id, body string) error {
	ag, err := m.store.Agent(ctx, id)
	if err != nil {
		return err
	}
	return m.checkCompactLength(ag.Harness, body)
}

// AgentCompactReads is when the agent's harness reads its compaction
// instructions.
func (m *Manager) AgentCompactReads(ctx context.Context, id string) (adapter.CompactReads, error) {
	ag, err := m.store.Agent(ctx, id)
	if err != nil {
		return adapter.CompactReadsNever, err
	}
	return m.compactUse(ag.Harness).Reads, nil
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

// agentRevisionsBehind is whether this is an agent's live thread whose last
// process started on an older prompt than the agent's latest, or on older
// compaction instructions when its harness reads them only at start: its
// next start has to restart onto them rather than resume on what the
// harness pinned.
func (a *Actor) agentRevisionsBehind(ctx context.Context) bool {
	ag, ok := a.agentOf(ctx)
	if !ok || ag.ThreadID != a.ID || ag.LivePromptRev == 0 {
		return false
	}
	if ag.LivePromptRev < ag.PromptRev {
		return true
	}
	return adapter.CompactInstructionsOf(a.adapter).Reads == adapter.CompactReadsAtStart && ag.LiveCompactRev < ag.CompactRev
}

func (a *Actor) notifyAgents() {
	a.mu.Lock()
	notify := a.onAgents
	a.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// startedOn records the revisions a process of the agent's live thread
// started on: the prompt's, which is how the agent page tells a restart
// still to come from one done, and the compaction instructions', which are
// what a harness that reads them only at start compacts on. It also notes
// whether this start moved the thread onto a newer prompt, which its restart
// notice names. A thread the
// agent has just been unlinked from, or not yet linked to, counts as live:
// New thread links the new one only once it exists, and its process may
// start before that.
func (a *Actor) startedOn(ctx context.Context, x *agentExtras) {
	ag, err := a.store.Agent(ctx, x.id)
	if err != nil || (ag.ThreadID != a.ID && ag.ThreadID != "") {
		return
	}
	if ag.LivePromptRev != 0 && ag.LivePromptRev != x.promptRev {
		a.promptMovedTo = x.promptRev
	}
	if err := a.store.SetAgentLiveRevs(ctx, x.id, x.promptRev, x.compactRev); err != nil {
		a.logf("live revisions of agent %s: %v", x.id, err)
		return
	}
	a.notifyAgents()
}

// recordCompaction adds a compaction of an agent thread to the agent's log
// and stamps the event with its epoch and row, so the transcript's notice
// and the log point at each other. Any other thread's compaction passes
// through as it came. The instructions recorded are the ones the harness
// compacted on, which depends on when it reads them: the latest revision for
// one that reads its file at each compaction, the one its process started on
// for one that reads it at start, and none for one that never does.
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
	c := store.Compaction{AgentID: ag.ID, ThreadID: a.ID, Trigger: p.Trigger, PreTokens: p.PreTokens, PostTokens: p.PostTokens, DurationMs: p.DurationMs, PromptRev: ag.LivePromptRev}
	if c.Trigger != "manual" {
		c.Trigger = "auto"
	}
	if c.PromptRev == 0 {
		c.PromptRev = ag.PromptRev
	}
	var rev store.AgentRevision
	var err error
	switch adapter.CompactInstructionsOf(a.adapter).Reads {
	case adapter.CompactReadsAtCompaction:
		rev, err = a.store.LatestAgentRevision(ctx, ag.ID, store.RevisionCompact)
	case adapter.CompactReadsAtStart:
		n := ag.LiveCompactRev
		if n == 0 {
			n = ag.CompactRev
		}
		rev, err = a.store.AgentRevision(ctx, ag.ID, store.RevisionCompact, n)
	}
	if err != nil {
		a.logf("compaction instructions of agent %s: %v", ag.ID, err)
	}
	c.CompactRev, c.Instructions = rev.N, rev.Body
	c, err = a.store.AddCompaction(ctx, c)
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
		return
	}
	a.notifyAgents()
}

// firstMessageLogged fills the agent's epoch 0 with the thread's first
// message, for a thread started without one: its brief's opening is then the
// message the user typed. Only once per actor, and only while epoch 0 is
// empty.
func (a *Actor) firstMessageLogged(ctx context.Context, text string) {
	if a.startFilled || strings.TrimSpace(text) == "" {
		return
	}
	a.startFilled = true
	meta, err := a.store.Thread(ctx, a.ID)
	if err != nil || meta.AgentID == "" {
		return
	}
	ok, err := a.store.FillStartMessage(ctx, a.ID, text)
	if err != nil {
		a.logf("first message of agent thread %s: %v", a.ID, err)
		return
	}
	if ok {
		a.notifyAgents()
	}
}

// restartLogged mirrors onto the agent whether its live thread has a restart
// waiting, which the agent page shows.
func (a *Actor) restartLogged(ctx context.Context, pending bool) {
	changed, err := a.store.SetThreadRestartPending(ctx, a.ID, pending)
	if err != nil {
		a.logf("restart pending of agent thread %s: %v", a.ID, err)
		return
	}
	if changed {
		a.notifyAgents()
	}
}

// agentSettings are an agent's settings for its live thread to take; nil
// fields are unchanged.
type agentSettings struct {
	model, mode, effort *string
}

// adoptAgentSettings hands the thread settings saved on its agent, the way
// the composer changes them. ErrClosed and ErrNotReady mean there is nothing
// to change: a closed thread, or one still being provisioned, whose process
// starts on the agent's settings.
func (a *Actor) adoptAgentSettings(ctx context.Context, s agentSettings) error {
	_, err := a.call(ctx, command{kind: cmdAgentSettings, agentSettings: &s})
	return err
}

// takeAgentSettings is adoptAgentSettings on the loop. A thread with no
// process records the settings, which its next process starts on (restart,
// activate). A running one switches its harness to each, one setting call
// apiece, and the reply is the first that failed.
func (a *Actor) takeAgentSettings(s agentSettings, reply chan cmdResult) {
	if a.state.Closed {
		reply <- cmdResult{err: ErrClosed}
		return
	}
	if a.sess == nil && a.preparing {
		reply <- cmdResult{err: ErrNotReady}
		return
	}
	type change struct {
		what    string
		payload proto.ThreadConfigChangedPayload
		call    func(adapter.Session) (func(context.Context) error, bool)
	}
	var changes []change
	if s.model != nil && *s.model != a.state.Model {
		v := *s.model
		changes = append(changes, change{"model", proto.ThreadConfigChangedPayload{Model: v}, func(sess adapter.Session) (func(context.Context) error, bool) {
			sw, ok := sess.(adapter.ModelSwitcher)
			return func(ctx context.Context) error { return sw.SetModel(ctx, v) }, ok
		}})
	}
	if s.mode != nil && *s.mode != a.state.Mode {
		v := *s.mode
		changes = append(changes, change{"permission mode", proto.ThreadConfigChangedPayload{Mode: v}, func(sess adapter.Session) (func(context.Context) error, bool) {
			sw, ok := sess.(adapter.ModeSwitcher)
			return func(ctx context.Context) error { return sw.SetMode(ctx, v) }, ok
		}})
	}
	if s.effort != nil && *s.effort != a.state.Effort {
		v := *s.effort
		changes = append(changes, change{"reasoning effort", proto.ThreadConfigChangedPayload{Effort: &v}, func(sess adapter.Session) (func(context.Context) error, bool) {
			sw, ok := sess.(adapter.EffortSwitcher)
			return func(ctx context.Context) error { return sw.SetEffort(ctx, v) }, ok
		}})
	}
	if a.sess == nil {
		for _, c := range changes {
			if err := a.append(proto.Emit(proto.ThreadConfigChanged, c.payload)); err != nil {
				reply <- cmdResult{err: err}
				return
			}
		}
		reply <- cmdResult{}
		return
	}
	var waits []chan cmdResult
	var refused error
	for _, c := range changes {
		call, ok := c.call(a.sess)
		if !ok {
			if refused == nil {
				refused = fmt.Errorf("this harness cannot change %s mid-thread; the agent's next thread starts on it", c.what)
			}
			continue
		}
		ch := make(chan cmdResult, 1)
		waits = append(waits, ch)
		a.setting(call, c.payload, ch)
	}
	go func() {
		err := refused
		for _, ch := range waits {
			select {
			case r := <-ch:
				if err == nil {
					err = r.err
				}
			case <-a.quit:
				reply <- cmdResult{err: ErrClosed}
				return
			}
		}
		reply <- cmdResult{err: err}
	}()
}

// writeThroughToAgent saves a setting changed on the agent's live thread to
// the agent, so its next thread starts the way this one was left. It writes
// only the columns that changed, so a SaveAgent of other settings running
// beside it keeps its own; it cannot take the agent's lock, which SaveAgent
// holds while it waits on this loop. A change SaveAgent itself handed the
// thread writes back the value it just saved.
func (a *Actor) writeThroughToAgent(changed proto.ThreadConfigChangedPayload) {
	var model, mode *string
	if changed.Model != "" {
		model = &changed.Model
	}
	if changed.Mode != "" {
		mode = &changed.Mode
	}
	if model == nil && mode == nil && changed.Effort == nil {
		return
	}
	ok, err := a.store.SetAgentLiveSettings(context.Background(), a.ID, model, mode, changed.Effort)
	if err != nil {
		a.logf("agent settings from %s: %v", a.ID, err)
		return
	}
	if ok {
		a.notifyAgents()
	}
}
