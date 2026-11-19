package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
)

func newTestAgent(t *testing.T, s *Store) Agent {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateProject(ctx, project.Project{ID: "p1", Name: "P"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	a, err := s.CreateAgent(ctx, Agent{ProjectID: "p1", Name: "Backlog", Harness: "claude", Workspace: "local"}, "be terse", "keep state")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

func TestCreatingAnAgentSavesItsFirstRevisions(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)

	if a.ID == "" || a.PromptRev != 1 || a.CompactRev != 1 || a.Epoch != 0 {
		t.Fatalf("agent = %+v, want an id at r1 c1 epoch 0", a)
	}
	for kind, body := range map[string]string{RevisionPrompt: "be terse", RevisionCompact: "keep state"} {
		r, err := s.LatestAgentRevision(ctx, a.ID, kind)
		if err != nil {
			t.Fatalf("latest %s: %v", kind, err)
		}
		if r.N != 1 || r.Body != body || r.Author != AuthorUser {
			t.Fatalf("%s revision = %+v, want n=1 by the user", kind, r)
		}
	}
}

func TestCreatingAnAgentInAMissingProjectFails(t *testing.T) {
	s := openTestStore(t)
	_, err := s.CreateAgent(context.Background(), Agent{ProjectID: "gone", Name: "x", Harness: "claude"}, "", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestRevisionsNumberPerKindAndListNewestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)

	r2, err := s.AddAgentRevision(ctx, a.ID, RevisionPrompt, "be terser", AuthorAgent, "you said so", 0)
	if err != nil {
		t.Fatal(err)
	}
	r3, _ := s.AddAgentRevision(ctx, a.ID, RevisionPrompt, "be terse", AuthorUser, "revert", 0)
	c2, _ := s.AddAgentRevision(ctx, a.ID, RevisionCompact, "less", AuthorUser, "", 0)
	if r2.N != 2 || r3.N != 3 || c2.N != 2 {
		t.Fatalf("numbers = r%d r%d c%d, want r2 r3 c2", r2.N, r3.N, c2.N)
	}

	list, err := s.AgentRevisions(ctx, a.ID, RevisionPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].N != 3 || list[2].N != 1 || list[1].Reason != "you said so" {
		t.Fatalf("prompt revisions = %+v, want r3 r2 r1", list)
	}
	got, _ := s.Agent(ctx, a.ID)
	if got.PromptRev != 3 || got.CompactRev != 2 {
		t.Fatalf("agent revs = r%d c%d, want r3 c2", got.PromptRev, got.CompactRev)
	}

	if _, err := s.AddAgentRevision(ctx, a.ID, "notes", "x", AuthorUser, "", 0); err == nil {
		t.Fatal("an unknown kind was saved")
	}
	if _, err := s.AddAgentRevision(ctx, "nobody", RevisionPrompt, "x", AuthorUser, "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revision of a missing agent: err = %v, want not found", err)
	}
}

func TestArchivedAgentsLeaveTheListButStayReadable(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)
	name, model, at, raw := "Renamed", "opus", int64(120000), true
	if err := s.PatchAgent(ctx, a.ID, AgentPatch{Name: &name, Model: &model, CompactAt: &at, RawLog: &raw}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentThread(ctx, a.ID, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentLiveRevs(ctx, a.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListAgents(ctx)
	if len(list) != 1 || list[0].Name != "Renamed" || list[0].Model != "opus" || list[0].CompactAt != 120000 || !list[0].RawLog || list[0].ThreadID != "t1" || list[0].LivePromptRev != 1 {
		t.Fatalf("list = %+v, want the updated agent", list)
	}

	if err := s.ArchiveAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListAgents(ctx); len(list) != 0 {
		t.Fatalf("list after archive = %+v, want none", list)
	}
	got, err := s.Agent(ctx, a.ID)
	if err != nil || got.ArchivedAt == 0 {
		t.Fatalf("archived agent = %+v, %v; want it readable and marked", got, err)
	}
	if err := s.ArchiveAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archiving twice: err = %v, want not found", err)
	}
}

func TestCompactionEpochsCountPerThreadAndTheListHasNoBodies(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)
	mustCreateThread(t, s, "t1")
	mustCreateThread(t, s, "t2")
	_ = s.SetAgentThread(ctx, a.ID, "t1")

	start, err := s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t1", Trigger: "start", PromptRev: 1, CompactRev: 1, Summary: "kick off"})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t1", Trigger: "auto", PreTokens: 160000, Instructions: "keep state"})
	second, _ := s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t1", Trigger: "manual"})
	other, _ := s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t2", Trigger: "auto"})
	if start.Epoch != 0 || first.Epoch != 1 || second.Epoch != 2 || other.Epoch != 1 {
		t.Fatalf("epochs = %d %d %d / %d, want 0 1 2 / 1", start.Epoch, first.Epoch, second.Epoch, other.Epoch)
	}
	if got, _ := s.Agent(ctx, a.ID); got.Epoch != 2 {
		t.Fatalf("agent epoch = %d, want the live thread's 2", got.Epoch)
	}

	// Detail lands on the latest row, a start detail on epoch 0, and an empty
	// part leaves what is there.
	att := []proto.CompactionAttachment{{Kind: "file", Name: "main.go", Chars: 5, Body: "hello"}}
	if err := s.FileCompactionDetail(ctx, "t1", false, "carry this", att); err != nil {
		t.Fatal(err)
	}
	if err := s.FileCompactionDetail(ctx, "t1", true, "", []proto.CompactionAttachment{{Kind: "instructions", Name: "CLAUDE.md", Chars: 2, Body: "hi"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.FileCompactionDetail(ctx, "t1", false, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FileCompactionDetail(ctx, "none", false, "x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detail with no row: err = %v, want not found", err)
	}

	rows, err := s.ThreadCompactions(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Epoch != 0 || rows[2].Epoch != 2 {
		t.Fatalf("rows = %+v, want epochs 0..2", rows)
	}
	for _, r := range rows {
		if r.Summary != "" || r.Instructions != "" {
			t.Fatalf("listed row %d carries a body: %+v", r.Epoch, r)
		}
		for _, x := range r.Attachments {
			if x.Body != "" {
				t.Fatalf("listed attachment carries its body: %+v", x)
			}
		}
	}
	if rows[0].SummaryChars != len("kick off") || len(rows[0].Attachments) != 1 || rows[0].Attachments[0].Name != "CLAUDE.md" {
		t.Fatalf("epoch 0 = %+v, want the first message's size and the start attachment", rows[0])
	}
	if rows[1].InstructionsChars != len("keep state") || rows[1].SummaryChars != 0 {
		t.Fatalf("epoch 1 = %+v, want instructions sized and no summary", rows[1])
	}
	if rows[2].SummaryChars != len("carry this") || len(rows[2].Attachments) != 1 || rows[2].Attachments[0].Chars != 5 {
		t.Fatalf("epoch 2 = %+v, want the filed detail", rows[2])
	}

	whole, err := s.Compaction(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if whole.Summary != "carry this" || whole.Attachments[0].Body != "hello" {
		t.Fatalf("whole row = %+v, want its bodies", whole)
	}
	if _, err := s.Compaction(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing row: err = %v, want not found", err)
	}

	if err := s.DeleteThread(ctx, "t2"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.ThreadCompactions(ctx, "t2"); len(rows) != 0 {
		t.Fatalf("deleting the thread left its log: %+v", rows)
	}
}

func TestArchivedAgentThreadsAreTheAgentsOtherThreads(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)
	for _, m := range []ThreadMeta{
		{ID: "old", Cwd: "/tmp", Harness: "h", Phase: "closed", AgentID: a.ID, ProjectID: "p1", CreatedAt: 1, UpdatedAt: 5},
		{ID: "live", Cwd: "/tmp", Harness: "h", Phase: "idle", AgentID: a.ID, ProjectID: "p1", CreatedAt: 2, UpdatedAt: 6},
		{ID: "plain", Cwd: "/tmp", Harness: "h", Phase: "closed", ProjectID: "p1", CreatedAt: 3, UpdatedAt: 7},
	} {
		if err := s.CreateThread(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Thread(ctx, "old"); got.AgentID != a.ID {
		t.Fatalf("thread agent = %q, want %q", got.AgentID, a.ID)
	}
	_, _ = s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "old", Trigger: "start"})
	_, _ = s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "old", Trigger: "auto"})
	_, _ = s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "old", Trigger: "auto"})

	got, err := s.ArchivedAgentThreads(ctx, a.ID, "live")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ThreadID != "old" || got[0].Compactions != 2 || got[0].ClosedAt == 0 {
		t.Fatalf("archived = %+v, want old with two compactions", got)
	}
}

func TestARevisionOnAStaleBaseIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind string
		base int
		want string
	}{
		{"no base", RevisionPrompt, 0, ""},
		{"the head", RevisionPrompt, 2, ""},
		{"a prompt behind the head", RevisionPrompt, 1, "The prompt changed while you were editing: r2 is the latest. Review it, then save again."},
		{"instructions behind the head", RevisionCompact, 1, "The compaction instructions changed while you were editing: c2 is the latest. Review it, then save again."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			a := newTestAgent(t, s)
			if _, err := s.AddAgentRevision(ctx, a.ID, tc.kind, "second", AuthorUser, "", 0); err != nil {
				t.Fatal(err)
			}
			_, err := s.AddAgentRevision(ctx, a.ID, tc.kind, "third", AuthorUser, "", tc.base)
			var moved *RevisionMovedError
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v, want saved", err)
			case tc.want != "" && (!errors.As(err, &moved) || err.Error() != tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got, _ := s.LatestAgentRevision(ctx, a.ID, tc.kind); (got.N == 3) != (tc.want == "") {
				t.Fatalf("head = %d after the save", got.N)
			}
		})
	}
}

func TestTheLogVersionMovesWithTheLog(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)
	mustCreateThread(t, s, "t1")
	_ = s.SetAgentThread(ctx, a.ID, "t1")
	version := func() int64 { got, _ := s.Agent(ctx, a.ID); return got.LogVersion }

	var row Compaction
	for _, step := range []struct {
		name  string
		moves bool
		do    func() error
	}{
		{"epoch 0", true, func() (err error) {
			row, err = s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t1", Trigger: "start"})
			return err
		}},
		{"the first message", true, func() error { _, err := s.FillStartMessage(ctx, "t1", "hello"); return err }},
		{"a second first message", false, func() error {
			if ok, err := s.FillStartMessage(ctx, "t1", "again"); ok || err != nil {
				return fmt.Errorf("filled again: %v, %v", ok, err)
			}
			return nil
		}},
		{"a boundary", true, func() error { return s.SetCompactionBoundary(ctx, row.ID, 7) }},
		{"detail", true, func() error { return s.FileCompactionDetail(ctx, "t1", true, "carry on", nil) }},
		{"a compaction", true, func() error {
			_, err := s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t1", Trigger: "auto"})
			return err
		}},
	} {
		before := version()
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if moved := version() != before; moved != step.moves {
			t.Fatalf("%s: version moved %v, want %v", step.name, moved, step.moves)
		}
	}
	if ok, err := s.FillStartMessage(ctx, "no-such-thread", "x"); ok || err != nil {
		t.Fatalf("filling a thread with no log = %v, %v; want nothing", ok, err)
	}
}

