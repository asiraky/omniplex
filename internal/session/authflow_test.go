package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/store"
)

// flowAdapter is a fakeAdapter with structured auth flows whose behaviour a
// test scripts through begin.
type flowAdapter struct {
	fakeAdapter
	methods  []adapter.AuthMethod
	statuses []adapter.AuthStatus
	begin    func(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error
	logout   func(methodID string) error
}

func (f *flowAdapter) AuthMethods(ctx context.Context, env map[string]string) ([]adapter.AuthMethod, error) {
	return f.methods, nil
}

func (f *flowAdapter) AuthStatuses(ctx context.Context, env map[string]string) ([]adapter.AuthStatus, error) {
	return f.statuses, nil
}

func (f *flowAdapter) BeginAuth(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error {
	return f.begin(ctx, env, methodID, ia)
}

func (f *flowAdapter) Logout(ctx context.Context, env map[string]string, methodID string) error {
	if f.logout != nil {
		return f.logout(methodID)
	}
	return nil
}

func flowTestManager(t *testing.T) (*Manager, *flowAdapter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &flowAdapter{}
	return NewManager(st, func(string, ...any) {}, fa), fa
}

func collect(t *testing.T, ch <-chan AuthFlowEvent) []AuthFlowEvent {
	t.Helper()
	var out []AuthFlowEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatal("flow never finished")
		}
	}
}

