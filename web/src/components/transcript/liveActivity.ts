// What the agent is doing right now, for the working line at the tail of a
// running turn. Pure, so the rules can be tested without rendering anything.
//
// Two things to say. Working: a call is running, or text is streaming — the
// call's own row, or the caret, already shows what; the line only keeps time.
// Thinking: nothing is running and nothing is being written, which is the
// model reasoning whether or not the harness lets it show. When it does, the
// reasoning streaming in is previewed on the line instead of drawn in full: a
// paragraph of half-formed thought growing at the tail fills a phone screen
// with the least settled thing in the transcript. It draws in full where it
// was as soon as the agent moves past it.

import type { Item, Turn } from "~/protocol";

export type LiveActivity = {
  state: "working" | "thinking";
  /** The reasoning streaming in at the tail, previewed on the line. */
  thought?: Item;
  /** When the running turn started, by the event log's clock (epoch ms). */
  startedAt?: number;
};

const blank = (it: Item) => it.kind === "message" && (it.text ?? "").trim() === "" && !it.images?.length;
const running = (it: Item) => it.status === "in_progress" || it.status === "pending";

function last<T>(xs: T[], ok: (x: T) => boolean): T | undefined {
  for (let i = xs.length - 1; i >= 0; i--) if (ok(xs[i])) return xs[i];
  return undefined;
}

/** Undefined unless a turn is running. */
export function liveActivity(items: Item[], turns: Turn[], phase: string): LiveActivity | undefined {
  if (phase !== "turn") return undefined;
  const startedAt = last(turns, (t) => !t.done)?.startedAt;
  // An opened block the harness never filled says nothing, and is skipped the
  // way the transcript skips it.
  const newest = last(items, (it) => !blank(it));
  // Anything from a finished turn is history: the running turn has not said
  // anything yet.
  const done = new Set(turns.filter((t) => t.done).map((t) => t.id));
  const settled = (it: Item) => it.turnId !== undefined && done.has(it.turnId);
  const current = newest && !settled(newest) ? newest : undefined;
  if (current?.kind === "message" && current.role === "agent") {
    if (current.contentKind === "thought") return { state: "thinking", thought: current, startedAt };
    return { state: "working", startedAt };
  }
  // Not only the newest call: calls run side by side, and the one started
  // last is often a quick read that finishes while a build started before it
  // is still going.
  for (let i = items.length - 1; i >= 0 && !settled(items[i]); i--) {
    if (items[i].kind === "tool" && running(items[i])) return { state: "working", startedAt };
  }
  return { state: "thinking", startedAt };
}

/**
 * The gist of reasoning still streaming, as one plain-text line for the
 * preview. Codex summarises its reasoning under bold headings, and the newest
 * heading is what it is thinking about now; prose without headings shows its
 * newest paragraph, which stays put until the next one starts rather than
 * shifting with every token. Markdown is flattened: the preview is a
 * clamped line, not a rendering.
 */
export function thoughtPreview(markdown: string): string {
  const headings = [...markdown.matchAll(/^[ \t]*\*\*([^*\r\n]+)\*\*[ \t]*$/gm)];
  const paragraphs = markdown.split(/\r?\n[ \t]*\r?\n/).filter((p) => p.trim() !== "");
  const source = headings.length > 0 ? headings[headings.length - 1][1] : (paragraphs[paragraphs.length - 1] ?? "");
  return source
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^[ \t]*(?:#{1,6}|[-*+>]|\d+\.)[ \t]+/gm, "")
    .replace(/`+|\*\*|~~/g, "")
    .replace(/(^|[^\w*])[*_]([^*_\n]+)[*_](?![\w*])/g, "$1$2")
    .replace(/\s+/g, " ")
    .trim();
}
