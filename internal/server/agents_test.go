package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// The agent commands end to end: create one with its first thread, change
// its prompt while that thread is busy, read its revisions and log, open
// epoch 0, move it to a new thread and archive it, with every change pushed
// as an agents frame.
func TestAgentCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(dir, "config.json"))
	st, err := store.Open(filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ad := &countingAdapter{}
	ctx := context.Background()
	now := proto.NowMillis()
	p := project.Project{ID: "p1", Name: "p1", Defaults: project.NormalizeDefaults(project.Defaults{}), Folders: []project.Folder{project.NewFolder("f1", t.TempDir())}, CreatedAt: now, UpdatedAt: now}
	p.Defaults.Harness = ad.ID()
	if err := st.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	mgr := newManager(st, t.Logf, ad)
	t.Cleanup(mgr.Shutdown)
	h := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort)}).Handler()
	c := dialWS(t, h)

	var created struct {
		Agent    store.Agent `json:"agent"`
		ThreadID string      `json:"threadId"`
	}
	c.ok("create_agent", map[string]any{"projectId": "p1", "name": "Backlog", "harness": ad.ID(), "workspace": "local", "prompt": "be terse", "compact": "keep the task", "kickoff": "triage", "start": true}, &created)
	if created.Agent.ID == "" || created.ThreadID == "" || created.Agent.ThreadID != created.ThreadID || created.Agent.PromptRev != 1 {
		t.Fatalf("created = %+v, want the agent on its new thread at r1", created)
	}
	agentID := created.Agent.ID
	if !c.sawAgents(func(list []store.Agent) bool { return len(list) == 1 && list[0].ID == agentID }) {
		t.Fatal("no agents frame carried the new agent")
	}

	a, err := mgr.View(ctx, created.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s, _ := a.State(ctx); s == nil || s.Phase != "turn"; s, _ = a.State(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("the kickoff never started a turn")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var saved struct {
		Revision store.AgentRevision `json:"revision"`
		Restart  string              `json:"restart"`
	}
	c.ok("save_agent_revision", map[string]any{"agentId": agentID, "kind": "prompt", "body": "be terser", "reason": "too long"}, &saved)
	if saved.Revision.N != 2 || saved.Revision.Author != store.AuthorUser || saved.Restart != thread.RestartPending {
		t.Fatalf("saved = %+v, want r2 by the user waiting on the turn", saved)
	}
	stale := "The prompt changed while you were editing: r2 is the latest. Review it, then save again."
	if _, msg := c.do("save_agent_revision", map[string]any{"agentId": agentID, "kind": "prompt", "body": "be kind", "baseRev": 1}); msg != stale {
		t.Fatalf("save on r1 after r2 = %q, want %q", msg, stale)
	}

	var revs struct {
		Revisions []store.AgentRevision `json:"revisions"`
	}
	c.ok("agent_revisions", map[string]any{"agentId": agentID, "kind": "prompt"}, &revs)
	if len(revs.Revisions) != 2 || revs.Revisions[0].N != 2 {
		t.Fatalf("revisions = %+v, want r2 then r1", revs.Revisions)
	}

	var log struct {
		ThreadID    string                      `json:"threadId"`
		Compactions []store.Compaction          `json:"compactions"`
		Archived    []store.ArchivedAgentThread `json:"archived"`
	}
	c.ok("agent_log", map[string]any{"agentId": agentID}, &log)
	if log.ThreadID != created.ThreadID || len(log.Compactions) != 1 || log.Compactions[0].Summary != "" || log.Archived == nil {
		t.Fatalf("log = %+v, want epoch 0 of the live thread without its body", log)
	}

	var detail struct {
		Compaction thread.CompactionDetail `json:"compaction"`
	}
	c.ok("agent_compaction", map[string]any{"agentId": agentID, "id": log.Compactions[0].ID}, &detail)
	if detail.Compaction.Summary != "triage" || len(detail.Compaction.System) != 3 {
		t.Fatalf("epoch 0 = %+v, want the first message and the system parts", detail.Compaction)
	}

	var renamed struct {
		Agent store.Agent `json:"agent"`
	}
	c.ok("save_agent", map[string]any{"agentId": agentID, "name": "Inbox"}, &renamed)
	if renamed.Agent.Name != "Inbox" || renamed.Agent.Kickoff != "triage" {
		t.Fatalf("saved agent = %+v, want renamed and the rest kept", renamed.Agent)
	}

	var next struct {
		ThreadID string `json:"threadId"`
	}
	c.ok("agent_new_thread", map[string]any{"agentId": agentID, "firstMessage": "again"}, &next)
	if next.ThreadID == "" || next.ThreadID == created.ThreadID {
		t.Fatalf("new thread = %q, want a fresh one", next.ThreadID)
	}

	if _, msg := c.do("set_thread_pinned", map[string]any{"threadId": next.ThreadID, "pinned": true}); msg == "" {
		t.Fatal("pinned an agent's thread")
	}

	c.ok("archive_agent", map[string]any{"agentId": agentID}, nil)
	if !c.sawAgents(func(list []store.Agent) bool { return len(list) == 0 }) {
		t.Fatal("no agents frame dropped the archived agent")
	}
	if _, msg := c.do("save_agent", map[string]any{"agentId": agentID, "name": "x"}); msg == "" {
		t.Fatal("saved an archived agent")
	}
}

// The welcome frame carries the agents, so a client has the list before it
// asks for anything.
func TestWelcomeCarriesTheAgents(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "welcome.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.CreateProject(ctx, project.Project{ID: "p1", Name: "p1"}); err != nil {
		t.Fatal(err)
	}
	ag, err := st.CreateAgent(ctx, store.Agent{ProjectID: "p1", Name: "Backlog", Harness: "busy-test", Workspace: "local"}, "be terse", "")
	if err != nil {
		t.Fatal(err)
	}
	mgr := newManager(st, t.Logf, &countingAdapter{})
	t.Cleanup(mgr.Shutdown)
	c := dialWS(t, New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort)}).Handler())
	for {
		f := c.read()
		if f.Type != "welcome" {
			continue
		}
		if len(f.Agents) != 1 || f.Agents[0].ID != ag.ID {
			t.Fatalf("welcome agents = %+v, want the one agent", f.Agents)
		}
		return
	}
}

// sawAgents reports whether an agents frame matching ok has arrived, among
// the frames already read past or the next few.
func (c *wsClient) sawAgents(ok func([]store.Agent) bool) bool {
	c.t.Helper()
	for _, f := range c.other {
		if f.Type == "agents" && ok(f.Agents) {
			return true
		}
	}
	// Pushes are coalesced and may trail the ack; a ping answered means
	// everything sent before it has been read.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.write(clientFrame{Type: "ping"})
		for {
			f := c.read()
			if f.Type == "pong" {
				break
			}
			c.other = append(c.other, f)
			if f.Type == "agents" && ok(f.Agents) {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