func TestLiveSettingsWriteOnlyTheirColumns(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)
	_ = s.SetAgentThread(ctx, a.ID, "t1")
	name := "Renamed"
	if err := s.PatchAgent(ctx, a.ID, AgentPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	model, effort := "opus", ""
	if ok, err := s.SetAgentLiveSettings(ctx, "t1", &model, nil, &effort); !ok || err != nil {
		t.Fatalf("live settings = %v, %v", ok, err)
	}
	got, _ := s.Agent(ctx, a.ID)
	if got.Name != "Renamed" || got.Model != "opus" || got.Workspace != "local" {
		t.Fatalf("agent = %+v, want the name kept and the model taken", got)
	}
	if ok, err := s.SetAgentLiveSettings(ctx, "someone-elses", &model, nil, nil); ok || err != nil {
		t.Fatalf("another thread's settings = %v, %v; want nothing written", ok, err)
	}
}

func TestRestartPendingReportsWhenItChanges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	a := newTestAgent(t, s)
	_ = s.SetAgentThread(ctx, a.ID, "t1")
	for _, step := range []struct {
		pending, changed bool
	}{{true, true}, {true, false}, {false, true}, {false, false}} {
		changed, err := s.SetThreadRestartPending(ctx, "t1", step.pending)
		if err != nil || changed != step.changed {
			t.Fatalf("set %v = %v, %v; want changed %v", step.pending, changed, err, step.changed)
		}
		if got, _ := s.Agent(ctx, a.ID); got.RestartPending != step.pending {
			t.Fatalf("pending = %v, want %v", got.RestartPending, step.pending)
		}
	}
	_, _ = s.SetThreadRestartPending(ctx, "t1", true)
	_ = s.SetAgentThread(ctx, a.ID, "t2")
	if got, _ := s.Agent(ctx, a.ID); got.RestartPending {
		t.Fatal("a new live thread kept the old one's pending restart")
	}
}

