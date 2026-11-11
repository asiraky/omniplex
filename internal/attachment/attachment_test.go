package attachment

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
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
	if _, err := s.Put("thread-1", strings.NewReader("#!/bin/sh\nrm -rf /\n")); err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func pdfBytes(size int) []byte {
	head := []byte("%PDF-1.7\n")
	return append(head, bytes.Repeat([]byte{' '}, size-len(head))...)
}

func TestPutStoresAPDF(t *testing.T) {
	s := New(t.TempDir())
	meta, err := s.Put("thread-1", bytes.NewReader(pdfBytes(64)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if meta.MediaType != PDF {
		t.Fatalf("media type = %q, want %s", meta.MediaType, PDF)
	}
	path, mediaType, err := s.Path("thread-1", meta.ID)
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
			_, err := s.Put("thread-1", bytes.NewReader(tc.body))
			if tc.refused != errors.Is(err, ErrTooLarge) {
				t.Fatalf("err = %v, refused want %v", err, tc.refused)
			}
			if !tc.refused && err != nil {
				t.Fatalf("put: %v", err)
			}
			entries, _ := os.ReadDir(filepath.Join(dir, "thread-1"))
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
	for _, tc := range []struct{ thread, id string }{
		{"../../etc", "passwd"},
		{"thread-1", "../../../etc/passwd"},
		{"thread-1", "..%2Fescape"},
		{"", "id"},
	} {
		if _, _, err := s.Path(tc.thread, tc.id); err == nil {
			t.Fatalf("Path(%q, %q) was allowed", tc.thread, tc.id)
		}
	}
}

func TestResolveFailsOnAMissingImage(t *testing.T) {
	s := New(t.TempDir())
	meta, err := s.Put("thread-1", bytes.NewReader(pngBytes(t, 2)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, _, err := s.Resolve("thread-1", []string{meta.ID}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// A prompt that quietly lost one of its pictures is worse than one that
	// refuses to go.
	if _, _, err := s.Resolve("thread-1", []string{meta.ID, "22222222-2222-2222-2222-222222222222"}); err == nil {
		t.Fatal("resolving an unknown id was allowed")
	}
}

// Every file passing its own limit says nothing about all of them together.
func TestResolveRefusesAPromptTooLargeInTotal(t *testing.T) {
	s := New(t.TempDir())
	put := func() string {
		meta, err := s.Put("thread-1", bytes.NewReader(pdfBytes(MaxPDFBytes)))
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		return meta.ID
	}
	a, b, c := put(), put(), put()
	if _, _, err := s.Resolve("thread-1", []string{a, b}); err != nil {
		t.Fatalf("two PDFs at exactly the total were refused: %v", err)
	}
	if _, _, err := s.Resolve("thread-1", []string{a, b, c}); !errors.Is(err, ErrPromptTooLarge) {
		t.Fatalf("err = %v, want ErrPromptTooLarge", err)
	}
}

func TestPurgeThreadRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	meta, err := s.Put("thread-1", bytes.NewReader(pngBytes(t, 2)))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.PurgeThread("thread-1"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, _, err := s.Path("thread-1", meta.ID); err == nil {
		t.Fatal("image survived the purge")
	}
	if _, err := os.Stat(filepath.Join(dir, "thread-1")); !os.IsNotExist(err) {
		t.Fatalf("thread directory survived the purge: %v", err)
	}
}

func encoded(t *testing.T, encode func(*bytes.Buffer, image.Image) error, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// A 1x1 lossless WebP: the standard library has no WebP encoder to make one.
const webp1x1 = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func TestPutToolImageRecordsPixelSize(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString(webp1x1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mediaType string
		data            []byte
		w, h            int
	}{
		{"png", "image/png", encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, 7, 3), 7, 3},
		{"jpeg", "image/jpeg", encoded(t, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }, 5, 9), 5, 9},
		{"gif", "image/gif", encoded(t, func(b *bytes.Buffer, i image.Image) error { return gif.Encode(b, i, nil) }, 11, 2), 11, 2},
		{"webp", "image/webp", webp, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(t.TempDir())
			meta, err := s.PutToolImage("thread-1", tc.data)
			if err != nil {
				t.Fatalf("put: %v", err)
			}
			if meta.MediaType != tc.mediaType || meta.Width != tc.w || meta.Height != tc.h || meta.Size != int64(len(tc.data)) {
				t.Fatalf("meta = %+v, want %s %dx%d", meta, tc.mediaType, tc.w, tc.h)
			}
			path, mediaType, err := s.Path("thread-1", meta.ID)
			if err != nil || mediaType != tc.mediaType {
				t.Fatalf("Path = %q, %q, %v", path, mediaType, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, tc.data) {
				t.Fatalf("stored bytes differ (err %v)", err)
			}
		})
	}
}

func TestPutToolImageRefusesWhatIsNotAPicture(t *testing.T) {
	pic := pngBytes(t, 2)
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"pdf", pdfBytes(64), ErrUnsupported},
		{"script", []byte("#!/bin/sh\nrm -rf /\n"), ErrUnsupported},
		{"empty", nil, ErrUnsupported},
		// The magic says PNG; the rest is not one.
		{"truncated png", pic[:12], ErrCorrupt},
		{"oversize", append(pic, make([]byte, MaxToolImageBytes+1-len(pic))...), ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := New(dir)
			if _, err := s.PutToolImage("thread-1", tc.data); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, "thread-1")); len(entries) != 0 {
				t.Fatalf("a refused image left %d file(s) behind", len(entries))
			}
		})
	}
}

