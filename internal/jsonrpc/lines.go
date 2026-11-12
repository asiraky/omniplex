package jsonrpc

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// headSize is how much of an oversized record ReadLines keeps to describe it.
const headSize = 4 << 10

// ReadLines reads LF-delimited records from r and hands each non-empty one to
// fn, without its line ending. The slice is only valid until fn returns.
//
// A record longer than max is not a reason to stop reading: it is discarded
// through its newline and reported to skip with its first few KB and its full
// size, and the next record is read as usual. A bufio.Scanner dies instead,
// and with it the harness session, over one line nobody needed — a pi
// agent_end that repeats every screenshot of a long run, a codex resume of a
// large rollout.
//
// ReadLines returns what ended the stream: nil at EOF, the read error
// otherwise.
func ReadLines(r io.Reader, max int, fn func(line []byte), skip func(head []byte, size int)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf, head []byte
	dropped := 0 // bytes of the current record already discarded
	for {
		chunk, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// The record goes on past the reader's buffer.
			if dropped == 0 && len(buf)+len(chunk) <= max {
				buf = append(buf, chunk...)
				continue
			}
			if dropped == 0 {
				start := buf
				if len(start) == 0 {
					start = chunk
				}
				head = append(head[:0], firstOf(start)...)
			}
			dropped += len(buf) + len(chunk)
			buf = buf[:0]
			continue
		}

		line := chunk
		if len(buf) > 0 {
			buf = append(buf, chunk...)
			line = buf
		}
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		switch {
		case dropped > 0:
			skip(head, dropped+len(line))
		case len(line) > max:
			skip(firstOf(line), len(line))
		case len(line) > 0:
			fn(line)
		}
		dropped = 0
		// One huge record must not pin its buffer for the session's life.
		if cap(buf) > 1<<20 {
			buf = nil
		} else {
			buf = buf[:0]
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func firstOf(b []byte) []byte {
	if len(b) > headSize {
		return b[:headSize]
	}
	return b
}
