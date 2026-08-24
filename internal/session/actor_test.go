package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
)

// fakeAdapter emits scripted events, so the seam can be tested without a real
// harness process.
type fakeAdapter struct {
	mu        sync.Mutex
	last      *fakeSession
	live      []adapter.ModelMeta
	liveErr   error
	listCalls int
	// listGate, when set, holds the listing open so a test can prove the
	// caller did not wait for it.
	listGate      chan struct{}
	createGate    <-chan struct{}
	createStarted chan<- struct{}
}

func TestViewRestoresProjectionWithoutStartingHarness(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "view.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	meta := store.SessionMeta{ID: "viewed", Cwd: t.TempDir(), Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "idle"}
	if err := st.CreateSession(ctx, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, meta.ID, proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness})); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	actor, err := mgr.View(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fa.session() != nil {
		t.Fatal("view started a harness")
	}
	state, err := actor.State(ctx)
	if err != nil || state.SessionID != meta.ID {
		t.Fatalf("viewed state = %+v, err = %v", state, err)
	}
	if _, err := mgr.Get(ctx, meta.ID); err != nil {
		t.Fatal(err)
	}
	if fa.session() == nil {
		t.Fatal("command path did not activate the harness")
	}
}

func TestViewedInterruptedTurnRecoversWhenActivated(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "view-turn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	first := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, first)
	actor, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Prompt(ctx, "keep going", nil); err != nil {
		t.Fatal(err)
	}
	<-first.session().prompts
	waitFor(t, func() bool {
		state, _ := actor.State(ctx)
		return state.Phase == "turn"
	})
	id := actor.ID
	mgr.Shutdown()

	resumed := &fakeAdapter{}
	mgr = NewManager(st, func(string, ...any) {}, resumed)
	defer mgr.Shutdown()
	if _, err := mgr.View(ctx, id); err != nil {
		t.Fatal(err)
	}
	if resumed.session() != nil {
		t.Fatal("view of interrupted turn started a harness")
	}
	if _, err := mgr.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session := resumed.session()
		if session == nil {
			return false
		}
		select {
		case prompt := <-session.prompts:
			return strings.Contains(prompt.Text, "restarted")
		default:
			return false
		}
	})
}

func TestCancelProcesslessInterruptedTurn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cancel-passive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	meta := store.SessionMeta{ID: "interrupted", Cwd: t.TempDir(), Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "turn"}
	if err := st.CreateSession(ctx, meta); err != nil {
		t.Fatal(err)
	}
	for _, emission := range []proto.Emission{
		proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness}),
		proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "t1", Prompt: "work"}),
	} {
		if _, err := st.Append(ctx, meta.ID, emission); err != nil {
			t.Fatal(err)
		}
	}
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	defer mgr.Shutdown()
	actor, err := mgr.View(ctx, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := actor.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := actor.State(ctx)
	if state.Phase != "idle" || !state.Turns[0].Done || state.Turns[0].StopReason != proto.StopCancelled {
		t.Fatalf("state after passive cancel = %+v", state)
	}
}

func TestHarnessActivationDoesNotBlockUnrelatedView(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "activation-lock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, id := range []string{"slow", "reader"} {
		meta := store.SessionMeta{ID: id, Cwd: t.TempDir(), Harness: "fake", CreatedAt: 1, UpdatedAt: 1, Phase: "idle"}
		if err := st.CreateSession(ctx, meta); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Append(ctx, id, proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{Cwd: meta.Cwd, Harness: meta.Harness})); err != nil {
			t.Fatal(err)
		}
	}
	gate := make(chan struct{})
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(gate) }) }
	started := make(chan struct{}, 1)
	fa := &fakeAdapter{createGate: gate, createStarted: started}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()
	defer release()
	if _, err := mgr.View(ctx, "slow"); err != nil {
		t.Fatal(err)
	}
	activation := make(chan error, 1)
	go func() {
		_, err := mgr.Get(ctx, "slow")
		activation <- err
	}()
	<-started

	viewed := make(chan error, 1)
	go func() {
		_, err := mgr.View(ctx, "reader")
		viewed <- err
	}()
	select {
	case err := <-viewed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated view blocked behind harness activation")
	}
	release()
	if err := <-activation; err != nil {
		t.Fatal(err)
	}
}

func (f *fakeAdapter) ID() string { return "fake" }

func (f *fakeAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: "fake", Name: "Fake", Accent: "oklch(0.7 0 0)"}
}

func (f *fakeAdapter) Models() []adapter.ModelMeta {
	return []adapter.ModelMeta{{ID: "fallback", Label: "Fallback", Default: true}}
}

// ListModels stands in for a harness that can be asked. By default it refuses,
// so tests that care about nothing else never spawn a background listing;
// live and liveErr let a test drive both outcomes.
func (f *fakeAdapter) ListModels(ctx context.Context, env map[string]string) ([]adapter.ModelMeta, error) {
	f.mu.Lock()
	gate := f.listGate
	f.listCalls++
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.liveErr != nil {
		return nil, f.liveErr
	}
	if f.live == nil {
		return nil, errors.New("this fake cannot list models")
	}
	return f.live, nil
}

func (f *fakeAdapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{{ID: "manual", Label: "Manual", Default: true}}
}

func (f *fakeAdapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	return adapter.Ready(nil)
}

