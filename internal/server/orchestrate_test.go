package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/artefact"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/push"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// orchAdapter is a harness whose turns a test scripts by prompt:
//
//	HOLD …  runs until release, or until stopped
//	ASK …   asks a question and replies with the answer
//	PERM …  asks for a permission and replies with the outcome
//	else    replies "done: <prompt>" at once
type orchAdapter struct {
	id string

	mu       sync.Mutex
	prompts  map[string][]string // by thread
	held     map[string]func()   // by prompt
	sessions map[string]int      // harness starts, by thread
}

func newOrchAdapter(id string) *orchAdapter {
	return &orchAdapter{id: id, prompts: map[string][]string{}, held: map[string]func(){}, sessions: map[string]int{}}
}

func (a *orchAdapter) ID() string { return a.id }
func (a *orchAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: a.id, Name: "Harness " + a.id}
}
func (*orchAdapter) Models() []adapter.ModelMeta {
	return []adapter.ModelMeta{{ID: "test-model", Label: "Test", Default: true, Efforts: []string{"low", "high"}}}
}
func (a *orchAdapter) ListModels(context.Context, map[string]string) ([]adapter.ModelMeta, error) {
	return a.Models(), nil
}
func (*orchAdapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{
		{ID: "ask", Label: "Ask", Level: adapter.LevelAsk, Default: true},
		{ID: "edits", Label: "Edits", Level: adapter.LevelEdits},
		{ID: "all", Label: "All", Level: adapter.LevelAll},
	}
}
func (*orchAdapter) Probe(context.Context, map[string]string) adapter.Availability {
	return adapter.Ready(nil)
}
func (a *orchAdapter) CreateSession(_ context.Context, hs adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	a.mu.Lock()
	a.sessions[o.ThreadID]++
	a.mu.Unlock()
	return &orchSession{owner: a, hs: hs, thread: o.ThreadID, events: make(chan proto.Emission, 64)}, nil
}

func (a *orchAdapter) promptsOf(threadID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.prompts[threadID]...)
}

// release ends a HOLD turn with a reply.
func (a *orchAdapter) release(t *testing.T, prompt string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		f := a.held[prompt]
		delete(a.held, prompt)
		a.mu.Unlock()
		if f != nil {
			f()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no turn holds %q", prompt)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type orchSession struct {
	owner  *orchAdapter
	hs     adapter.HostServices
	thread string
	events chan proto.Emission

	mu     sync.Mutex
	closed bool
	turn   string // running
}

func (s *orchSession) emit(e proto.Emission) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.events <- e
	}
}

// finish ends turn with reply, once: a stop may have ended it already.
func (s *orchSession) finish(turn, reply string, stop string) {
	s.mu.Lock()
	if s.turn != turn || s.closed {
		s.mu.Unlock()
		return
	}
	s.turn = ""
	s.mu.Unlock()
	if reply != "" {
		s.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: turn, Role: "agent", Kind: "text", BlockID: turn, Delta: reply}))
	}
	s.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: turn, StopReason: stop}))
}

func (s *orchSession) Prompt(_ context.Context, p adapter.PromptInput) error {
	s.owner.mu.Lock()
	s.owner.prompts[s.thread] = append(s.owner.prompts[s.thread], p.Text)
	s.owner.mu.Unlock()
	s.mu.Lock()
	s.turn = p.TurnID
	s.mu.Unlock()
	turn := p.TurnID
	switch {
	case strings.HasPrefix(p.Text, "HOLD"):
		s.owner.mu.Lock()
		s.owner.held[p.Text] = func() { s.finish(turn, "finished "+p.Text, proto.StopEndTurn) }
		s.owner.mu.Unlock()
	case strings.HasPrefix(p.Text, "ASK"):
		go func() {
			res, err := s.hs.Elicit(context.Background(), adapter.ElicitationRequest{TurnID: turn, Prompt: "Which colour?",
				Schema: json.RawMessage(`{"type":"object","properties":{"q0":{"type":"string","title":"Colour","enum":["red","blue"]}},"required":["q0"]}`)})
			if err != nil {
				return
			}
			s.finish(turn, "answered "+string(res.Value), proto.StopEndTurn)
		}()
	case strings.HasPrefix(p.Text, "PERM"):
		go func() {
			out, err := s.hs.RequestPermission(context.Background(), adapter.PermissionRequest{TurnID: turn, ToolCallID: "c-" + turn, ToolName: "Bash", Title: "rm -rf build"})
			if err != nil {
				return
			}
			s.finish(turn, "permission "+out.Outcome, proto.StopEndTurn)
		}()
	default:
		go s.finish(turn, "done: "+p.Text, proto.StopEndTurn)
	}
	return nil
}

