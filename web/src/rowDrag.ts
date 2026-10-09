/**
 * The geometry of dragging one row through a list, kept apart from the
 * pointer handling so it can be tested without a layout engine.
 *
 * Everything is measured once, when the drag starts, and never again: the
 * other rows only ever move by a transform, which getBoundingClientRect would
 * report back, so re-measuring mid-drag would chase the animation instead of
 * the layout. Indexes are into the list as it was at the start; `to` is an
 * index among the *other* rows, the shape `dropPosition` takes.
 */

/**
 * Where the dragged row would land: one slot past every other row whose
 * middle it has passed. `centers` are the rows' resting midpoints, the
 * dragged one's included; `center` is the dragged row's midpoint now.
 *
 * Reaching a middle counts as passing it. The drag is held between the first
 * and last row, so with rows of one height the dragged row's middle can only
 * ever reach theirs, never cross it — and the end slots must still be
 * reachable.
 */
export function dropIndex(centers: number[], from: number, center: number): number {
  let to = 0;
  for (let i = 0; i < centers.length; i++) {
    if (i < from ? centers[i] < center : i > from && centers[i] <= center) to++;
  }
  return to;
}

/**
 * How far another row slides to make room: one slot down if the dragged row
 * has gone above it, one slot up if the dragged row has gone below it, and
 * nowhere otherwise.
 */
export function rowShift(i: number, from: number, to: number, slot: number): number {
  if (i < from && i >= to) return slot;
  if (i > from && i <= to) return -slot;
  return 0;
}

/**
 * How far the list scrolls on its own this frame, with the pointer `y` near
 * an edge of a scroller spanning `top` to `bottom`. Faster the closer it
 * gets, so a long list can be crossed and a short nudge still lands.
 */
export function edgeScroll(y: number, top: number, bottom: number, edge = 48, max = 14): number {
  if (y < top + edge) return -Math.ceil(((top + edge - Math.max(y, top)) / edge) * max);
  if (y > bottom - edge) return Math.ceil(((Math.min(y, bottom) - (bottom - edge)) / edge) * max);
  return 0;
}
