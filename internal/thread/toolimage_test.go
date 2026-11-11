package thread

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asiraky/omniplex/internal/attachment"
	"github.com/asiraky/omniplex/internal/store"
)

// An image a tool returned is stored under the thread and named by a URL the
// attachment route serves; the bytes behind that URL are the ones handed in.
func TestStoreToolImageNamesTheStoredFile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	images := attachment.New(t.TempDir())
	mgr := NewManager(st, func(string, ...any) {}, &fakeAdapter{})
	mgr.SetAttachments(images)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Shutdown() })

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 6, 4))); err != nil {
		t.Fatal(err)
	}
	content, err := hostServices{a: actor}.StoreToolImage(buf.Bytes())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if content.Type != "image" || content.MediaType != "image/png" || content.Width != 6 || content.Height != 4 || content.Text != "" {
		t.Fatalf("content = %+v", content)
	}

	prefix := "/api/threads/" + actor.ID + "/attachments/"
	id, ok := strings.CutPrefix(content.URL, prefix)
	if !ok {
		t.Fatalf("url = %q, want it under %q", content.URL, prefix)
	}
	path, _, err := images.Path(actor.ID, id)
	if err != nil {
		t.Fatalf("the URL names nothing the store holds: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, buf.Bytes()) {
		t.Fatalf("stored bytes differ (err %v)", err)
	}
}

// Without a store there is nowhere to put the bytes, and saying so is what
// lets the adapter show a placeholder instead of inlining them.
func TestStoreToolImageFailsWithoutAStore(t *testing.T) {
	actor, _, _ := newTestActor(t)
	if _, err := (hostServices{a: actor}).StoreToolImage([]byte("\x89PNG\r\n\x1a\n")); err == nil {
		t.Fatal("stored an image with no store configured")
	}
}
