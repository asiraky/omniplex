package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/push"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// Orchestration: a thread delegates work to child threads in its own
// project, steers them, and is woken with their results. The tools only ever
// reach the caller's own descendants (reading reaches its project too), and
// none of them can approve a permission request: only the user does that.

var (
	// defaultWait and maxWait bound a wait on delegated threads. The harness
	// gives a tool 30 minutes; no wait here may outlast cardWait's 25.
	defaultWait = 10 * time.Minute
	maxWait     = 25 * time.Minute
	// wakeWindow is how long after the first delegated thread finishes the
	// parent's wake waits for others, so close finishes wake it once.
	wakeWindow = 2 * time.Second
)

const (
	// wakeBudget bounds the replies inlined into one wake; the rest are
	// pointers to thread_read.
	wakeBudget = 16 << 10
	// readBudget bounds one thread_read page and one wait's replies.
	readBudget = 30000
	// statusReply bounds the last reply task_status shows.
	statusReply = 4000
)

// ---- settings: harness, model, effort, mode ----

// agentSettings is what a thread runs with.
type agentSettings struct {
	Harness, Instance, Model, Effort, ServiceTier, Mode string
}

// harnessCatalog is what one harness offers a new thread.
type harnessCatalog struct {
	ID     string
	Models []adapter.ModelMeta
	Modes  []adapter.PermissionModeMeta
	// ProjectModel is the project's default model for this harness, "" for
	// none or no project.
	ProjectModel string
}

// settingsAsk is what delegate_task asked for; empty inherits. The harness
// asked for is the catalogue's.
type settingsAsk struct{ Model, Effort, Mode string }

// levelRank is a level on the shared scale: ask (2) < edits (4) < all (6).
// 0 is no level.
func levelRank(l string) int {
	switch l {
	case adapter.LevelAsk:
		return 2
	case adapter.LevelEdits:
		return 4
	case adapter.LevelAll:
		return 6
	}
	return 0
}

// modeRank places a mode on the shared scale; 0 is an unknown mode. A mode
// with no level is judged by its levelled neighbours, harnesses listing
// modes least permissive first, and the doubt goes against it: as what a
// thread may do (granted false) it ranks as the levelled mode before it,
// ask when there is none; as what a delegated thread would get (granted
// true) it ranks as the one after it, all when there is none.
func modeRank(modes []adapter.PermissionModeMeta, id string, granted bool) int {
	i := slices.IndexFunc(modes, func(m adapter.PermissionModeMeta) bool { return m.ID == id })
	if i < 0 {
		return 0
	}
	if r := levelRank(modes[i].Level); r > 0 {
		return r
	}
	if granted {
		for _, m := range modes[i+1:] {
			if r := levelRank(m.Level); r > 0 {
				return r
			}
		}
		return 6
	}
	for j := i - 1; j >= 0; j-- {
		if r := levelRank(modes[j].Level); r > 0 {
			return r
		}
	}
	return 2
}

// effectiveMode is the mode a thread is in: its own, or its harness's
// default when it never chose one.
func effectiveMode(modes []adapter.PermissionModeMeta, id string) string {
	if id != "" || len(modes) == 0 {
		return id
	}
	for _, m := range modes {
		if m.Default {
			return m.ID
		}
	}
	return modes[0].ID
}

func modeIDs(modes []adapter.PermissionModeMeta) string {
	ids := make([]string, len(modes))
	for i, m := range modes {
		ids[i] = m.ID
	}
	return strings.Join(ids, ", ")
}

func findModel(models []adapter.ModelMeta, id string) (adapter.ModelMeta, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	// A 1M-context alias runs the model it tags.
	if base, ok := strings.CutSuffix(id, "[1m]"); ok {
		for _, m := range models {
			if m.ID == base && m.Supports1M {
				return m, true
			}
		}
	}
	return adapter.ModelMeta{}, false
}

// resolveChildSettings is what a delegated thread runs with: what was asked,
// else the parent's, by T3's rules. The same harness inherits the parent's
// account, model, effort, tier and mode. Another harness gets its project
// default model (else its own default), the parent's effort and tier only
// where that model takes them, and the broadest of its modes that grants no
// more than the parent's. An asked mode must exist there and grant no more
// than the parent's either: a delegated thread never has more say than the
// thread that started it.
func resolveChildSettings(parent agentSettings, parentModes []adapter.PermissionModeMeta, child harnessCatalog, ask settingsAsk) (agentSettings, error) {
	out := agentSettings{Harness: child.ID}
	same := child.ID == parent.Harness
	if same {
		out.Instance = parent.Instance
	}

	switch {
	case ask.Model != "":
		if _, ok := findModel(child.Models, ask.Model); !ok && len(child.Models) > 0 {
			ids := make([]string, len(child.Models))
			for i, m := range child.Models {
				ids[i] = m.ID
			}
			return out, fmt.Errorf("%s has no model %q; choose one of: %s", child.ID, ask.Model, strings.Join(ids, ", "))
		}
		out.Model = ask.Model
	case same:
		out.Model = parent.Model
	case child.ProjectModel != "":
		out.Model = child.ProjectModel
	default:
		for _, m := range child.Models {
			if m.Default {
				out.Model = m.ID
				break
			}
		}
		if out.Model == "" && len(child.Models) > 0 {
			out.Model = child.Models[0].ID
		}
	}
	model, known := findModel(child.Models, out.Model)
	// The parent's model on the same harness takes what the parent runs
	// with, even when the catalogue does not list it.
	sameModel := same && out.Model == parent.Model

	switch {
	case ask.Effort != "":
		if known && !slices.Contains(model.Efforts, ask.Effort) {
			if len(model.Efforts) == 0 {
				return out, fmt.Errorf("%s takes no effort setting; leave effort out", out.Model)
			}
			return out, fmt.Errorf("%s takes effort %s, not %q", out.Model, strings.Join(model.Efforts, ", "), ask.Effort)
		}
		out.Effort = ask.Effort
	case parent.Effort != "":
		if (known && slices.Contains(model.Efforts, parent.Effort)) || (!known && sameModel) {
			out.Effort = parent.Effort
		}
	}
	if parent.ServiceTier != "" && (sameModel || (known && model.FastTier == parent.ServiceTier)) {
		out.ServiceTier = parent.ServiceTier
	}

	parentMode := effectiveMode(parentModes, parent.Mode)
	ceiling := modeRank(parentModes, parentMode, false)
	if ceiling == 0 {
		ceiling = 2 // a mode its harness does not list: no more than ask
	}
	switch {
	case ask.Mode != "":
		rank := modeRank(child.Modes, ask.Mode, true)
		if rank == 0 {
			return out, fmt.Errorf("%s has no mode %q; its modes are %s", child.ID, ask.Mode, modeIDs(child.Modes))
		}
		if !(same && ask.Mode == parentMode) && rank > ceiling {
			return out, fmt.Errorf("mode %q would let the delegated thread do more than this thread may (%s); choose a narrower one or leave mode out", ask.Mode, cmp.Or(parentMode, "its default"))
		}
		out.Mode = ask.Mode
	case same:
		out.Mode = parent.Mode
	default:
		best := 0
		for _, m := range child.Modes {
			if r := levelRank(m.Level); r > 0 && r <= ceiling && r > best {
				best, out.Mode = r, m.ID
			}
		}
		if best == 0 {
			return out, fmt.Errorf("%s offers no mode as narrow as this thread's (%s): its modes are %s. Delegate to another harness, or ask the user", child.ID, cmp.Or(parentMode, "default"), modeIDs(child.Modes))
		}
	}
	return out, nil
}

// parentSettings reads what the caller runs with now: its live settings,
// which the user may have changed since it was created.
func (s *Server) parentSettings(ctx context.Context, meta store.ThreadMeta) agentSettings {
	out := agentSettings{Harness: meta.Harness, Instance: meta.ProviderInstance, Model: meta.Model, Effort: meta.Effort, ServiceTier: meta.ServiceTier, Mode: meta.Mode}
	if a, err := s.mgr.View(ctx, meta.ID); err == nil {
		if st, err := a.WireState(ctx, 1); err == nil {
			out.Model, out.Effort, out.ServiceTier, out.Mode = st.Model, st.Effort, st.ServiceTier, st.Mode
		}
	}
	return out
}