func (s *orchSession) Cancel(context.Context) error {
	s.mu.Lock()
	turn := s.turn
	s.mu.Unlock()
	if turn != "" {
		go s.finish(turn, "", proto.StopCancelled)
	}
	return nil
}
func (s *orchSession) Events() <-chan proto.Emission { return s.events }
func (s *orchSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

type orchRig struct {
	*toolRig
	h *orchAdapter

	pushMu sync.Mutex
	pushes []push.Message
}

func newOrchRig(t *testing.T) *orchRig {
	t.Helper()
	prev := wakeWindow
	wakeWindow = 100 * time.Millisecond
	t.Cleanup(func() { wakeWindow = prev })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	st, err := store.Open(filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := newOrchAdapter("orch")
	mgr := newManager(st, t.Logf, h)
	t.Cleanup(mgr.Shutdown)
	signer := artefact.NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	srv := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), ArtefactSigner: signer})
	t.Cleanup(srv.Close)
	r := &orchRig{h: h}
	srv.orch.send = func(_ context.Context, m push.Message) {
		r.pushMu.Lock()
		r.pushes = append(r.pushes, m)
		r.pushMu.Unlock()
	}
	remote := httptest.NewServer(asRemote(srv.Handler()))
	t.Cleanup(remote.Close)
	r.toolRig = &toolRig{t: t, st: st, mgr: mgr, srv: srv, signer: signer, remote: remote}
	return r
}

// parent starts a thread in a new project, in mode edits.
func (r *orchRig) parent() (string, project.Project) {
	r.t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("p%d", time.Now().UnixNano())
	now := proto.NowMillis()
	p := project.Project{ID: id, Name: "Kiosk", Home: r.t.TempDir(), Folders: []project.Folder{project.NewFolder(id+"a", r.t.TempDir()), project.NewFolder(id+"b", r.t.TempDir())}, CreatedAt: now, UpdatedAt: now}
	if err := r.st.CreateProject(ctx, p); err != nil {
		r.t.Fatal(err)
	}
	return r.threadIn(p), p
}

// threadIn starts a thread across the whole project, in mode edits.
func (r *orchRig) threadIn(p project.Project) string {
	r.t.Helper()
	a, err := r.mgr.CreateProject(context.Background(), thread.CreateProjectOptions{ProjectID: p.ID, Harness: "orch", Instance: "orch", Model: "test-model", Mode: "edits", AgentSettingsExplicit: true, Workspace: "local"})
	if err != nil {
		r.t.Fatal(err)
	}
	return a.ID
}

func (r *orchRig) ok(threadID, tool string, args any) string {
	r.t.Helper()
	got := r.call(threadID, tool, args)
	if got.code != http.StatusOK {
		r.t.Fatalf("%s: %d %s", tool, got.code, got.err)
	}
	return got.text
}

// delegate starts a child that does not wait, and returns its id.
func (r *orchRig) delegate(parent, prompt string) string {
	r.t.Helper()
	text := r.ok(parent, "delegate_task", map[string]any{"prompt": prompt})
	id, _, _ := strings.Cut(strings.TrimPrefix(text, "Started delegated thread "), " ")
	if _, err := r.st.Thread(context.Background(), id); err != nil {
		r.t.Fatalf("delegate_task said %q: no thread %q", text, id)
	}
	return id
}