func (f *fakeAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	if f.createStarted != nil {
		f.createStarted <- struct{}{}
	}
	if f.createGate != nil {
		select {
		case <-f.createGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s := &fakeSession{
		host: host, events: make(chan proto.Emission, 4096),
		prompts: make(chan adapter.PromptInput, 16), actions: make(chan adapter.ComposerActionInput, 16),
	}
	f.mu.Lock()
	f.last = s
	f.mu.Unlock()
	return s, nil
}

func (f *fakeAdapter) session() *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

type fakeSession struct {
	host         adapter.HostServices
	events       chan proto.Emission
	prompts      chan adapter.PromptInput
	actions      chan adapter.ComposerActionInput
	closeOnce    sync.Once
	closeStarted chan struct{}
	closeRelease <-chan struct{}

	mu     sync.Mutex
	mode   string
	refuse error
}

func (s *fakeSession) Prompt(ctx context.Context, in adapter.PromptInput) error {
	s.mu.Lock()
	refuse := s.refuse
	s.mu.Unlock()
	if refuse != nil {
		return refuse
	}
	s.prompts <- in
	return nil
}
func (s *fakeSession) Cancel(ctx context.Context) error { return nil }
func (s *fakeSession) RunComposerAction(ctx context.Context, in adapter.ComposerActionInput) (any, error) {
	s.actions <- in
	return map[string]any{}, nil
}
func (s *fakeSession) SetMode(ctx context.Context, mode string) error {
	if mode == "rejected" {
		return errors.New("the harness refused this mode")
	}
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
	return nil
}
func (s *fakeSession) currentMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}
func (s *fakeSession) Events() <-chan proto.Emission { return s.events }
func (s *fakeSession) Close() error {
	s.closeOnce.Do(func() {
		if s.closeStarted != nil {
			close(s.closeStarted)
		}
		if s.closeRelease != nil {
			<-s.closeRelease
		}
		close(s.events)
	})
	return nil
}
func (s *fakeSession) emit(e proto.Emission) { s.events <- e }

func newTestActor(t *testing.T) (*Actor, *fakeAdapter, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	waitFor(t, func() bool { return actor.Head() >= 1 }) // session.created landed
	return actor, fa, st
}

func TestComposerActionReservesTheTurnBeforeCallingHarness(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	ctx := context.Background()

	if _, err := actor.RunComposerAction(ctx, "review", "focus on races", "/review focus on races"); err != nil {
		t.Fatal(err)
	}
	in := <-fa.session().actions
	if in.Action != "review" || in.Args != "focus on races" || in.TurnID == "" {
		t.Fatalf("action input = %+v", in)
	}
	state, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "turn" || len(state.Turns) == 0 || state.Turns[len(state.Turns)-1].Prompt != "/review focus on races" {
		t.Fatalf("action did not become a canonical turn: %+v", state)
	}
	res, err := actor.Prompt(ctx, "must wait", nil)
	if err != nil || !res.Queued() {
		t.Fatalf("concurrent prompt = %+v, %v; want queued", res, err)
	}

	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: in.TurnID, StopReason: proto.StopEndTurn}))
	// The action's turn ending releases the queued prompt.
	if next := <-fa.session().prompts; next.Text != "must wait" {
		t.Fatalf("queued prompt after action = %+v", next)
	}
}

// TestSetModeSwitchesHarnessAndRecordsEvent covers the mid-session switch: the
// harness is told, and the change lands in the log as session.config_changed
// so every presenter's projection follows.
func TestSetModeSwitchesHarnessAndRecordsEvent(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	ctx := context.Background()

	if err := actor.SetMode(ctx, "acceptEdits"); err != nil {
		t.Fatal(err)
	}
	if got := fa.session().currentMode(); got != "acceptEdits" {
		t.Fatalf("harness mode = %q, want acceptEdits", got)
	}
	state, err := actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != "acceptEdits" {
		t.Fatalf("projection mode = %q, want acceptEdits", state.Mode)
	}

	// A refused mode is a legible error and leaves the recorded mode alone.
	if err := actor.SetMode(ctx, "rejected"); err == nil {
		t.Fatal("expected an error for a mode the harness refuses")
	}
	state, err = actor.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != "acceptEdits" {
		t.Fatalf("projection mode after refusal = %q, want acceptEdits", state.Mode)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 5s")
}

func TestProjectProvisionBlocksHarnessAndStreamsOutput(t *testing.T) {
	root := t.TempDir()
	hook := filepath.Join(root, "provision")
	cleanupHook := filepath.Join(root, "deprovision")
	script := "#!/bin/sh\ntest \"$OMNIPLEX_LIFECYCLE_VERSION\" = 2\necho preparing-test-workspace\nsleep 0.1\nprintf '{\"cwd\":\"%s\"}' \"$OMNIPLEX_PROJECT_ROOT\" > \"$OMNIPLEX_RESULT_FILE\"\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cleanupHook, []byte("#!/bin/sh\ntest \"$OMNIPLEX_LIFECYCLE_VERSION\" = 2\ntest -f \"$OMNIPLEX_CONTEXT_FILE\"\necho cleanup-test-workspace\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := proto.NowMillis()
	p := project.Project{ID: "p1", Root: root, CreatedAt: now, UpdatedAt: now, Config: project.DefaultConfig(root)}
	p.Config.Defaults.Harness = "fake"
	// Hooks belong to provisioning, so the session has to be one that
	// provisions: a local session runs in a checkout that already exists and
	// deliberately skips them.
	p.Config.Defaults.Workspace = "managed"
	p.Config.Workspace.Provision = "provision"
	p.Config.Workspace.Deprovision = "deprovision"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if fa.session() != nil {
		t.Fatal("harness started before provision hook completed")
	}
	waitFor(t, func() bool {
		state, e := a.State(context.Background())
		return e == nil && state.Workspace.Phase == "ready"
	})
	if fa.session() == nil {
		t.Fatal("harness was not started after provision")
	}
	state, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Workspace.Output != "preparing-test-workspace\n" {
		t.Fatalf("output = %q", state.Workspace.Output)
	}
	if err := mgr.Cleanup(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		meta, e := st.Session(context.Background(), a.ID)
		if e == nil && meta.Phase == "cleanup_failed" {
			t.Fatalf("cleanup failed: %+v", meta)
		}
		return e == nil && meta.Phase == "closed"
	})
	closed, err := mgr.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = closed.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Workspace.Output, "cleanup-test-workspace") {
		t.Fatalf("cleanup output was not durable: %q", state.Workspace.Output)
	}
}

