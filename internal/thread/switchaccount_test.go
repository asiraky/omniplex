package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/provider"
	"github.com/asiraky/omniplex/internal/store"
)

// moverAdapter is an instAdapter that can carry a conversation between
// accounts, recording each move so a test can see what went where.
type moverAdapter struct {
	*instAdapter
	moveMu  sync.Mutex
	moves   []move
	moveErr error
}

type move struct {
	from, to  map[string]string
	cwd, conv string
}

func (f *moverAdapter) MoveConversation(from, to map[string]string, cwd, id string) error {
	f.moveMu.Lock()
	defer f.moveMu.Unlock()
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moves = append(f.moves, move{from, to, cwd, id})
	return nil
}

func (f *moverAdapter) recorded() []move {
	f.moveMu.Lock()
	defer f.moveMu.Unlock()
	return append([]move(nil), f.moves...)
}

func switchTestManager(t *testing.T) (*Manager, *moverAdapter, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &moverAdapter{instAdapter: &instAdapter{}}
	mgr := NewManager(st, func(string, ...any) {}, fa)
	mgr.ConfigureInstances([]provider.Instance{workInstance()}, nil)
	return mgr, fa, st
}

func accountNotices(state *projection.State) []string {
	var out []string
	for _, it := range state.Items {
		if it.Kind == projection.ItemNotice && it.NoticeKind == "account" {
			out = append(out, it.Title)
		}
	}
	return out
}

// The switch stops the running harness, moves the conversation, and the next
// command resumes it under the other account's credentials — on the same
// actor, so every presenter attached to the session stays attached.
func TestSwitchAccountResumesUnderTheNewAccount(t *testing.T) {
	mgr, fa, st := switchTestManager(t)
	ctx := context.Background()
	cwd := t.TempDir()
	a, err := mgr.Create(ctx, "fake", "", cwd, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	old := fa.session()

	if err := mgr.SwitchAccount(ctx, a.ID, "fake-work"); err != nil {
		t.Fatal(err)
	}

	moves := fa.recorded()
	if len(moves) != 1 {
		t.Fatalf("moves = %d, want 1", len(moves))
	}
	if moves[0].to["FAKE_HOME"] != "/work" || moves[0].from["FAKE_HOME"] != "" || moves[0].conv != a.ID || moves[0].cwd != cwd {
		t.Errorf("move = %+v", moves[0])
	}
	if _, open := <-old.events; open {
		t.Error("the old account's harness process was left running")
	}
	meta, _ := st.Session(ctx, a.ID)
	if meta.ProviderInstance != "fake-work" {
		t.Errorf("ProviderInstance = %q, want fake-work", meta.ProviderInstance)
	}

	got, err := mgr.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != a {
		t.Error("the switch replaced the actor; attached presenters would be orphaned")
	}
	if fa.session() == old {
		t.Fatal("no new harness process was started")
	}
	if env := fa.sessionEnv(); env["FAKE_HOME"] != "/work" {
		t.Errorf("resumed with env %v, want the work account's", env)
	}
	state, _ := a.State(ctx)
	if notices := accountNotices(state); len(notices) != 1 || notices[0] != "Fake Work" {
		t.Errorf("account notices = %v, want one naming Fake Work", notices)
	}
}

// A failed move leaves the session on the account it had: the next turn must
// find the conversation where that account keeps it.
func TestSwitchAccountFailedMoveKeepsTheOldAccount(t *testing.T) {
	mgr, fa, st := switchTestManager(t)
	ctx := context.Background()
	a, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	fa.moveErr = errors.New("disk full")

	if err := mgr.SwitchAccount(ctx, a.ID, "fake-work"); err == nil {
		t.Fatal("switch succeeded although the conversation could not move")
	}
	meta, _ := st.Session(ctx, a.ID)
	if meta.ProviderInstance != "" && meta.ProviderInstance != "fake" {
		t.Errorf("ProviderInstance = %q, want the default account still", meta.ProviderInstance)
	}
	if _, err := mgr.Get(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if env := fa.sessionEnv(); env["FAKE_HOME"] != "" {
		t.Errorf("resumed with env %v, want the old account's", env)
	}
	state, _ := a.State(ctx)
	if notices := accountNotices(state); len(notices) != 0 {
		t.Errorf("a failed switch left an account notice: %v", notices)
	}
}

// A running turn belongs to the process that would be replaced; switching
// under it would lose the turn.
func TestSwitchAccountRefusedMidTurn(t *testing.T) {
	mgr, fa, _ := switchTestManager(t)
	ctx := context.Background()
	a, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")
	if _, err := a.Prompt(ctx, "work", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		state, err := a.State(ctx)
		return err == nil && state.Phase == "turn"
	})

	if err := mgr.SwitchAccount(ctx, a.ID, "fake-work"); err == nil {
		t.Fatal("switched account mid-turn")
	}
	if len(fa.recorded()) != 0 {
		t.Error("the conversation was moved although the switch was refused")
	}
}

func TestSwitchAccountRefusesUnknownAccount(t *testing.T) {
	mgr, fa, _ := switchTestManager(t)
	ctx := context.Background()
	a, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")

	if err := mgr.SwitchAccount(ctx, a.ID, "nope"); err == nil {
		t.Fatal("switched to an account that does not exist")
	}
	if len(fa.recorded()) != 0 {
		t.Error("moved the conversation for a refused switch")
	}
}

// Two switches at once must chain: each moves the conversation from wherever
// the one before left it, and the session ends on the account that holds it.
func TestConcurrentSwitchesChain(t *testing.T) {
	mgr, fa, st := switchTestManager(t)
	mgr.ConfigureInstances([]provider.Instance{workInstance(), {
		ID: "fake-spare", Driver: "fake", DisplayName: "Fake Spare", Enabled: true,
		Env: []provider.EnvVar{{Name: "FAKE_HOME", Value: "/spare"}},
	}}, nil)
	ctx := context.Background()
	a, err := mgr.Create(ctx, "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Dispose("test done")

	var wg sync.WaitGroup
	for _, target := range []string{"fake-work", "fake-spare"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.SwitchAccount(ctx, a.ID, target)
		}()
	}
	wg.Wait()

	moves := fa.recorded()
	if len(moves) != 2 {
		t.Fatalf("moves = %d, want 2", len(moves))
	}
	if moves[1].from["FAKE_HOME"] != moves[0].to["FAKE_HOME"] {
		t.Errorf("second move came from %q, but the first left the conversation in %q",
			moves[1].from["FAKE_HOME"], moves[0].to["FAKE_HOME"])
	}
	meta, _ := st.Session(ctx, a.ID)
	want := map[string]string{"/work": "fake-work", "/spare": "fake-spare"}[moves[1].to["FAKE_HOME"]]
	if meta.ProviderInstance != want {
		t.Errorf("ProviderInstance = %q, but the conversation is with %q", meta.ProviderInstance, want)
	}
}
