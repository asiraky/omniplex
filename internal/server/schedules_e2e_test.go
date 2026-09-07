package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/attachment"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/store"
)

// Opt-in real-browser test: npm run test:e2e:schedules. Only the provider is
// deterministic; commands, persistence, scheduling, reconnect and UI are real.
func TestSchedulesBrowser(t *testing.T) {
	if os.Getenv("OMNIPLEX_BROWSER_TEST") != "1" {
		t.Skip("run npm run test:e2e:schedules")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fa := &scheduleBrowserAdapter{}
	images := attachment.New(filepath.Join(dir, "images"))
	var mgr *session.Manager
	start := func() http.Handler {
		mgr = session.NewManager(st, t.Logf, fa)
		mgr.SetAttachments(images)
		mgr.StartScheduler()
		return New(Options{Manager: mgr, Store: st, Guard: auth.New(st, false), DefaultCwd: dir, WebFS: os.DirFS(filepath.Join(root, "cmd/omniplex/webdist")), Attachments: images}).Handler()
	}
	handler := start()
	defer func() { mgr.Shutdown() }()
	a, err := mgr.Create(context.Background(), fa.ID(), "", dir, "test-model", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTitle(context.Background(), a.ID, "Scheduled send E2E"); err != nil {
		t.Fatal(err)
	}
	var mu sync.RWMutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__test/restart" && r.Method == "POST" {
			mu.Lock()
			defer mu.Unlock()
			mgr.Shutdown()
			handler = start()
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__test/deliveries" {
			fa.mu.Lock()
			defer fa.mu.Unlock()
			_ = json.NewEncoder(w).Encode(fa.deliveries)
			return
		}
		mu.RLock()
		h := handler
		mu.RUnlock()
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()
	cmd := exec.Command("node", "scripts/test-scheduled-prompts.mjs", ts.URL, a.ID)
	cmd.Dir = root
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}

type scheduleBrowserAdapter struct {
	mu         sync.Mutex
	deliveries []string
}

func (*scheduleBrowserAdapter) ID() string { return "schedule-test" }
func (a *scheduleBrowserAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: a.ID(), Name: "Test harness"}
}
func (*scheduleBrowserAdapter) Models() []adapter.ModelMeta {
	return []adapter.ModelMeta{{ID: "test-model", Label: "Test model", Default: true}}
}
func (a *scheduleBrowserAdapter) ListModels(context.Context, map[string]string) ([]adapter.ModelMeta, error) {
	return a.Models(), nil
}
func (*scheduleBrowserAdapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{{ID: "default", Label: "Default"}}
}
func (*scheduleBrowserAdapter) Probe(context.Context, map[string]string) adapter.Availability {
	return adapter.Ready(nil)
}
func (a *scheduleBrowserAdapter) CreateSession(context.Context, adapter.HostServices, adapter.CreateOptions) (adapter.Session, error) {
	return &scheduleBrowserSession{owner: a, events: make(chan proto.Emission, 32)}, nil
}

type scheduleBrowserSession struct {
	owner  *scheduleBrowserAdapter
	events chan proto.Emission
	once   sync.Once
}

func (s *scheduleBrowserSession) Prompt(_ context.Context, p adapter.PromptInput) error {
	s.owner.mu.Lock()
	s.owner.deliveries = append(s.owner.deliveries, p.Text)
	s.owner.mu.Unlock()
	if p.Text == "FAIL scheduled delivery" {
		return errors.New("test provider is out of tokens")
	}
	s.events <- proto.Emit(proto.MessageChunk, proto.MessageChunkPayload{TurnID: p.TurnID, Role: "agent", Kind: "text", BlockID: p.TurnID, Delta: "Completed: " + p.Text})
	s.events <- proto.Emit(proto.TurnFinished, proto.TurnFinishedPayload{TurnID: p.TurnID, StopReason: proto.StopEndTurn})
	return nil
}
func (*scheduleBrowserSession) Cancel(context.Context) error    { return nil }
func (s *scheduleBrowserSession) Events() <-chan proto.Emission { return s.events }
func (s *scheduleBrowserSession) Close() error                  { s.once.Do(func() { close(s.events) }); return nil }
