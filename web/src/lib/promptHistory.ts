import type { Item } from "~/protocol";

/**
 * The prompts the user sent in this thread, oldest first, for ArrowUp recall
 * and the composer's recent-prompts menu. A subagent's messages carry a
 * parentId and were never typed by the user, so they are left out. Sending the
 * same prompt twice in a row is kept once, as a shell does, so ArrowUp never
 * lands on what looks like the same entry again.
 */
export function promptHistory(items: readonly Item[]): string[] {
  const out: string[] = [];
  for (const item of items) {
    if (item.kind !== "message" || item.role !== "user" || item.parentId) continue;
    const text = item.text?.trim();
    if (!text || out[out.length - 1] === text) continue;
    out.push(text);
  }
  return out;
}

/** Equality for useThread's isEqual, so the composer only re-renders when the history changes. */
export function sameHistory(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((s, i) => s === b[i]);
}

/** Where ArrowUp has got to: the entry's index and the text it put in the composer. */
export interface HistoryPosition {
  index: number;
  recalled: string;
}

/**
 * Where a position really points now, or -1 if it no longer counts. Once the
 * user edits a recalled prompt it is their draft, not a place in history. The
 * index can go stale when a prompt lands mid-recall (entries grow at the end),
 * so fall back to the last entry with the same text.
 */
function liveIndex(entries: readonly string[], position: HistoryPosition | null, current: string): number {
  if (!position || current !== position.recalled) return -1;
  if (entries[position.index] === position.recalled) return position.index;
  return entries.lastIndexOf(position.recalled);
}

/**
 * One ArrowUp ("back") or ArrowDown ("forward") through `entries`, oldest
 * first. Returns null when the key should do nothing here, so the caller can
 * let the textarea move the caret as usual. Back never replaces a draft the
 * user typed; forward past the newest entry clears the composer, as a shell
 * returns to an empty line.
 */
export function stepHistory(input: {
  direction: "back" | "forward";
  entries: readonly string[];
  position: HistoryPosition | null;
  current: string;
}): { position: HistoryPosition | null; text: string } | null {
  const { direction, entries, position, current } = input;
  const at = liveIndex(entries, position, current);
  const to = (index: number) => ({ position: { index, recalled: entries[index] }, text: entries[index] });

  if (direction === "back") {
    if (entries.length === 0) return null;
    if (at === -1) return current === "" ? to(entries.length - 1) : null;
    return at === 0 ? null : to(at - 1);
  }

  if (at === -1) return null;
  return at + 1 < entries.length ? to(at + 1) : { position: null, text: "" };
}

/** For the menu: newest first, each prompt once (where it was last sent), at most `limit`. */
export function recentPrompts(entries: readonly string[], limit: number): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (let i = entries.length - 1; i >= 0 && out.length < limit; i--) {
    if (seen.has(entries[i])) continue;
    seen.add(entries[i]);
    out.push(entries[i]);
  }
  return out;
}

/**
 * Whether the caret is on the first line. ArrowUp only recalls from there, so
 * in a multi-line draft it still moves the caret up a line first.
 */
export function caretOnFirstLine(value: string, start: number, _end: number): boolean {
  return !value.slice(0, start).includes("\n");
}

/** Whether the caret is on the last line, where ArrowDown steps forward instead of moving down. */
export function caretOnLastLine(value: string, _start: number, end: number): boolean {
  return !value.slice(end).includes("\n");
}
