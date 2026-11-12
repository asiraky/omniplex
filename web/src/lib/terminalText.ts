// A running tool's tail is what it wrote to a terminal, byte for byte: colour
// codes, cursor moves, progress bars that redraw one line with "\r". Shown
// raw, that is noise around the text; this reduces it to the lines a terminal
// would have left on screen, near enough for a glance at a live call.

// One escape sequence, in any of the shapes programs actually emit:
//   OSC  ESC ] … BEL | ESC \   (window titles, hyperlinks)
//   CSI  ESC [ params final    (colour, cursor, erase)
//   any other ESC + intermediates + final (charset switches, ESC 7, ESC =)
// Each alternative also accepts end-of-input in place of its terminator: the
// tail is a window onto a stream, so it can end halfway through a sequence,
// and the half that has arrived must not render as "[3".
const ESCAPE =
  /\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\|$)|\[[0-?]*[ -/]*(?:[@-~]|$)|[ -/]*(?:[0-~]|$))/g;

// Control characters left once the sequences are gone (BEL, backspace, a lone
// ESC before junk). Tab, newline and carriage return carry layout and stay.
const CONTROL = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g;

/**
 * The lines a raw terminal tail shows: escapes stripped, and each line its
 * last "\r" redraw. Taking the last non-empty segment rather than the last
 * one keeps "done\r" (and CRLF, which is the same thing) as "done" instead of
 * blank. Trailing blank lines are dropped so the newest line with text is the
 * bottom one; an empty array means there is nothing to show.
 */
export function terminalLines(raw: string): string[] {
  const lines = raw
    .replace(ESCAPE, "")
    .replace(CONTROL, "")
    .split("\n")
    .map((line) => line.split("\r").filter((seg) => seg !== "").pop() ?? "");
  while (lines.length > 0 && lines[lines.length - 1].trim() === "") lines.pop();
  return lines;
}