func TestClawdCompatibilityHookGetsBranchAndNeedsNoResultFile(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "omniplex@test.invalid"}, {"config", "user.name", "omniplex test"}, {"commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	hook := filepath.Join(root, "worktree-setup.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ndir=$(printf '%s' \"$1\" | tr / -)\ntest -d \".worktrees/$dir\"\necho compatibility-branch:$1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	teardown := filepath.Join(root, "worktree-teardown.sh")
	if err := os.WriteFile(teardown, []byte("#!/bin/sh\necho simulated-teardown-failure >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := proto.NowMillis()
	p := project.Project{ID: "compat", Root: root, CreatedAt: now, UpdatedAt: now, Config: project.DefaultConfig(root)}
	p.Config.Defaults.Harness = "fake"
	p.Config.Defaults.Workspace = "managed"
	p.Config.Workspace.Provision = "worktree-setup.sh"
	p.Config.Workspace.Deprovision = "worktree-teardown.sh"
	if err := st.PutProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()
	a, err := mgr.CreateProject(context.Background(), CreateProjectOptions{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, e := a.State(context.Background())
		if e == nil && s.Workspace.Phase == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Cwd == root || !strings.Contains(s.Workspace.Output, "compatibility-branch:feature/omniplex-") {
		t.Fatalf("compatibility result not synthesized: cwd=%q output=%q", s.Cwd, s.Workspace.Output)
	}
	if err := mgr.Delete(context.Background(), a.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		meta, e := st.Session(context.Background(), a.ID)
		return e == nil && meta.Phase == "cleanup_failed"
	})
	if _, err := os.Stat(s.Cwd); err != nil {
		t.Fatalf("failed teardown unexpectedly removed workspace: %v", err)
	}
	if err := mgr.ForceDelete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, e := st.Session(context.Background(), a.ID)
		return errors.Is(e, store.ErrNotFound)
	})
	if _, err := os.Stat(s.Cwd); !os.IsNotExist(err) {
		t.Fatalf("force delete left workspace at %s", s.Cwd)
	}
	branchCheck := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+s.Workspace.Branch)
	branchCheck.Dir = root
	if err := branchCheck.Run(); err != nil {
		t.Fatalf("force delete removed the branch: %v", err)
	}
}

// Invariant 1: seq is gapless and strictly increasing.
func TestSeqIsGaplessAndMonotonic(t *testing.T) {
	actor, fa, st := newTestActor(t)
	sess := fa.session()

	for i := 0; i < 50; i++ {
		sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
			Role: "agent", Kind: "text", BlockID: "b1", Delta: "x",
		}))
	}
	waitFor(t, func() bool { return actor.Head() >= 51 })

	evs, err := st.ReadEvents(context.Background(), actor.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d; want %d", i, ev.Seq, i+1)
		}
	}
}

// Invariant 2: rebuilding from the log alone yields identical state.
func TestRebuildFromLogMatchesLiveState(t *testing.T) {
	actor, fa, st := newTestActor(t)
	sess := fa.session()

	for i := 0; i < 20; i++ {
		sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
			Role: "agent", Kind: "text", BlockID: "b1", Delta: "chunk ",
		}))
	}
	sess.emit(proto.Emit(proto.ToolCallStarted, proto.ToolCallStartedPayload{
		ToolCallID: "t1", Kind: proto.KindExecute, Title: "ls", Status: proto.StatusInProgress,
	}))
	waitFor(t, func() bool { return actor.Head() >= 22 })

	live, err := actor.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	rebuilt := projection.New(actor.ID)
	evs, _ := st.ReadEvents(context.Background(), actor.ID, 0, 10000)
	for _, ev := range evs {
		rebuilt.Apply(ev)
	}

	a, _ := json.Marshal(live)
	b, _ := json.Marshal(rebuilt)
	if string(a) != string(b) {
		t.Fatalf("rebuilt state differs from live state:\nlive:    %s\nrebuilt: %s", a, b)
	}
}

// Invariant 4: a client that discards seq <= lastApplied converges even when
// the same event is delivered twice.
func TestDuplicateApplyIsIdempotent(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sess := fa.session()

	sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		Role: "agent", Kind: "text", BlockID: "b1", Delta: "hello",
	}))
	waitFor(t, func() bool { return actor.Head() >= 2 })

	state, _ := actor.State(context.Background())
	before, _ := json.Marshal(state)

	// Replay every event a second time.
	evs, _ := actor.store.ReadEvents(context.Background(), actor.ID, 0, 1000)
	for _, ev := range evs {
		state.Apply(ev)
	}
	after, _ := json.Marshal(state)

	if string(before) != string(after) {
		t.Fatalf("re-applying events changed state:\nbefore: %s\nafter:  %s", before, after)
	}
}

