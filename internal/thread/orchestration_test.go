package thread

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

func agentText(turnID, block, text string) proto.Emission {
	return proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: turnID, Role: "agent", Kind: "text", BlockID: block, Delta: text})
}

func turn(id, prompt, reply string) []proto.Emission {
	return []proto.Emission{
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: id, Prompt: prompt}),
		agentText(id, id+"-b", reply),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: id, StopReason: proto.StopEndTurn}),
	}
}

func seededActor(t *testing.T, ems ...proto.Emission) *Actor {
	t.Helper()
	mgr, _, _, id := seedThread(t, "idle", ems...)
	a, err := mgr.View(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The digest is the latest turn's ask and answer — its top-level agent text
// only — and what the thread is waiting on, less omniplex's own cards.
func TestDigestReadsTheLatestTurnAndWhatIsPending(t *testing.T) {
	ems := turn("t1", "first", "one")
	ems = append(ems,
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t2", Prompt: "second"}),
		proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: "t2", Role: "agent", Kind: "thought", BlockID: "th", Delta: "musing"}),
		agentText("t2", "b1", "hel"),
		agentText("t2", "b1", "lo"),
		proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: "t2", Role: "agent", Kind: "text", BlockID: "sub", Delta: "subagent", ParentToolCallID: "task-1"}),
		agentText("t2", "b2", "world"),
		proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: "t2", StopReason: proto.StopError, Error: "boom"}),
		proto.Emit(proto.PermissionRequested, proto.PermissionRequestedPayload{RequestID: "perm", TurnID: "t2", ToolCallID: "c1", ToolName: "Bash", Title: "rm -rf"}),
		proto.Emit(proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "q", Prompt: "which?", Schema: json.RawMessage(`{"type":"object"}`)}),
		proto.Emit(proto.ElicitationRequested, proto.ElicitationRequestedPayload{RequestID: "card", Prompt: "add server", Schema: json.RawMessage(`{}`), Card: json.RawMessage(`{"kind":"mcp"}`)}),
	)
	a := seededActor(t, ems...)

	d, err := a.Digest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Turns != 2 || d.Last == nil {
		t.Fatalf("digest = %+v, want two turns and a last one", d)
	}
	want := TurnDigest{Index: 1, ID: "t2", Prompt: "second", Reply: "hello\n\nworld", StopReason: proto.StopError, Error: "boom", Done: true}
	if *d.Last != want {
		t.Fatalf("last = %+v, want %+v", *d.Last, want)
	}
	if !d.TurnFailed || d.Attention != projection.AttentionNeedsPermission {
		t.Fatalf("attention %q failed %v, want needs_permission and failed", d.Attention, d.TurnFailed)
	}
	if len(d.Questions) != 1 || d.Questions[0].RequestID != "q" || d.Questions[0].Prompt != "which?" || string(d.Questions[0].Schema) != `{"type":"object"}` {
		t.Fatalf("questions = %+v, want only q", d.Questions)
	}
	if len(d.Approvals) != 1 || d.Approvals[0] != (PendingApproval{RequestID: "perm", ToolName: "Bash", Title: "rm -rf"}) {
		t.Fatalf("approvals = %+v", d.Approvals)
	}
}

func TestDigestBeforeAnyTurn(t *testing.T) {
	d, err := seededActor(t).Digest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Turns != 0 || d.Last != nil || len(d.Questions) != 0 || len(d.Approvals) != 0 {
		t.Fatalf("digest = %+v, want empty", d)
	}
}

func digestIDs(ts []TurnDigest) []string {
	ids := []string{}
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return ids
}

