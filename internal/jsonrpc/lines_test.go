package jsonrpc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type skipped struct {
	head string
	size int
}

func readAll(t *testing.T, r io.Reader, max int) ([]string, []skipped, error) {
	t.Helper()
	var lines []string
	var skips []skipped
	err := ReadLines(r, max, func(line []byte) {
		lines = append(lines, string(line))
	}, func(head []byte, size int) {
		skips = append(skips, skipped{string(head), size})
	})
	return lines, skips, err
}

// One oversized record must cost only itself: the lines around it are still
// delivered, and skip hears how big it was and how it began, so the log can
// say what was dropped.
func TestReadLinesSkipsOversizedRecordAndCarriesOn(t *testing.T) {
	const max = 128 << 10 // two reader buffers
	cases := []struct {
		name string
		size int
	}{
		// Overflows in the reader's buffer fills, long before its newline.
		{"spans many buffer fills", 1 << 20},
		// Fits through the fills; only the final chunk takes it over max.
		{"just over max", max + 1},
		{"over max in one fill", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lim := max
			if tc.size < max {
				lim = 100
			}
			big := "START" + strings.Repeat("x", tc.size-len("START"))
			in := "before\n" + big + "\nafter\n"
			lines, skips, err := readAll(t, strings.NewReader(in), lim)
			if err != nil {
				t.Fatalf("ReadLines: %v", err)
			}
			if len(lines) != 2 || lines[0] != "before" || lines[1] != "after" {
				t.Fatalf("lines = %q, want [before after]", lines)
			}
			if len(skips) != 1 {
				t.Fatalf("skips = %d, want 1", len(skips))
			}
			if skips[0].size != tc.size {
				t.Errorf("skip size = %d, want %d", skips[0].size, tc.size)
			}
			if !strings.HasPrefix(skips[0].head, "START") || len(skips[0].head) > headSize {
				t.Errorf("skip head = %d bytes starting %q", len(skips[0].head), skips[0].head[:min(10, len(skips[0].head))])
			}
		})
	}
}

// The limit is inclusive: a record of exactly max bytes, assembled across
// buffer fills, is delivered whole.
func TestReadLinesDeliversRecordAtMax(t *testing.T) {
	const max = 128 << 10
	rec := strings.Repeat("y", max)
	lines, skips, err := readAll(t, strings.NewReader(rec+"\n"+"next\n"), max)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("a record at max was skipped: %+v", skips[0].size)
	}
	if len(lines) != 2 || lines[0] != rec || lines[1] != "next" {
		t.Fatalf("got %d lines (first %d bytes)", len(lines), len(lines[0]))
	}
}

// Line endings are framing, not content; blank lines are not records; and a
// stream that ends without a final newline still delivers its last record.
func TestReadLinesFraming(t *testing.T) {
	lines, skips, err := readAll(t, strings.NewReader("a\r\n\n\r\nb\nc"), 100)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if strings.Join(lines, "|") != "a|b|c" {
		t.Fatalf("lines = %q, want [a b c]", lines)
	}
}

// A broken stream is not a clean end: the caller has to be able to tell the
// harness why it stopped hearing from it.
func TestReadLinesReturnsReadError(t *testing.T) {
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader("ok\n"), errReader{boom})
	lines, _, err := readAll(t, r, 100)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(lines) != 1 || lines[0] != "ok" {
		t.Fatalf("lines before the error = %q", lines)
	}

	if _, _, err := readAll(t, bytes.NewReader([]byte("x\n")), 100); err != nil {
		t.Fatalf("clean EOF: err = %v, want nil", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
