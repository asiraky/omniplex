// What the agent is doing right now, for the working line at the tail of a
// running turn. Pure, so the rules can be tested without rendering anything.
//
// Three things to say. Waiting: a permission or a question is up, and nothing
// moves until the user answers it. Working: a call is running, or text is
// streaming — the call's own row, or the caret, already shows what; the line
// only keeps time. Thinking: nothing is running and nothing is being written,
// which is the model reasoning whether or not the harness lets it show. When it
// does, the reasoning streaming in is previewed on the line, which is all a
// phone shows of it: a paragraph of half-formed thought growing at the tail
// fills a phone screen with the least settled thing in the transcript.
//
// A call's row names it only while its run is the last thing drawn. Once
// something is drawn after a call that is still going (the agent writes or
// thinks on, or spawns agents, while a build runs), the row folds to a
// summary, and the line names the call instead. Not a spawn: its own card
// shows its agents working.

import type { Item, ThreadState, Turn } from "~/protocol";
import { isEmptyMessage, isSpawn } from "~/rows";

export type LiveActivity = {
  state: "working" | "thinking" | "waiting";
  /** The reasoning streaming in at the tail, previewed on the line. */
  thought?: Item;
  /** A call still running whose row has folded to a summary. */
  foldedCall?: Item;
  /** When the running turn started, by the event log's clock (epoch ms). */
  startedAt?: number;
};

const running = (it: Item) => it.status === "in_progress" || it.status === "pending";

/** Undefined unless a turn is running. */
export function liveActivity(
  items: Item[],
  turns: Turn[],
  phase: ThreadState["phase"],
  /** A permission or a question is up for the user to answer. */
  asking: boolean,
): LiveActivity | undefined {
  if (phase !== "turn") return undefined;
  let startedAt: number | undefined;
  for (let i = turns.length - 1; i >= 0; i--) {
    if (!turns[i].done) {
      startedAt = turns[i].startedAt;
      break;
    }
  }
  if (asking) return { state: "waiting", startedAt };

  // Back through the running turn: anything from a finished turn is history.
  // An opened block the harness never filled says nothing, and is skipped the
  // way the transcript skips it. Not only the newest call: calls run side by
  // side, and the one started last is often a quick read that finishes while
  // a build started before it is still going.
  const done = new Set(turns.filter((t) => t.done).map((t) => t.id));
  let current: Item | undefined;
  let call: Item | undefined;
  let folded = false;
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.turnId !== undefined && done.has(it.turnId)) break;
    if (isEmptyMessage(it)) continue;
    current ??= it;
    if (it.kind === "tool" && running(it)) {
      call = it;
      break;
    }
    // Anything but a plain call drawn after the running one ends its run.
    if (it.kind !== "tool" || isSpawn(it)) folded = true;
  }
  const foldedCall = folded && call && !isSpawn(call) ? call : undefined;

  if (current?.kind === "message" && current.role === "agent") {
    if (current.contentKind === "thought") return { state: "thinking", thought: current, foldedCall, startedAt };
    return { state: "working", foldedCall, startedAt };
  }
  if (call) return { state: "working", foldedCall, startedAt };
  return { state: "thinking", startedAt };
}

// A line that is nothing but bold text, opening a paragraph: Codex's headings.
const HEADING = /^[ \t]*\*\*([^*\r\n]+)\*\*[ \t]*(?:\r?\n|$)/;

/**
 * The gist of reasoning still streaming, as one plain-text line for the
 * preview. Codex summarises its reasoning in sections under bold headings, and
 * the heading of the section streaming now is what it is thinking about.
 * Otherwise the newest paragraph, or the end of it when it runs long; a bold
 * line far back up the prose does not pin it. Markdown is flattened: the preview is a clamped line, not
 * a rendering.
 */
export function thoughtPreview(markdown: string): string {
  const paragraphs = markdown.split(/\r?\n[ \t]*\r?\n/).filter((p) => p.trim() !== "");
  const last = paragraphs[paragraphs.length - 1];
  const prev = paragraphs[paragraphs.length - 2];
  const heading = last?.match(HEADING)?.[1] ?? prev?.match(HEADING)?.[1];
  return heading !== undefined ? flatten(heading) : tail(flatten(last ?? ""));
}

// Past this a phone's two lines cut the paragraph off at its start.
const PREVIEW_CHARS = 160;

/**
 * The end of a paragraph too long for the preview: its newest finished
 * sentences that fit, so a long paragraph still moves as it streams, but a
 * sentence at a time rather than a token at a time. A paragraph without a
 * finished sentence yet shows from its start.
 */
function tail(text: string): string {
  if (text.length <= PREVIEW_CHARS) return text;
  const sentences = text.match(/[^.!?]+[.!?]+(?=\s|$)/g) ?? [];
  let out = "";
  for (let i = sentences.length - 1; i >= 0; i--) {
    const next = sentences[i].trim() + (out ? " " + out : "");
    if (next.length > PREVIEW_CHARS && out) break;
    out = next;
  }
  if (!out) return text;
  return text.startsWith(out) ? out : `…${out}`;
}

function flatten(source: string): string {
  return source
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^[ \t]*(?:#{1,6}|[-*+>]|\d+\.)[ \t]+/gm, "")
    .replace(/`+|\*\*|~~/g, "")
    .replace(/(^|[^\w*])[*_](?=\S)([^*_\n]*?\S)[*_](?![\w*])/g, "$1$2")
    .replace(/\s+/g, " ")
    .trim();
}