// catalog is what a harness offers, through the parent's account when it is
// the parent's harness.
func catalog(hs []thread.Harness, id, instance string) (harnessCatalog, []adapter.PermissionModeMeta, bool) {
	for _, h := range hs {
		if h.ID != id {
			continue
		}
		c := harnessCatalog{ID: h.ID, Models: h.Models, Modes: h.PermissionModes}
		for _, inst := range h.Instances {
			if inst.ID == instance && len(inst.Models) > 0 {
				c.Models = inst.Models
			}
		}
		return c, h.PermissionModes, true
	}
	return harnessCatalog{}, nil, false
}

// ---- delegate_task ----

type delegateArgs struct {
	Prompt    string `json:"prompt"`
	Title     string `json:"title"`
	Harness   string `json:"harness"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	Mode      string `json:"mode"`
	Workspace string `json:"workspace"`
	Wait      bool   `json:"wait"`
	Timeout   *int   `json:"timeout_seconds"`
}

func waitTimeout(sec *int) (time.Duration, error) {
	if sec == nil {
		return defaultWait, nil
	}
	if *sec < 1 || time.Duration(*sec)*time.Second > maxWait {
		return 0, badArgs("timeout_seconds is 1 to %d", int(maxWait/time.Second))
	}
	return time.Duration(*sec) * time.Second, nil
}

func (s *Server) delegateTask(ctx context.Context, t toolThread, body []byte) (string, bool, error) {
	var a delegateArgs
	if err := decodeArgs(body, &a); err != nil {
		return "", false, err
	}
	a.Prompt = strings.TrimSpace(a.Prompt)
	if a.Prompt == "" {
		return "", false, badArgs("prompt is required: the task, with everything the delegated thread needs to know")
	}
	if a.Workspace == "" {
		a.Workspace = "shared"
	}
	if a.Workspace != "shared" && a.Workspace != "worktree" {
		return "", false, badArgs("workspace is shared or worktree, not %q", a.Workspace)
	}
	timeout, err := waitTimeout(a.Timeout)
	if err != nil {
		return "", false, err
	}
	parent, err := s.store.Thread(ctx, t.id)
	if err != nil {
		return "", false, err
	}

	hs := s.mgr.Harnesses(ctx)
	_, parentModes, _ := catalog(hs, parent.Harness, parent.ProviderInstance)
	childHarness := cmp.Or(a.Harness, parent.Harness)
	child, _, ok := catalog(hs, childHarness, parent.ProviderInstance)
	if !ok {
		return "", false, badArgs("there is no harness %q here", childHarness)
	}
	var p project.Project
	if parent.ProjectID != "" {
		if p, err = s.store.Project(ctx, parent.ProjectID); err != nil {
			return "", false, err
		}
		if childHarness != parent.Harness {
			child.ProjectModel = project.NormalizeDefaults(p.Defaults).Harnesses[childHarness].Model
		}
	}
	set, err := resolveChildSettings(s.parentSettings(ctx, parent), parentModes, child, settingsAsk{Model: a.Model, Effort: a.Effort, Mode: a.Mode})
	if err != nil {
		return "", false, badArgs("%v", err)
	}

	var (
		actor *thread.Actor
		where string
	)
	if parent.ProjectID == "" {
		if a.Workspace == "worktree" {
			return "", false, badArgs("this thread has no project, so it has no repository to make a worktree of; use workspace shared")
		}
		actor, err = s.mgr.CreateAssistant(ctx, thread.AssistantOptions{Title: a.Title, Harness: set.Harness, Instance: set.Instance, Model: set.Model, Mode: set.Mode, Effort: set.Effort, ServiceTier: set.ServiceTier, Private: parent.Private, ParentID: parent.ID})
		where = "its own chat folder"
	} else {
		o := thread.CreateProjectOptions{ProjectID: parent.ProjectID, Title: a.Title, Harness: set.Harness, Instance: set.Instance, Model: set.Model, Mode: set.Mode, Effort: set.Effort, ServiceTier: set.ServiceTier, AgentSettingsExplicit: true, FolderID: parent.FolderID, ParentID: parent.ID}
		switch {
		case a.Workspace == "worktree":
			f, scoped := p.Folder(parent.FolderID)
			if parent.FolderID == "" || !scoped {
				return "", false, badArgs("this thread works across the whole project, not in one folder, so there is no one repository to make a worktree of; use workspace shared")
			}
			if !f.Git {
				return "", false, badArgs("%s is not a git repository, so it cannot have a worktree; use workspace shared", f.Path)
			}
			head, err := thread.HeadCommit(ctx, parent.Cwd)
			if err != nil {
				return "", false, fmt.Errorf("read this thread's commit: %w", err)
			}
			o.Workspace, o.BaseRef = "managed", head
			where = "a new worktree from commit " + head[:min(12, len(head))]
		case parent.FolderID == "":
			o.Workspace = "local"
			where = "the project's folders, shared with this thread"
		case parent.WorkspaceMode == "managed" || parent.WorkspaceMode == "borrowed":
			o.Workspace, o.WorkspacePath = "local", parent.Cwd
			where = parent.Cwd + ", shared with this thread"
		default:
			o.Workspace = "local"
			where = parent.Cwd + ", shared with this thread"
		}
		actor, err = s.mgr.CreateProject(ctx, o)
	}
	if err != nil {
		return "", false, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Started delegated thread %s", actor.ID)
	if a.Title != "" {
		fmt.Fprintf(&b, " %q", a.Title)
	}
	fmt.Fprintf(&b, " on %s", set.Harness)
	if set.Model != "" {
		fmt.Fprintf(&b, ", model %s", set.Model)
	}
	if set.Effort != "" {
		fmt.Fprintf(&b, ", effort %s", set.Effort)
	}
	fmt.Fprintf(&b, ", mode %s, working in %s.", cmp.Or(set.Mode, "default"), where)
	// The thread exists now: an error from here on must not read as nothing
	// having happened, or a retry makes a second one.
	if _, err := actor.Prompt(ctx, a.Prompt, nil); err != nil {
		fmt.Fprintf(&b, " Its prompt could not be sent: %v. Send it again with thread_send.", err)
		return b.String(), false, nil
	}
	if !a.Wait {
		// Owed before the ping, so the scan it triggers sees a child that
		// finished at once.
		s.orch.owe(actor.ID, parent.ID, 0)
	}
	s.mgr.NotifyList()

	if !a.Wait {
		b.WriteString(" It runs on its own: you will be woken with its result when it finishes. Do not poll it or wait on it; carry on with your own work.")
		return b.String(), false, nil
	}
	text, gone, err := s.waitOn(ctx, t, []string{actor.ID}, timeout)
	if gone || err != nil {
		return "", gone, err
	}
	return b.String() + "\n\n" + text, false, nil
}

// ---- authorization ----

func notFound(id string) error {
	return &toolError{code: http.StatusNotFound, msg: "there is no thread " + id}
}

func forbidden(format string, a ...any) error {
	return &toolError{code: http.StatusForbidden, msg: fmt.Sprintf(format, a...)}
}

// steerable is a thread the caller may steer: one it delegated, or one
// those delegated, in its own project.
func (s *Server) steerable(ctx context.Context, t toolThread, id string) (store.ThreadMeta, error) {
	if id == "" {
		return store.ThreadMeta{}, badArgs("id is required: a thread id from delegate_task or thread_list")
	}
	meta, err := s.store.Thread(ctx, id)
	if err != nil {
		return meta, notFound(id)
	}
	desc, err := s.mgr.IsDescendant(ctx, t.id, id)
	if err != nil {
		return meta, err
	}
	if !desc || meta.ProjectID != t.projectID {
		return meta, forbidden("thread %s was not delegated by this thread or one it delegated, so you cannot steer it; you can only steer threads from thread_list", id)
	}
	return meta, nil
}

// readable is a thread the caller may read: its own descendants, and any
// other thread of its project, which is how a thread the user links in a
// prompt is read. A private thread only when the caller delegated it.
func (s *Server) readable(ctx context.Context, t toolThread, id string) (store.ThreadMeta, bool, error) {
	if id == "" {
		return store.ThreadMeta{}, false, badArgs("id is required")
	}
	meta, err := s.store.Thread(ctx, id)
	if err != nil {
		return meta, false, notFound(id)
	}
	desc, err := s.mgr.IsDescendant(ctx, t.id, id)
	if err != nil {
		return meta, false, err
	}
	if desc && meta.ProjectID == t.projectID {
		return meta, true, nil
	}
	if t.projectID != "" && meta.ProjectID == t.projectID && !meta.Private {
		return meta, false, nil
	}
	return meta, false, forbidden("thread %s is outside this thread's project, so you cannot read it", id)
}

// ---- where a thread stands ----

// look is a thread as the tools see it.
type look struct {
	meta store.ThreadMeta
	att  string // as Manager.List reports it
	d    thread.Digest
}

func (s *Server) lookAt(ctx context.Context, meta store.ThreadMeta) (look, error) {
	l := look{meta: meta}
	if a, ok := s.mgr.Peek(meta.ID); ok {
		d, err := a.Digest(ctx)
		if err != nil && !errors.Is(err, thread.ErrClosed) {
			return l, err
		}
		l.d, l.att = d, d.Attention
		if err != nil {
			l.att = projection.AttentionClosed
		}
		return l, nil
	}
	l.att = projection.AttentionForPhase(meta.Phase)
	a, err := s.mgr.View(ctx, meta.ID)
	if err != nil {
		return l, nil // no transcript to read: the phase is the whole story
	}
	if d, err := a.Digest(ctx); err == nil {
		l.d = d
	}
	return l, nil
}

// finished is what became of a thread whose work is over for now, judged
// by its turns from index since on: completed, failed or stopped. ok is
// false while it is still at work, waiting on someone, or yet to start.
func finished(att string, d thread.Digest, since int) (status string, ok bool) {
	switch att {
	case projection.AttentionClosed:
		return "stopped", true
	case projection.AttentionFailed:
		return "failed", true
	case projection.AttentionNeedsPrompt:
		if d.Last == nil || !d.Last.Done || d.Last.Index < since {
			return "", false
		}
		switch {
		case d.Last.StopReason == proto.StopCancelled:
			return "stopped", true
		case d.TurnFailed:
			return "failed", true
		}
		return "completed", true
	}
	return "", false
}

// result is what became of a thread whose result is in: finished, and not
// still collecting what it delegated.
func (s *Server) result(l look) (status string, ok bool) {
	st, ok := finished(l.att, l.d, s.orch.since(l.meta.ID))
	return st, ok && !s.orch.busy(l.meta.ID)
}

// statusWord is where a thread stands, in a word or two.
func statusWord(att string, d thread.Digest) string {
	switch att {
	case projection.AttentionNeedsPermission:
		return "needs approval"
	case projection.AttentionNeedsAnswer:
		return "needs answer"
	case projection.AttentionFailed:
		return "failed"
	case projection.AttentionClosed:
		return "closed"
	case projection.AttentionWorking, projection.AttentionBackground:
		return "working"
	}
	if st, ok := finished(att, d, 0); ok {
		if st == "completed" {
			return "done"
		}
		return st
	}
	return "not started"
}

func (l look) title() string {
	return cmp.Or(l.meta.Title, "untitled")
}

// header is a thread's one-line description.
func (l look) header() string {
	model := l.meta.Harness
	if l.meta.Model != "" {
		model += "/" + l.meta.Model
	}
	return fmt.Sprintf("%s %q (%s): %s, %d %s", l.meta.ID, l.title(), model, statusWord(l.att, l.d), l.d.Turns, plural(l.d.Turns, "turn", "turns"))
}

// ---- thread_list, thread_read, task_status ----

func (s *Server) threadList(ctx context.Context, t toolThread) (string, error) {
	desc, err := s.mgr.Descendants(ctx, t.id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range desc {
		if m.ProjectID != t.projectID {
			continue
		}
		l, err := s.lookAt(ctx, m)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "- %s", l.header())
		if m.ParentID != t.id {
			fmt.Fprintf(&b, ", delegated by %s", m.ParentID)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "This thread has delegated nothing.", nil
	}
	return "Threads this thread delegated, and theirs (id \"title\" (harness/model): status, turns):\n" + b.String(), nil
}

type readArgs struct {
	ID    string `json:"id"`
	Since *int   `json:"since"`
}

func (s *Server) threadRead(ctx context.Context, t toolThread, body []byte) (string, error) {
	var a readArgs
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	meta, desc, err := s.readable(ctx, t, a.ID)
	if err != nil {
		return "", err
	}
	l, err := s.lookAt(ctx, meta)
	if err != nil {
		return "", err
	}
	since := 0
	if a.Since != nil {
		since = max(*a.Since, 0)
	}
	var turns []thread.TurnDigest
	next := l.d.Turns
	if actor, err := s.mgr.View(ctx, meta.ID); err == nil {
		if turns, next, err = actor.Transcript(ctx, since, readBudget); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Thread %s.\n", l.header())
	for _, tu := range turns {
		fmt.Fprintf(&b, "\n--- turn %d", tu.Index)
		switch {
		case !tu.Done:
			b.WriteString(" (running)")
		case tu.StopReason == proto.StopCancelled:
			b.WriteString(" (stopped)")
		case tu.Error != "":
			fmt.Fprintf(&b, " (failed: %s)", tu.Error)
		}
		fmt.Fprintf(&b, " ---\nPrompt:\n%s\n\nReply:\n%s\n", tu.Prompt, cmp.Or(tu.Reply, "(no reply)"))
	}
	switch {
	case len(turns) == 0 && since >= l.d.Turns:
		fmt.Fprintf(&b, "\nNo turns from %d on.", since)
	case next < l.d.Turns:
		fmt.Fprintf(&b, "\nMore follows: call thread_read again with since %d.", next)
	default:
		fmt.Fprintf(&b, "\nThat is the end. Pass since %d to read only what comes after.", next)
	}
	// Reading the end of a finished delegated thread is its result delivered.
	if desc && next >= l.d.Turns && len(turns) > 0 {
		if _, done := s.result(l); done {
			s.orch.delivered(meta.ID)
		}
	}
	return b.String(), nil
}

func (s *Server) taskStatus(ctx context.Context, t toolThread, body []byte) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	meta, err := s.steerable(ctx, t, a.ID)
	if err != nil {
		return "", err
	}
	l, err := s.lookAt(ctx, meta)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Thread %s.", l.header())
	if n := len(l.d.Questions); n > 0 {
		fmt.Fprintf(&b, " %d %s pending: read and answer with thread_pending, or ask the user.", n, plural(n, "question", "questions"))
	}
	if n := len(l.d.Approvals); n > 0 {
		fmt.Fprintf(&b, " %d %s pending: only the user can approve.", n, plural(n, "approval", "approvals"))
	}
	if st, done := s.result(l); done && l.d.Last != nil {
		reply := l.d.Last.Reply
		cut := utf8.RuneCountInString(reply) > statusReply
		fmt.Fprintf(&b, "\n\nLast reply (%s):\n%s", st, cmp.Or(thread.KeepTail(reply, statusReply), "(no reply)"))
		if cut {
			fmt.Fprintf(&b, "\n\nThat is its end; thread_read with id %s has all of it.", meta.ID)
		} else {
			s.orch.delivered(meta.ID)
		}
	}
	return b.String(), nil
}

// ---- thread_send, thread_interrupt ----

func (s *Server) threadSend(ctx context.Context, t toolThread, body []byte) (string, error) {
	var a struct {
		ID     string `json:"id"`
		Prompt string `json:"prompt"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	if a.Prompt = strings.TrimSpace(a.Prompt); a.Prompt == "" {
		return "", badArgs("prompt is required")
	}
	meta, err := s.steerable(ctx, t, a.ID)
	if err != nil {
		return "", err
	}
	actor, err := s.mgr.Get(ctx, meta.ID)
	if err != nil {
		return "", err
	}
	before, err := actor.Digest(ctx)
	if err != nil {
		return "", err
	}
	res, err := actor.PromptWith(ctx, a.Prompt, nil, proto.DeliveryDefault)
	if err != nil {
		return "", err
	}
	// The result is owed from the turn this prompt lands in: the running
	// one when it steered in, else the next.
	since, how := before.Turns, "started a turn"
	switch {
	case res.Queued():
		how = "queued behind its running turn"
	case before.Last != nil && !before.Last.Done && res.TurnID == before.Last.ID:
		since, how = before.Last.Index, "steered into its running turn"
	}
	s.orch.owe(meta.ID, t.id, since)
	s.mgr.NotifyList()
	return fmt.Sprintf("Sent to %s: %s. You will be woken with its result when it finishes; do not poll it.", meta.ID, how), nil
}