// running waits for a HOLD turn to be under way: a stop before it starts
// drops the prompt instead of stopping a turn.
func (r *orchRig) running(prompt string) {
	r.t.Helper()
	eventually(r.t, prompt+" runs", func() bool {
		r.h.mu.Lock()
		defer r.h.mu.Unlock()
		return r.h.held[prompt] != nil
	})
}

func (r *orchRig) digest(id string) thread.Digest {
	r.t.Helper()
	a, err := r.mgr.View(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	d, err := a.Digest(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

// wakes are the prompts Omniplex sent the thread to wake it.
func (r *orchRig) wakes(id string) []string {
	var out []string
	for _, p := range r.h.promptsOf(id) {
		if strings.HasPrefix(p, "[Omniplex]") {
			out = append(out, p)
		}
	}
	return out
}

func TestDelegatingWithWaitReturnsTheChildsResult(t *testing.T) {
	r := newOrchRig(t)
	parent, p := r.parent()

	text := r.ok(parent, "delegate_task", map[string]any{"prompt": "build it", "title": "Builder", "wait": true, "effort": "high"})
	if !strings.Contains(text, "done: build it") || !strings.Contains(text, "completed") {
		t.Fatalf("delegate_task = %q, want the child's reply", text)
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(text, "Started delegated thread "), " ")
	child, err := r.st.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != parent || child.ProjectID != p.ID || child.Mode != "edits" || child.Effort != "high" || child.Model != "test-model" {
		t.Fatalf("child = %+v, want parent %s's project and inherited settings", child, parent)
	}
	// The wait delivered it: nothing wakes the parent with it again.
	time.Sleep(3 * wakeWindow)
	if w := r.wakes(parent); len(w) != 0 {
		t.Fatalf("parent woken with %q after its wait had the result", w)
	}
}

func TestChildrenFinishingTogetherWakeTheParentOnce(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()
	a := r.delegate(parent, "HOLD a")
	b := r.delegate(parent, "HOLD b")

	r.h.release(t, "HOLD a")
	r.h.release(t, "HOLD b")
	eventually(t, "the parent is woken", func() bool { return len(r.wakes(parent)) > 0 })
	time.Sleep(3 * wakeWindow)
	w := r.wakes(parent)
	if len(w) != 1 {
		t.Fatalf("parent woken %d times: %q", len(w), w)
	}
	for _, want := range []string{a, b, "finished HOLD a", "finished HOLD b"} {
		if !strings.Contains(w[0], want) {
			t.Fatalf("wake %q lacks %q", w[0], want)
		}
	}
}

func TestASendIsOwedBackAsAWake(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()
	child := r.ok(parent, "delegate_task", map[string]any{"prompt": "first", "wait": true})
	id, _, _ := strings.Cut(strings.TrimPrefix(child, "Started delegated thread "), " ")

	r.ok(parent, "thread_send", map[string]any{"id": id, "prompt": "HOLD second"})
	r.h.release(t, "HOLD second")
	eventually(t, "the parent is woken", func() bool { return len(r.wakes(parent)) == 1 })
	if w := r.wakes(parent)[0]; !strings.Contains(w, "finished HOLD second") || strings.Contains(w, "done: first") {
		t.Fatalf("wake = %q, want the second turn's result only", w)
	}
}

func TestToolsReachOnlyTheCallersThreads(t *testing.T) {
	r := newOrchRig(t)
	parent, p := r.parent()
	mine := r.delegate(parent, "mine")
	sibling := r.threadIn(p)
	_, other := r.parent()
	stranger := r.threadIn(other)

	list := r.ok(parent, "thread_list", nil)
	if !strings.Contains(list, mine) || strings.Contains(list, sibling) {
		t.Fatalf("thread_list = %q, want only %s", list, mine)
	}
	for _, tool := range []string{"thread_send", "thread_interrupt", "task_status", "thread_pending"} {
		args := map[string]any{"id": sibling, "prompt": "x", "action": "list"}
		if got := r.call(parent, tool, args); got.code != http.StatusForbidden {
			t.Fatalf("%s on a thread it did not delegate = %d %q, want 403", tool, got.code, got.text)
		}
	}
	if got := r.call(parent, "thread_wait", map[string]any{"ids": []string{mine, sibling}}); got.code != http.StatusForbidden {
		t.Fatalf("thread_wait on a sibling = %d, want 403", got.code)
	}
	// Reading reaches the project, as a link in a prompt does, and no further.
	if got := r.call(parent, "thread_read", map[string]any{"id": sibling}); got.code != http.StatusOK {
		t.Fatalf("thread_read in the project = %d %s", got.code, got.err)
	}
	if got := r.call(parent, "thread_read", map[string]any{"id": stranger}); got.code != http.StatusForbidden {
		t.Fatalf("thread_read in another project = %d, want 403", got.code)
	}
	// A child cannot steer the thread that delegated it.
	if got := r.call(mine, "thread_send", map[string]any{"id": parent, "prompt": "x"}); got.code != http.StatusForbidden {
		t.Fatalf("child steering its parent = %d, want 403", got.code)
	}
}

func TestInterruptingStopsEveryDescendant(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()
	child := r.delegate(parent, "HOLD child")
	grandchild := r.delegate(child, "HOLD grandchild")
	r.running("HOLD child")
	r.running("HOLD grandchild")

	text := r.ok(parent, "thread_interrupt", map[string]any{"id": child})
	if !strings.Contains(text, "1 thread") {
		t.Fatalf("thread_interrupt = %q, want it to count the grandchild", text)
	}
	for _, id := range []string{child, grandchild} {
		eventually(t, id+" stops", func() bool {
			d := r.digest(id)
			return d.Last != nil && d.Last.Done && d.Last.StopReason == proto.StopCancelled
		})
	}
	// An interrupted thread's stop is not news.
	time.Sleep(3 * wakeWindow)
	if w := r.wakes(parent); len(w) != 0 {
		t.Fatalf("parent woken with %q by a stop it made", w)
	}
}

func TestStoppingAThreadStopsWhatItDelegated(t *testing.T) {
	r := newOrchRig(t)
	r.ws = dialWS(t, r.srv.Handler())
	parent, _ := r.parent()
	child := r.delegate(parent, "HOLD child")
	r.running("HOLD child")
	r.ws.ok("cancel", map[string]any{"threadId": parent}, nil)
	eventually(t, "the child stops", func() bool {
		d := r.digest(child)
		return d.Last != nil && d.Last.Done && d.Last.StopReason == proto.StopCancelled
	})
}

func TestAThreadThatDelegatedFinishesOnceItHasAnswered(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()
	child := r.delegate(parent, "HOLD child")
	r.delegate(child, "HOLD grandchild")

	// The child's turn ends with its own delegate still out: not its result.
	r.h.release(t, "HOLD child")
	time.Sleep(3 * wakeWindow)
	if w := r.wakes(parent); len(w) != 0 {
		t.Fatalf("parent woken with %q before the child had its delegate's result", w)
	}
	r.h.release(t, "HOLD grandchild")
	eventually(t, "the child is woken", func() bool { return len(r.wakes(child)) == 1 })
	eventually(t, "the parent is woken", func() bool { return len(r.wakes(parent)) == 1 })
	if w := r.wakes(parent)[0]; !strings.Contains(w, "done: [Omniplex]") {
		t.Fatalf("wake = %q, want the child's answer to its own wake", w)
	}
}

func TestStoppingAWaitingThreadDoesNotWakeItAgain(t *testing.T) {
	r := newOrchRig(t)
	r.ws = dialWS(t, r.srv.Handler())
	parent, _ := r.parent()
	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	res := r.start(ctx, parent, "delegate_task", map[string]any{"prompt": "HOLD child", "wait": true})
	r.running("HOLD child")

	// Stop, then the harness drops the call it was in, as a stopped turn does.
	r.ws.ok("cancel", map[string]any{"threadId": parent}, nil)
	leave()
	<-res
	time.Sleep(3 * wakeWindow)
	if w := r.wakes(parent); len(w) != 0 {
		t.Fatalf("stopped parent woken with %q", w)
	}
}

func TestAModeWithNoLevelIsJudgedAgainstItself(t *testing.T) {
	// narrow and loose have no level: narrow sits between ask and edits,
	// loose between edits and all.
	modes := []adapter.PermissionModeMeta{
		{ID: "ask", Level: adapter.LevelAsk, Default: true},
		{ID: "narrow"},
		{ID: "edits", Level: adapter.LevelEdits},
		{ID: "loose"},
		{ID: "all", Level: adapter.LevelAll},
	}
	same := harnessCatalog{ID: "a", Modes: modes}
	other := harnessCatalog{ID: "b", Modes: []adapter.PermissionModeMeta{{ID: "ask", Level: adapter.LevelAsk}, {ID: "edits", Level: adapter.LevelEdits}}}
	for _, c := range []struct {
		parent, ask string
		to          harnessCatalog
		want        string // "" is refused
	}{
		{"narrow", "edits", same, ""},
		{"narrow", "loose", same, ""},
		{"narrow", "narrow", same, "narrow"},
		{"narrow", "ask", same, "ask"},
		{"narrow", "", other, "ask"},
		{"ask", "narrow", same, ""},
		{"edits", "narrow", same, "narrow"},
		{"edits", "loose", same, ""},
		{"loose", "edits", same, "edits"},
		{"loose", "all", same, ""},
		{"loose", "", other, "edits"},
	} {
		parent := agentSettings{Harness: "a", Model: "x", Mode: c.parent}
		got, err := resolveChildSettings(parent, modes, c.to, settingsAsk{Mode: c.ask})
		switch {
		case c.want == "" && err == nil:
			t.Errorf("a %s parent delegated %s to %s", c.parent, c.ask, c.to.ID)
		case c.want != "" && (err != nil || got.Mode != c.want):
			t.Errorf("a %s parent asking %q of %s = %q, %v; want %s", c.parent, c.ask, c.to.ID, got.Mode, err, c.want)
		}
	}
}

func TestThreadLinkEscapesItsTitle(t *testing.T) {
	if got, want := threadLink("t1", `Plan [v2] \ x`), `[@Plan \[v2\] \\ x](omniplex-thread://t1)`; got != want {
		t.Fatalf("threadLink = %s, want %s", got, want)
	}
}

func TestAWaitEndsWhenAChildNeedsTheUser(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()

	text := r.ok(parent, "delegate_task", map[string]any{"prompt": "PERM deploy", "wait": true})
	if !strings.Contains(text, "needs approval") || !strings.Contains(text, "rm -rf build") {
		t.Fatalf("delegate_task = %q, want it to stop at the approval", text)
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(text, "Started delegated thread "), " ")
	d := r.digest(id)
	if len(d.Approvals) != 1 {
		t.Fatalf("approvals = %+v", d.Approvals)
	}
	got := r.call(parent, "thread_pending", map[string]any{"id": id, "action": "respond", "request_id": d.Approvals[0].RequestID, "answers": map[string]any{"q0": "allow"}})
	if got.code != http.StatusForbidden {
		t.Fatalf("answering a permission = %d %q, want 403", got.code, got.text)
	}
	if len(r.digest(id).Approvals) != 1 {
		t.Fatal("the permission was resolved by the parent")
	}
	eventually(t, "the user is pushed, to the child", func() bool {
		r.pushMu.Lock()
		defer r.pushMu.Unlock()
		return len(r.pushes) == 1 && r.pushes[0].ThreadID == id && strings.Contains(r.pushes[0].Title, "needs approval")
	})
}

func TestAWaitEndsWhenAThreadFurtherDownNeedsApproval(t *testing.T) {
	r := newOrchRig(t)
	top, _ := r.parent()
	mid := r.delegate(top, "HOLD mid")
	r.running("HOLD mid")

	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	res := r.start(ctx, top, "thread_wait", map[string]any{"ids": []string{mid}, "timeout_seconds": 600})
	// The wait is under way before the approval exists, so it is the
	// approval that ends it.
	select {
	case got := <-res:
		t.Fatalf("thread_wait returned before anything needed the user: %q", got.text)
	case <-time.After(3 * wakeWindow):
	}
	bottom := r.delegate(mid, "PERM deploy")

	select {
	case got := <-res:
		if got.code != http.StatusOK {
			t.Fatalf("thread_wait = %d %s", got.code, got.err)
		}
		for _, want := range []string{mid, "needs approval", bottom, "rm -rf build"} {
			if !strings.Contains(got.text, want) {
				t.Fatalf("thread_wait = %q, want %q in it", got.text, want)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("thread_wait ran on while a thread two levels down waited on the user")
	}
	if d := r.digest(bottom); len(d.Approvals) != 1 {
		t.Fatalf("approvals = %+v, want the request still the user's", d.Approvals)
	}
}

func TestAParentAnswersAChildsQuestion(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()

	text := r.ok(parent, "delegate_task", map[string]any{"prompt": "ASK colour", "wait": true})
	if !strings.Contains(text, "needs answer") {
		t.Fatalf("delegate_task = %q, want it to stop at the question", text)
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(text, "Started delegated thread "), " ")
	qs := r.digest(id).Questions
	if len(qs) != 1 {
		t.Fatalf("questions = %+v", qs)
	}
	reqID := qs[0].RequestID
	if list := r.ok(parent, "thread_pending", map[string]any{"id": id, "action": "list"}); !strings.Contains(list, reqID) {
		t.Fatalf("list = %q, want %s", list, reqID)
	}
	read := r.ok(parent, "thread_pending", map[string]any{"id": id, "action": "read", "request_id": reqID})
	if !strings.Contains(read, "red") || !strings.Contains(read, "blue") {
		t.Fatalf("read = %q, want its options", read)
	}
	if got := r.call(parent, "thread_pending", map[string]any{"id": id, "action": "respond", "request_id": reqID, "answers": map[string]any{"q0": "green"}}); got.code != http.StatusBadRequest {
		t.Fatalf("an answer not among the options = %d, want 400", got.code)
	}
	r.ok(parent, "thread_pending", map[string]any{"id": id, "action": "respond", "request_id": reqID, "answers": map[string]any{"colour": "Blue"}})

	done := r.ok(parent, "thread_wait", map[string]any{"ids": []string{id}, "timeout_seconds": 10})
	if !strings.Contains(done, `answered {"q0":"blue"}`) {
		t.Fatalf("thread_wait = %q, want the child's reply to the answer", done)
	}
}

func TestAQuestionFromAChildNobodyWaitsOnWakesTheParent(t *testing.T) {
	r := newOrchRig(t)
	parent, _ := r.parent()
	id := r.delegate(parent, "ASK colour")
	eventually(t, "the parent hears the question", func() bool { return len(r.wakes(parent)) == 1 })
	if w := r.wakes(parent)[0]; !strings.Contains(w, "Which colour?") || !strings.Contains(w, id) {
		t.Fatalf("wake = %q, want the question", w)
	}
}

func TestChildSettings(t *testing.T) {
	modes := []adapter.PermissionModeMeta{
		{ID: "ask", Level: adapter.LevelAsk, Default: true},
		{ID: "edits", Level: adapter.LevelEdits},
		{ID: "all", Level: adapter.LevelAll},
	}
	other := harnessCatalog{ID: "b",
		Models: []adapter.ModelMeta{{ID: "m1", Default: true}, {ID: "m2", Efforts: []string{"low", "high"}}},
		Modes:  []adapter.PermissionModeMeta{{ID: "plan", Level: adapter.LevelAsk}, {ID: "auto", Level: adapter.LevelEdits}, {ID: "yolo", Level: adapter.LevelAll}},
	}
	same := harnessCatalog{ID: "a", Models: []adapter.ModelMeta{{ID: "x", Efforts: []string{"high"}}}, Modes: modes}
	parent := agentSettings{Harness: "a", Instance: "acct", Model: "x", Effort: "high", Mode: "edits"}

	for _, c := range []struct {
		name    string
		parent  agentSettings
		child   harnessCatalog
		ask     settingsAsk
		want    agentSettings
		wantErr string
	}{
		{name: "same harness inherits all", parent: parent, child: same, want: agentSettings{Harness: "a", Instance: "acct", Model: "x", Effort: "high", Mode: "edits"}},
		{name: "another harness gets its default model and the closest mode", parent: parent, child: other, want: agentSettings{Harness: "b", Model: "m1", Mode: "auto"}},
		{name: "the project's model wins over the harness default", parent: parent, child: func() harnessCatalog { c := other; c.ProjectModel = "m2"; return c }(), want: agentSettings{Harness: "b", Model: "m2", Effort: "high", Mode: "auto"}},
		{name: "an asked narrower mode", parent: parent, child: other, ask: settingsAsk{Mode: "plan"}, want: agentSettings{Harness: "b", Model: "m1", Mode: "plan"}},
		{name: "an asked broader mode is refused", parent: parent, child: other, ask: settingsAsk{Mode: "yolo"}, wantErr: "do more than this thread"},
		{name: "an unknown model is refused", parent: parent, child: other, ask: settingsAsk{Model: "nope"}, wantErr: "no model"},
		{name: "an effort the model does not take is refused", parent: parent, child: other, ask: settingsAsk{Model: "m2", Effort: "max"}, wantErr: "takes effort"},
		{name: "a parent on its harness default steps down from it", parent: agentSettings{Harness: "a", Model: "x"}, child: other, want: agentSettings{Harness: "b", Model: "m1", Mode: "plan"}},
		{name: "no mode narrow enough", parent: agentSettings{Harness: "a", Model: "x", Mode: "ask"}, child: harnessCatalog{ID: "b", Modes: []adapter.PermissionModeMeta{{ID: "yolo", Level: adapter.LevelAll}}}, wantErr: "offers no mode"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveChildSettings(c.parent, modes, c.child, c.ask)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestMapAnswers(t *testing.T) {
	fields := questionFields(json.RawMessage(`{"properties":{
		"q0":{"type":"string","title":"Colour","enum":["red","blue"]},
		"q1":{"type":"array","title":"Toppings","items":{"enum":["ham","egg"]}},
		"q2":{"type":"boolean","title":"Hot"},
		"q3":{"type":"integer","title":"Count"}},
		"required":["q0"]}`))
	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	got, err := mapAnswers(fields, raw(`{"colour":"RED","q1":"egg","hot":"true","q3":"2"}`))
	if err != nil || string(got) != `{"q0":"red","q1":["egg"],"q2":true,"q3":2}` {
		t.Fatalf("got %s, %v", got, err)
	}
	for in, want := range map[string]string{
		`{"q1":["ham"]}`:        "q0 is required",
		`{"q0":"green"}`:        "one of red, blue",
		`{"q0":"red","q3":1.5}`: "whole number",
		`{"q0":"red","q9":1}`:   "no field",
	} {
		if _, err := mapAnswers(fields, raw(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v, want %q", in, err, want)
		}
	}
}
