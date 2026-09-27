package attachment

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestPutAndPathRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	want := pngBytes(t, 4)

	meta, err := s.Put("11111111-1111-1111-1111-111111111111", bytes.NewReader(want))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if meta.MediaType != "image/png" {
		t.Fatalf("media type = %q, want image/png", meta.MediaType)
	}
	if meta.Size != int64(len(want)) {
		t.Fatalf("size = %d, want %d", meta.Size, len(want))
	}

	path, mediaType, err := s.Path("11111111-1111-1111-1111-111111111111", meta.ID)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if mediaType != "image/png" {
		t.Fatalf("media type = %q", mediaType)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("stored bytes differ from what was uploaded")
	}
}

// The declared type decides nothing: what is stored is what the bytes are.
func TestPutRefusesNonImages(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Put("session-1", strings.NewReader("#!/bin/sh\nrm -rf /\n")); err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func pdfBytes(size int) []byte {
	head := []byte("%PDF-1.7\n")
	return append(head, bytes.Repeat([]byte{' '}, size-len(head))...)
}

func TestPutStoresAPDF(t *testing.T) {
	s := New(t.TempDir())
	meta, err := s.Put("session-1", bytes.NewReader(pdfBytes(64)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if meta.MediaType != PDF {
		t.Fatalf("media type = %q, want %s", meta.MediaType, PDF)
	}
	path, mediaType, err := s.Path("session-1", meta.ID)
	if err != nil || mediaType != PDF || filepath.Ext(path) != ".pdf" {
		t.Fatalf("Path = %q, %q, %v", path, mediaType, err)
	}
}

// Each kind is held to its own limit: a PDF may be bigger than any image, and
// an image is not let through at a PDF's size.
func TestPutEnforcesTheLimitForWhatWasSent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    []byte
		refused bool
	}{
		{"image at its limit", append(pngBytes(t, 4), make([]byte, MaxImageBytes-len(pngBytes(t, 4)))...), false},
		{"image past its limit", append(pngBytes(t, 4), make([]byte, MaxImageBytes+1-len(pngBytes(t, 4)))...), true},
		{"PDF past the image limit", pdfBytes(MaxImageBytes + 1), false},
		{"PDF at its limit", pdfBytes(MaxPDFBytes), false},
		{"PDF past its limit", pdfBytes(MaxPDFBytes + 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := New(dir)
			_, err := s.Put("session-1", bytes.NewReader(tc.body))
			if tc.refused != errors.Is(err, ErrTooLarge) {
				t.Fatalf("err = %v, refused want %v", err, tc.refused)
			}
			if !tc.refused && err != nil {
				t.Fatalf("put: %v", err)
			}
			entries, _ := os.ReadDir(filepath.Join(dir, "session-1"))
			if tc.refused && len(entries) != 0 {
				t.Fatalf("a refused upload left %d file(s) behind", len(entries))
			}
		})
	}
}

// The ids in a request are the only thing standing between a caller and the
// rest of the filesystem, so they are matched, never cleaned.
func TestPathRefusesTraversal(t *testing.T) {
	s := New(t.TempDir())
	for _, tc := range []struct{ session, id string }{
		{"../../etc", "passwd"},
		{"session-1", "../../../etc/passwd"},
		{"session-1", "..%2Fescape"},
		{"", "id"},
	} {
		if _, _, err := s.Path(tc.session, tc.id); err == nil {
			t.Fatalf("Path(%q, %q) was allowed", tc.session, tc.id)
		}
	}
}

func TestResolveFailsOnAMissingImage(t *testing.T) {
	s := New(t.TempDir())
	meta, err := s.Put("session-1", bytes.NewReader(pngBytes(t, 2)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, _, err := s.Resolve("session-1", []string{meta.ID}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A prompt that quietly lost one of its pictures is worse than one that
	// refuses to go.
	if _, _, err := s.Resolve("session-1", []string{meta.ID, "22222222-2222-2222-2222-222222222222"}); err == nil {
		t.Fatal("resolving an unknown id was allowed")
	}
}

// Every file passing its own limit says nothing about all of them together.
func TestResolveRefusesAPromptTooLargeInTotal(t *testing.T) {
	s := New(t.TempDir())
	put := func() string {
		meta, err := s.Put("session-1", bytes.NewReader(pdfBytes(MaxPDFBytes)))
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		return meta.ID
	}
	a, b, c := put(), put(), put()
	if _, _, err := s.Resolve("session-1", []string{a, b}); err != nil {
		t.Fatalf("two PDFs at exactly the total were refused: %v", err)
	}
	if _, _, err := s.Resolve("session-1", []string{a, b, c}); !errors.Is(err, ErrPromptTooLarge) {
		t.Fatalf("err = %v, want ErrPromptTooLarge", err)
	}
}

func TestPurgeSessionRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	meta, err := s.Put("session-1", bytes.NewReader(pngBytes(t, 2)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.PurgeSession("session-1"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, _, err := s.Path("session-1", meta.ID); err == nil {
		t.Fatal("image survived the purge")
	}
	if _, err := os.Stat(filepath.Join(dir, "session-1")); !os.IsNotExist(err) {
		t.Fatalf("session directory survived the purge: %v", err)
	}
}