// Invariant 6: no event is skipped or duplicated across the attach seam, even
// when events land while the attach is in flight.
func TestAttachCompletenessUnderConcurrentAppends(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sess := fa.session()

	// Background writer, so events land during the attach.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
					Role: "agent", Kind: "text", BlockID: "b1", Delta: "x",
				}))
				time.Sleep(time.Millisecond)
			}
		}
	}()

	time.Sleep(20 * time.Millisecond)

	// The attach order under test: subscribe, then read history.
	sub := actor.Subscribe()
	defer actor.Unsubscribe(sub)

	res, err := actor.Attach(context.Background(), 0, false)
	if err != nil {
		t.Fatal(err)
	}

	seen := []int64{}
	last := res.Seq
	deadline := time.After(2 * time.Second)
collect:
	for len(seen) < 20 {
		select {
		case ev := <-sub.Ch:
			if ev.Seq <= last {
				continue // catch-up already covered it
			}
			seen = append(seen, ev.Seq)
		case <-deadline:
			break collect
		}
	}
	close(stop)
	<-done

	if len(seen) == 0 {
		t.Fatal("received no live events after attach")
	}
	want := last + 1
	for _, s := range seen {
		if s != want {
			t.Fatalf("gap or duplicate at the attach seam: got seq %d, want %d (snapshot at %d)", s, want, last)
		}
		want++
	}
}

// Invariant 9: any presenter may resolve a pending permission; the first wins
// and the loser gets an ack rather than an error.
func TestPermissionIsFungibleAndFirstResolutionWins(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sess := fa.session()

	outcomes := make(chan adapter.PermissionOutcome, 1)
	go func() {
		out, err := sess.host.RequestPermission(context.Background(), adapter.PermissionRequest{
			ToolCallID: "t1", ToolName: "Bash", Title: "rm -rf /",
		})
		if err != nil {
			t.Error(err)
			return
		}
		outcomes <- out
	}()

	// The request is durable state, so it shows up in the projection.
	var requestID string
	waitFor(t, func() bool {
		st, err := actor.State(context.Background())
		if err != nil || len(st.Pending) == 0 {
			return false
		}
		requestID = st.Pending[0].RequestID
		return true
	})

	// Two presenters answer at once; exactly one resolution takes effect.
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for i, o := range []string{proto.OutcomeAllowOnce, proto.OutcomeRejectOnce} {
		wg.Add(1)
		go func(i int, o string) {
			defer wg.Done()
			err := actor.ResolvePermission(context.Background(), requestID, adapter.PermissionOutcome{Outcome: o})
			if err != nil {
				results <- "error:" + err.Error()
				return
			}
			results <- "ok"
		}(i, o)
	}
	wg.Wait()
	close(results)

	for r := range results {
		if r != "ok" {
			t.Fatalf("a presenter got %q; losing a permission race must be an ack, not an error", r)
		}
	}

	select {
	case <-outcomes:
	case <-time.After(2 * time.Second):
		t.Fatal("adapter was never unblocked by the resolution")
	}

	// Exactly one permission.resolved was appended.
	evs, _ := actor.store.ReadEvents(context.Background(), actor.ID, 0, 10000)
	n := 0
	for _, ev := range evs {
		if ev.Type == proto.PermissionResolved {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("appended %d permission.resolved events; want exactly 1", n)
	}

	st, _ := actor.State(context.Background())
	if len(st.Pending) != 0 {
		t.Fatalf("pending permission survived resolution: %+v", st.Pending)
	}
}

func TestElicitationIsDurableAndFungible(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	resultCh := make(chan adapter.ElicitationResult, 1)
	go func() {
		result, err := fa.session().host.Elicit(context.Background(), adapter.ElicitationRequest{
			Prompt: "Choose", Schema: json.RawMessage(`{"type":"object"}`),
		})
		if err != nil {
			t.Error(err)
			return
		}
		resultCh <- result
	}()

	var requestID string
	waitFor(t, func() bool {
		state, err := actor.State(context.Background())
		if err != nil || len(state.Elicitations) != 1 {
			return false
		}
		requestID = state.Elicitations[0].RequestID
		return true
	})

	want := json.RawMessage(`{"answer":"yes"}`)
	if err := actor.ResolveElicitation(context.Background(), requestID, adapter.ElicitationResult{Action: "accept", Value: want}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-resultCh:
		if got.Action != "accept" || string(got.Value) != string(want) {
			t.Fatalf("elicitation result=%+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("adapter was not unblocked")
	}
	state, _ := actor.State(context.Background())
	if len(state.Elicitations) != 0 {
		t.Fatalf("resolved elicitation remains pending: %+v", state.Elicitations)
	}
}

// Invariant 12: a stalled consumer is dropped and resynced. Its queue never
// grows and the session actor never blocks on it.
func TestSlowConsumerIsDroppedAndResynced(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sess := fa.session()

	slow := actor.Subscribe() // never drained
	fast := actor.Subscribe()
	defer actor.Unsubscribe(fast)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range fast.Ch {
		}
	}()

	total := SubscriberQueue * 3
	for i := 0; i < total; i++ {
		sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
			Role: "agent", Kind: "text", BlockID: "b1", Delta: "x",
		}))
	}

	// The actor kept making progress despite the stalled subscriber.
	waitFor(t, func() bool { return actor.Head() >= int64(total) })

	select {
	case <-slow.Resync:
	case <-time.After(2 * time.Second):
		t.Fatal("slow consumer was never asked to resync")
	}

	if got := len(slow.Ch); got > SubscriberQueue {
		t.Fatalf("slow consumer queue grew to %d; bound is %d", got, SubscriberQueue)
	}

	actor.Unsubscribe(fast)
}

