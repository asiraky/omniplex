package thread

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

type agentRig struct {
	mgr *Manager
	fa  *fakeAdapter
	st  *store.Store
}

func newAgentRig(t *testing.T) agentRig {
	t.Helper()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	st, _ := testProject(t, t.TempDir())
	fa := &fakeAdapter{}
	mgr := newManager(st, func(string, ...any) {}, fa)
	t.Cleanup(mgr.Shutdown)
	return agentRig{mgr: mgr, fa: fa, st: st}
}

// create makes an agent on the test project and, with kickoff non-empty or
// start, its first thread, waiting until that thread's harness is running.
func (r agentRig) create(t *testing.T, n NewAgent) (store.Agent, *Actor) {
	t.Helper()
	n.ProjectID, n.Harness = "p1", "fake"
	if n.Name == "" {
		n.Name = "Backlog"
	}
	if n.Prompt == "" {
		n.Prompt = "be terse"
	}
	if n.Compact == "" {
		n.Compact = "keep the task"
	}
	before := r.fa.thread()
	ag, threadID, err := r.mgr.CreateAgent(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Start {
		return ag, nil
	}
	return ag, r.running(t, threadID, before)
}

// running waits for the thread's harness to start, a new one after before.
func (r agentRig) running(t *testing.T, threadID string, before *fakeThread) *Actor {
	t.Helper()
	if threadID == "" {
		t.Fatal("no thread was started")
	}
	waitFor(t, func() bool { th := r.fa.thread(); return th != nil && th != before })
	a, err := r.mgr.View(context.Background(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, err := a.State(context.Background())
		return err == nil && (s.Phase == "idle" || s.Phase == "turn")
	})
	return a
}

func (r agentRig) agent(t *testing.T, id string) store.Agent {
	t.Helper()
	ag, err := r.mgr.Agent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return ag
}

func compactedPayload(t *testing.T, st *store.Store, threadID string) (proto.ContextCompactedPayload, int64) {
	t.Helper()
	evs, err := st.ReadEvents(context.Background(), threadID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Type == proto.ContextCompacted {
			var p proto.ContextCompactedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			return p, ev.Seq
		}
	}
	t.Fatal("no compaction in the log")
	return proto.ContextCompactedPayload{}, 0
}

func TestAnAgentThreadStartsOnItsBrief(t *testing.T) {
	r := newAgentRig(t)
	ag, a := r.create(t, NewAgent{Kickoff: "triage the inbox", RawLog: true, CompactAt: 150000, Start: true, Workspace: "local"})

	if in := nextInput(t, r.fa.thread()); in.Text != "triage the inbox" {
		t.Fatalf("first message = %q, want the kickoff", in.Text)
	}
	o, _ := lastOpts(r.fa)
	if o.Instructions != composeAgentInstructions("be terse") {
		t.Fatalf("instructions = %q, want Omniplex's, the preamble and the prompt", o.Instructions)
	}
	if !o.NoMemory || o.CompactAt != 150000 || o.RawRequestLogDir == "" {
		t.Fatalf("options = no memory %v, compact at %d, raw log %q", o.NoMemory, o.CompactAt, o.RawRequestLogDir)
	}
	if body, err := os.ReadFile(o.CompactInstructionsFile); err != nil || string(body) != "keep the task" {
		t.Fatalf("compaction instructions file = %q, %v", body, err)
	}

	meta, _ := r.st.Thread(context.Background(), a.ID)
	got := r.agent(t, ag.ID)
	if meta.AgentID != ag.ID || got.ThreadID != a.ID {
		t.Fatalf("thread agent %q, agent thread %q; want them linked", meta.AgentID, got.ThreadID)
	}
	waitFor(t, func() bool { return r.agent(t, ag.ID).LivePromptRev == 1 })
	rows, _ := r.st.ThreadCompactions(context.Background(), a.ID)
	if len(rows) != 1 || rows[0].Epoch != 0 || rows[0].Trigger != "start" || rows[0].SummaryChars != len("triage the inbox") {
		t.Fatalf("log = %+v, want epoch 0 holding the first message", rows)
	}
}

func TestAPlainThreadGetsNoAgentSetup(t *testing.T) {
	r := newAgentRig(t)
	a, err := r.mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	r.running(t, a.ID, nil)
	o, _ := lastOpts(r.fa)
	if o.Instructions != omniplexInstructions || o.NoMemory || o.CompactInstructionsFile != "" || o.RawRequestLogDir != "" {
		t.Fatalf("plain thread options = %+v, want none of an agent's", o)
	}
}

func TestAnAgentCompactionIsLoggedAndItsDetailFiled(t *testing.T) {
	r := newAgentRig(t)
	ag, a := r.create(t, NewAgent{Start: true, Workspace: "local"})
	_, sub := r.mgr.SubscribeAgents()
	select {
	case <-sub:
	default:
	}

	r.fa.thread().emit(proto.Emit(proto.ContextCompacted, proto.ContextCompactedPayload{Trigger: "auto", PreTokens: 160000, PostTokens: 9000, DurationMs: 4200}))
	waitFor(t, func() bool { return countEvents(t, r.st, a.ID, proto.ContextCompacted) == 1 })
	p, seq := compactedPayload(t, r.st, a.ID)
	if p.Epoch != 1 || p.CompactionID == 0 {
		t.Fatalf("logged compaction = %+v, want epoch 1 and its row", p)
	}
	select {
	case <-sub:
	case <-time.After(5 * time.Second):
		t.Fatal("agents subscribers were not told of the compaction")
	}
	row, err := r.st.Compaction(context.Background(), p.CompactionID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Epoch != 1 || row.Trigger != "auto" || row.PreTokens != 160000 || row.DurationMs != 4200 || row.PromptRev != 1 || row.CompactRev != 1 || row.Instructions != "keep the task" || row.BoundarySeq != seq {
		t.Fatalf("row = %+v, want the compaction at r1 c1 pointing at seq %d", row, seq)
	}
	if got := r.agent(t, ag.ID); got.Epoch != 1 {
		t.Fatalf("agent epoch = %d, want 1", got.Epoch)
	}

	att := []proto.CompactionAttachment{{Kind: "file", Name: "a.go", Chars: 3, Body: "abc"}}
	r.fa.thread().emit(proto.Emit(proto.CompactionDetail, proto.CompactionDetailPayload{Summary: "carry on", Attachments: att}))
	r.fa.thread().emit(proto.Emit(proto.CompactionDetail, proto.CompactionDetailPayload{Start: true, Attachments: []proto.CompactionAttachment{{Kind: "instructions", Name: "CLAUDE.md", Chars: 2, Body: "hi"}}}))
	waitFor(t, func() bool {
		rows, _ := r.st.ThreadCompactions(context.Background(), a.ID)
		return len(rows) == 2 && len(rows[0].Attachments) == 1 && rows[1].SummaryChars == len("carry on")
	})
	if n := countEvents(t, r.st, a.ID, proto.CompactionDetail); n != 0 {
		t.Fatalf("%d compaction details were logged on the thread, want none", n)
	}
}

func TestAPlainThreadsCompactionIsNotAnAgentsBusiness(t *testing.T) {
	r := newAgentRig(t)
	a, err := r.mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	r.running(t, a.ID, nil)
	r.fa.thread().emit(proto.Emit(proto.CompactionDetail, proto.CompactionDetailPayload{Summary: "dropped"}))
	r.fa.thread().emit(proto.Emit(proto.ContextCompacted, proto.ContextCompactedPayload{Trigger: "manual"}))
	waitFor(t, func() bool { return countEvents(t, r.st, a.ID, proto.ContextCompacted) == 1 })
	if p, _ := compactedPayload(t, r.st, a.ID); p.Epoch != 0 || p.CompactionID != 0 {
		t.Fatalf("plain compaction = %+v, want no epoch stamped", p)
	}
	if n := countEvents(t, r.st, a.ID, proto.CompactionDetail); n != 0 {
		t.Fatalf("%d details logged, want the detail dropped", n)
	}
	if rows, _ := r.st.ThreadCompactions(context.Background(), a.ID); len(rows) != 0 {
		t.Fatalf("plain thread has a compaction log: %+v", rows)
	}
}

func TestSavingAPromptRestartsTheLiveThreadOntoIt(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	ag, a := r.create(t, NewAgent{Start: true, Workspace: "local"})
	waitFor(t, func() bool { return r.agent(t, ag.ID).LivePromptRev == 1 })

	rev, restart, err := r.mgr.SaveAgentRevision(ctx, ag.ID, store.RevisionPrompt, "be terser", store.AuthorUser, "")
	if err != nil || restart != RestartNow || rev.N != 2 {
		t.Fatalf("save on an idle thread = r%d %q, %v; want r2 restarted now", rev.N, restart, err)
	}
	if o, _ := lastOpts(r.fa); o.Instructions != composeAgentInstructions("be terser") {
		t.Fatalf("restarted on %q, want the new prompt", o.Instructions)
	}
	waitFor(t, func() bool { return r.agent(t, ag.ID).LivePromptRev == 2 })

	if _, err := a.Prompt(ctx, "work", nil); err != nil {
		t.Fatal(err)
	}
	nextInput(t, r.fa.thread())
	_, restart, err = r.mgr.SaveAgentRevision(ctx, ag.ID, store.RevisionPrompt, "be kind", store.AuthorAgent, "you asked")
	if err != nil || restart != RestartPending {
		t.Fatalf("save on a busy thread = %q, %v; want pending", restart, err)
	}
	if got := r.agent(t, ag.ID); got.PromptRev != 3 || got.LivePromptRev != 2 {
		t.Fatalf("agent at r%d live r%d, want r3 waiting on r2", got.PromptRev, got.LivePromptRev)
	}

	_, restart, err = r.mgr.SaveAgentRevision(ctx, ag.ID, store.RevisionCompact, "keep less", store.AuthorUser, "")
	if err != nil || restart != RestartNone {
		t.Fatalf("compact save = %q, %v; want no restart", restart, err)
	}
	o, _ := lastOpts(r.fa)
	if body, _ := os.ReadFile(o.CompactInstructionsFile); string(body) != "keep less" {
		t.Fatalf("compaction file = %q, want the new revision", body)
	}
}

func TestSavingAPromptWithNoLiveThreadRestartsNothing(t *testing.T) {
	r := newAgentRig(t)
	ag, _ := r.create(t, NewAgent{Workspace: "local"})
	_, restart, err := r.mgr.SaveAgentRevision(context.Background(), ag.ID, store.RevisionPrompt, "x", store.AuthorUser, "")
	if err != nil || restart != RestartNone {
		t.Fatalf("save = %q, %v; want none", restart, err)
	}
}

func TestNewThreadArchivesTheLiveOne(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	ag, old := r.create(t, NewAgent{Kickoff: "first", Start: true, Workspace: "local"})
	nextInput(t, r.fa.thread())

	before := r.fa.thread()
	threadID, err := r.mgr.AgentNewThread(ctx, ag.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	r.running(t, threadID, before)
	if threadID == old.ID || r.agent(t, ag.ID).ThreadID != threadID {
		t.Fatalf("new thread %s, agent on %s; want the agent moved to a new thread", threadID, r.agent(t, ag.ID).ThreadID)
	}
	waitFor(t, func() bool { m, _ := r.st.Thread(ctx, old.ID); return m.Phase == "closed" })

	live, rows, archived, err := r.mgr.AgentLog(ctx, ag.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if live != threadID || len(rows) != 1 || rows[0].SummaryChars != len("second") {
		t.Fatalf("live log = %s %+v, want the new thread's epoch 0", live, rows)
	}
	if len(archived) != 1 || archived[0].ThreadID != old.ID {
		t.Fatalf("archived = %+v, want the old thread", archived)
	}
	if got, rows, _, err := r.mgr.AgentLog(ctx, ag.ID, old.ID); err != nil || got != old.ID || len(rows) != 1 {
		t.Fatalf("old log = %s %+v, %v", got, rows, err)
	}

	plain, err := r.mgr.CreateProject(ctx, CreateProjectOptions{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.mgr.AgentLog(ctx, ag.ID, plain.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("log of another thread: err = %v, want not found", err)
	}
}

func TestEpochZeroShowsTheSystemPromptItStartedOn(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	ag, a := r.create(t, NewAgent{Start: true, Workspace: "local"})
	_, _, _ = r.mgr.SaveAgentRevision(ctx, ag.ID, store.RevisionPrompt, "newer", store.AuthorUser, "")
	_, rows, _, _ := r.mgr.AgentLog(ctx, ag.ID, a.ID)

	d, err := r.mgr.AgentCompaction(ctx, ag.ID, rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.System) != 3 || d.System[2].Kind != "prompt" || d.System[2].Body != "be terse" || d.System[2].Chars != len("be terse") {
		t.Fatalf("system = %+v, want the r1 prompt last", d.System)
	}
	if _, err := r.mgr.AgentCompaction(ctx, "someone-else", rows[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another agent's row: err = %v, want not found", err)
	}
}

func TestAModeChangeOnTheLiveThreadIsSavedToTheAgent(t *testing.T) {
	r := newAgentRig(t)
	ag, a := r.create(t, NewAgent{Start: true, Workspace: "local", Mode: "manual"})
	if err := a.SetMode(context.Background(), "acceptEdits"); err != nil {
		t.Fatal(err)
	}
	if got := r.agent(t, ag.ID); got.Mode != "acceptEdits" {
		t.Fatalf("agent mode = %q, want the thread's new one", got.Mode)
	}
}

func TestAnAgentsThreadCannotBePinnedRenamedOrDeleted(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	_, a := r.create(t, NewAgent{Start: true, Workspace: "local"})
	if err := r.mgr.SetThreadPinned(ctx, a.ID, true); err == nil {
		t.Fatal("pinned an agent's thread")
	}
	if _, err := r.mgr.RenameThread(ctx, a.ID, "mine"); err == nil {
		t.Fatal("renamed an agent's thread")
	}
	if err := r.mgr.Delete(ctx, a.ID, false); err == nil {
		t.Fatal("deleted an agent's thread")
	}
	if _, err := r.st.Thread(ctx, a.ID); err != nil {
		t.Fatalf("the thread is gone: %v", err)
	}
}

func TestArchivingAnAgentClosesItsThread(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	ag, a := r.create(t, NewAgent{Start: true, Workspace: "local"})
	if err := r.mgr.ArchiveAgent(ctx, ag.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { m, _ := r.st.Thread(ctx, a.ID); return m.Phase == "closed" })
	if list, _ := r.mgr.Agents(ctx); len(list) != 0 {
		t.Fatalf("agents = %+v, want the archived one gone", list)
	}
	if _, err := r.mgr.SaveAgent(ctx, ag.ID, AgentPatch{}); !errors.Is(err, errAgentArchived) {
		t.Fatalf("saving an archived agent: err = %v", err)
	}
	if _, err := r.mgr.AgentNewThread(ctx, ag.ID, "x"); !errors.Is(err, errAgentArchived) {
		t.Fatalf("new thread on an archived agent: err = %v", err)
	}
}

func TestSaveAgentChangesOnlyWhatItIsGiven(t *testing.T) {
	r := newAgentRig(t)
	ctx := context.Background()
	ag, _ := r.create(t, NewAgent{Workspace: "local", Model: "m1", Kickoff: "go"})
	name, at := "Renamed", int64(120000)
	got, err := r.mgr.SaveAgent(ctx, ag.ID, AgentPatch{Name: &name, CompactAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.CompactAt != 120000 || got.Model != "m1" || got.Kickoff != "go" {
		t.Fatalf("saved = %+v, want name and compact-at changed, the rest kept", got)
	}
	bad, blank := "copy", " "
	if _, err := r.mgr.SaveAgent(ctx, ag.ID, AgentPatch{Workspace: &bad}); err == nil {
		t.Fatal("saved an unknown workspace mode")
	}
	if _, err := r.mgr.SaveAgent(ctx, ag.ID, AgentPatch{Name: &blank}); err == nil {
		t.Fatal("saved a blank name")
	}
}
