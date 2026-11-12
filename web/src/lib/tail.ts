// The tail of a running call's output: the last lines it has printed, folded
// from its output deltas. Mirrors appendTail in internal/projection/tail.go, since
// the client folds the same deltas the server does and must cut in the same
// place.

// The projection must not grow with tool output (#239), so the tail is
// bounded. tailLines is what the running row shows: about the last 20 lines.
const tailLines = 20;
// tailChars bounds one runaway line — a progress bar redrawn with "\r", which
// never prints a newline — that the line cap alone would keep whole. Counted
// in code points, as the server counts runes, not UTF-16 units.
const tailChars = 4000;

/** Adds delta to tail and keeps the last tailLines lines, then at most the
    last tailChars code points of those. A newline at the very end terminates
    the last line rather than starting an empty one; an unterminated partial
    line counts as a line. Both cuts keep a suffix measured from the end, so
    appending in pieces agrees with appending at once. */
export function appendTail(tail: string, delta: string): string {
  let s = tail + delta;
  // A newline is never half of a surrogate pair, so walking units is
  // walking lines.
  const end = s.endsWith("\n") ? s.length - 1 : s.length;
  for (let i = end - 1, n = 0; i >= 0; i--) {
    if (s[i] === "\n" && ++n === tailLines) {
      s = s.slice(i + 1);
      break;
    }
  }
  // No more code points than UTF-16 units, so the count is only paid on a
  // long line.
  if (s.length > tailChars) {
    const points = Array.from(s);
    if (points.length > tailChars) s = points.slice(-tailChars).join("");
  }
  return s;
}
