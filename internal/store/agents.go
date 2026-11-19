package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/proto"
)

// ---- Agents: long-running threads with a saved brief ----
//
// An agent runs one thread at a time (thread_id, '' when it has none). Its
// system prompt and compaction instructions are versioned in agent_revisions,
// whole bodies numbered 1.. per agent and kind, so the current text is the
// highest n and a revert is a new revision with an old body. Each of its
// threads keeps a log in compactions: epoch 0 is what the first request
// carried, then one row per compaction.

const agentsSchema = `
CREATE TABLE IF NOT EXISTS agents (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL,
  name            TEXT NOT NULL,
  harness         TEXT NOT NULL,
  instance        TEXT NOT NULL DEFAULT '',
  model           TEXT NOT NULL DEFAULT '',
  effort          TEXT NOT NULL DEFAULT '',
  mode            TEXT NOT NULL DEFAULT '',
  folder_id       TEXT NOT NULL DEFAULT '',
  workspace       TEXT NOT NULL DEFAULT 'local',
  compact_at      INTEGER NOT NULL DEFAULT 0,
  kickoff         TEXT NOT NULL DEFAULT '',
  thread_id       TEXT NOT NULL DEFAULT '',
  live_prompt_rev INTEGER NOT NULL DEFAULT 0,
  live_compact_rev INTEGER NOT NULL DEFAULT 0,
  restart_pending INTEGER NOT NULL DEFAULT 0,
  log_version     INTEGER NOT NULL DEFAULT 0,
  raw_log         INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  archived_at     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS agent_revisions (
  id          INTEGER PRIMARY KEY,
  agent_id    TEXT NOT NULL,
  kind        TEXT NOT NULL,
  n           INTEGER NOT NULL,
  body        TEXT NOT NULL,
  author      TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  UNIQUE (agent_id, kind, n)
);

CREATE TABLE IF NOT EXISTS compactions (
  id            INTEGER PRIMARY KEY,
  agent_id      TEXT NOT NULL,
  thread_id     TEXT NOT NULL,
  epoch         INTEGER NOT NULL,
  trigger       TEXT NOT NULL,
  pre_tokens    INTEGER NOT NULL DEFAULT 0,
  post_tokens   INTEGER NOT NULL DEFAULT 0,
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  prompt_rev    INTEGER NOT NULL DEFAULT 0,
  compact_rev   INTEGER NOT NULL DEFAULT 0,
  instructions  TEXT NOT NULL DEFAULT '',
  summary       TEXT NOT NULL DEFAULT '',
  attachments   TEXT NOT NULL DEFAULT '[]',
  boundary_seq  INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS compactions_thread ON compactions(thread_id, epoch);
`

// Revision kinds and authors.
const (
	RevisionPrompt  = "prompt"
	RevisionCompact = "compact"
	AuthorUser      = "user"
	AuthorAgent     = "agent"
)

// Agent is one agent as the sidebar and the agent page see it. PromptRev,
// CompactRev and Epoch are not columns: they are read from the revisions and
// the live thread's log on every load, so they cannot disagree with them.
type Agent struct {
	ID        string `json:"id"`
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
	ThreadID  string `json:"threadId,omitempty"`
	PromptRev int    `json:"promptRev"`
	// CompactRev is the current compaction instructions revision.
	CompactRev int `json:"compactRev"`
	// LivePromptRev is the prompt revision the live thread's process was
	// started on; below PromptRev while a restart onto the newer one waits.
	LivePromptRev int `json:"livePromptRev,omitempty"`
	// LiveCompactRev is the compaction instructions revision the live
	// thread's process was started on: what a harness that reads them at
	// start compacts with.
	LiveCompactRev int `json:"-"`
	// RestartPending is set while the live thread has a restart waiting for
	// its turn to end.
	RestartPending bool `json:"restartPending,omitempty"`
	// LogVersion changes whenever a row of the agent's log is added or
	// changed, so a client knows to read the log again.
	LogVersion int64 `json:"logVersion"`
	// Epoch is how many times the live thread has compacted.
	Epoch      int   `json:"epoch"`
	RawLog     bool  `json:"rawLog,omitempty"`
	CreatedAt  int64 `json:"createdAt"`
	ArchivedAt int64 `json:"-"`
}

