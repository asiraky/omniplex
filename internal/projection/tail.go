package projection

import "unicode/utf8"

// The bounds on a running call's tail. The projection must not grow with tool
// output (#239): a build that prints for ten minutes is one item, and its
// output lands whole in Content only when it finishes. Until then the item
// holds just enough to show it is alive.
const (
	// tailLines is what the running row shows: about the last 20 lines.
	tailLines = 20
	// tailChars bounds one runaway line — a progress bar redrawn with "\r",
	// which never prints a newline — that the line cap alone would keep whole.
	// Counted in code points, not bytes, so the web client cuts in the same
	// place.
	tailChars = 4000
)

// appendTail adds delta to tail and keeps what the bounds allow: the last
// tailLines lines, then at most the last tailChars code points of those. A
// newline at the very end terminates the last line rather than starting an
// empty one; an unterminated partial line counts as a line.
//
// Both cuts keep a suffix measured from the end, so appending in pieces
// agrees with appending at once: appendTail(appendTail(t, a), b) equals
// appendTail(t, a+b). The coalescer relies on that when it joins deltas.
// Mirrored in web/src/lib/tail.ts.
func appendTail(tail, delta string) string {
	s := tail + delta
	// A newline is one byte in UTF-8 and never part of a longer sequence, so
	// walking bytes is walking lines.
	end := len(s)
	if end > 0 && s[end-1] == '\n' {
		end--
	}
	for i, n := end-1, 0; i >= 0; i-- {
		if s[i] == '\n' {
			if n++; n == tailLines {
				s = s[i+1:]
				break
			}
		}
	}
	// A string no longer than tailChars bytes has no more code points than
	// that, so the count is only paid on a long line.
	if len(s) > tailChars {
		if over := utf8.RuneCountInString(s) - tailChars; over > 0 {
			for i := range s {
				if over == 0 {
					s = s[i:]
					break
				}
				over--
			}
		}
	}
	return s
}