// Invariant 8: losing every client does not interrupt a turn.
func TestDisconnectIsNotCancel(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	sess := fa.session()

	res, err := actor.Prompt(context.Background(), "do a thing", nil)
	turnID := res.TurnID
	if err != nil {
		t.Fatal(err)
	}
	<-sess.prompts

	// A presenter attaches and leaves mid-turn.
	sub := actor.Subscribe()
	actor.Unsubscribe(sub)

	sess.emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: turnID, Role: "agent", Kind: "text", BlockID: "b1", Delta: "still working",
	}))
	sess.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: turnID, StopReason: proto.StopEndTurn,
	}))

	waitFor(t, func() bool {
		st, err := actor.State(context.Background())
		return err == nil && len(st.Turns) == 1 && st.Turns[0].Done
	})

	st, _ := actor.State(context.Background())
	if st.Turns[0].StopReason != proto.StopEndTurn {
		t.Fatalf("turn ended with %q; the disconnect must not have cancelled it", st.Turns[0].StopReason)
	}
}

// Invariant 5: the same commandId executes at most once.
func TestCommandIdempotency(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "cmd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	if _, done, err := st.ClaimCommand(ctx, "cmd-1", "s1"); err != nil || done {
		t.Fatalf("first claim: done=%v err=%v; want a fresh claim", done, err)
	}
	if _, _, err := st.ClaimCommand(ctx, "cmd-1", "s1"); !errors.Is(err, store.ErrCommandInProgress) {
		t.Fatalf("concurrent retry err=%v; want ErrCommandInProgress", err)
	}
	if err := st.CompleteCommand(ctx, "cmd-1", map[string]any{"turnId": "abc"}); err != nil {
		t.Fatal(err)
	}
	stored, done, err := st.ClaimCommand(ctx, "cmd-1", "s1")
	if err != nil || !done {
		t.Fatalf("retry: done=%v err=%v; want the stored result", done, err)
	}
	if string(stored) != `{"turnId":"abc"}` {
		t.Fatalf("retry returned %s; want the stored result", stored)
	}

	if _, done, err := st.ClaimCommand(ctx, "cmd-2", "s1"); err != nil || done {
		t.Fatalf("failed-command claim: done=%v err=%v", done, err)
	}
	if err := st.ReleaseCommand(ctx, "cmd-2"); err != nil {
		t.Fatal(err)
	}
	if _, done, err := st.ClaimCommand(ctx, "cmd-2", "s1"); err != nil || done {
		t.Fatalf("released retry: done=%v err=%v; want a fresh claim", done, err)
	}
}

func TestResumeFinishesInterruptedTurnAndCancelsPendingPermission(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })
	if _, err := actor.Prompt(context.Background(), "keep working", nil); err != nil {
		t.Fatal(err)
	}

	permissionDone := make(chan adapter.PermissionOutcome, 1)
	go func() {
		out, _ := fa.session().host.RequestPermission(context.Background(), adapter.PermissionRequest{Title: "approve"})
		permissionDone <- out
	}()
	waitFor(t, func() bool {
		s, _ := actor.State(context.Background())
		return s.Phase == "turn" && len(s.Pending) == 1
	})

	id := actor.ID
	mgr.Shutdown()
	if out := <-permissionDone; out.Outcome != proto.OutcomeCancelled {
		t.Fatalf("shutdown resolved permission as %q; want cancelled", out.Outcome)
	}

	mgr2 := NewManager(st, func(string, ...any) {}, fa)
	defer mgr2.Shutdown()
	resumed, err := mgr2.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, _ := resumed.State(context.Background())
		return len(s.Pending) == 0 && len(s.Turns) >= 1 && s.Turns[0].Done
	})
	state, _ := resumed.State(context.Background())
	if state.Turns[0].StopReason != proto.StopError {
		t.Fatalf("interrupted turn stop reason=%q; want error", state.Turns[0].StopReason)
	}
}

