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
	actor, images := toolImageActor(t)
	data := pngOf(t, 6, 4)
	content, err := hostServices{a: actor}.StoreToolImage(data)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if content.Type != "image" || content.MediaType != "image/png" || content.Width != 6 || content.Height != 4 || content.Text != "" {
		t.Fatalf("content = %+v", content)
	}
	// Small enough to be its own thumbnail.
	if content.ThumbURL != "" {
		t.Fatalf("thumbUrl = %q, want none", content.ThumbURL)
	}

	path, _ := servedPath(t, images, actor.ID, content.URL)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
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

// A screenshot bigger than a thumbnail is named twice: the original for the
// viewer, and a small JPEG the attachment route also serves for the strip.
func TestStoreToolImageNamesAThumbnailOfALargeImage(t *testing.T) {
	actor, images := toolImageActor(t)
	content, err := hostServices{a: actor}.StoreToolImage(pngOf(t, 1600, 1000))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if content.Width != 1600 || content.Height != 1000 {
		t.Fatalf("content = %+v, want the original's size", content)
	}
	if _, mediaType := servedPath(t, images, actor.ID, content.URL); mediaType != "image/png" {
		t.Fatalf("url serves %s, want the original png", mediaType)
	}
	if _, mediaType := servedPath(t, images, actor.ID, content.ThumbURL); mediaType != "image/jpeg" {
		t.Fatalf("thumbUrl serves %s, want a jpeg", mediaType)
	}
}

func toolImageActor(t *testing.T) (*Actor, *attachment.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	images := attachment.New(t.TempDir())
	mgr := newManager(st, func(string, ...any) {}, &fakeAdapter{})
	mgr.SetAttachments(images)
	actor, err := mgr.Create(context.Background(), "fake", "", t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Shutdown() })
	return actor, images
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// servedPath resolves a URL the host handed out to the stored file behind it.
func servedPath(t *testing.T, images *attachment.Store, threadID, u string) (path, mediaType string) {
	t.Helper()
	prefix := "/api/threads/" + threadID + "/attachments/"
	id, ok := strings.CutPrefix(u, prefix)
	if !ok {
		t.Fatalf("url = %q, want it under %q", u, prefix)
	}
	path, mediaType, err := images.Path(threadID, id)
	if err != nil {
		t.Fatalf("%s names nothing the store holds: %v", u, err)
	}
	return path, mediaType
}