func TestAuthFlowNarratesAndCompletes(t *testing.T) {
	mgr, fa := flowTestManager(t)
	fa.begin = func(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error {
		ia.Notify(adapter.AuthEvent{Type: adapter.AuthEventURL, URL: "https://auth.example"})
		return nil
	}
	id, ch, err := mgr.BeginAuthFlow("fake", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	if len(evs) != 2 {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].Event == nil || evs[0].Event.URL != "https://auth.example" || evs[0].FlowID != id {
		t.Errorf("notify event = %+v", evs[0])
	}
	if !evs[1].Done || evs[1].Err != "" {
		t.Errorf("final event = %+v", evs[1])
	}
}

func TestAuthFlowPromptRoundTrip(t *testing.T) {
	mgr, fa := flowTestManager(t)
	got := make(chan string, 1)
	fa.begin = func(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error {
		v, err := ia.Prompt(ctx, adapter.AuthPrompt{Message: "API key", Secret: true})
		if err != nil {
			return err
		}
		got <- v
		return nil
	}
	id, ch, err := mgr.BeginAuthFlow("fake", "api_key")
	if err != nil {
		t.Fatal(err)
	}
	ev := <-ch
	if ev.Prompt == nil || !ev.Prompt.Secret {
		t.Fatalf("first event should be the secret prompt: %+v", ev)
	}
	if err := mgr.RespondAuthFlow(id, ev.Prompt.ID, "sk-123"); err != nil {
		t.Fatal(err)
	}
	if v := <-got; v != "sk-123" {
		t.Errorf("adapter received %q", v)
	}
	// Answering the same prompt twice must fail rather than block or panic.
	if err := mgr.RespondAuthFlow(id, ev.Prompt.ID, "again"); err == nil {
		t.Error("double answer must be refused")
	}
	collect(t, ch)
}

func TestAuthFlowFailureCarriesError(t *testing.T) {
	mgr, fa := flowTestManager(t)
	fa.begin = func(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error {
		return errors.New("provider said no")
	}
	_, ch, err := mgr.BeginAuthFlow("fake", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	last := evs[len(evs)-1]
	if !last.Done || last.Err != "provider said no" {
		t.Errorf("failure event = %+v", last)
	}
}

func TestAuthFlowCancelUnblocksPrompt(t *testing.T) {
	mgr, fa := flowTestManager(t)
	fa.begin = func(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error {
		_, err := ia.Prompt(ctx, adapter.AuthPrompt{Message: "key"})
		return err
	}
	id, ch, err := mgr.BeginAuthFlow("fake", "api_key")
	if err != nil {
		t.Fatal(err)
	}
	<-ch // the prompt
	mgr.CancelAuthFlow(id)
	evs := collect(t, ch)
	last := evs[len(evs)-1]
	if !last.Done || last.Err == "" {
		t.Errorf("cancelled flow must end with an error: %+v", last)
	}
	// The flow is gone; responding must fail legibly.
	if err := mgr.RespondAuthFlow(id, "1", "late"); err == nil {
		t.Error("responding to a dead flow must fail")
	}
}

func TestAuthFlowSuccessDropsCachedProbe(t *testing.T) {
	mgr, fa := flowTestManager(t)
	fa.begin = func(ctx context.Context, env map[string]string, methodID string, ia adapter.AuthInteraction) error {
		return nil
	}
	// Prime the probe cache.
	mgr.Harnesses(context.Background())
	mgr.probeMu.Lock()
	_, primed := mgr.probes["fake"]
	mgr.probeMu.Unlock()
	if !primed {
		t.Fatal("probe cache never primed")
	}
	_, ch, err := mgr.BeginAuthFlow("fake", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	mgr.probeMu.Lock()
	_, still := mgr.probes["fake"]
	mgr.probeMu.Unlock()
	if still {
		t.Error("a completed flow must drop the cached probe so the new credential is observed")
	}
}

func TestBeginAuthFlowRefusesTerminalMethod(t *testing.T) {
	mgr, _ := flowTestManager(t)
	if _, _, err := mgr.BeginAuthFlow("fake", TerminalAuthMethod); err == nil {
		t.Fatal("terminal method is client-side; BeginAuthFlow must refuse it")
	}
}

func TestAuthOverviewStructuredFlows(t *testing.T) {
	mgr, fa := flowTestManager(t)
	fa.methods = []adapter.AuthMethod{{ID: "openrouter:api_key", Label: "OpenRouter", Kind: adapter.AuthKindSecret}}
	fa.statuses = []adapter.AuthStatus{{MethodID: "openrouter:api_key", State: adapter.AuthConnected, Account: "a@b.c"}}
	auth, err := mgr.AuthOverview(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	if len(auth.Methods) != 1 || auth.Methods[0].ID != "openrouter:api_key" {
		t.Fatalf("methods = %+v", auth.Methods)
	}
	if len(auth.Statuses) != 1 || auth.Statuses[0].Account != "a@b.c" {
		t.Fatalf("statuses = %+v", auth.Statuses)
	}
}

// termAdapter has only the old argv-style login.
type termAdapter struct{ fakeAdapter }

func (t *termAdapter) LoginCommand(ctx context.Context) ([]string, error) {
	return []string{"fake", "login"}, nil
}

func (t *termAdapter) Probe(ctx context.Context, env map[string]string) adapter.Availability {
	return adapter.Ready(map[string]string{"account": "me@example.com", "auth": "oauth"})
}

func TestAuthOverviewSynthesisesTerminalMethod(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &termAdapter{})

	auth, err := mgr.AuthOverview(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	if len(auth.Methods) != 1 || auth.Methods[0].ID != TerminalAuthMethod || auth.Methods[0].Kind != adapter.AuthKindTerminal {
		t.Fatalf("methods = %+v", auth.Methods)
	}
	if len(auth.Statuses) != 1 || auth.Statuses[0].State != adapter.AuthConnected || auth.Statuses[0].Account != "me@example.com" {
		t.Fatalf("statuses = %+v", auth.Statuses)
	}

	// A terminal-only adapter has no structured flow to begin or revoke.
	if _, _, err := mgr.BeginAuthFlow("fake", "anything"); err == nil {
		t.Error("terminal-only adapter must refuse structured flows")
	}
	if err := mgr.LogoutInstance(context.Background(), "fake", TerminalAuthMethod); err == nil {
		t.Error("terminal-only adapter cannot disconnect from here")
	}
}

func TestLogoutInstanceDropsCaches(t *testing.T) {
	mgr, fa := flowTestManager(t)
	logged := ""
	fa.logout = func(methodID string) error { logged = methodID; return nil }
	mgr.Harnesses(context.Background()) // prime probe cache
	if err := mgr.LogoutInstance(context.Background(), "fake", "m1"); err != nil {
		t.Fatal(err)
	}
	if logged != "m1" {
		t.Errorf("logout method = %q", logged)
	}
	mgr.probeMu.Lock()
	_, still := mgr.probes["fake"]
	mgr.probeMu.Unlock()
	if still {
		t.Error("logout must drop the cached probe")
	}
}

func TestAuthSurfaceInListing(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mgr := NewManager(st, func(string, ...any) {}, &flowAdapter{})
	hs := mgr.Harnesses(context.Background())
	if got := hs[0].Instances[0].Auth; got != AuthSurfaceFlows {
		t.Errorf("flows adapter lists auth=%q", got)
	}

	st2, err := store.Open(filepath.Join(t.TempDir(), "test2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })
	mgr2 := NewManager(st2, func(string, ...any) {}, &termAdapter{})
	hs2 := mgr2.Harnesses(context.Background())
	if got := hs2[0].Instances[0].Auth; got != AuthSurfaceTerminal {
		t.Errorf("terminal adapter lists auth=%q", got)
	}
}