// Transcript pages by size: whole turns until the next would overflow, and a
// cursor that walks to the end.
func TestTranscriptPagesWholeTurnsBySize(t *testing.T) {
	var ems []proto.Emission
	for _, id := range []string{"t1", "t2", "t3"} {
		ems = append(ems, turn(id, "ask"+id, "reply-"+id)...) // 5 + 8 = 13 chars each
	}
	a := seededActor(t, ems...)
	ctx := context.Background()

	got, next, err := a.Transcript(ctx, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(digestIDs(got), []string{"t1", "t2"}) || next != 2 {
		t.Fatalf("first page = %v next %d, want [t1 t2] next 2", digestIDs(got), next)
	}
	if got[1].Index != 1 || got[1].Prompt != "askt2" || got[1].Reply != "reply-t2" || !got[1].Done {
		t.Fatalf("t2 = %+v", got[1])
	}
	got, next, err = a.Transcript(ctx, next, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(digestIDs(got), []string{"t3"}) || next != 3 {
		t.Fatalf("second page = %v next %d, want [t3] next 3", digestIDs(got), next)
	}
	got, next, err = a.Transcript(ctx, next, 30)
	if err != nil || len(got) != 0 || next != 3 {
		t.Fatalf("past the end = %v next %d err %v, want nothing and next 3", digestIDs(got), next, err)
	}
	got, next, err = a.Transcript(ctx, 0, 0)
	if err != nil || len(got) != 3 || next != 3 {
		t.Fatalf("unlimited = %v next %d err %v, want all three", digestIDs(got), next, err)
	}
}

// A turn too big for the page on its own still comes back, its reply cut to
// the tail so the page fits.
func TestTranscriptCutsTheReplyOfATurnTooBigAlone(t *testing.T) {
	reply := strings.Repeat("a", 50) + "the end"
	a := seededActor(t, append(turn("big", "ask", reply), turn("small", "ask", "ok")...)...)

	got, next, err := a.Transcript(context.Background(), 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || next != 1 {
		t.Fatalf("page = %v next %d, want just the big turn", digestIDs(got), next)
	}
	r := got[0].Reply
	if !strings.HasPrefix(r, "…") || !strings.HasSuffix(r, "the end") || utf8.RuneCountInString(got[0].Prompt)+utf8.RuneCountInString(r) != 20 {
		t.Fatalf("reply = %q, want its tail behind … filling the 20 chars", r)
	}
	got, _, err = a.Transcript(context.Background(), 1, 20)
	if err != nil || len(got) != 1 || got[0].Reply != "ok" {
		t.Fatalf("next page = %+v, %v", got, err)
	}
}

// Descendants walks the whole tree under a thread and nothing else, and a
// parent chain that loops ends.
func TestDescendantsWalksTheTree(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "tree.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	for id, parent := range map[string]string{
		"root": "", "a": "root", "b": "a", "c": "root", "d": "b", "other": "", "o1": "other",
		"p": "q", "q": "p",
	} {
		if err := st.CreateThread(ctx, store.ThreadMeta{ID: id, Cwd: "/w", Harness: "fake", Phase: "idle", ParentID: parent}); err != nil {
			t.Fatal(err)
		}
	}
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	t.Cleanup(mgr.Shutdown)

	ids := func(id string) []string {
		t.Helper()
		ms, err := mgr.Descendants(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, m := range ms {
			out = append(out, m.ID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids("root"); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("descendants of root = %v", got)
	}
	if got := ids("b"); !slices.Equal(got, []string{"d"}) {
		t.Fatalf("descendants of b = %v", got)
	}
	if got := ids("d"); len(got) != 0 {
		t.Fatalf("descendants of a leaf = %v", got)
	}
	if got := ids("p"); !slices.Equal(got, []string{"q"}) {
		t.Fatalf("descendants in a cycle = %v", got)
	}

	for _, c := range []struct {
		ancestor, id string
		want         bool
	}{
		{"root", "d", true},
		{"a", "d", true},
		{"c", "d", false},
		{"d", "root", false},
		{"root", "root", false},
		{"root", "o1", false},
		{"root", "p", false},
		{"p", "q", true},
		{"root", "missing", false},
	} {
		got, err := mgr.IsDescendant(ctx, c.ancestor, c.id)
		if err != nil || got != c.want {
			t.Errorf("IsDescendant(%s, %s) = %v, %v; want %v", c.ancestor, c.id, got, err, c.want)
		}
	}
}

// A managed worktree based on a commit id starts at that commit, not at the
// branch tip that has moved on since — how a delegated thread starts where its
// parent is — and the delegating thread's id goes on its row.
func TestManagedWorktreeBranchesFromACommit(t *testing.T) {
	root, _, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "A"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "A")
	git(t, root, "commit", "-m", "a")
	want, err := HeadCommit(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want != git(t, root, "rev-parse", "HEAD") {
		t.Fatalf("HeadCommit = %s, want the checkout's HEAD", want)
	}
	if err := os.WriteFile(filepath.Join(root, "B"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "B")
	git(t, root, "commit", "-m", "b")

	st, p := testProject(t, root)
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{
		ProjectID: p.ID, Workspace: "managed", Branch: "issue/1-child", BaseRef: want, ParentID: "the-parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := ready(t, st, a.ID)
	if got, err := HeadCommit(context.Background(), meta.Cwd); err != nil || got != want {
		t.Fatalf("worktree HEAD = %s, %v; want the commit %s", got, err, want)
	}
	if meta.ParentID != "the-parent" {
		t.Fatalf("parent = %q, want the-parent", meta.ParentID)
	}
}

// A commit id the clone does not have is not a dead end: it falls back to
// the default branch like any base that exists nowhere.
func TestResolveBaseRefFallsBackFromAnUnknownCommit(t *testing.T) {
	root, _, _ := gitRepo(t)
	res, err := resolveBaseRef(context.Background(), root, strings.Repeat("ab", 20))
	if err != nil {
		t.Fatal(err)
	}
	if res.Ref != "main" || !res.FellBack {
		t.Fatalf("resolution = %+v, want main as a fallback", res)
	}
}

func TestHeadCommitOutsideARepositoryFails(t *testing.T) {
	if _, err := HeadCommit(context.Background(), t.TempDir()); err == nil {
		t.Fatal("HeadCommit outside a repository succeeded")
	}
}

// A private chat's delegated child is private too, and knows its parent.
func TestAssistantChildKeepsItsParent(t *testing.T) {
	mgr, st, _, _ := assistantSetup(t)
	parent, err := mgr.CreateAssistant(context.Background(), AssistantOptions{Private: true})
	if err != nil {
		t.Fatal(err)
	}
	child, err := mgr.CreateAssistant(context.Background(), AssistantOptions{Private: true, ParentID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	m := ready(t, st, child.ID)
	if !m.Private || m.ParentID != parent.ID {
		t.Fatalf("child = %+v, want private with parent %s", m, parent.ID)
	}
	ds, err := mgr.Descendants(context.Background(), parent.ID)
	if err != nil || len(ds) != 1 || ds[0].ID != child.ID {
		t.Fatalf("descendants = %+v, %v; want the child", ds, err)
	}
}