func (s *Server) threadInterrupt(ctx context.Context, t toolThread, body []byte) (string, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	meta, err := s.steerable(ctx, t, a.ID)
	if err != nil {
		return "", err
	}
	// Dropped first, so the stop it makes is never news.
	s.orch.abandon(meta.ID)
	if actor, ok := s.mgr.Peek(meta.ID); ok {
		if err := actor.Cancel(ctx); err != nil && !errors.Is(err, thread.ErrClosed) {
			return "", err
		}
	}
	n, err := s.stopDescendants(ctx, meta.ID)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return fmt.Sprintf("Stopped %s.", meta.ID), nil
	}
	return fmt.Sprintf("Stopped %s and the %d %s it delegated.", meta.ID, n, plural(n, "thread", "threads")), nil
}

// stopDescendants stops every thread under id, all the way down, and drops
// what they were owed to deliver: stopping a thread stops what it started.
// It returns how many there were.
func (s *Server) stopDescendants(ctx context.Context, id string) (int, error) {
	desc, err := s.mgr.Descendants(ctx, id)
	if err != nil {
		return 0, err
	}
	for _, m := range desc {
		s.orch.abandon(m.ID)
		if a, ok := s.mgr.Peek(m.ID); ok {
			if err := a.Cancel(ctx); err != nil && !errors.Is(err, thread.ErrClosed) {
				s.logf("stop delegated thread %s: %v", m.ID, err)
			}
		}
	}
	return len(desc), nil
}

