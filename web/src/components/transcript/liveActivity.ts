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
// A call's row names it only while its run is the last thing drawn. Once the
// agent writes or thinks past a call that is still going (a build left running
// while it reads on), the row folds to a summary, and the line names the call
// instead.

import type { Item, ThreadState, Turn } from "~/protocol";
import { isEmptyMessage } from "~/rows";

export type LiveActivity = {
  state: "working" | "thinking" | "waiting";
  /** The reasoning streaming in at the tail, previewed on the line. */
  thought?: Item;
  /** A call still running that its row no longer shows as live. */
  call?: Item;
  /** When the running turn started, by the event log's clock (epoch ms). */
  startedAt?: number;
};

const running = (it: Item) => it.status === "in_progress" || it.status === "pending";

/** Undefined unless a turn is running. `waiting`: the user has something to answer. */
export function liveActivity(
  items: Item[],
  turns: Turn[],
  phase: ThreadState["phase"],
  waiting: boolean,
): LiveActivity | undefined {
  if (phase !== "turn") return undefined;
  let startedAt: number | undefined;
  for (let i = turns.length - 1; i >= 0; i--) {
    if (!turns[i].done) {
      startedAt = turns[i].startedAt;
      break;
    }
  }
  if (waiting) return { state: "waiting", startedAt };

  // Back through the running turn: anything from a finished turn is history.
  // An opened block the harness never filled says nothing, and is skipped the
  // way the transcript skips it. Not only the newest call: calls run side by
  // side, and the one started last is often a quick read that finishes while
  // a build started before it is still going.
  const done = new Set(turns.filter((t) => t.done).map((t) => t.id));
  let current: Item | undefined;
  let call: Item | undefined;
  let passed = false;
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.turnId !== undefined && done.has(it.turnId)) break;
    if (isEmptyMessage(it)) continue;
    current ??= it;
    if (it.kind !== "tool") passed = true;
    else if (running(it)) {
      call = it;
      break;
    }
  }
  const named = passed ? call : undefined;

  if (current?.kind === "message" && current.role === "agent") {
    if (current.contentKind === "thought") return { state: "thinking", thought: current, call: named, startedAt };
    return { state: "working", call: named, startedAt };
  }
  if (call) return { state: "working", call: named, startedAt };
  return { state: "thinking", startedAt };
}

// A line that is nothing but bold text, opening a paragraph: Codex's headings.
const HEADING = /^[ \t]*\*\*([^*\r\n]+)\*\*[ \t]*(?:\r?\n|$)/;

/**
 * The gist of reasoning still streaming, as one plain-text line for the
 * preview. Codex summarises its reasoning in sections under bold headings, and
 * the heading of the section streaming now is what it is thinking about.
 * Otherwise the newest paragraph, which stays put until the next one starts
 * rather than shifting with every token; a bold line far back up the prose
 * does not pin it. Markdown is flattened: the preview is a clamped line, not
 * a rendering.
 */
export function thoughtPreview(markdown: string): string {
  const paragraphs = markdown.split(/\r?\n[ \t]*\r?\n/).filter((p) => p.trim() !== "");
  const last = paragraphs[paragraphs.length - 1];
  const prev = paragraphs[paragraphs.length - 2];
  const source = last?.match(HEADING)?.[1] ?? prev?.match(HEADING)?.[1] ?? last ?? "";
  return source
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^[ \t]*(?:#{1,6}|[-*+>]|\d+\.)[ \t]+/gm, "")
    .replace(/`+|\*\*|~~/g, "")
    .replace(/(^|[^\w*])[*_](?=\S)([^*_\n]*?\S)[*_](?![\w*])/g, "$1$2")
    .replace(/\s+/g, " ")
    .trim();
}