func TestResumeContinuesTheInterruptedWork(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "recover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })
	if _, err := actor.Prompt(context.Background(), "do the long thing", nil); err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	waitFor(t, func() bool {
		s, _ := actor.State(context.Background())
		return s.Phase == "turn"
	})

	id := actor.ID
	mgr.Shutdown()

	// A session killed mid-turn stays marked as such, so the next start can
	// find it without folding every log in the database.
	meta, err := st.Session(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Phase != "turn" {
		t.Fatalf("phase after mid-turn shutdown = %q; want turn", meta.Phase)
	}

	mgr2 := NewManager(st, func(string, ...any) {}, fa)
	defer mgr2.Shutdown()
	resumed, err := mgr2.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	in := <-fa.session().prompts
	if !strings.Contains(in.Text, "restarted") {
		t.Fatalf("recovery prompt = %q; want it to explain the restart", in.Text)
	}
	waitFor(t, func() bool {
		s, _ := resumed.State(context.Background())
		return len(s.Turns) == 2
	})
	state, _ := resumed.State(context.Background())
	if !state.Turns[0].Done || state.Turns[0].StopReason != proto.StopError {
		t.Fatalf("interrupted turn = %+v; want it closed with an error", state.Turns[0])
	}
	if state.Turns[1].Recovery == nil || state.Turns[1].Recovery.Attempt != 1 {
		t.Fatalf("continuation turn = %+v; want recovery attempt 1", state.Turns[1])
	}
	if state.Turns[1].Recovery.ResumeOf != state.Turns[0].ID {
		t.Fatalf("continuation resumes %q; want %q", state.Turns[1].Recovery.ResumeOf, state.Turns[0].ID)
	}
	// The session is named after what the human asked for, never after the
	// prompt the server wrote to itself.
	if state.Title != "do the long thing" {
		t.Fatalf("title = %q; want the human prompt", state.Title)
	}
}

func TestContinueRestartsWorkAfterTheAutomaticTriesRunOut(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "continue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })

	// A session whose last turn ended cleanly has nothing to continue, so the
	// button cannot start a turn out of nowhere.
	if _, err := actor.Continue(context.Background()); !errors.Is(err, ErrNothingToContinue) {
		t.Fatalf("continue on a fresh session: %v; want ErrNothingToContinue", err)
	}

	if _, err := actor.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	waitFor(t, func() bool {
		s, _ := actor.State(context.Background())
		return s.Phase == "turn"
	})
	id := actor.ID
	mgr.Shutdown()

	// Burn every automatic attempt, so the session is left for a human.
	for attempt := 1; attempt <= maxRecoveryAttempts; attempt++ {
		m := NewManager(st, func(string, ...any) {}, fa)
		a, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		<-fa.session().prompts
		waitFor(t, func() bool {
			s, _ := a.State(context.Background())
			return s.Phase == "turn"
		})
		m.Shutdown()
	}

	m := NewManager(st, func(string, ...any) {}, fa)
	defer m.Shutdown()
	a, err := m.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, _ := a.State(context.Background())
		return s.Phase == "idle"
	})
	stalled, _ := a.State(context.Background())
	last := stalled.Turns[len(stalled.Turns)-1]
	if !last.Done || last.StopReason != proto.StopError {
		t.Fatalf("last turn = %+v; want an errored turn for a human to act on", last)
	}

	// The button the UI shows against that turn.
	turnID, err := a.Continue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	in := <-fa.session().prompts
	// A human continuing a failed turn is not a restart, and must not be
	// described as one — to the agent or on the screen.
	if strings.Contains(in.Text, "restarted") {
		t.Fatalf("continue prompt claims a restart: %q", in.Text)
	}
	if !strings.Contains(in.Text, "ended in an error") {
		t.Fatalf("continue prompt = %q; want it to say the turn failed", in.Text)
	}
	waitFor(t, func() bool {
		s, _ := a.State(context.Background())
		return len(s.Turns) == len(stalled.Turns)+1
	})
	state, _ := a.State(context.Background())
	started := state.Turns[len(state.Turns)-1]
	if started.ID != turnID {
		t.Fatalf("continue reported turn %q; log has %q", turnID, started.ID)
	}
	// A human asking again is not bound by the automatic cap, and the turn
	// still records what it is continuing.
	if started.Recovery == nil || started.Recovery.Attempt != maxRecoveryAttempts+1 {
		t.Fatalf("continued turn = %+v; want recovery attempt %d", started, maxRecoveryAttempts+1)
	}
	if started.Recovery.ResumeOf != last.ID {
		t.Fatalf("continued turn resumes %q; want %q", started.Recovery.ResumeOf, last.ID)
	}
	if started.Recovery.Cause != proto.RecoveryContinue {
		t.Fatalf("continued turn cause = %q; want %q", started.Recovery.Cause, proto.RecoveryContinue)
	}
}

func TestStartupResumesInterruptedWorkWithoutAnAttach(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "startup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })
	if _, err := actor.Prompt(context.Background(), "long job", nil); err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	waitFor(t, func() bool {
		s, _ := actor.State(context.Background())
		return s.Phase == "turn"
	})
	id := actor.ID
	mgr.Shutdown()

	// Nobody attaches. The work should come back anyway.
	mgr2 := NewManager(st, func(string, ...any) {}, fa)
	defer mgr2.Shutdown()
	mgr2.recoverAll(context.Background())

	in := <-fa.session().prompts
	if !strings.Contains(in.Text, "restarted") {
		t.Fatalf("recovery prompt = %q; want it to explain the restart", in.Text)
	}
	resumed, ok := mgr2.Peek(id)
	if !ok {
		t.Fatal("session was not brought back")
	}
	waitFor(t, func() bool {
		s, _ := resumed.State(context.Background())
		return len(s.Turns) == 2 && s.Turns[1].Recovery != nil
	})
	// The screen distinguishes a restart from every other reason a turn is
	// picked back up, so the log has to.
	final, _ := resumed.State(context.Background())
	if final.Turns[1].Recovery.Cause != proto.RecoveryRestart {
		t.Fatalf("recovery cause = %q; want %q", final.Turns[1].Recovery.Cause, proto.RecoveryRestart)
	}
}

func TestRepeatedlyInterruptedTurnStopsRecoveringItself(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "recover-cap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })
	if _, err := actor.Prompt(context.Background(), "start", nil); err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts
	waitFor(t, func() bool {
		s, _ := actor.State(context.Background())
		return s.Phase == "turn"
	})
	id := actor.ID
	mgr.Shutdown()

	// Every restart dies in the middle of the turn the previous one started.
	for attempt := 1; attempt <= maxRecoveryAttempts; attempt++ {
		m := NewManager(st, func(string, ...any) {}, fa)
		a, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		<-fa.session().prompts
		waitFor(t, func() bool {
			s, _ := a.State(context.Background())
			return s.Phase == "turn"
		})
		state, _ := a.State(context.Background())
		last := state.Turns[len(state.Turns)-1]
		if last.Recovery == nil || last.Recovery.Attempt != attempt {
			t.Fatalf("restart %d started %+v; want recovery attempt %d", attempt, last, attempt)
		}
		m.Shutdown()
	}

	// The cap is reached: this resume closes the turn and leaves the session
	// alone rather than starting a fourth continuation.
	m := NewManager(st, func(string, ...any) {}, fa)
	defer m.Shutdown()
	a, err := m.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, _ := a.State(context.Background())
		return s.Phase == "idle"
	})
	before, _ := a.State(context.Background())
	time.Sleep(150 * time.Millisecond)
	after, _ := a.State(context.Background())
	if len(after.Turns) != len(before.Turns) {
		t.Fatalf("turns grew from %d to %d; want the recovery to stop at the cap", len(before.Turns), len(after.Turns))
	}
	if !after.Turns[len(after.Turns)-1].Done {
		t.Fatalf("last turn left open after the cap: %+v", after.Turns[len(after.Turns)-1])
	}
}

