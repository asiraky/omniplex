/**
 * Hiding threads the label filter has switched off.
 *
 * Pure, like the grouping it replaces: the sidebar hands in whatever list it
 * is currently rendering — including the delete flow's frozen ordering — and
 * gets a shorter list back, so filtering composes with the exit animation
 * instead of fighting it.
 *
 * The rules:
 * - The filter names what is *hidden*, not what is shown. A device that has
 *   never touched it hides nothing, and a label created on another device
 *   shows up rather than arriving pre-hidden.
 * - Unlabelled threads are a checkbox of their own (`UNLABELLED`). They are
 *   not a label, but they are the one run of threads you cannot otherwise
 *   switch off, and "show me only what I have filed" is the whole point.
 * - A thread pointing at a label that no longer exists counts as unlabelled,
 *   the way it did when labels grouped: the assignment broadcast can land a
 *   beat after the deletion broadcast.
 * - Hidden ids for labels that no longer exist are ignored, so deleting a
 *   hidden label cannot leave threads stranded behind a checkbox that is no
 *   longer in the menu. With no labels left at all that includes `UNLABELLED`:
 *   deleting the last label unlabels every thread, and a filter switched off
 *   before that would otherwise empty the sidebar with no checkbox left in the
 *   menu to switch back on.
 */

import type { Label, ThreadMeta } from "~/protocol";

/** The filter key standing in for "no label": ids are uuids, so it cannot collide. */
export const UNLABELLED = "none";

/** The filter key a thread answers to — its label, or `UNLABELLED`. */
export function filterKey(thread: ThreadMeta, labels: Label[]): string {
  return thread.labelId && labels.some((l) => l.id === thread.labelId)
    ? thread.labelId
    : UNLABELLED;
}

export function visibleThreads(
  threads: ThreadMeta[],
  labels: Label[],
  hidden: Set<string>,
): ThreadMeta[] {
  if (hidden.size === 0 || labels.length === 0) return threads;
  const live = new Set<string>([UNLABELLED, ...labels.map((l) => l.id)]);
  const off = new Set([...hidden].filter((id) => live.has(id)));
  if (off.size === 0) return threads;
  return threads.filter((s) => !off.has(filterKey(s, labels)));
}