// AgentRevision is one saved version of an agent's prompt or compaction
// instructions.
type AgentRevision struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	N         int    `json:"n"`
	Body      string `json:"body"`
	Author    string `json:"author"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// Compaction is one row of an agent thread's log. Read as a list it carries
// no bodies: Summary and Instructions are empty, and so are the attachments'
// bodies, with the sizes standing in for them.
type Compaction struct {
	ID                int64                        `json:"id"`
	AgentID           string                       `json:"-"`
	ThreadID          string                       `json:"threadId"`
	Epoch             int                          `json:"epoch"`
	Trigger           string                       `json:"trigger"`
	PreTokens         int64                        `json:"preTokens,omitempty"`
	PostTokens        int64                        `json:"postTokens,omitempty"`
	DurationMs        int64                        `json:"durationMs,omitempty"`
	PromptRev         int                          `json:"promptRev"`
	CompactRev        int                          `json:"compactRev"`
	SummaryChars      int                          `json:"summaryChars,omitempty"`
	InstructionsChars int                          `json:"instructionsChars,omitempty"`
	Attachments       []proto.CompactionAttachment `json:"attachments"`
	BoundarySeq       int64                        `json:"boundarySeq,omitempty"`
	CreatedAt         int64                        `json:"createdAt"`
	Summary           string                       `json:"summary,omitempty"`
	Instructions      string                       `json:"instructions,omitempty"`
}

// ArchivedAgentThread is an agent thread that is no longer its live one.
type ArchivedAgentThread struct {
	ThreadID    string `json:"threadId"`
	Title       string `json:"title"`
	CreatedAt   int64  `json:"createdAt"`
	ClosedAt    int64  `json:"closedAt,omitempty"`
	Compactions int    `json:"compactions"`
}

func (s *Store) initAgents() error {
	_, err := s.db.Exec(agentsSchema)
	return err
}

// agentColumns reads an agent with its derived numbers. The epoch counts the
// live thread's compactions; epoch 0 is not one.
const agentColumns = `a.id, a.project_id, a.name, a.harness, a.instance, a.model, a.effort, a.mode, a.folder_id, a.workspace,
	a.compact_at, a.kickoff, a.thread_id, a.live_prompt_rev, a.live_compact_rev, a.restart_pending, a.log_version, a.raw_log, a.created_at, a.archived_at,
	COALESCE((SELECT MAX(n) FROM agent_revisions r WHERE r.agent_id = a.id AND r.kind = 'prompt'), 0),
	COALESCE((SELECT MAX(n) FROM agent_revisions r WHERE r.agent_id = a.id AND r.kind = 'compact'), 0),
	COALESCE((SELECT MAX(epoch) FROM compactions c WHERE c.thread_id = a.thread_id AND a.thread_id != ''), 0)`

func scanAgent(sc interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	err := sc.Scan(&a.ID, &a.ProjectID, &a.Name, &a.Harness, &a.Instance, &a.Model, &a.Effort, &a.Mode, &a.FolderID, &a.Workspace,
		&a.CompactAt, &a.Kickoff, &a.ThreadID, &a.LivePromptRev, &a.LiveCompactRev, &a.RestartPending, &a.LogVersion, &a.RawLog, &a.CreatedAt, &a.ArchivedAt,
		&a.PromptRev, &a.CompactRev, &a.Epoch)
	return a, err
}

// CreateAgent stores a new agent with its first prompt and compaction
// instructions, r1 and c1, both the user's. The id is made here when a.ID is
// empty.
func (s *Store) CreateAgent(ctx context.Context, a Agent, prompt, compact string) (Agent, error) {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Workspace == "" {
		a.Workspace = "local"
	}
	now := proto.NowMillis()
	s.mu.Lock()
	err := func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, a.ProjectID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: project %s", ErrNotFound, a.ProjectID)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO agents (id, project_id, name, harness, instance, model, effort, mode, folder_id, workspace, compact_at, kickoff, raw_log, created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.ID, a.ProjectID, a.Name, a.Harness, a.Instance, a.Model, a.Effort, a.Mode, a.FolderID, a.Workspace, a.CompactAt, a.Kickoff, a.RawLog, now); err != nil {
			return err
		}
		for _, r := range []struct{ kind, body string }{{RevisionPrompt, prompt}, {RevisionCompact, compact}} {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO agent_revisions (agent_id, kind, n, body, author, reason, created_at) VALUES (?,?,1,?,?,'',?)`,
				a.ID, r.kind, r.body, AuthorUser, now); err != nil {
				return err
			}
		}
		return tx.Commit()
	}()
	s.mu.Unlock()
	if err != nil {
		return Agent{}, err
	}
	return s.Agent(ctx, a.ID)
}

