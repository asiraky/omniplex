// Turning terminal output into something to say in a chat: a selection, or
// the last few lines of the buffer, as a fenced block the agent reads verbatim.

/** Terminal output as a block to put in a chat composer: CRLF→LF, trailing whitespace stripped per line, leading/trailing blank lines dropped, wrapped in a Markdown code fence longer than any backtick run inside it (min ```). Returns "" when nothing but whitespace is left. */
export function quoteTerminalOutput(text: string): string {
  const lines = text
    .replace(/\r\n/g, "\n")
    .split("\n")
    .map((l) => l.trimEnd());
  let first = 0;
  let last = lines.length;
  while (first < last && lines[first] === "") first++;
  while (last > first && lines[last - 1] === "") last--;
  if (first === last) return "";
  const body = lines.slice(first, last).join("\n");
  // A fence closes on a backtick run at least as long as itself, so it must
  // outrun anything the output already contains.
  let longest = 0;
  for (const m of body.matchAll(/`+/g)) longest = Math.max(longest, m[0].length);
  const fence = "`".repeat(Math.max(3, longest + 1));
  return `${fence}\n${body}\n${fence}`;
}

/** One row of an xterm buffer: its text (translateToString(true)) and whether it continues the row above (IBufferLine.isWrapped). */
export interface TerminalRow {
  text: string;
  wrapped: boolean;
}

/** The last `n` logical lines of a buffer: wrapped rows joined onto the row they continue, trailing blank lines (the empty screen below the prompt) dropped first. Joined with "\n". */
export function lastTerminalLines(rows: readonly TerminalRow[], n: number): string {
  if (n <= 0) return "";
  const lines: string[] = [];
  for (const row of rows) {
    // A row that wraps had a full-width row above it, so the two join with no
    // separator. That row's text was trimmed, though, so spaces it ended in
    // right at the wrap are lost.
    if (row.wrapped && lines.length > 0) lines[lines.length - 1] += row.text;
    else lines.push(row.text);
  }
  while (lines.length > 0 && lines[lines.length - 1].trim() === "") lines.pop();
  return lines.slice(-n).join("\n");
}