// ---- waiting ----

type waitArgs struct {
	IDs     []string `json:"ids"`
	Timeout *int     `json:"timeout_seconds"`
}

func (s *Server) threadWait(ctx context.Context, t toolThread, body []byte) (string, bool, error) {
	var a waitArgs
	if err := decodeArgs(body, &a); err != nil {
		return "", false, err
	}
	if len(a.IDs) == 0 {
		return "", false, badArgs("ids is required: the delegated threads to wait on")
	}
	timeout, err := waitTimeout(a.Timeout)
	if err != nil {
		return "", false, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range a.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := s.steerable(ctx, t, id); err != nil {
			return "", false, err
		}
		ids = append(ids, id)
	}
	return s.waitOn(ctx, t, ids, timeout)
}

// waited is one thread at the end of a wait.
type waited struct {
	l      look
	status string // completed, failed, stopped, needs approval, needs answer, working
	done   bool
	// below is the thread under this one, delegated by it or by one it
	// delegated, whose approval it waits on; nil when it needs none.
	below *look
}

// waitOn waits until every thread has finished, one needs the user, or the
// timeout. Finished threads' results are delivered by the reply. The others
// are owed to the caller (those that were not yet, as a delegate_task that
// waited, become so), so a later result still wakes it; so are all of them
// when the caller leaves first, which gone reports.
func (s *Server) waitOn(ctx context.Context, t toolThread, ids []string, timeout time.Duration) (string, bool, error) {
	s.orch.hold(t.id, ids, true)
	defer func() {
		s.orch.hold(t.id, ids, false)
		// A result that landed while the wait held it, and that the wait did
		// not take, is the wake's again.
		s.orch.poke()
	}()
	sub, ping := s.mgr.SubscribeList()
	defer s.mgr.UnsubscribeList(sub)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var (
		got      []waited
		timedOut bool
	)
	for {
		var err error
		var settled bool
		got, settled, err = s.checkWait(ctx, ids)
		if err != nil {
			return "", false, err
		}
		if settled || timedOut {
			break
		}
		select {
		case <-ping:
		case <-timer.C:
			timedOut = true
		case <-ctx.Done():
			for _, w := range got {
				if !w.done {
					s.orch.oweUnlessOwed(w.l.meta.ID, t.id, pendingTurn(w.l.d))
				} else {
					s.orch.unheld(w.l.meta.ID)
				}
			}
			return "", true, nil
		}
	}

	var b strings.Builder
	budget := readBudget
	for i, w := range got {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Thread %s %q: %s", w.l.meta.ID, w.l.title(), w.status)
		switch {
		case w.done:
			s.orch.delivered(w.l.meta.ID)
			reply := ""
			if w.l.d.Last != nil {
				reply = w.l.d.Last.Reply
			}
			if w.l.d.Last != nil && w.l.d.Last.Error != "" {
				fmt.Fprintf(&b, " (%s)", w.l.d.Last.Error)
			}
			b.WriteString(".\n")
			b.WriteString(inline(w.l.meta.ID, reply, &budget))
			continue
		case w.status == "needs approval" && w.below != nil:
			fmt.Fprintf(&b, ": thread %s %q, delegated under it, needs the user's approval. Only the user can approve it; both wait until they do.", w.below.meta.ID, w.below.title())
			for _, p := range w.below.d.Approvals {
				fmt.Fprintf(&b, " Pending: %s.", cmp.Or(p.Title, p.ToolName))
			}
		case w.status == "needs approval":
			b.WriteString(": only the user can approve it; it waits until they do.")
			for _, p := range w.l.d.Approvals {
				fmt.Fprintf(&b, " Pending: %s.", cmp.Or(p.Title, p.ToolName))
			}
		case w.status == "needs answer":
			fmt.Fprintf(&b, ": read and answer it with thread_pending (id %s), or ask the user.", w.l.meta.ID)
			s.orch.announced(w.l.meta.ID, w.l.d.Questions)
		default:
			b.WriteString(".")
		}
		s.orch.claim(w.l.meta.ID, t.id, pendingTurn(w.l.d))
		b.WriteString(" You will be woken with its result when it finishes.")
	}
	if timedOut {
		b.WriteString("\n\nThe wait timed out; the threads still at work carry on. Do not wait again straight away: you will be woken when they finish.")
	}
	return b.String(), false, nil
}

// pendingTurn is the index of the turn a thread's result will come from:
// the running one, else the next.
func pendingTurn(d thread.Digest) int {
	if d.Last != nil && !d.Last.Done {
		return d.Last.Index
	}
	return d.Turns
}

// checkWait reads each thread; settled is every one finished or one
// needing the user, itself or through a thread delegated under it waiting
// on an approval: only the user can move that, so waiting on is waiting
// for nothing.
func (s *Server) checkWait(ctx context.Context, ids []string) ([]waited, bool, error) {
	out := make([]waited, 0, len(ids))
	all, user := true, false
	var tree map[string][]store.ThreadMeta // by parent, read once
	for _, id := range ids {
		meta, err := s.store.Thread(ctx, id)
		if err != nil {
			return nil, false, notFound(id)
		}
		l, err := s.lookAt(ctx, meta)
		if err != nil {
			return nil, false, err
		}
		w := waited{l: l, status: "working"}
		if st, ok := s.result(l); ok {
			w.status, w.done = st, true
		} else {
			all = false
			switch l.att {
			case projection.AttentionNeedsPermission:
				w.status, user = "needs approval", true
			case projection.AttentionNeedsAnswer:
				w.status, user = "needs answer", true
			default:
				if tree == nil {
					if tree, err = s.childrenByParent(ctx); err != nil {
						return nil, false, err
					}
				}
				if below, ok := s.approvalBelow(ctx, tree, meta); ok {
					w.status, w.below, user = "needs approval", &below, true
				}
			}
		}
		out = append(out, w)
	}
	return out, all || user, nil
}

// childrenByParent is the thread list as a tree, with each thread's
// attention as the list reports it.
func (s *Server) childrenByParent(ctx context.Context) (map[string][]store.ThreadMeta, error) {
	list, err := s.mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	tree := map[string][]store.ThreadMeta{}
	for _, m := range list {
		if m.ParentID != "" {
			tree[m.ParentID] = append(tree[m.ParentID], m)
		}
	}
	return tree, nil
}

// approvalBelow is the nearest thread under top, in its project, waiting on
// the user's approval. A question below is not the user's alone: the thread
// that delegated it answers or passes it up, and is woken for it.
func (s *Server) approvalBelow(ctx context.Context, tree map[string][]store.ThreadMeta, top store.ThreadMeta) (look, bool) {
	seen := map[string]bool{top.ID: true}
	for queue := []string{top.ID}; len(queue) > 0; queue = queue[1:] {
		for _, m := range tree[queue[0]] {
			if seen[m.ID] || m.ProjectID != top.ProjectID {
				continue
			}
			seen[m.ID] = true
			if m.Attention == projection.AttentionNeedsPermission {
				if l, err := s.lookAt(ctx, m); err == nil {
					return l, true
				}
			}
			queue = append(queue, m.ID)
		}
	}
	return look{}, false
}