// Agent reads one agent, archived or not. ErrNotFound when there is none.
func (s *Store) Agent(ctx context.Context, id string) (Agent, error) {
	a, err := scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agents a WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("%w: agent %s", ErrNotFound, id)
	}
	return a, err
}

// ListAgents returns every agent not archived, oldest first, which is the
// order the sidebar lists them in.
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents a WHERE a.archived_at = 0 ORDER BY a.created_at, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AgentPatch is a change to an agent's settings, everything the user edits
// on the agent page; nil fields stay as they are.
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

// Apply is a with the patch's fields set.
func (p AgentPatch) Apply(a Agent) Agent {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&a.Name, p.Name)
	set(&a.Model, p.Model)
	set(&a.Effort, p.Effort)
	set(&a.Mode, p.Mode)
	set(&a.FolderID, p.FolderID)
	set(&a.Workspace, p.Workspace)
	set(&a.Kickoff, p.Kickoff)
	if p.CompactAt != nil {
		a.CompactAt = *p.CompactAt
	}
	if p.RawLog != nil {
		a.RawLog = *p.RawLog
	}
	return a
}

// PatchAgent writes only the settings the patch has, so a change the live
// thread writes through at the same time (SetAgentLiveSettings) is not
// overwritten with what was read before it.
func (s *Store) PatchAgent(ctx context.Context, id string, p AgentPatch) error {
	var sets []string
	var args []any
	col := func(name string, v any) {
		sets = append(sets, name+"=?")
		args = append(args, v)
	}
	if p.Name != nil {
		col("name", *p.Name)
	}
	if p.Model != nil {
		col("model", *p.Model)
	}
	if p.Effort != nil {
		col("effort", *p.Effort)
	}
	if p.Mode != nil {
		col("mode", *p.Mode)
	}
	if p.FolderID != nil {
		col("folder_id", *p.FolderID)
	}
	if p.Workspace != nil {
		col("workspace", *p.Workspace)
	}
	if p.CompactAt != nil {
		col("compact_at", *p.CompactAt)
	}
	if p.Kickoff != nil {
		col("kickoff", *p.Kickoff)
	}
	if p.RawLog != nil {
		col("raw_log", *p.RawLog)
	}
	if len(sets) == 0 {
		_, err := s.Agent(ctx, id)
		return err
	}
	return s.execOne(ctx, `UPDATE agents SET `+strings.Join(sets, ", ")+` WHERE id=?`, append(args, id)...)
}