func TestPutToolImageAcceptsOneAtItsLimit(t *testing.T) {
	pic := pngBytes(t, 2)
	data := append(pic, make([]byte, MaxToolImageBytes-len(pic))...)
	if _, err := New(t.TempDir()).PutToolImage("thread-1", data); err != nil {
		t.Fatalf("put: %v", err)
	}
}

// A tool image bigger than a thumbnail gets one: a JPEG stored beside it,
// shrunk to the bound on its long edge with the shape kept, and the original
// untouched behind its own id.
func TestPutToolImageStoresAThumbnailOfALargeImage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		w, h         int
		wantW, wantH int
	}{
		{"landscape", 2560, 1600, ThumbBound, 300},
		{"portrait", 600, 1200, 240, ThumbBound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Transparent, so the thumbnail has to lay it on something.
			data := encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, tc.w, tc.h)
			s := New(t.TempDir())
			meta, err := s.PutToolImage("thread-1", data)
			if err != nil {
				t.Fatalf("put: %v", err)
			}
			if meta.Width != tc.w || meta.Height != tc.h {
				t.Fatalf("meta = %+v, want the original's size", meta)
			}
			if meta.ThumbID == "" || meta.ThumbID == meta.ID {
				t.Fatalf("thumb id = %q (original %q)", meta.ThumbID, meta.ID)
			}
			path, mediaType, err := s.Path("thread-1", meta.ThumbID)
			if err != nil || mediaType != "image/jpeg" {
				t.Fatalf("thumb Path = %q, %q, %v", path, mediaType, err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			thumb, err := jpeg.Decode(f)
			if err != nil {
				t.Fatalf("thumb is not a JPEG: %v", err)
			}
			if b := thumb.Bounds(); b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Fatalf("thumb is %dx%d, want %dx%d", b.Dx(), b.Dy(), tc.wantW, tc.wantH)
			}
			if r, g, b, _ := thumb.At(tc.wantW/2, tc.wantH/2).RGBA(); r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
				t.Fatalf("transparent pixels came out (%d,%d,%d), want white", r>>8, g>>8, b>>8)
			}
			orig, _, _ := s.Path("thread-1", meta.ID)
			if got, err := os.ReadFile(orig); err != nil || !bytes.Equal(got, data) {
				t.Fatalf("original bytes differ (err %v)", err)
			}
		})
	}
}

// An image already within the bound is its own thumbnail.
func TestPutToolImageMakesNoThumbnailOfASmallImage(t *testing.T) {
	dir := t.TempDir()
	data := encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, ThumbBound, ThumbBound-1)
	meta, err := New(dir).PutToolImage("thread-1", data)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if meta.ThumbID != "" {
		t.Fatalf("thumb id = %q, want none", meta.ThumbID)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "thread-1")); len(entries) != 1 {
		t.Fatalf("stored %d files, want only the original", len(entries))
	}
}

// A picture whose header decodes but whose pixels do not is still stored:
// losing the thumbnail is not losing the image.
func TestPutToolImageKeepsAnImageThatWillNotShrink(t *testing.T) {
	data := encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, 1000, 800)
	data = data[:len(data)/2]
	meta, err := New(t.TempDir()).PutToolImage("thread-1", data)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if meta.ThumbID != "" || meta.Width != 1000 {
		t.Fatalf("meta = %+v, want the original stored without a thumbnail", meta)
	}
}
