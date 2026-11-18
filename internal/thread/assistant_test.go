package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/asiraky/omniplex/internal/ephemeral"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// assistantSetup is a manager whose config puts projects in a temp folder and
// gives assistant chats their own prompt, and whose private scratch space is
// this test's.
func assistantSetup(t *testing.T) (*Manager, *store.Store, *fakeAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OMNIPLEX_CONFIG", filepath.Join(dir, "config.json"))
	projects := filepath.Join(dir, "projects")
	if _, err := userconfig.Update(func(c *userconfig.Config) error {
		c.ProjectsDir, c.AssistantPrompt, c.DefaultInstance = projects, "Answer like a pirate.", "fake"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scratch, err := ephemeral.Init(filepath.Join(dir, "omniplex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(scratch) })
	st, err := store.Open(filepath.Join(dir, "omniplex.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fa := &fakeAdapter{}
	mgr := newManager(st, func(string, ...any) {}, fa)
	t.Cleanup(mgr.Shutdown)
	return mgr, st, fa, projects
}

// An assistant chat works in the projects folder's assistant folder, on the
// user's assistant prompt instead of Omniplex's, and keeps its transcript.
func TestAssistantChatRunsOnTheAssistantPrompt(t *testing.T) {
	mgr, st, fa, projects := assistantSetup(t)
	a, err := mgr.CreateAssistant(context.Background(), AssistantOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := ready(t, st, a.ID)
	if m.Private || m.ProjectID != "" {
		t.Fatalf("meta = %+v, want a plain chat with no project", m)
	}
	if want := filepath.Join(projects, "assistant"); m.Cwd != want {
		t.Fatalf("cwd = %s, want %s", m.Cwd, want)
	}
	fa.mu.Lock()
	o := fa.opts[len(fa.opts)-1]
	fa.mu.Unlock()
	if o.Instructions != "Answer like a pirate." || o.Ephemeral {
		t.Fatalf("instructions %q ephemeral %v; want the assistant prompt, kept", o.Instructions, o.Ephemeral)
	}
}

// A private chat's harness is told to keep nothing, it works in a scratch
// folder, and closing it is the end of it: row and folder both gone.
func TestClosingAPrivateChatForgetsIt(t *testing.T) {
	mgr, st, fa, _ := assistantSetup(t)
	ctx := context.Background()
	a, err := mgr.CreateAssistant(ctx, AssistantOptions{Private: true})
	if err != nil {
		t.Fatal(err)
	}
	m := ready(t, st, a.ID)
	if !m.Private {
		t.Fatalf("meta = %+v, want private", m)
	}
	if info, err := os.Stat(m.Cwd); err != nil || !info.IsDir() {
		t.Fatalf("scratch folder %s: %v", m.Cwd, err)
	}
	fa.mu.Lock()
	o := fa.opts[len(fa.opts)-1]
	fa.mu.Unlock()
	if !o.Ephemeral {
		t.Fatal("a private chat's harness must be asked to keep nothing")
	}

	if err := mgr.Cleanup(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Thread(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after close: %v, want not found", err)
	}
	if _, err := os.Stat(m.Cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch folder after close: %v, want gone", err)
	}
}