// SetAgentLiveSettings saves a setting changed on a thread to its agent,
// while that thread is still the agent's live one; nil leaves a setting as
// it is. It reports whether an agent took it.
func (s *Store) SetAgentLiveSettings(ctx context.Context, threadID string, model, mode, effort *string) (bool, error) {
	err := s.execOne(ctx,
		`UPDATE agents SET model = COALESCE(?, model), mode = COALESCE(?, mode), effort = COALESCE(?, effort)
		 WHERE thread_id = ? AND thread_id != ''`, model, mode, effort, threadID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ArchiveAgent takes an agent out of the list. Its rows stay.
func (s *Store) ArchiveAgent(ctx context.Context, id string) error {
	return s.execOne(ctx, `UPDATE agents SET archived_at=? WHERE id=? AND archived_at = 0`, proto.NowMillis(), id)
}

// SetAgentThread records the agent's live thread; "" for none. A restart
// the old one had waiting is not the new one's.
func (s *Store) SetAgentThread(ctx context.Context, id, threadID string) error {
	return s.execOne(ctx, `UPDATE agents SET thread_id=?, restart_pending=0 WHERE id=?`, threadID, id)
}

// SetAgentLiveRevs records the prompt and compaction instructions revisions
// the live thread's process started on.
func (s *Store) SetAgentLiveRevs(ctx context.Context, id string, promptRev, compactRev int) error {
	return s.execOne(ctx, `UPDATE agents SET live_prompt_rev=?, live_compact_rev=? WHERE id=?`, promptRev, compactRev, id)
}

// SetThreadRestartPending mirrors a thread's waiting restart onto the agent
// whose live thread it is. It reports whether an agent's flag changed; a
// thread that is no agent's live one changes nothing.
func (s *Store) SetThreadRestartPending(ctx context.Context, threadID string, pending bool) (bool, error) {
	err := s.execOne(ctx, `UPDATE agents SET restart_pending=? WHERE thread_id=? AND thread_id != '' AND restart_pending != ?`, pending, threadID, pending)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// execOne runs an update that must hit one row: ErrNotFound when it hit none.
func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevisionMovedError refuses a revision written against one that is no
// longer the latest of its kind: someone saved another in between.
type RevisionMovedError struct {
	Kind string
	Head int
}

func (e *RevisionMovedError) Error() string {
	noun, letter := "prompt", "r"
	if e.Kind == RevisionCompact {
		noun, letter = "compaction instructions", "c"
	}
	return fmt.Sprintf("The %s changed while you were editing: %s%d is the latest. Review it, then save again.", noun, letter, e.Head)
}

// RevisionUnchangedError refuses a revision whose body is the latest of its
// kind already: it would change nothing, so nothing is written.
type RevisionUnchangedError struct {
	Kind string
	Head int
}

func (e *RevisionUnchangedError) Error() string {
	noun, verb, letter := "prompt", "is", "r"
	if e.Kind == RevisionCompact {
		noun, verb, letter = "compaction instructions", "are", "c"
	}
	return fmt.Sprintf("The %s %s already this text, as %s%d. Nothing to save.", noun, verb, letter, e.Head)
}

// AddAgentRevision saves a new revision of one of an agent's texts, numbered
// one past the highest of its kind. A body the same as the latest's is
// refused with a *RevisionUnchangedError; reverting to an older body that
// differs from it is saved like any other. A base above 0 is the revision the
// edit started from: when another has been saved since, the save is refused
// with a *RevisionMovedError. Both are checked in the same transaction as the
// write.
func (s *Store) AddAgentRevision(ctx context.Context, agentID, kind, body, author, reason string, base int) (AgentRevision, error) {
	if kind != RevisionPrompt && kind != RevisionCompact {
		return AgentRevision{}, fmt.Errorf("unknown revision kind %q", kind)
	}
	if author != AuthorUser && author != AuthorAgent {
		return AgentRevision{}, fmt.Errorf("unknown revision author %q", author)
	}
	r := AgentRevision{Kind: kind, Body: body, Author: author, Reason: reason, CreatedAt: proto.NowMillis()}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE id = ?`, agentID).Scan(&exists); err != nil {
		return r, err
	}
	if exists == 0 {
		return r, fmt.Errorf("%w: agent %s", ErrNotFound, agentID)
	}
	var (
		head     int
		headBody string
	)
	err = tx.QueryRowContext(ctx, `SELECT n, body FROM agent_revisions WHERE agent_id = ? AND kind = ? ORDER BY n DESC LIMIT 1`, agentID, kind).Scan(&head, &headBody)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	// Unchanged first: a stale base saving the text that landed since has
	// nothing to review.
	if head > 0 && body == headBody {
		return r, &RevisionUnchangedError{Kind: kind, Head: head}
	}
	if base > 0 && head != base {
		return r, &RevisionMovedError{Kind: kind, Head: head}
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO agent_revisions (agent_id, kind, n, body, author, reason, created_at)
		 VALUES (?, ?, COALESCE((SELECT MAX(n) FROM agent_revisions WHERE agent_id = ? AND kind = ?), 0) + 1, ?, ?, ?, ?)
		 RETURNING id, n`,
		agentID, kind, agentID, kind, body, author, reason, r.CreatedAt).Scan(&r.ID, &r.N); err != nil {
		return r, err
	}
	return r, tx.Commit()
}

// AgentRevisions lists one kind of an agent's revisions, newest first.
func (s *Store) AgentRevisions(ctx context.Context, agentID, kind string) ([]AgentRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, n, body, author, reason, created_at FROM agent_revisions
		 WHERE agent_id = ? AND kind = ? ORDER BY n DESC`, agentID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentRevision{}
	for rows.Next() {
		var r AgentRevision
		if err := rows.Scan(&r.ID, &r.Kind, &r.N, &r.Body, &r.Author, &r.Reason, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AgentRevision is revision n of one kind. ErrNotFound when there is none.
func (s *Store) AgentRevision(ctx context.Context, agentID, kind string, n int) (AgentRevision, error) {
	var r AgentRevision
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, n, body, author, reason, created_at FROM agent_revisions
		 WHERE agent_id = ? AND kind = ? AND n = ?`, agentID, kind, n).
		Scan(&r.ID, &r.Kind, &r.N, &r.Body, &r.Author, &r.Reason, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s revision %d of agent %s", ErrNotFound, kind, n, agentID)
	}
	return r, err
}

// LatestAgentRevision is the current text of one kind. ErrNotFound when the
// agent has none.
func (s *Store) LatestAgentRevision(ctx context.Context, agentID, kind string) (AgentRevision, error) {
	var r AgentRevision
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, n, body, author, reason, created_at FROM agent_revisions
		 WHERE agent_id = ? AND kind = ? ORDER BY n DESC LIMIT 1`, agentID, kind).
		Scan(&r.ID, &r.Kind, &r.N, &r.Body, &r.Author, &r.Reason, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s revision of agent %s", ErrNotFound, kind, agentID)
	}
	return r, err
}

// AddCompaction writes the next row of a thread's log and returns it with its
// id and epoch. A start row is epoch 0; any other is one past the thread's
// highest, so the first compaction is epoch 1 whether or not epoch 0 was ever
// written.
func (s *Store) AddCompaction(ctx context.Context, c Compaction) (Compaction, error) {
	if c.Attachments == nil {
		c.Attachments = []proto.CompactionAttachment{}
	}
	attachments, err := json.Marshal(c.Attachments)
	if err != nil {
		return c, err
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = proto.NowMillis()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO compactions (agent_id, thread_id, epoch, trigger, pre_tokens, post_tokens, duration_ms, prompt_rev, compact_rev, instructions, summary, attachments, boundary_seq, created_at)
		 VALUES (?, ?, CASE WHEN ? = 'start' THEN 0 ELSE COALESCE((SELECT MAX(epoch) FROM compactions WHERE thread_id = ?), 0) + 1 END,
		         ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 RETURNING id, epoch`,
		c.AgentID, c.ThreadID, c.Trigger, c.ThreadID,
		c.Trigger, c.PreTokens, c.PostTokens, c.DurationMs, c.PromptRev, c.CompactRev, c.Instructions, c.Summary, string(attachments), c.BoundarySeq, c.CreatedAt).
		Scan(&c.ID, &c.Epoch)
	if err != nil {
		return c, err
	}
	if _, err := tx.ExecContext(ctx, bumpLogVersion+`?`, c.AgentID); err != nil {
		return c, err
	}
	c.SummaryChars, c.InstructionsChars = len([]rune(c.Summary)), len([]rune(c.Instructions))
	return c, tx.Commit()
}

