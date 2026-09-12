/**
 * File references and pasted blobs in the composer.
 *
 * Two very different things end up as chips, and they are stored differently
 * on purpose.
 *
 * A **file reference** is nothing but text: the token `@web/src/App.tsx` sits
 * in the draft string like any other word. That is the whole trick. The draft
 * stays a plain string, so it survives a session switch, a reload and a
 * schedule without a second store to keep in sync; the caret, selection, undo
 * and every key a phone keyboard sends keep working because the input is still
 * a real `<textarea>`; and what crosses the wire is a path, not a file. Both
 * harnesses resolve `@path` themselves and read the live file, which is both
 * smaller on 4G and more correct than a snapshot taken when the chip was made.
 * On a desktop the token is *painted* as a pill (see ComposerMirror); on a
 * phone it is left as legible text.
 *
 * A **blob** is a large paste that would otherwise bury the message. It cannot
 * be a token — the text has nowhere else to live — so it is held beside the
 * draft and folded into the prompt at send.
 */

/** A file reference token, with an optional line range: `@a/b.ts#L3-L9`. */
export interface FileRef {
  path: string;
  from?: number;
  to?: number;
}

/**
 * What counts as a file token.
 *
 * Deliberately not "anything after an @": `@channel` and an email address are
 * far more common in a sentence than a bare filename, and painting those as
 * chips would be worse than painting nothing. A token qualifies when it looks
 * like a path — it contains a `/`, or it ends in an extension — which is what
 * every reference this composer creates looks like.
 *
 * `#` is excluded from the path so the greedy run cannot swallow the `#L3-L9`
 * that follows it: with the range group optional, the match would otherwise
 * succeed with the range inside the path and never backtrack.
 */
