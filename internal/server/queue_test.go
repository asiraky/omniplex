package server

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/asiraky/omniplex/internal/adapter"
	"github.com/asiraky/omniplex/internal/attachment"
	"github.com/asiraky/omniplex/internal/auth"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/thread"
)

// TestQueuedPromptCommands: the queue commands as a client sends them. An
// edit that leaves imageIds out keeps the attachments, one that sends an
// empty list clears them, and a delivery the server does not know is refused
// rather than quietly treated as the default.
func TestQueuedPromptCommands(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	images := attachment.New(filepath.Join(dir, "images"))
	mgr := thread.NewManager(st, t.Logf, busyAdapter{})
	t.Cleanup(mgr.Shutdown)
	mgr.SetAttachments(images)
	h := New(Options{Manager: mgr, Store: st, Guard: auth.New(st, true, auth.DefaultPort), Attachments: images}).Handler()

	ctx := context.Background()
	a, err := mgr.Create(ctx, busyAdapter{}.ID(), "", dir, "test-model", "default")
	if err != nil {
		t.Fatal(err)
	}
	img, err := images.Put(a.ID, bytes.NewReader([]byte("\x89PNG\r\n\x1a\n0000")))
	if err != nil {
		t.Fatal(err)
	}
	c := dialWS(t, h)

	c.ok("prompt", map[string]any{"threadId": a.ID, "text": "first"}, nil)
	if _, e := c.do("prompt", map[string]any{"threadId": a.ID, "text": "x", "delivery": "later"}); e == "" {
		t.Fatal("an unknown delivery was accepted")
	}
	var queued struct {
		QueueID string `json:"queueId"`
	}
	c.ok("prompt", map[string]any{"threadId": a.ID, "text": "later", "imageIds": []string{img.ID}, "delivery": "queue"}, &queued)
	if queued.QueueID == "" {
		t.Fatal("a prompt sent mid-turn was not queued")
	}

	entry := func() (string, []string) {
		t.Helper()
		s, err := a.State(ctx)
		if err != nil || len(s.Queued) != 1 {
			t.Fatalf("queue = %+v, %v", s.Queued, err)
		}
		var ids []string
		for _, im := range s.Queued[0].Images {
			ids = append(ids, im.ID)
		}
		return s.Queued[0].Prompt, ids
	}

	c.ok("edit_queued_prompt", map[string]any{"threadId": a.ID, "queueId": queued.QueueID, "text": "reworded"}, nil)
	if text, ids := entry(); text != "reworded" || len(ids) != 1 || ids[0] != img.ID {
		t.Fatalf("after an edit without imageIds: %q %v, want the attachment kept", text, ids)
	}
	if _, e := c.do("edit_queued_prompt", map[string]any{"threadId": a.ID, "queueId": queued.QueueID, "text": "", "imageIds": []string{}}); e == "" {
		t.Fatal("an edit to nothing was accepted")
	}
	c.ok("edit_queued_prompt", map[string]any{"threadId": a.ID, "queueId": queued.QueueID, "text": "plain", "imageIds": []string{}}, nil)
	if text, ids := entry(); text != "plain" || len(ids) != 0 {
		t.Fatalf("after an edit with no imageIds: %q %v, want them cleared", text, ids)
	}

	if _, e := c.do("send_queued_prompt", map[string]any{"threadId": a.ID, "queueId": queued.QueueID}); e != thread.ErrCannotSteer.Error() {
		t.Fatalf("send now on a harness that cannot steer: %q", e)
	}
}

// busyAdapter starts every turn and never finishes one, so whatever is sent
// next waits in the queue.
type busyAdapter struct{}

func (busyAdapter) ID() string { return "busy-test" }
func (a busyAdapter) Meta() adapter.HarnessMeta {
	return adapter.HarnessMeta{ID: a.ID(), Name: "Busy harness"}
}
func (busyAdapter) Models() []adapter.ModelMeta {
	return []adapter.ModelMeta{{ID: "test-model", Label: "Test model", Default: true}}
}
func (a busyAdapter) ListModels(context.Context, map[string]string) ([]adapter.ModelMeta, error) {
	return a.Models(), nil
}
func (busyAdapter) PermissionModes() []adapter.PermissionModeMeta {
	return []adapter.PermissionModeMeta{{ID: "default", Label: "Default"}}
}
func (busyAdapter) Probe(context.Context, map[string]string) adapter.Availability {
	return adapter.Ready(nil)
}
func (busyAdapter) CreateSession(context.Context, adapter.HostServices, adapter.CreateOptions) (adapter.Session, error) {
	return &busySession{events: make(chan proto.Emission)}, nil
}

type busySession struct {
	events chan proto.Emission
	once   sync.Once
}

func (*busySession) Prompt(context.Context, adapter.PromptInput) error { return nil }
func (*busySession) Cancel(context.Context) error                      { return nil }
func (s *busySession) Events() <-chan proto.Emission                   { return s.events }
func (s *busySession) Close() error {
	s.once.Do(func() { close(s.events) })
	return nil
}
