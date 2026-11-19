package thread

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// setSystemPrompt sets the prompt a thread outside any project runs on.
func setSystemPrompt(t *testing.T, prompt string) {
	t.Helper()
	if _, err := userconfig.Update(func(c *userconfig.Config) error {
		c.AssistantPrompt = prompt
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// restartSetup is a thread outside any project, so its system prompt is one
// the test can change, on a manager driving ad.
func restartSetup(t *testing.T, ad adapter.Adapter) (*Manager, *Actor, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(dir, "config.json"))
	setSystemPrompt(t, "codeword APPLE")
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := newManager(st, func(string, ...any) {}, ad)
	t.Cleanup(mgr.Shutdown)
	a, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return a.Head() >= 1 })
	return mgr, a, st
}

func lastOpts(fa *fakeAdapter) (adapter.CreateOptions, int) {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.opts[len(fa.opts)-1], len(fa.opts)
}

func restartNotices(s *projection.State) []projection.Item {
	var out []projection.Item
	for _, it := range s.Items {
		if it.Kind == projection.ItemNotice && it.NoticeKind == "restart" {
			out = append(out, it)
		}
	}
	return out
}

func countEvents(t *testing.T, st *store.Store, threadID, typ string) int {
	t.Helper()
	evs, err := st.ReadEvents(context.Background(), threadID, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func stateOf(t *testing.T, a *Actor) *projection.State {
	t.Helper()
	s, err := a.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A restart on an idle thread replaces the process before it returns: the
// old one is closed, the new one resumes the conversation on the system
// prompt as it is now, and the timeline says where it happened.
func TestRestartOnAnIdleThreadReplacesTheProcess(t *testing.T) {
	fa := &fakeAdapter{}
	mgr, a, _ := restartSetup(t, fa)
	ctx := context.Background()
	runTurn(t, a, fa, "hello", "r1")
	old := fa.thread()
	setSystemPrompt(t, "codeword BANANA")

	pending, err := mgr.RestartThread(ctx, a.ID)
	if err != nil || pending {
		t.Fatalf("restart = pending %v, %v; want done", pending, err)
	}
	if !old.isClosed() {
		t.Fatal("the old process was left running")
	}
	if fa.thread() == old {
		t.Fatal("no new process was started")
	}
	o, _ := lastOpts(fa)
	if !o.Resume || !o.Restart || o.Instructions != "codeword BANANA" {
		t.Fatalf("new process started with resume %v restart %v instructions %q; want a restart on the new prompt", o.Resume, o.Restart, o.Instructions)
	}
	s := stateOf(t, a)
	if notes := restartNotices(s); len(notes) != 1 || notes[0].Text != "" {
		t.Fatalf("restart notices = %+v, want one without an error", notes)
	}
	if s.RestartPending || len(s.Turns) != 1 {
		t.Fatalf("pending %v turns %d; want the transcript kept and nothing waiting", s.RestartPending, len(s.Turns))
	}

	// The next turn runs on the new process.
	if _, err := a.Prompt(ctx, "again", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case in := <-fa.thread().prompts:
		if in.Text != "again" {
			t.Fatalf("new process got %q", in.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next prompt never reached the new process")
	}
}

// A restart asked for mid-turn waits for the turn, then happens before the
// prompt queued behind the turn starts, so that prompt runs on the new
// process. Asking twice is asking once.
func TestRestartWhileBusyWaitsForTheTurnThenGoesBeforeTheQueue(t *testing.T) {
	fa := &fakeAdapter{}
	_, a, st := restartSetup(t, fa)
	ctx := context.Background()
	runTurn(t, a, fa, "first", "r1")
	res, err := a.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	<-old.prompts
	_, starts := lastOpts(fa)

	for i := 0; i < 2; i++ {
		pending, err := a.Restart(ctx)
		if err != nil || !pending {
			t.Fatalf("restart mid-turn = pending %v, %v; want pending", pending, err)
		}
	}
	if n := countEvents(t, st, a.ID, proto.ThreadRestartRequested); n != 1 {
		t.Fatalf("restart requested %d times in the log, want 1", n)
	}
	if !stateOf(t, a).RestartPending {
		t.Fatal("the thread does not say a restart is waiting")
	}
	if q, err := a.Prompt(ctx, "queued", nil); err != nil || !q.Queued() {
		t.Fatalf("prompt mid-turn = %+v, %v; want queued", q, err)
	}
	if _, n := lastOpts(fa); n != starts || old.isClosed() {
		t.Fatal("the process was replaced while its turn was still running")
	}

	old.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool { return fa.thread() != old })
	select {
	case in := <-fa.thread().prompts:
		if in.Text != "queued" {
			t.Fatalf("new process got %q, want the queued prompt", in.Text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued prompt never reached the new process")
	}
	if len(old.prompts) != 0 {
		t.Fatal("the queued prompt went to the old process")
	}
	if !old.isClosed() {
		t.Fatal("the old process was left running")
	}
	if o, _ := lastOpts(fa); !o.Resume || !o.Restart {
		t.Fatalf("new process resume %v restart %v; want both", o.Resume, o.Restart)
	}
	s := stateOf(t, a)
	if s.RestartPending || len(restartNotices(s)) != 1 {
		t.Fatalf("pending %v notices %d; want the restart done once", s.RestartPending, len(restartNotices(s)))
	}
}

// A cancelled restart does not happen when the turn ends.
func TestCancelledRestartDoesNotHappen(t *testing.T) {
	fa := &fakeAdapter{}
	mgr, a, _ := restartSetup(t, fa)
	ctx := context.Background()
	res, err := a.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	<-old.prompts
	_, starts := lastOpts(fa)
	if pending, err := mgr.RestartThread(ctx, a.ID); err != nil || !pending {
		t.Fatalf("restart mid-turn = pending %v, %v", pending, err)
	}

	if err := mgr.CancelRestart(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if stateOf(t, a).RestartPending {
		t.Fatal("still pending after the cancel")
	}
	// With nothing waiting, a cancel is not an error.
	if err := mgr.CancelRestart(ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	old.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool { return stateOf(t, a).Phase == "idle" })
	// One more command through the loop, after the one that ended the turn.
	stateOf(t, a)
	if _, n := lastOpts(fa); n != starts || old.isClosed() || fa.thread() != old {
		t.Fatal("a cancelled restart replaced the process")
	}
	if len(restartNotices(stateOf(t, a))) != 0 {
		t.Fatal("a cancelled restart left a restart notice")
	}
}

// failRestartAdapter refuses every start that is a restart, as a harness that
// cannot rewrite the prompt it pinned does.
type failRestartAdapter struct {
	*fakeAdapter
	err error
}

func (f failRestartAdapter) CreateSession(ctx context.Context, host adapter.HostServices, o adapter.CreateOptions) (adapter.Session, error) {
	if o.Restart {
		return nil, f.err
	}
	return f.fakeAdapter.CreateSession(ctx, host, o)
}

// A restart whose new process fails to start says why in the timeline and to
// whoever asked, and leaves the thread with no process: the next command
// resumes the conversation the ordinary way.
func TestFailedRestartIsLoggedAndReturned(t *testing.T) {
	fa := &fakeAdapter{}
	refusal := errors.New("no prompt snapshot in the transcript")
	mgr, a, _ := restartSetup(t, failRestartAdapter{fakeAdapter: fa, err: refusal})
	ctx := context.Background()
	runTurn(t, a, fa, "hello", "r1")
	old := fa.thread()

	if _, err := mgr.RestartThread(ctx, a.ID); !errors.Is(err, refusal) {
		t.Fatalf("restart = %v, want the start's error", err)
	}
	if !old.isClosed() {
		t.Fatal("the old process was left running")
	}
	s := stateOf(t, a)
	if notes := restartNotices(s); len(notes) != 1 || notes[0].Text != refusal.Error() {
		t.Fatalf("restart notices = %+v, want one carrying the error", notes)
	}
	if s.RestartPending {
		t.Fatal("a failed restart left one pending")
	}

	if _, err := mgr.Get(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if fa.thread() == old {
		t.Fatal("the next command started no process")
	}
	if o, _ := lastOpts(fa); !o.Resume || o.Restart {
		t.Fatalf("resumed with resume %v restart %v; want an ordinary resume", o.Resume, o.Restart)
	}
}

// A deferred restart that fails still lands in the timeline, with nobody
// waiting on a reply to hear it.
func TestFailedDeferredRestartIsLogged(t *testing.T) {
	fa := &fakeAdapter{}
	refusal := errors.New("no prompt snapshot in the transcript")
	_, a, _ := restartSetup(t, failRestartAdapter{fakeAdapter: fa, err: refusal})
	ctx := context.Background()
	runTurn(t, a, fa, "hello", "r1")
	res, err := a.Prompt(ctx, "work", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := fa.thread()
	<-old.prompts
	if pending, err := a.Restart(ctx); err != nil || !pending {
		t.Fatalf("restart mid-turn = pending %v, %v", pending, err)
	}

	old.emit(proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: res.TurnID, StopReason: proto.StopEndTurn}))
	waitFor(t, func() bool { return len(restartNotices(stateOf(t, a))) == 1 })
	s := stateOf(t, a)
	if notes := restartNotices(s); notes[0].Text != refusal.Error() || s.RestartPending {
		t.Fatalf("notice %+v pending %v; want the error and nothing waiting", notes[0], s.RestartPending)
	}
}

// A closed thread is a transcript: there is no process to restart, and the
// log is not told one was asked for.
func TestRestartRefusedOnAClosedThread(t *testing.T) {
	fa := &fakeAdapter{}
	mgr, a, st := restartSetup(t, fa)
	ctx := context.Background()
	if err := mgr.Close(ctx, a.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RestartThread(ctx, a.ID); !errors.Is(err, ErrClosed) {
		t.Fatalf("restart of a closed thread = %v, want ErrClosed", err)
	}
	if n := countEvents(t, st, a.ID, proto.ThreadRestartRequested) + countEvents(t, st, a.ID, proto.ThreadRestarted); n != 0 {
		t.Fatalf("a refused restart wrote %d events", n)
	}
}

// A private chat's conversation is only in its process; a restart would
// start it over, so it is refused.
func TestRestartRefusedOnAPrivateChat(t *testing.T) {
	mgr, st, fa, _ := assistantSetup(t)
	ctx := context.Background()
	a, err := mgr.CreateAssistant(ctx, AssistantOptions{Private: true})
	if err != nil {
		t.Fatal(err)
	}
	ready(t, st, a.ID)
	_, starts := lastOpts(fa)
	if _, err := mgr.RestartThread(ctx, a.ID); !errors.Is(err, errRestartPrivate) {
		t.Fatalf("restart of a private chat = %v, want refused", err)
	}
	if _, n := lastOpts(fa); n != starts {
		t.Fatal("a refused restart started a process")
	}
}

// A thread that never ran a turn has nothing pinned, so its new process is
// an ordinary resume.
func TestRestartOfAThreadThatNeverRan(t *testing.T) {
	fa := &fakeAdapter{}
	mgr, a, _ := restartSetup(t, fa)
	old := fa.thread()
	if pending, err := mgr.RestartThread(context.Background(), a.ID); err != nil || pending {
		t.Fatalf("restart = pending %v, %v", pending, err)
	}
	if fa.thread() == old {
		t.Fatal("no new process was started")
	}
	if o, _ := lastOpts(fa); !o.Resume || o.Restart {
		t.Fatalf("resume %v restart %v; want a resume that is not marked a restart", o.Resume, o.Restart)
	}
}

// A rewind on its way to the harness keeps the process busy: a restart asked
// for meanwhile waits for it, then happens.
func TestRestartWaitsForARewind(t *testing.T) {
	g := make(chan struct{})
	fa := &fakeAdapter{rewind: true, rewindGate: g}
	_, a, _ := restartSetup(t, fa)
	ctx := context.Background()
	first := runTurn(t, a, fa, "one", "r1")
	runTurn(t, a, fa, "two", "r2")
	old := fa.thread()

	rewound := make(chan error, 1)
	go func() { rewound <- a.Rewind(ctx, first, false) }()
	waitFor(t, func() bool { return len(old.rewindTargets()) == 1 })
	pending, err := a.Restart(ctx)
	if err != nil || !pending {
		t.Fatalf("restart mid-rewind = pending %v, %v; want pending", pending, err)
	}
	if old.isClosed() {
		t.Fatal("the process was replaced under the rewind")
	}

	close(g)
	if err := <-rewound; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fa.thread() != old })
	if !old.isClosed() {
		t.Fatal("the old process was left running")
	}
	if s := stateOf(t, a); s.RestartPending || len(restartNotices(s)) != 1 {
		t.Fatalf("pending %v notices %d; want the restart done", s.RestartPending, len(restartNotices(s)))
	}
}

// A thread whose process has gone, restored only to be looked at, starts its
// new process on the restart itself.
func TestRestartOfAThreadWithNoProcess(t *testing.T) {
	fa := &fakeAdapter{}
	mgr, a, _ := restartSetup(t, fa)
	ctx := context.Background()
	runTurn(t, a, fa, "hello", "r1")
	old := fa.thread()
	a.Dispose("test")
	_, starts := lastOpts(fa)

	pending, err := mgr.RestartThread(ctx, a.ID)
	if err != nil || pending {
		t.Fatalf("restart = pending %v, %v; want done", pending, err)
	}
	if _, n := lastOpts(fa); n != starts+1 || fa.thread() == old {
		t.Fatal("the restart started no process")
	}
	if o, _ := lastOpts(fa); !o.Resume || !o.Restart {
		t.Fatalf("resume %v restart %v; want both", o.Resume, o.Restart)
	}
	b, ok := mgr.Peek(a.ID)
	if !ok {
		t.Fatal("no live actor after the restart")
	}
	if s := stateOf(t, b); len(restartNotices(s)) != 1 || len(s.Turns) != 1 {
		t.Fatalf("notices %d turns %d; want the note on the kept transcript", len(restartNotices(s)), len(s.Turns))
	}
}