const TOKEN = /@([^\s@#]*[^\s@#.,;:!?)\]}'"])(#L(\d+)(?:-L?(\d+))?)?/g;

function looksLikePath(path: string): boolean {
  if (!path) return false;
  if (path.includes("/")) return true;
  return /\.[A-Za-z0-9]{1,8}$/.test(path);
}

/** Build the token text for a reference. The inverse of `parseRefs`. */
export function refToken(ref: FileRef): string {
  const range =
    ref.from === undefined
      ? ""
      : ref.to === undefined || ref.to === ref.from
        ? `#L${ref.from}`
        : `#L${ref.from}-L${ref.to}`;
  return `@${ref.path}${range}`;
}

export interface RefMatch extends FileRef {
  /** Offsets of the whole token in the text, `#L…` range included. */
  start: number;
  end: number;
  text: string;
}

/** Every file token in the text, in order. */
export function parseRefs(text: string): RefMatch[] {
  const out: RefMatch[] = [];
  for (const m of text.matchAll(TOKEN)) {
    const path = m[1];
    if (!looksLikePath(path)) continue;
    const start = m.index;
    out.push({
      path,
      ...(m[3] ? { from: Number(m[3]) } : {}),
      ...(m[4] ? { to: Number(m[4]) } : {}),
      start,
      end: start + m[0].length,
      text: m[0],
    });
  }
  return out;
}

/** The token containing or ending at `offset`, if there is one. */
export function refAt(text: string, offset: number): RefMatch | null {
  for (const ref of parseRefs(text)) {
    if (offset > ref.start && offset <= ref.end) return ref;
  }
  return null;
}

/** Drop a token, and the one space it left behind. */
export function removeRef(text: string, ref: RefMatch): { value: string; cursor: number } {
  let end = ref.end;
  // A chip is inserted with a trailing space; taking it back out with the chip
  // is what keeps "word @chip word" from collapsing to "word  word".
  if (text[end] === " " && (ref.start === 0 || text[ref.start - 1] === " ")) end++;
  return { value: text.slice(0, ref.start) + text.slice(end), cursor: ref.start };
}

/**
 * Insert a reference at `cursor`, spaced into the sentence around it.
 *
 * Dropping a file between two words has to read as a word, so a space is added
 * on whichever side does not already have one rather than unconditionally —
 * dropping at the end of "look at " must not produce a double space.
 */
export function insertRef(
  text: string,
  cursor: number,
  ref: FileRef,
): { value: string; cursor: number } {
  const at = Math.max(0, Math.min(text.length, cursor));
  const before = text.slice(0, at);
  const after = text.slice(at);
  const lead = before && !/\s$/.test(before) ? " " : "";
  const trail = /^\s/.test(after) ? "" : " ";
  const token = `${lead}${refToken(ref)}${trail}`;
  return { value: before + token + after, cursor: at + token.length };
}

// ---- dragging a reference ----

/** Our own drag flavour, so the composer can tell a file row from a text
    selection someone dragged in off a page. */
export const REF_DRAG_TYPE = "application/x-omniplex-ref";

/**
 * Put a reference on a drag.
 *
 * `text/plain` carries the token as well, and that is not a fallback — it is
 * the mechanism. Dropped on the textarea, the browser inserts plain text at
 * the character the pointer is over, which is the exact caret placement this
 * feature is about and which no `caretRangeFromPoint` reimplementation gets
 * right across engines. The custom type only tells the composer to light up.
 */
export function setRefDrag(data: DataTransfer, ref: FileRef): void {
  data.effectAllowed = "copy";
  data.setData(REF_DRAG_TYPE, JSON.stringify(ref));
  // Spaced so a drop lands as a word rather than gluing itself to what it
  // was dropped next to. The composer tidies up any doubled space after.
  data.setData("text/plain", `${refToken(ref)} `);
}

/** Whether a drag is one of ours. */
export function dragHasRef(data: DataTransfer | null): boolean {
  return Array.from(data?.types ?? []).includes(REF_DRAG_TYPE);
}

export function refFromDrag(data: DataTransfer | null): FileRef | null {
  // Types first: a drag from anywhere else — an OS file, a text selection —
  // never carries ours, and asking such a transfer for data we know is not
  // there is pointless work on every drop.
  if (!dragHasRef(data)) return null;
  const raw = data?.getData(REF_DRAG_TYPE);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as FileRef;
    return typeof parsed?.path === "string" && parsed.path ? parsed : null;
  } catch {
    return null;
  }
}

/**
 * The character index in a textarea under a point, or null if the engine
 * will not say.
 *
 * Used to place a dropped chip where the pointer actually is. The two APIs are
 * split down browser lines and neither is universal, hence the null: a caller
 * that cannot get an answer here lets the browser perform the text drop
 * itself, which lands in the right place but cannot fix up spacing.
 */
export function caretOffsetFromPoint(
  el: HTMLTextAreaElement,
  x: number,
  y: number,
): number | null {
  const doc = document as Document & {
    caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null;
  };
  // Firefox: reports the textarea itself and a character index into its value.
  const position = doc.caretPositionFromPoint?.(x, y);
  if (position && (position.offsetNode === el || el.contains(position.offsetNode))) {
    return clamp(position.offset, el.value.length);
  }
  // Chromium and WebKit: a range inside the control's inner text node, whose
  // offset is the same index as long as the value is one node — which it is
  // for a textarea, newlines included.
  const range = document.caretRangeFromPoint?.(x, y);
  if (range && el.contains(range.startContainer)) return clamp(range.startOffset, el.value.length);
  return null;
}

function clamp(n: number, max: number): number {
  return Math.max(0, Math.min(max, n));
}

// ---- blobs ----

/** Where a blob came from, which is what its chip and quickview announce. */
export type BlobOrigin =
  | { kind: "paste" }
  | { kind: "terminal"; label: string }
  | { kind: "file"; path: string; from: number; to: number };

/** A large paste, held beside the draft rather than dumped into it. */
export interface Blob {
  key: string;
  text: string;
  origin: BlobOrigin;
  lines: number;
}

/**
 * When a paste is too big to belong in the box.
 *
 * Tuned to catch what actually floods it — a stack trace, a log tail, a chunk
 * of a file — while leaving a pasted paragraph, a URL or a short snippet as
 * ordinary text. Either bound alone is enough: 40 lines of `ls` output is well
 * under the character limit and still buries the message.
 */
export const BLOB_CHARS = 2000;
export const BLOB_LINES = 15;

export function isBlobWorthy(text: string): boolean {
  return text.length > BLOB_CHARS || countLines(text) > BLOB_LINES;
}

export function countLines(text: string): number {
  let n = 1;
  for (let i = 0; i < text.length; i++) if (text.charCodeAt(i) === 10) n++;
  return n;
}

/** The chip's title: where this came from, in as few characters as fit. */
export function blobLabel(blob: Blob): string {
  switch (blob.origin.kind) {
    case "terminal":
      return blob.origin.label;
    case "file":
      return `${fileBase(blob.origin.path)}:${blob.origin.from}-${blob.origin.to}`;
    case "paste":
      return "Pasted text";
  }
}

/** The quickview's heading: the same thing, unabbreviated. */
export function blobTitle(blob: Blob): string {
  switch (blob.origin.kind) {
    case "terminal":
      return `${blob.origin.label} output`;
    case "file":
      return `${blob.origin.path} · lines ${blob.origin.from}–${blob.origin.to}`;
    case "paste":
      return "Pasted text";
  }
}

function fileBase(path: string): string {
  const i = path.lastIndexOf("/");
  return i < 0 ? path : path.slice(i + 1);
}

/** The first non-empty line, for the chip's sneak peek. */
export function blobPeek(blob: Blob, max = 48): string {
  const line = blob.text.split("\n").find((l) => l.trim()) ?? "";
  const trimmed = line.trim();
  return trimmed.length > max ? `${trimmed.slice(0, max - 1)}…` : trimmed;
}

/** `38 lines · 1,204 chars`, for the chip's second row. */
export function blobSize(blob: Blob): string {
  const lines = blob.lines === 1 ? "1 line" : `${blob.lines.toLocaleString()} lines`;
  return `${lines} · ${blob.text.length.toLocaleString()} chars`;
}

/**
 * Fold the blobs into the message.
 *
 * They go after the prose as fenced blocks, each labelled with where it came
 * from: the reader wrote a sentence about them, and the sentence has to come
 * first for the agent to know what the dump is for. The fence is grown past
 * any backtick run inside the content, so a pasted markdown file cannot close
 * its own block.
 */
export function composePrompt(text: string, blobs: Blob[]): string {
  if (blobs.length === 0) return text;
  const parts = blobs.map((blob) => {
    const fence = "`".repeat(Math.max(3, longestBacktickRun(blob.text) + 1));
    const body = blob.text.endsWith("\n") ? blob.text.slice(0, -1) : blob.text;
    return `${fence}${blobFenceInfo(blob)}\n${body}\n${fence}`;
  });
  const head = text.trim();
  return head ? `${head}\n\n${parts.join("\n\n")}` : parts.join("\n\n");
}

function blobFenceInfo(blob: Blob): string {
  switch (blob.origin.kind) {
    case "terminal":
      return ` ${blob.origin.label}`;
    case "file":
      return ` ${blob.origin.path}#L${blob.origin.from}-L${blob.origin.to}`;
    case "paste":
      return "";
  }
}

function longestBacktickRun(text: string): number {
  let best = 0;
  let run = 0;
  for (const ch of text) {
    if (ch === "`") best = Math.max(best, ++run);
    else run = 0;
  }
  return best;
}