// inline is a reply as far as budget allows, the tail kept when it is cut,
// else a pointer to thread_read.
func inline(id, reply string, budget *int) string {
	if reply == "" {
		return "(no reply)"
	}
	n := utf8.RuneCountInString(reply)
	if n <= *budget {
		*budget -= n
		return reply
	}
	if *budget >= 512 {
		out := thread.KeepTail(reply, *budget) + "\n… (its start is cut: use thread_read with id " + id + " to read the whole result.)"
		*budget = 0
		return out
	}
	return "… use thread_read with id " + id + " to read the result."
}

// ---- thread_pending ----

type pendingArgs struct {
	ID        string                     `json:"id"`
	Action    string                     `json:"action"`
	RequestID string                     `json:"request_id"`
	Answers   map[string]json.RawMessage `json:"answers"`
}

// field is one property of a question's schema.
type field struct {
	key, title, description, kind string
	options                       []string
	optionNotes                   map[string]string
	multi, other, required        bool
}

func questionFields(schema json.RawMessage) []field {
	var sch struct {
		Properties map[string]struct {
			Type        string               `json:"type"`
			Title       string               `json:"title"`
			Description string               `json:"description"`
			Enum        []any                `json:"enum"`
			Items       struct{ Enum []any } `json:"items"`
			Notes       map[string]string    `json:"x-optionDescriptions"`
			Multi       bool                 `json:"x-multiSelect"`
			Other       bool                 `json:"x-allowOther"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	_ = json.Unmarshal(schema, &sch)
	var out []field
	for key, p := range sch.Properties {
		f := field{key: key, title: p.Title, description: p.Description, kind: cmp.Or(p.Type, "string"), optionNotes: p.Notes, multi: p.Multi || p.Type == "array", other: p.Other, required: slices.Contains(sch.Required, key)}
		enum := p.Enum
		if len(enum) == 0 {
			enum = p.Items.Enum
		}
		for _, e := range enum {
			f.options = append(f.options, fmt.Sprint(e))
		}
		out = append(out, f)
	}
	// q0, q1, … in their order, not the map's.
	sort.Slice(out, func(i, j int) bool { return natLess(out[i].key, out[j].key) })
	return out
}

func natLess(a, b string) bool {
	ai, aerr := strconv.Atoi(strings.TrimLeft(a, "abcdefghijklmnopqrstuvwxyz_"))
	bi, berr := strconv.Atoi(strings.TrimLeft(b, "abcdefghijklmnopqrstuvwxyz_"))
	if aerr == nil && berr == nil && strings.TrimRight(a, "0123456789") == strings.TrimRight(b, "0123456789") {
		return ai < bi
	}
	return a < b
}

func (f field) render(b *strings.Builder) {
	fmt.Fprintf(b, "- %s", f.key)
	if f.title != "" {
		fmt.Fprintf(b, ": %s", f.title)
	}
	if f.description != "" {
		fmt.Fprintf(b, " (%s)", f.description)
	}
	switch {
	case len(f.options) > 0 && f.multi:
		b.WriteString(". Pick one or more")
	case len(f.options) > 0:
		b.WriteString(". Pick one")
	case f.kind == "boolean":
		b.WriteString(". true or false")
	case f.kind == "number" || f.kind == "integer":
		fmt.Fprintf(b, ". A %s", f.kind)
	default:
		b.WriteString(". Free text")
	}
	if f.other {
		b.WriteString(", or answer in your own words")
	}
	if !f.required {
		b.WriteString(". Optional")
	}
	b.WriteString(".\n")
	for _, o := range f.options {
		fmt.Fprintf(b, "  - %s", o)
		if n := f.optionNotes[o]; n != "" {
			fmt.Fprintf(b, ": %s", n)
		}
		b.WriteString("\n")
	}
}

func (s *Server) threadPending(ctx context.Context, t toolThread, body []byte) (string, error) {
	var a pendingArgs
	if err := decodeArgs(body, &a); err != nil {
		return "", err
	}
	meta, err := s.steerable(ctx, t, a.ID)
	if err != nil {
		return "", err
	}
	var d thread.Digest
	actor, live := s.mgr.Peek(meta.ID)
	if live {
		if d, err = actor.Digest(ctx); err != nil {
			return "", err
		}
	}
	approvals := ""
	if n := len(d.Approvals); n > 0 {
		approvals = fmt.Sprintf("%d %s pending: only the user can approve %s; you cannot.", n, plural(n, "approval", "approvals"), plural(n, "it", "them"))
	}
	find := func() (thread.PendingQuestion, error) {
		if a.RequestID == "" {
			return thread.PendingQuestion{}, badArgs("request_id is required: take it from action list")
		}
		for _, q := range d.Questions {
			if q.RequestID == a.RequestID {
				return q, nil
			}
		}
		for _, p := range d.Approvals {
			if p.RequestID == a.RequestID {
				return thread.PendingQuestion{}, forbidden("%s is a permission request; only the user can approve it", a.RequestID)
			}
		}
		return thread.PendingQuestion{}, badArgs("thread %s has no pending question %s; list them with action list", meta.ID, a.RequestID)
	}

	switch a.Action {
	case "", "list":
		var b strings.Builder
		for _, q := range d.Questions {
			fs := questionFields(q.Schema)
			fmt.Fprintf(&b, "- %s: %s", q.RequestID, q.Prompt)
			for _, f := range fs {
				if f.title != "" {
					fmt.Fprintf(&b, " / %s", f.title)
				}
			}
			b.WriteString("\n")
		}
		if b.Len() == 0 {
			return strings.TrimSpace("Thread " + meta.ID + " has no pending questions. " + approvals), nil
		}
		out := "Pending questions (read one with action read):\n" + b.String()
		if approvals != "" {
			out += approvals
		}
		return out, nil
	case "read":
		q, err := find()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Question %s: %s\n", q.RequestID, q.Prompt)
		for _, f := range questionFields(q.Schema) {
			f.render(&b)
		}
		b.WriteString("Answer with action respond and answers: an object keyed by each field's id above (or its question text).")
		return b.String(), nil
	case "respond":
		q, err := find()
		if err != nil {
			return "", err
		}
		value, err := mapAnswers(questionFields(q.Schema), a.Answers)
		if err != nil {
			return "", err
		}
		err = actor.ResolveElicitation(ctx, q.RequestID, adapter.ElicitationResult{Action: "accept", Value: value})
		if errors.Is(err, thread.ErrAlreadyResolved) {
			return "That question was already answered.", nil
		}
		if err != nil {
			return "", err
		}
		s.mgr.NotifyList()
		return fmt.Sprintf("Answered %s; thread %s carries on.", q.RequestID, meta.ID), nil
	}
	return "", badArgs("action is list, read or respond, not %q", a.Action)
}

// mapAnswers turns an agent's answers into the value a question takes:
// keyed by field id, each the shape its schema asks for. A key may be the
// field's question text instead of its id.
func mapAnswers(fields []field, answers map[string]json.RawMessage) (json.RawMessage, error) {
	if len(answers) == 0 {
		return nil, badArgs("answers is required: an object keyed by field id")
	}
	byKey := map[string]field{}
	for _, f := range fields {
		byKey[f.key] = f
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	out := map[string]any{}
	for k, raw := range answers {
		f, ok := byKey[k]
		if !ok {
			for _, c := range fields {
				if c.title != "" && norm(c.title) == norm(k) {
					f, ok = c, true
					break
				}
			}
		}
		if !ok {
			keys := make([]string, len(fields))
			for i, f := range fields {
				keys[i] = f.key
			}
			return nil, badArgs("the question has no field %q; its fields are %s", k, strings.Join(keys, ", "))
		}
		v, err := f.value(raw)
		if err != nil {
			return nil, err
		}
		out[f.key] = v
	}
	for _, f := range fields {
		if _, ok := out[f.key]; !ok && f.required {
			return nil, badArgs("field %s is required", f.key)
		}
	}
	b, err := json.Marshal(out)
	return b, err
}

// value checks one answer against its field.
func (f field) value(raw json.RawMessage) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, badArgs("answer for %s: %v", f.key, err)
	}
	pick := func(s string) (string, error) {
		if len(f.options) == 0 || f.other {
			return s, nil
		}
		for _, o := range f.options {
			if strings.EqualFold(o, strings.TrimSpace(s)) {
				return o, nil
			}
		}
		return "", badArgs("%s is one of %s, not %q", f.key, strings.Join(f.options, ", "), s)
	}
	switch {
	case f.multi:
		var items []string
		switch x := v.(type) {
		case string:
			items = []string{x}
		case []any:
			for _, e := range x {
				s, ok := e.(string)
				if !ok {
					return nil, badArgs("answer for %s is a list of strings", f.key)
				}
				items = append(items, s)
			}
		default:
			return nil, badArgs("answer for %s is a list of strings", f.key)
		}
		out := make([]string, 0, len(items))
		for _, s := range items {
			p, err := pick(s)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
		return out, nil
	case f.kind == "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			if b, err := strconv.ParseBool(x); err == nil {
				return b, nil
			}
		}
		return nil, badArgs("answer for %s is true or false", f.key)
	case f.kind == "number" || f.kind == "integer":
		var n float64
		switch x := v.(type) {
		case float64:
			n = x
		case string:
			p, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return nil, badArgs("answer for %s is a number", f.key)
			}
			n = p
		default:
			return nil, badArgs("answer for %s is a number", f.key)
		}
		if f.kind == "integer" && n != math.Trunc(n) {
			return nil, badArgs("answer for %s is a whole number", f.key)
		}
		return n, nil
	}
	switch x := v.(type) {
	case string:
		return pick(x)
	case float64, bool:
		return pick(fmt.Sprint(x))
	}
	return nil, badArgs("answer for %s is a string", f.key)
}

// ---- the orchestrator: wakes and pushes ----

// owed is a delegated thread whose result its parent has not had yet.
type owed struct {
	parent string
	since  int // the result is the first turn finished from this index on
}

// wakeEntry is one delegated thread's news in a parent's wake.
type wakeEntry struct {
	id, title, status, text string
	// gone is news delivered another way before its wake went: left out.
	gone bool
}

// wakeBatch is the wake a parent is getting: collected for wakeWindow, then
// queued on the parent. While it waits in the parent's queue, later news is
// added to it rather than sent again.
type wakeBatch struct {
	entries []wakeEntry
	sent    int    // entries in what the parent holds
	queueID string // the queued prompt, "" before it is sent
	// restored is a wake the last process queued: since the restart the
	// parent holds it for a human, ran it, or lost it to a stop. flush
	// settles which before sending anything.
	restored bool
}

// orchestrator owes parents their delegated threads' results and wakes them
// with those, batched, and pushes the user a notification when a delegated
// thread needs them. What is owed and the wakes on their way are written
// through to the store as they change, and read back at start, so a restart
// leaves a parent still owed its results and still due the wake it had not
// had. The rest — waits, questions told, pushes — lives in memory only.
type orchestrator struct {
	s *Server
	// send pushes a notification; nil turns pushes off. watched reports a
	// thread on a user's screen.
	send    func(context.Context, push.Message)
	watched func(string) bool

	mu sync.Mutex
	// owed is keyed by child.
	owed map[string]owed
	// abandoned is the delegated threads stopped from above: owed nothing
	// until something sends them work again.
	abandoned map[string]bool
	// held counts waits on a thread: its result goes to the wait, not a wake.
	held map[string]int
	// asked is the questions already told to the parent, by child.
	asked map[string]map[string]bool
	// batches is keyed by parent.
	batches map[string]*wakeBatch
	// pushed is the last state each delegated thread was pushed for, so a
	// state is pushed once.
	pushed map[string]string
	primed bool

	sendMu sync.Mutex    // one wake delivery at a time
	kick   chan struct{} // a scan without a list change
	stop   chan struct{}
	done   chan struct{}
}

func newOrchestrator(s *Server) *orchestrator {
	o := &orchestrator{s: s, watched: s.watched, kick: make(chan struct{}, 1), owed: map[string]owed{}, abandoned: map[string]bool{}, held: map[string]int{}, asked: map[string]map[string]bool{}, batches: map[string]*wakeBatch{}, pushed: map[string]string{}}
	if s.push != nil {
		o.send = func(ctx context.Context, m push.Message) {
			if _, err := s.push.Send(ctx, m); err != nil {
				s.logf("push for delegated thread %s: %v", m.ThreadID, err)
			}
		}
	}
	return o
}

// start runs the orchestrator until close, rechecking on every change to
// the thread list.
func (o *orchestrator) start() {
	o.stop, o.done = make(chan struct{}), make(chan struct{})
	wakes := o.load()
	for _, parent := range wakes {
		go o.flush(parent)
	}
	sub, ping := o.s.mgr.SubscribeList()
	go func() {
		defer close(o.done)
		defer o.s.mgr.UnsubscribeList(sub)
		o.scan(context.Background())
		for {
			select {
			case <-o.stop:
				return
			case <-ping:
				o.scan(context.Background())
			case <-o.kick:
				o.scan(context.Background())
			}
		}
	}()
}

// load reads back what the last process left owed and on its way, and
// returns the parents with a wake to send.
func (o *orchestrator) load() []string {
	if o.s.store == nil {
		return nil
	}
	ctx := context.Background()
	owedRows, err := o.s.store.OwedResults(ctx)
	if err != nil {
		o.s.logf("read delegated results owed: %v", err)
	}
	wakeRows, err := o.s.store.PendingWakes(ctx)
	if err != nil {
		o.s.logf("read wakes on their way: %v", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range owedRows {
		o.owed[r.Child] = owed{parent: r.Parent, since: r.Since}
	}
	var parents []string
	for _, r := range wakeRows {
		b := &wakeBatch{queueID: r.QueueID, sent: min(max(r.Sent, 0), len(r.Entries)), restored: r.QueueID != ""}
		for _, e := range r.Entries {
			b.entries = append(b.entries, wakeEntry{id: e.ID, title: e.Title, status: e.Status, text: e.Text, gone: e.Gone})
		}
		if !b.restored {
			// Never queued: all of it is still to send.
			if b.entries, b.sent = b.entries[b.sent:], 0; len(b.entries) == 0 {
				o.persistWake(r.Parent)
				continue
			}
		}
		o.batches[r.Parent] = b
		parents = append(parents, r.Parent)
	}
	return parents
}

// persistOwed writes child's row as o.owed has it, under o.mu, so the store
// changes in the order memory does. A failed write costs only a restart's
// memory of it.
func (o *orchestrator) persistOwed(child string) {
	if o.s.store == nil {
		return
	}
	ctx := context.Background()
	var err error
	if w, ok := o.owed[child]; ok {
		err = o.s.store.SaveOwed(ctx, store.OwedResult{Child: child, Parent: w.parent, Since: w.since})
	} else {
		err = o.s.store.DeleteOwed(ctx, child)
	}
	if err != nil {
		o.s.logf("record result owed by %s: %v", child, err)
	}
}

// persistWake writes parent's row as o.batches has it, under o.mu.
func (o *orchestrator) persistWake(parent string) {
	if o.s.store == nil {
		return
	}
	ctx := context.Background()
	var err error
	if b := o.batches[parent]; b != nil {
		w := store.PendingWake{Parent: parent, QueueID: b.queueID, Sent: b.sent, Entries: make([]store.WakeEntry, 0, len(b.entries))}
		for _, e := range b.entries {
			w.Entries = append(w.Entries, store.WakeEntry{ID: e.id, Title: e.title, Status: e.status, Text: e.text, Gone: e.gone})
		}
		err = o.s.store.SaveWake(ctx, w)
	} else {
		err = o.s.store.DeleteWake(ctx, parent)
	}
	if err != nil {
		o.s.logf("record wake for %s: %v", parent, err)
	}
}

func (o *orchestrator) close() {
	if o.stop == nil {
		return
	}
	select {
	case <-o.stop:
	default:
		close(o.stop)
	}
	<-o.done
}

// poke asks for a scan.
func (o *orchestrator) poke() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// owe records that parent is owed child's result from turn since on.
func (o *orchestrator) owe(child, parent string, since int) {
	o.mu.Lock()
	o.owed[child] = owed{parent: parent, since: since}
	delete(o.abandoned, child)
	o.persistOwed(child)
	o.mu.Unlock()
}

// oweUnlessOwed is owe for a thread not owed already: one that is keeps
// the turn its result is owed from. A thread stopped from above is not
// owed again by a wait that saw it before the stop.
func (o *orchestrator) oweUnlessOwed(child, parent string, since int) {
	o.mu.Lock()
	if _, ok := o.owed[child]; !ok {
		if !o.abandoned[child] {
			o.owed[child] = owed{parent: parent, since: since}
		}
		// Either way the wait's provisional row goes (hold).
		o.persistOwed(child)
	}
	o.mu.Unlock()
}

// claim is oweUnlessOwed for a parent asking after child itself: it is
// owed even when it was stopped from above before.
func (o *orchestrator) claim(child, parent string, since int) {
	o.mu.Lock()
	if _, ok := o.owed[child]; !ok {
		o.owed[child] = owed{parent: parent, since: since}
		o.persistOwed(child)
	}
	delete(o.abandoned, child)
	o.mu.Unlock()
}

// abandon is delivered for a thread stopped from above: its stop is no
// one's news.
func (o *orchestrator) abandon(child string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.drop(child)
	o.abandoned[child] = true
}

// busy reports a thread still collecting what it delegated: a result owed
// to it, or news on its way to it. Its own result is not in until those
// have woken it and it has answered.
func (o *orchestrator) busy(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if b := o.batches[id]; b != nil && (b.restored || b.queueID == "" && len(b.entries) > b.sent) {
		return true
	}
	for child, w := range o.owed {
		if w.parent == id && !o.abandoned[child] {
			return true
		}
	}
	return false
}

// delivered drops what child owed: its result reached the parent another
// way, or the parent stopped it. News of it still waiting for a wake's
// window goes too; a wake already in the parent's queue keeps it.
func (o *orchestrator) delivered(child string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.drop(child)
}

// drop is delivered under o.mu.
func (o *orchestrator) drop(child string) {
	delete(o.owed, child)
	o.persistOwed(child)
	for parent, b := range o.batches {
		changed := false
		for i := b.sent; i < len(b.entries); i++ {
			if b.entries[i].id == child && !b.entries[i].gone {
				b.entries[i].gone, changed = true, true
			}
		}
		if changed {
			o.persistWake(parent)
		}
	}
}

func (o *orchestrator) since(child string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.owed[child].since
}

// hold turns parent's wait on ids on or off. A wait owes its threads to the
// caller only at its end, which a restart never reaches: so each one not owed
// already is written to the store as owed to the caller from the start, and
// a restart mid-wait leaves the result owed. Memory is left alone, where
// owing it would change what the wait counts as its result. The end of the
// wait settles the row: delivered, claimed, owed, or unheld.
func (o *orchestrator) hold(parent string, ids []string, on bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, id := range ids {
		if !on {
			if o.held[id]--; o.held[id] <= 0 {
				delete(o.held, id)
			}
			continue
		}
		o.held[id]++
		if _, ok := o.owed[id]; ok || o.abandoned[id] || o.s.store == nil {
			continue
		}
		if err := o.s.store.SaveOwed(context.Background(), store.OwedResult{Child: id, Parent: parent}); err != nil {
			o.s.logf("record result owed by %s: %v", id, err)
		}
	}
}

// unheld drops the row hold wrote for a thread whose result a wait had but
// its caller left before taking: not owed, as before.
func (o *orchestrator) unheld(child string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.owed[child]; !ok {
		o.persistOwed(child)
	}
}

// announced marks questions as told to the parent.
func (o *orchestrator) announced(child string, qs []thread.PendingQuestion) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, q := range qs {
		if o.asked[child] == nil {
			o.asked[child] = map[string]bool{}
		}
		o.asked[child][q.RequestID] = true
	}
}

// scan looks at every thread once: what owed threads have to tell their
// parents, and which delegated threads need the user.
func (o *orchestrator) scan(ctx context.Context) {
	list, err := o.s.mgr.List(ctx)
	if err != nil {
		return
	}
	byID := make(map[string]store.ThreadMeta, len(list))
	for _, m := range list {
		byID[m.ID] = m
	}
	o.wakes(ctx, byID)
	o.pushes(ctx, list, byID)
}

func (o *orchestrator) wakes(ctx context.Context, byID map[string]store.ThreadMeta) {
	o.mu.Lock()
	type due struct {
		child string
		owed
	}
	var check []due
	for child, w := range o.owed {
		if o.held[child] == 0 {
			check = append(check, due{child, w})
		}
	}
	o.mu.Unlock()

	for _, c := range check {
		meta, ok := byID[c.child]
		if !ok {
			// Created after this scan read the list, and the ping that made
			// it brings another; or deleted, and owed nothing.
			if _, err := o.s.store.Thread(ctx, c.child); err != nil {
				o.delivered(c.child)
			}
			continue
		}
		if parent, ok := byID[c.parent]; !ok || parent.Attention == projection.AttentionClosed {
			o.delivered(c.child)
			continue
		}
		var d thread.Digest
		att := meta.Attention
		if a, ok := o.s.mgr.Peek(c.child); ok {
			if got, err := a.Digest(ctx); err == nil {
				d, att = got, got.Attention
			}
		} else if a, err := o.s.mgr.View(ctx, c.child); err == nil {
			d, _ = a.Digest(ctx)
		}
		title := cmp.Or(meta.Title, "untitled")
		if st, ok := finished(att, d, c.since); ok {
			if o.busy(c.child) {
				// It waits on what it delegated: its answer comes after.
				continue
			}
			reply := ""
			if d.Last != nil {
				reply = d.Last.Reply
				if d.Last.Error != "" {
					reply = strings.TrimSpace(reply + "\n(error: " + d.Last.Error + ")")
				}
			}
			// Taken off owed and put in the parent's batch in one step, so
			// the parent is never seen with neither.
			o.mu.Lock()
			if cur, still := o.owed[c.child]; !still || cur != c.owed {
				o.mu.Unlock()
				continue
			}
			delete(o.owed, c.child)
			o.persistOwed(c.child)
			fresh, queued := o.enqueue(c.parent, wakeEntry{id: c.child, title: title, status: st, text: reply})
			o.mu.Unlock()
			o.sendAfter(c.parent, fresh, queued)
			continue
		}
		if att != projection.AttentionNeedsAnswer {
			continue
		}
		for _, q := range d.Questions {
			o.mu.Lock()
			told := o.asked[c.child][q.RequestID]
			if !told {
				if o.asked[c.child] == nil {
					o.asked[c.child] = map[string]bool{}
				}
				o.asked[c.child][q.RequestID] = true
			}
			o.mu.Unlock()
			if told {
				continue
			}
			var b strings.Builder
			b.WriteString(q.Prompt)
			for _, f := range questionFields(q.Schema) {
				if f.title != "" {
					fmt.Fprintf(&b, "\n- %s", f.title)
				}
			}
			fmt.Fprintf(&b, "\nRead and answer it with thread_pending (id %s, request_id %s), or ask the user. Its result will still wake you when it finishes.", c.child, q.RequestID)
			o.add(c.parent, wakeEntry{id: c.child, title: title, status: "needs answer", text: b.String()})
		}
	}
}

// add puts news in the parent's wake: a new batch sent after wakeWindow,
// or the batch already waiting in the parent's queue, amended now.
func (o *orchestrator) add(parent string, e wakeEntry) {
	o.mu.Lock()
	fresh, queued := o.enqueue(parent, e)
	o.mu.Unlock()
	o.sendAfter(parent, fresh, queued)
}

// enqueue puts e in the parent's batch, under o.mu: fresh is a new batch,
// queued one already in the parent's queue.
func (o *orchestrator) enqueue(parent string, e wakeEntry) (fresh, queued bool) {
	b := o.batches[parent]
	fresh = b == nil
	if fresh {
		b = &wakeBatch{}
		o.batches[parent] = b
	}
	b.entries = append(b.entries, e)
	o.persistWake(parent)
	return fresh, b.queueID != ""
}

// sendAfter schedules what enqueue made due.
func (o *orchestrator) sendAfter(parent string, fresh, queued bool) {
	switch {
	case fresh:
		o.later(parent)
	case queued:
		o.flush(parent)
	}
}

// later flushes the parent's batch once its window has passed.
func (o *orchestrator) later(parent string) {
	time.AfterFunc(wakeWindow, func() { o.flush(parent) })
}

func (o *orchestrator) stopped() bool {
	if o.stop == nil {
		return false
	}
	select {
	case <-o.stop:
		return true
	default:
		return false
	}
}

// flush delivers a parent's batch: amends the wake it holds queued, or
// queues one. A wake that has started takes no more; what came after it
// starts a batch of its own, with a window of its own.
func (o *orchestrator) flush(parent string) {
	o.sendMu.Lock()
	defer o.sendMu.Unlock()
	if o.stopped() {
		return
	}
	ctx := context.Background()
	for {
		o.mu.Lock()
		b := o.batches[parent]
		if b == nil || !b.restored && len(b.entries) == b.sent {
			o.mu.Unlock()
			return
		}
		entries, qid, restored := slices.Clone(b.entries), b.queueID, b.restored
		o.mu.Unlock()
		if restored {
			if !o.settle(ctx, parent, b) {
				return
			}
			continue
		}
		text, some := wakeText(entries)
		if !some && qid == "" {
			// Everything in it was delivered another way: unless more came
			// meanwhile, there is nothing to wake the parent for.
			o.mu.Lock()
			more := len(b.entries) > len(entries)
			if !more && o.batches[parent] == b {
				delete(o.batches, parent)
				o.persistWake(parent)
			}
			o.mu.Unlock()
			if more {
				continue
			}
			return
		}

		actor, ok := o.reach(ctx, parent, b)
		if !ok {
			return
		}
		if qid != "" {
			if actor.EditQueuedPrompt(ctx, qid, text, nil) == nil {
				o.mu.Lock()
				b.sent = len(entries)
				o.persistWake(parent)
				o.mu.Unlock()
				continue
			}
			// It started: what it did not hold starts a batch of its own.
			o.mu.Lock()
			if o.batches[parent] == b {
				o.batches[parent] = &wakeBatch{entries: slices.Clone(b.entries[b.sent:])}
				o.persistWake(parent)
			}
			o.mu.Unlock()
			o.later(parent)
			return
		}
		// A parent a restart cut off goes on with its own work first: a wake
		// that started ahead of that continuation would take its place.
		if err := actor.Recover(ctx); err != nil {
			o.s.logf("continue %s before its wake: %v", parent, err)
		}
		res, err := actor.PromptWith(ctx, text, nil, proto.DeliveryQueue)
		if err != nil {
			o.s.logf("wake %s: %v", parent, err)
			o.forget(parent, b)
			return
		}
		o.s.mgr.NotifyList()
		o.mu.Lock()
		b.sent = len(entries)
		if res.Queued() {
			b.queueID = res.QueueID
		} else if o.batches[parent] == b {
			// Started at once: anything later is a new wake.
			delete(o.batches, parent)
			if len(b.entries) > b.sent {
				o.batches[parent] = &wakeBatch{entries: slices.Clone(b.entries[b.sent:])}
				defer o.later(parent)
			}
		}
		o.persistWake(parent)
		o.mu.Unlock()
		if res.Queued() {
			// Later news amends it while it waits; nothing to send now.
			continue
		}
		return
	}
}

// reach is the parent a wake goes to, with its harness up. One closed or
// gone, or that will not start, has its wake forgotten.
func (o *orchestrator) reach(ctx context.Context, parent string, b *wakeBatch) (*thread.Actor, bool) {
	meta, err := o.s.store.Thread(ctx, parent)
	if err != nil || meta.Phase == "closed" {
		o.forget(parent, b)
		return nil, false
	}
	actor, err := o.s.mgr.Get(ctx, parent)
	if err != nil {
		o.s.logf("wake %s: %v", parent, err)
		o.forget(parent, b)
		return nil, false
	}
	return actor, true
}

// forget drops a parent's batch b, unless another has taken its place.
func (o *orchestrator) forget(parent string, b *wakeBatch) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.batches[parent] == b {
		delete(o.batches, parent)
		o.persistWake(parent)
	}
}

// settle finds out what became of a wake the last process queued, and
// reports whether there is more to do. Still in the parent's queue — held
// there since the restart, waiting for a human — it is taken back, to go
// again as a wake of this process's. Gone from it, it ran, or a stop dropped
// it with the turn it waited behind, and what it held is done either way.
// Getting the parent is what starts its harness and holds its queue, so
// the queue is settled by the time the wake is looked for.
func (o *orchestrator) settle(ctx context.Context, parent string, b *wakeBatch) bool {
	actor, ok := o.reach(ctx, parent, b)
	if !ok {
		return false
	}
	o.mu.Lock()
	qid := b.queueID
	o.mu.Unlock()
	taken := actor.DequeuePrompt(ctx, qid) == nil
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.batches[parent] != b {
		return true
	}
	if !taken {
		b.entries = slices.Clone(b.entries[b.sent:])
	}
	b.restored, b.queueID, b.sent = false, "", 0
	if len(b.entries) == 0 {
		delete(o.batches, parent)
	}
	o.persistWake(parent)
	return true
}

// wakeText is the prompt that wakes a parent; some is false when every
// entry is gone.
func wakeText(entries []wakeEntry) (text string, some bool) {
	head := "[Omniplex] Delegated threads finished."
	for _, e := range entries {
		if e.status == "needs answer" && !e.gone {
			head = "[Omniplex] News from threads you delegated."
			break
		}
	}
	parts := []string{head}
	budget := wakeBudget
	for _, e := range entries {
		if e.gone {
			continue
		}
		parts = append(parts, fmt.Sprintf("Delegated thread %s [%s]:\n%s", threadLink(e.id, e.title), e.status, inline(e.id, e.text, &budget)))
	}
	return strings.Join(parts, "\n\n"), len(parts) > 1
}

// threadLink is a thread as an omniplex-thread:// link, which the web app
// shows as a pill that opens it, so a finished child stays a tap away from
// the message that reported it.
func threadLink(id, title string) string {
	esc := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(title)
	return "[@" + esc + "](omniplex-thread://" + id + ")"
}

// pushes notifies the user once each time a delegated thread comes to need
// them, unless they are looking at it or at its parent. Tapping it opens
// the delegated thread.
func (o *orchestrator) pushes(ctx context.Context, list []store.ThreadMeta, byID map[string]store.ThreadMeta) {
	o.mu.Lock()
	primed := o.primed
	o.primed = true
	o.mu.Unlock()
	seen := map[string]bool{}
	for _, m := range list {
		if m.ParentID == "" {
			continue
		}
		seen[m.ID] = true
		key, what, detail := "", "", ""
		switch {
		case m.Attention == projection.AttentionNeedsPermission, m.Attention == projection.AttentionNeedsAnswer:
			what = "needs approval"
			if m.Attention == projection.AttentionNeedsAnswer {
				what = "needs an answer"
			}
			key = m.Attention
			if a, ok := o.s.mgr.Peek(m.ID); ok {
				if d, err := a.Digest(ctx); err == nil {
					for _, p := range d.Approvals {
						key += " " + p.RequestID
						detail = cmp.Or(detail, p.Title, p.ToolName)
					}
					for _, q := range d.Questions {
						key += " " + q.RequestID
						detail = cmp.Or(detail, q.Prompt)
					}
				}
			}
		case m.Attention == projection.AttentionFailed:
			key, what = "failed", "failed"
		case m.TurnFailed:
			what, key = "failed", "turn failed"
			if a, ok := o.s.mgr.Peek(m.ID); ok {
				if d, err := a.Digest(ctx); err == nil && d.Last != nil {
					key += " " + d.Last.ID
					detail = d.Last.Error
				}
			}
		}
		o.mu.Lock()
		last := o.pushed[m.ID]
		if key == "" {
			delete(o.pushed, m.ID)
		} else {
			o.pushed[m.ID] = key
		}
		o.mu.Unlock()
		if key == "" || key == last || !primed || o.send == nil {
			continue
		}
		if o.watched != nil && (o.watched(m.ID) || o.watched(m.ParentID)) {
			continue
		}
		title := clip(cmp.Or(m.Title, "A delegated thread")+" "+what, maxNotifyTitle)
		body := "Delegated by " + cmp.Or(byID[m.ParentID].Title, "another thread")
		if detail != "" {
			body = clip(detail, maxNotifyBody-utf8.RuneCountInString(body)-1) + "\n" + body
		}
		urgency := "high"
		if what == "failed" {
			urgency = "normal"
		}
		o.send(ctx, push.Message{Title: title, Body: clip(body, maxNotifyBody), ThreadID: m.ID, Urgency: urgency})
	}
	o.mu.Lock()
	for id := range o.pushed {
		if !seen[id] {
			delete(o.pushed, id)
		}
	}
	for id := range o.asked {
		if _, ok := byID[id]; !ok {
			delete(o.asked, id)
		}
	}
	for id := range o.abandoned {
		if _, ok := byID[id]; !ok {
			delete(o.abandoned, id)
		}
	}
	o.mu.Unlock()
}
