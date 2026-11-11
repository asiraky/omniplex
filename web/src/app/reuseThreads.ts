import type { ThreadMeta } from "~/protocol";

import { shallow } from "./threadStore";

/**
 * The thread list a broadcast carries, with every entry that did not change
 * kept as the object the previous list held, and the previous list itself
 * when nothing changed at all.
 *
 * The server re-sends the whole list as fresh JSON whenever any thread in it
 * moves, so every entry arrives as a new object. The sidebar's rows are
 * memoised on theirs, and a new object for a thread that is exactly as it
 * was would redraw every row for each broadcast. A ThreadMeta is flat, so a
 * shallow comparison is a full one.
 */
export function reuseThreads(prev: ThreadMeta[], next: ThreadMeta[]): ThreadMeta[] {
  const byId = new Map(prev.map((t) => [t.id, t]));
  let changed = prev.length !== next.length;
  const out = next.map((t, i) => {
    const old = byId.get(t.id);
    const kept = old && shallow(old, t) ? old : t;
    if (kept !== prev[i]) changed = true;
    return kept;
  });
  return changed ? out : prev;
}
