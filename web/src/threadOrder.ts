/**
 * The sidebar's order: the user's, kept on the server as a position per
 * thread, smallest first.
 *
 * Pure, like `projectGroups`: the hook that applies a move ahead of the server
 * and the drag that works out where a row landed both come through here, so
 * the arithmetic has one home and one set of tests.
 *
 * The rules:
 * - Position decides, and nothing else does. Activity, status and renames
 *   never move a row; a new thread arrives on top because the server gives it
 *   a position below the smallest, not because it is new.
 * - A move writes one thread: the midpoint of the two rows it was dropped
 *   between, or one step past the end it was dropped at. Nothing else is
 *   renumbered, so the command is one id and one number however long the
 *   list.
 * - Neighbours are the rows the user could see when they dropped it — inside
 *   a project group, the group's rows. Positions are global, so a midpoint of
 *   two rows in one group lands between them in the whole list too; whatever
 *   sits between them from other projects, or behind a filter, is untouched.
 * - Ties sort the way the server breaks them: newest created first, then id.
 *   Repeatedly halving one gap runs out of precision after ~50 drops into the
 *   very same slot; the tie-break keeps that from ever being an unstable
 *   order, only a drop that lands one place off.
 */

import type { ThreadMeta } from "~/protocol";

/** The server's ORDER BY, so a list sorted here agrees with a list sent. */
function compare(a: ThreadMeta, b: ThreadMeta): number {
  return (
    a.position - b.position || b.createdAt - a.createdAt || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0)
  );
}

/**
 * The threads in position order. Hands back the same array when it is already
 * in order, which is every list the server sends: a new array on every
 * broadcast would re-render everything downstream for nothing.
 */
export function byPosition(threads: ThreadMeta[]): ThreadMeta[] {
  for (let i = 1; i < threads.length; i++) {
    if (compare(threads[i - 1], threads[i]) > 0) return [...threads].sort(compare);
  }
  return threads;
}

/**
 * A position between two neighbours. Either can be missing: dropped at the
 * top there is nothing above, at the bottom nothing below, and into an empty
 * list neither.
 */
export function positionBetween(above?: number, below?: number): number {
  if (above === undefined && below === undefined) return 0;
  if (above === undefined) return below! - 1;
  if (below === undefined) return above + 1;
  return (above + below) / 2;
}

/**
 * Where a dragged thread goes. `rows` is what the user was looking at, in
 * order, the dragged one included; `to` is the index it was dropped at among
 * the others. Null when it went back where it came from, which is no move and
 * nothing to send.
 */
export function dropPosition(rows: ThreadMeta[], id: string, to: number): number | null {
  const from = rows.findIndex((s) => s.id === id);
  if (from < 0 || to === from) return null;
  const rest = rows.filter((s) => s.id !== id);
  const at = Math.max(0, Math.min(to, rest.length));
  return positionBetween(rest[at - 1]?.position, rest[at]?.position);
}

/** The list with some threads moved ahead of the server, in its new order. */
export function withPositions(
  threads: ThreadMeta[],
  positions: ReadonlyMap<string, number>,
): ThreadMeta[] {
  if (positions.size === 0) return byPosition(threads);
  return byPosition(
    threads.map((s) => {
      const position = positions.get(s.id);
      return position === undefined || position === s.position ? s : { ...s, position };
    }),
  );
}
