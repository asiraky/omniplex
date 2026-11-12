// Ported from T3 Code (pingdotgg/t3code@d8c926c395), with its fence detection
// shared with `closeOpenFence` so the two can never disagree about where a
// fence is open.

const FENCE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
// Definitions, footnotes, raw HTML, container directives and display math can
// tie blocks together. Matched a line at a time outside fences, so code that
// only looks like one of them (a JSX line, a `[^a-z]` regex) leaves the text
// splittable. A definition is any `]:` at all: its label may hold escaped
// brackets or code, run across lines, or sit in a quote or a list item, and
// it applies to the whole document from any of them. Prose that merely has a
// `]:` in it renders whole, which is only slower.
const DOCUMENT_WIDE_SYNTAX = /\]:|^ {0,3}(?:<[A-Za-z!?/]|:::|\$\$)/;
const FOOTNOTE = /\[\^/;
// A code span, which no footnote reference is inside: a run of backticks to
// the next run of the same length.
const CODE_SPAN = /(`+)[^`]*?\1/g;

function documentWide(line: string): boolean {
  return DOCUMENT_WIDE_SYNTAX.test(line) || FOOTNOTE.test(line.replace(CODE_SPAN, ""));
}
// After a blank line these still continue the previous block: a list item or indented content.
const CONTINUATION_PATTERN = /^(?:[ \t]|(?:[-*+]|\d{1,9}[.)])(?:[ \t]|$))/;
// A fence behind indentation or a list marker. It may sit in a list item,
// which this does not follow and which can close it, so where it ends is
// unknown.
const FENCE_OFF_MARGIN = /^(?:[ \t]|(?:[-*+]|\d{1,9}[.)])[ \t])[ \t]*(?:(?:[-*+]|\d{1,9}[.)])[ \t]+)*(?:`{3,}|~{3,})/;

/**
 * The fence open after `line`, given the one open before it (null for none).
 * A closing fence is the same character, at least as long as the opener, and
 * carries no info string — "```js" inside a ```` block is content. A backtick
 * fence's info string cannot itself contain a backtick, so "``` a ` b" opens
 * nothing.
 */
function fenceAfter(line: string, open: string | null): string | null {
  const m = FENCE.exec(line);
  if (!m) return open;
  const [, fence, rest] = m;
  if (open === null) return fence[0] === "`" && rest.includes("`") ? null : fence;
  return fence[0] === open[0] && fence.length >= open.length && rest.trim() === "" ? null : open;
}

// Text arrives a character at a time, so at any moment the parser is being
// handed a document that is very likely mid-sentence. Most half-written
// constructs are harmless — an unfinished `**bold` is just literal text until
// its closing pair lands. A fence is the exception: an opening ``` with no
// close makes every line after it a paragraph, then reflows the lot into a
// code block the instant the close arrives. Closing it here means the block is
// a code block from its first line and simply grows.
export function closeOpenFence(text: string): string {
  let open: string | null = null;
  for (const line of text.split("\n")) open = fenceAfter(line, open);
  return open === null ? text : `${text}\n${open}`;
}

/**
 * Cuts Markdown at blank lines that close every open block, so each piece
 * renders exactly as it would inside the whole text. Returns null when the
 * text, outside its code, uses syntax that spans blocks, or opens a fence
 * anywhere but the margin.
 *
 * A cut is never made inside a fence, so a fence still open at the end of the
 * text is always in the last piece: `closeOpenFence` on that piece alone is
 * the same as on the whole.
 */
export function splitMarkdownBlocks(text: string): string[] | null {
  if (text.includes("\r")) return null;
  const blocks: string[] = [];
  let blockStart = 0;
  let lineStart = 0;
  // Only ever a fence opened at the margin, so everything in it is code.
  let fence: string | null = null;
  let afterBlankLine = false;
  for (const line of text.split("\n")) {
    if (fence !== null) {
      fence = fenceAfter(line, fence);
    } else if (line.trim() === "") {
      afterBlankLine = true;
    } else {
      if (documentWide(line)) return null;
      if (FENCE_OFF_MARGIN.test(line)) return null;
      if (afterBlankLine && lineStart > blockStart && !CONTINUATION_PATTERN.test(line)) {
        blocks.push(text.slice(blockStart, lineStart));
        blockStart = lineStart;
      }
      afterBlankLine = false;
      fence = fenceAfter(line, null);
    }
    lineStart += line.length + 1;
  }
  blocks.push(text.slice(blockStart));
  return blocks;
}
