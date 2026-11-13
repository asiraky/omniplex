package server

import (
	"bytes"
	"testing"
)

func TestRingReadsBackWhatWasWritten(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("abc"))
	r.Write([]byte("de"))

	data, from := r.Since(0, 100)
	if string(data) != "abcde" || from != 0 {
		t.Fatalf("Since(0) = %q at %d, want \"abcde\" at 0", data, from)
	}
	data, from = r.Since(3, 100)
	if string(data) != "de" || from != 3 {
		t.Fatalf("Since(3) = %q at %d, want \"de\" at 3", data, from)
	}
	data, from = r.Since(5, 100)
	if len(data) != 0 || from != 5 {
		t.Fatalf("Since(total) = %q at %d, want nothing at 5", data, from)
	}
}

func TestRingWrapsAroundKeepingTheNewestBytes(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("012345"))
	r.Write([]byte("6789AB")) // wraps: 0-3 are overwritten

	if r.Start() != 4 || r.Total() != 12 {
		t.Fatalf("window is [%d, %d), want [4, 12)", r.Start(), r.Total())
	}
	data, from := r.Since(4, 100)
	if string(data) != "456789AB" || from != 4 {
		t.Fatalf("Since(4) = %q at %d, want \"456789AB\" at 4", data, from)
	}
	// Inside the window, across the physical end of the buffer.
	data, from = r.Since(7, 100)
	if string(data) != "789AB" || from != 7 {
		t.Fatalf("Since(7) = %q at %d, want \"789AB\" at 7", data, from)
	}
}

func TestRingAnswersAnOffsetOutsideTheWindowFromItsStart(t *testing.T) {
	r := newTermRing(4)
	r.Write([]byte("abcdefgh"))

	// Behind: those bytes are gone.
	data, from := r.Since(1, 100)
	if string(data) != "efgh" || from != 4 {
		t.Fatalf("Since(behind) = %q at %d, want \"efgh\" at 4", data, from)
	}
	// Ahead: a client that claims bytes never written (a server restart under
	// it) is resent what there is rather than nothing.
	data, from = r.Since(100, 100)
	if string(data) != "efgh" || from != 4 {
		t.Fatalf("Since(ahead) = %q at %d, want \"efgh\" at 4", data, from)
	}
}

func TestRingSinceHonoursTheLimit(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("0123456789")) // held: 2-9, wrapped

	data, from := r.Since(2, 3)
	if string(data) != "234" || from != 2 {
		t.Fatalf("Since(2, 3) = %q at %d, want \"234\" at 2", data, from)
	}
	data, from = r.Since(5, 4)
	if string(data) != "5678" || from != 5 {
		t.Fatalf("Since(5, 4) = %q at %d, want \"5678\" at 5", data, from)
	}
}

func TestRingWriteLargerThanCapacityKeepsItsTail(t *testing.T) {
	r := newTermRing(4)
	r.Write([]byte("xy"))
	r.Write([]byte("0123456789"))

	if r.Total() != 12 {
		t.Fatalf("total = %d, want 12: offsets count every byte, kept or not", r.Total())
	}
	data, from := r.Since(0, 100)
	if string(data) != "6789" || from != 8 {
		t.Fatalf("Since(0) = %q at %d, want \"6789\" at 8", data, from)
	}
}

// Many uneven writes, checked against the plain stream they should equal.
func TestRingMatchesTheStreamTailAcrossManyWrites(t *testing.T) {
	const size = 37
	r := newTermRing(size)
	var stream []byte
	var last int64
	for i := range 200 {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, i%(2*size))
		r.Write(chunk)
		stream = append(stream, chunk...)

		if r.Total() < last {
			t.Fatalf("total went backwards: %d after %d", r.Total(), last)
		}
		last = r.Total()

		start := max(0, len(stream)-size)
		data, from := r.Since(int64(start), size)
		if from != int64(start) || !bytes.Equal(data, stream[start:]) {
			t.Fatalf("after write %d: ring holds %q at %d, want %q at %d", i, data, from, stream[start:], start)
		}
	}
}

func TestRingTailStartFindsWhereTheLastLinesBegin(t *testing.T) {
	r := newTermRing(64)
	r.Write([]byte("a\nb\nc\n"))

	// The final newline ends "c", it does not start an empty fourth line.
	for lines, want := range map[int]int64{1: 4, 2: 2, 3: 0} {
		if got := r.TailStart(lines); got != want {
			t.Errorf("TailStart(%d) = %d, want %d", lines, got, want)
		}
	}
}

func TestRingTailStartWithFewerLinesThanAskedIsStart(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("xxxxa\nb\nc\n")) // held: "xxa\nb\nc\n" from 2

	if got := r.TailStart(9); got != r.Start() || got != 2 {
		t.Fatalf("TailStart(9) = %d, want Start %d", got, r.Start())
	}
	// The oldest line held is cut short, but it still counts as one.
	if got := r.TailStart(3); got != 2 {
		t.Fatalf("TailStart(3) = %d, want 2", got)
	}
}

// A progress bar redrawing with \r, or a TUI, never ends a line: the whole
// ring goes, as it would without a tail.
func TestRingTailStartWithNoNewlineIsStart(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("10%\r50%\r100%"))

	if got := r.TailStart(1); got != r.Start() {
		t.Fatalf("TailStart(1) = %d, want Start %d", got, r.Start())
	}
}

func TestRingTailStartCountsATrailingPartialLine(t *testing.T) {
	r := newTermRing(64)
	r.Write([]byte("a\nb\nc"))

	if got := r.TailStart(1); got != 4 {
		t.Fatalf("TailStart(1) = %d, want 4 (\"c\")", got)
	}
	if got := r.TailStart(2); got != 2 {
		t.Fatalf("TailStart(2) = %d, want 2 (\"b\\nc\")", got)
	}
}

func TestRingTailStartAcrossTheWrap(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("012345"))
	// Total 13, held 5-12: "5\nab\ncd\n". The newline after "ab" sits at
	// offset 9, index 1, past the physical end of the buffer.
	r.Write([]byte("\nab\ncd\n"))

	for lines, want := range map[int]int64{1: 10, 2: 7, 3: 5, 4: 5} {
		if got := r.TailStart(lines); got != want {
			t.Errorf("TailStart(%d) = %d, want %d", lines, got, want)
		}
	}
}

func TestRingTailStartOfNoLinesIsTheWholeRing(t *testing.T) {
	r := newTermRing(8)
	r.Write([]byte("a\nb\n"))
	if got := r.TailStart(0); got != r.Start() {
		t.Fatalf("TailStart(0) = %d, want Start %d", got, r.Start())
	}
}
