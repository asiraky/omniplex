/**
 * Lifting pinned threads out of the sidebar list into a section of their own
 * at the top.
 *
 * Pure, like `projectGroups`: the sidebar hands in whatever list it is
 * currently rendering, after the project filter, including the delete flow's
 * frozen ordering and its departing row, and gets two lists back. Neither half
 * is reordered, so a row still folds away exactly where it stood.
 *
 * The rules:
 * - A pinned thread leaves its normal place: it is in the Pinned section and
 *   nowhere else, so grouping only ever sees what is left.
 * - Pinned rows keep the list's order, which is the user's own (`threadOrder`).
 *   Positions are global, so a drag inside the Pinned section moves a thread
 *   among the pinned ones and leaves the rest of the list alone.
 * - The project filter runs first. Pinning is a way to keep a thread in sight,
 *   not a way past the filter: a hidden project's pinned threads stay hidden.
 */

import type { ThreadMeta } from "~/protocol";

export function splitPinned(threads: ThreadMeta[]): { pinned: ThreadMeta[]; rest: ThreadMeta[] } {
  if (!threads.some((s) => s.pinned)) return { pinned: [], rest: threads };
  const pinned: ThreadMeta[] = [];
  const rest: ThreadMeta[] = [];
  for (const s of threads) (s.pinned ? pinned : rest).push(s);
  return { pinned, rest };
}