func TestClosedSessionRemainsAttachableWithoutHarness(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()

	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })
	id := actor.ID
	if err := mgr.Close(context.Background(), id, "test"); err != nil {
		t.Fatal(err)
	}

	view, err := mgr.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get closed transcript: %v", err)
	}
	state, err := view.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Closed || state.Phase != "closed" {
		t.Fatalf("closed transcript state: closed=%v phase=%q", state.Closed, state.Phase)
	}
	if _, err := view.Prompt(context.Background(), "must not run", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("prompt on closed transcript err=%v; want ErrClosed", err)
	}
}

func TestStoppedActorCallsReturnInsteadOfWaitingOnUnreadInbox(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	started := make(chan struct{})
	release := make(chan struct{})
	fa.session().closeStarted = started
	fa.session().closeRelease = release
	closeDone := make(chan struct{})
	go func() {
		actor.Close("test")
		close(closeDone)
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stateDone := make(chan error, 1)
	go func() {
		_, err := actor.State(ctx)
		stateDone <- err
	}()
	waitFor(t, func() bool { return len(actor.inbox) == 1 })
	close(release)
	<-closeDone
	if err := <-stateDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("state on stopped actor err=%v; want ErrClosed", err)
	}
}

func TestStoppedActorRejectsLateSubscriber(t *testing.T) {
	actor, _, _ := newTestActor(t)
	actor.Close("test")

	sub := actor.Subscribe()
	select {
	case _, ok := <-sub.Ch:
		if ok {
			t.Fatal("stopped actor delivered an event to a late subscriber")
		}
	case <-time.After(time.Second):
		t.Fatal("late subscriber was left open on a stopped actor")
	}
}

func TestGetCannotResumeWhileCloseIsInProgress(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fa := &fakeAdapter{}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	defer mgr.Shutdown()
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return actor.Head() >= 1 })

	started := make(chan struct{})
	release := make(chan struct{})
	fa.session().closeStarted = started
	fa.session().closeRelease = release
	closeDone := make(chan error, 1)
	go func() { closeDone <- mgr.Close(context.Background(), actor.ID, "test") }()
	<-started

	getDone := make(chan error, 1)
	go func() {
		view, err := mgr.Get(context.Background(), actor.ID)
		if err == nil {
			state, stateErr := view.State(context.Background())
			if stateErr != nil {
				err = stateErr
			} else if !state.Closed {
				err = errors.New("Get returned a writable actor during close")
			}
		}
		getDone <- err
	}()
	select {
	case err := <-getDone:
		t.Fatalf("Get completed before close committed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-getDone; err != nil {
		t.Fatal(err)
	}
}

// TestHarnessInitiatedTurnIsTracked covers work the harness starts by itself —
// a background task completing and waking the agent without a prompt. The
// adapter opens a turn for it; the actor must track that turn like any other:
// the phase moves to turn, prompts are refused while it runs, and the phase
// returns to idle when it finishes.
func TestHarnessInitiatedTurnIsTracked(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()

	// The harness resumes work on its own: turn.started with no prompt.
	fa.session().emit(proto.Emit(proto.TurnStarted, proto.TurnStartedPayload{TurnID: "harness-turn"}))
	fa.session().emit(proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{
		TurnID: "harness-turn", Role: "agent", Kind: "text", BlockID: "b1", Delta: "The web",
	}))

	waitFor(t, func() bool {
		state, err := actor.State(ctx)
		return err == nil && state.Phase == "turn"
	})

	// The turn is real: a prompt while it runs waits behind it rather than
	// starting a second one.
	if res, err := actor.Prompt(ctx, "hello", nil); err != nil || !res.Queued() {
		t.Fatalf("prompt during harness-initiated turn = %+v, %v; want queued", res, err)
	}

	// The store's phase follows too, so the session list agrees.
	meta, err := st.Session(ctx, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Phase != "turn" {
		t.Fatalf("stored phase = %q, want turn", meta.Phase)
	}

	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{
		TurnID: "harness-turn", StopReason: proto.StopEndTurn,
	}))
	// The harness's turn ending releases the queued prompt as a turn of its own.
	if in := <-fa.session().prompts; in.Text != "hello" {
		t.Fatalf("queued prompt after harness-initiated turn = %+v", in)
	}
	waitFor(t, func() bool {
		state, err := actor.State(ctx)
		return err == nil && state.Phase == "turn" && len(state.Turns) == 2 && state.Turns[0].Done && len(state.Queued) == 0
	})
}