func TestDeletingAProjectTakesItsArchivedAgentsOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		archive bool
		plain   bool
		want    error
	}{
		{"a live agent", false, false, ErrProjectHasAgents},
		{"an archived agent", true, false, nil},
		{"a thread of its own", true, true, ErrProjectInUse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()
			a := newTestAgent(t, s)
			if err := s.CreateThread(ctx, ThreadMeta{ID: "t1", ProjectID: "p1", AgentID: a.ID, Cwd: "/tmp", Harness: "h", Phase: "closed"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddCompaction(ctx, Compaction{AgentID: a.ID, ThreadID: "t1", Trigger: "start"}); err != nil {
				t.Fatal(err)
			}
			if tc.plain {
				if err := s.CreateThread(ctx, ThreadMeta{ID: "mine", ProjectID: "p1", Cwd: "/tmp", Harness: "h", Phase: "idle"}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.archive {
				if err := s.ArchiveAgent(ctx, a.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.DeleteProject(ctx, "p1"); !errors.Is(err, tc.want) {
				t.Fatalf("delete = %v, want %v", err, tc.want)
			}
			_, projectErr := s.Project(ctx, "p1")
			_, agentErr := s.Agent(ctx, a.ID)
			_, threadErr := s.Thread(ctx, "t1")
			revs, _ := s.AgentRevisions(ctx, a.ID, RevisionPrompt)
			rows, _ := s.ThreadCompactions(ctx, "t1")
			gone := errors.Is(projectErr, ErrNotFound) && errors.Is(agentErr, ErrNotFound) && errors.Is(threadErr, ErrNotFound) && len(revs) == 0 && len(rows) == 0
			kept := projectErr == nil && agentErr == nil && threadErr == nil && len(revs) == 1 && len(rows) == 1
			if tc.want == nil && !gone || tc.want != nil && !kept {
				t.Fatalf("project %v agent %v thread %v, %d revisions %d rows after delete %v", projectErr, agentErr, threadErr, len(revs), len(rows), tc.want)
			}
		})
	}
}