// bumpLogVersion, with the agent's id after it, marks the agent's log
// changed (Agent.LogVersion).
const bumpLogVersion = `UPDATE agents SET log_version = log_version + 1 WHERE id = `

// changeLog updates one compaction row, the one row finds, and marks its
// agent's log changed, together: update is "SET ... WHERE id = ?" with its
// own arguments before the row's id. It reports whether the update changed
// the row; ErrNotFound when row finds none.
func (s *Store) changeLog(ctx context.Context, row string, rowArgs []any, update string, args ...any) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, row, rowArgs...).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	} else if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE compactions `+update, append(args, id)...)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, bumpLogVersion+`(SELECT agent_id FROM compactions WHERE id = ?)`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SetCompactionBoundary records the seq of the compaction notice in the
// thread, once it has one.
func (s *Store) SetCompactionBoundary(ctx context.Context, id, seq int64) error {
	_, err := s.changeLog(ctx, `SELECT id FROM compactions WHERE id = ?`, []any{id}, `SET boundary_seq=? WHERE id=?`, seq)
	return err
}

// FillStartMessage puts the first message sent to a thread on its epoch 0
// row, when that row has none: the thread was started with no first message.
// It reports whether it did. A thread with no epoch 0 row is no agent's, and
// gets false.
func (s *Store) FillStartMessage(ctx context.Context, threadID, text string) (bool, error) {
	ok, err := s.changeLog(ctx, `SELECT id FROM compactions WHERE thread_id = ? AND epoch = 0`, []any{threadID},
		`SET summary=? WHERE id=? AND summary = ''`, text)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return ok, err
}

// FileCompactionDetail fills in what a harness said after the fact about the
// start of an epoch: on the thread's epoch 0 row when start, else on its
// latest row. An empty part leaves what the row holds, since either part may
// come on its own. ErrNotFound when there is no row to file it on.
func (s *Store) FileCompactionDetail(ctx context.Context, threadID string, start bool, summary string, attachments []proto.CompactionAttachment) error {
	target := `SELECT id FROM compactions WHERE thread_id = ? ORDER BY epoch DESC LIMIT 1`
	if start {
		target = `SELECT id FROM compactions WHERE thread_id = ? AND epoch = 0`
	}
	var blob any
	if len(attachments) > 0 {
		b, err := json.Marshal(attachments)
		if err != nil {
			return err
		}
		blob = string(b)
	}
	var text any
	if summary != "" {
		text = summary
	}
	_, err := s.changeLog(ctx, target, []any{threadID},
		`SET summary = COALESCE(?, summary), attachments = COALESCE(?, attachments) WHERE id = ?`, text, blob)
	return err
}

// compactionColumns are a row without its bodies, the sizes in their place.
// The attachments are read whole and their bodies dropped in Go.
const compactionColumns = `id, agent_id, thread_id, epoch, trigger, pre_tokens, post_tokens, duration_ms, prompt_rev, compact_rev,
	length(summary), length(instructions), attachments, boundary_seq, created_at`

func scanCompaction(sc interface{ Scan(...any) error }, extra ...any) (Compaction, error) {
	var c Compaction
	var attachments string
	err := sc.Scan(append([]any{&c.ID, &c.AgentID, &c.ThreadID, &c.Epoch, &c.Trigger, &c.PreTokens, &c.PostTokens, &c.DurationMs, &c.PromptRev, &c.CompactRev,
		&c.SummaryChars, &c.InstructionsChars, &attachments, &c.BoundarySeq, &c.CreatedAt}, extra...)...)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(attachments), &c.Attachments); err != nil {
		return c, fmt.Errorf("decode compaction attachments: %w", err)
	}
	if c.Attachments == nil {
		c.Attachments = []proto.CompactionAttachment{}
	}
	return c, nil
}

// ThreadCompactions is a thread's log, epoch order, with no bodies.
func (s *Store) ThreadCompactions(ctx context.Context, threadID string) ([]Compaction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+compactionColumns+` FROM compactions WHERE thread_id = ? ORDER BY epoch`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Compaction{}
	for rows.Next() {
		c, err := scanCompaction(rows)
		if err != nil {
			return nil, err
		}
		for i := range c.Attachments {
			c.Attachments[i].Body = ""
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Compaction reads one row whole, bodies and all.
func (s *Store) Compaction(ctx context.Context, id int64) (Compaction, error) {
	var summary, instructions string
	c, err := scanCompaction(s.db.QueryRowContext(ctx,
		`SELECT `+compactionColumns+`, summary, instructions FROM compactions WHERE id = ?`, id), &summary, &instructions)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("%w: compaction %d", ErrNotFound, id)
	}
	c.Summary, c.Instructions = summary, instructions
	return c, err
}

// ArchivedAgentThreads lists an agent's threads other than live, newest
// first, each with how many times it compacted. A closed thread's last
// change is its closing, so updated_at stands in for when it closed.
func (s *Store) ArchivedAgentThreads(ctx context.Context, agentID, live string) ([]ArchivedAgentThread, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.title, t.created_at, CASE WHEN t.phase = 'closed' THEN t.updated_at ELSE 0 END,
		        (SELECT COUNT(*) FROM compactions c WHERE c.thread_id = t.id AND c.epoch > 0)
		 FROM threads t WHERE t.agent_id = ? AND t.id != ? ORDER BY t.created_at DESC, t.id DESC`, agentID, live)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArchivedAgentThread{}
	for rows.Next() {
		var a ArchivedAgentThread
		if err := rows.Scan(&a.ThreadID, &a.Title, &a.CreatedAt, &a.ClosedAt, &a.Compactions); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