// A harness that dies mid-turn used to leave the turn open in the log. The
// only other thing that leaves a turn open is a server restart, so every
// screen downstream — and the automatic recovery that follows one — described
// the death as a restart and offered to resume work that no restart had
// interrupted. The most common cause by far is an expired login, where the
// harness exits within a second of being asked to work.
func TestHarnessDeathClosesItsTurnRatherThanLookingLikeARestart(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()

	res, err := actor.Prompt(ctx, "do the thing", nil)
	turnID := res.TurnID
	if err != nil {
		t.Fatal(err)
	}
	<-fa.session().prompts

	// The harness dies without ever reporting a result: its event stream ends.
	_ = fa.session().Close()

	waitFor(t, func() bool {
		state, err := loadState(ctx, st, actor.ID)
		return err == nil && len(state.Turns) > 0 && state.Turns[len(state.Turns)-1].Done
	})

	state, err := loadState(ctx, st, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := state.Turns[len(state.Turns)-1]
	if last.ID != turnID {
		t.Fatalf("closed turn = %s, want %s", last.ID, turnID)
	}
	if last.StopReason != proto.StopError {
		t.Fatalf("stop reason = %q, want error", last.StopReason)
	}
	if strings.Contains(last.Error, "restart") {
		t.Fatalf("a harness death was described as a restart: %q", last.Error)
	}
	if last.Error == "" {
		t.Fatal("the turn was closed without saying why")
	}

	// The session row must not claim work is still in flight either; that is
	// what makes the next start treat it as an interrupted turn and resume it.
	// The row is written when the actor shuts down, a moment after the closing
	// event reaches the log, so it is waited for rather than read once.
	waitFor(t, func() bool {
		metas, err := st.ListSessions(ctx)
		if err != nil {
			return false
		}
		for _, m := range metas {
			if m.ID == actor.ID {
				return m.Phase != "turn"
			}
		}
		return false
	})
}

// An adapter that refuses a prompt because the harness needs a login has
// already worked out what kind of failure this is. Recording the wording and
// throwing the classification away leaves the turn indistinguishable from a
// crash, which is what gets offered a continue button that cannot help.
func TestARefusedPromptKeepsTheAdapterSClassification(t *testing.T) {
	actor, fa, st := newTestActor(t)
	ctx := context.Background()

	// Make the session refuse, the way the Claude bridge does once it has
	// died on a login failure.
	sess := fa.session()
	sess.mu.Lock()
	sess.refuse = &adapter.FailureError{Kind: proto.FailureAuth, Err: errors.New("claude needs you to sign in again")}
	sess.mu.Unlock()

	if _, err := actor.Prompt(ctx, "do the thing", nil); err == nil {
		t.Fatal("the refused prompt reported success")
	}

	waitFor(t, func() bool {
		state, err := loadState(ctx, st, actor.ID)
		return err == nil && len(state.Turns) > 0 && state.Turns[len(state.Turns)-1].Done
	})

	state, err := loadState(ctx, st, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := state.Turns[len(state.Turns)-1]
	if last.Failure != proto.FailureAuth {
		t.Fatalf("failure kind = %q, want %q (error %q)", last.Failure, proto.FailureAuth, last.Error)
	}
}

// TestPromptQueuesBehindRunningTurn: a prompt sent mid-turn is not refused. It
// waits in the log, shows in the projection, and starts its own turn — with
// its own prompt item — the moment the running turn ends.
func TestPromptQueuesBehindRunningTurn(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	ctx := context.Background()

	first, err := actor.Prompt(ctx, "first", nil)
	if err != nil || first.Queued() {
		t.Fatalf("first prompt = %+v, %v", first, err)
	}
	<-fa.session().prompts

	second, err := actor.Prompt(ctx, "second", []proto.PromptImage{{ID: "img", MediaType: "image/png", Path: "/tmp/x.png"}})
	if err != nil || !second.Queued() {
		t.Fatalf("second prompt = %+v, %v; want queued", second, err)
	}
	state, _ := actor.State(ctx)
	if len(state.Queued) != 1 || state.Queued[0].QueueID != second.QueueID || state.Queued[0].Prompt != "second" || len(state.Turns) != 1 {
		t.Fatalf("queued state = %+v", state.Queued)
	}

	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopEndTurn}))
	in := <-fa.session().prompts
	if in.Text != "second" || len(in.Images) != 1 || in.TurnID == "" || in.TurnID == first.TurnID {
		t.Fatalf("dispatched prompt = %+v", in)
	}
	waitFor(t, func() bool {
		next, _ := actor.State(ctx)
		return len(next.Queued) == 0 && len(next.Turns) == 2 && next.Turns[1].Prompt == "second" && next.Phase == "turn"
	})
}

// TestDequeuePromptTakesItBack: a queued prompt can be removed before it runs,
// and removing it twice is an error rather than a silent no-op.
func TestDequeuePromptTakesItBack(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "first", nil)
	<-fa.session().prompts
	queued, _ := actor.Prompt(ctx, "later", nil)
	if err := actor.DequeuePrompt(ctx, queued.QueueID); err != nil {
		t.Fatal(err)
	}
	if err := actor.DequeuePrompt(ctx, queued.QueueID); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("second dequeue err = %v, want ErrNotQueued", err)
	}
	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool {
		next, _ := actor.State(ctx)
		return next.Phase == "idle"
	})
	select {
	case in := <-fa.session().prompts:
		t.Fatalf("removed prompt still ran: %+v", in)
	default:
	}
	state, _ := actor.State(ctx)
	if len(state.Queued) != 0 || len(state.Turns) != 1 {
		t.Fatalf("state after dequeue = %+v", state)
	}
}

// TestCancelDropsQueuedPrompts: interrupting a turn must not let the next
// queued prompt start the instant the interrupt lands.
func TestCancelDropsQueuedPrompts(t *testing.T) {
	actor, fa, _ := newTestActor(t)
	ctx := context.Background()

	first, _ := actor.Prompt(ctx, "first", nil)
	<-fa.session().prompts
	if _, err := actor.Prompt(ctx, "later", nil); err != nil {
		t.Fatal(err)
	}
	if err := actor.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	fa.session().emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: first.TurnID, StopReason: proto.StopCancelled}))
	waitFor(t, func() bool {
		next, _ := actor.State(ctx)
		return next.Phase == "idle"
	})
	select {
	case in := <-fa.session().prompts:
		t.Fatalf("cancelled queue still ran: %+v", in)
	default:
	}
	if state, _ := actor.State(ctx); len(state.Queued) != 0 {
		t.Fatalf("queue survived cancel: %+v", state.Queued)
	}
}
