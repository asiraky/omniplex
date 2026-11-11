import { layoutTop } from "~/useAutoScroll";

// Remembering a scroll position as a pixel offset only holds while nothing
// above it changes height, and in a transcript something always does: a
// diagram renders a beat after the markdown around it, an image loads, a run
// folds, a font swaps in. Each one shoves everything below it, so a restored
// `scrollTop` lands the reader a screen away from the paragraph they left.
//
// So the position is remembered as "this row, this far into it" — the row at
// the top of the view, which is the one being read — with the raw offset kept
// as the fallback for a row that no longer exists. Restoring finds the row and
// puts the view back on it, then keeps putting it back for a few frames while
// late content above it settles.

/** The row at the top of the view, and how far the view's top edge sits into it. */
export type RowAnchor = { id: string; offset: number };

export type ScrollPosition = { top: number; atBottom: boolean; anchor?: RowAnchor };

const ROW = "[data-row-id]";

// A row only has to hold still this many frames in a row to count as settled.
const STABLE_FRAMES = 2;
// A streaming reply or a slow diagram can keep moving things for as long as it
// likes; past this the reader has their place back and anything later is theirs.
const MAX_FRAMES = 60;

// Any of these on the scroller means a reader has taken the view; a settle
// still running would drag it back out from under their finger.
const INTENT = ["wheel", "touchstart", "touchmove", "pointerdown", "keydown"] as const;

// Where a row sits down the scroller's content. A row is a `display: contents`
// wrapper so that tagging it adds no box to the transcript's flex layout —
// which also means it has no box to measure, so its span is read from its
// first and last children instead.
function span(row: Element, base: number): { top: number; bottom: number } | undefined {
  const first = row.firstElementChild as HTMLElement | null;
  const last = row.lastElementChild as HTMLElement | null;
  if (!first || !last) return undefined;
  return { top: layoutTop(first) - base, bottom: layoutTop(last) + last.offsetHeight - base };
}

/**
 * The row the reader is looking at: the first whose bottom is below the top
 * edge of the view. Called as the view is left rather than on every scroll, so
 * the hot path on a phone stays a read of `scrollTop`; a binary search keeps
 * even a long transcript to a handful of measurements.
 */
export function captureAnchor(scroller: HTMLElement): RowAnchor | undefined {
  const rows = scroller.querySelectorAll(ROW);
  const base = layoutTop(scroller);
  const top = scroller.scrollTop;
  // Rows run down the page in document order, so "bottom is below the edge"
  // is false and then true. A row that rendered nothing has no span; it is
  // treated as above the edge, which at worst settles on a later row — still a
  // row near the view, and the offset is measured from whichever is chosen.
  let lo = 0;
  let hi = rows.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    const s = span(rows[mid], base);
    if (!s || s.bottom <= top) lo = mid + 1;
    else hi = mid;
  }
  // Nothing below the edge means the view is down among what follows the rows
  // (queued prompts, the merged card), so it is held relative to the last row.
  for (let i = Math.min(lo, rows.length - 1); i >= 0; i--) {
    const s = span(rows[i], base);
    const id = rows[i].getAttribute("data-row-id");
    if (s && id) return { id, offset: top - s.top };
  }
  return undefined;
}

function findRow(scroller: HTMLElement, id: string): Element | undefined {
  for (const row of scroller.querySelectorAll(ROW)) if (row.getAttribute("data-row-id") === id) return row;
  return undefined;
}

/**
 * Puts the view back where `position` says, and keeps it there while what is
 * above it settles. Returns a cancel for the settle.
 *
 * By anchor when the row is still here; by raw offset when it is not, which is
 * the old behaviour and as good as anything for a row that has gone. The
 * settle stops after two still frames, after `MAX_FRAMES` regardless, on any
 * sign of a reader, or when something else moves the view somewhere other
 * than the anchor — the pin re-arming at the tail, a just-sent prompt being
 * lifted, a scrollbar drag. A prepend of older history moves the view and the
 * row by the same amount, so it never reads as a rival.
 */
export function restoreScroll(scroller: HTMLElement, position: ScrollPosition): () => void {
  const anchor = position.anchor;
  const row = anchor && findRow(scroller, anchor.id);
  if (!anchor || !row) {
    scroller.scrollTop = position.top;
    return () => {};
  }

  const target = () => {
    if (!row.isConnected) return undefined;
    const s = span(row, layoutTop(scroller));
    if (!s) return undefined;
    const max = scroller.scrollHeight - scroller.clientHeight;
    return Math.max(0, Math.min(s.top + anchor.offset, max));
  };

  let frames = 0;
  let still = 0;
  let raf = 0;
  let done = false;
  // Where this loop last left the view, to tell its own position from one
  // somebody else wrote.
  let left = scroller.scrollTop;

  const stop = () => {
    if (done) return;
    done = true;
    cancelAnimationFrame(raf);
    for (const type of INTENT) scroller.removeEventListener(type, stop);
  };

  const step = () => {
    const want = target();
    if (want === undefined) return stop();
    const at = scroller.scrollTop;
    if (Math.abs(at - left) > 1 && Math.abs(at - want) > 1) return stop();
    if (Math.abs(at - want) > 1) {
      scroller.scrollTop = want;
      still = 0;
    } else {
      still += 1;
    }
    left = scroller.scrollTop;
    frames += 1;
    if (still >= STABLE_FRAMES || frames >= MAX_FRAMES) return stop();
    raf = requestAnimationFrame(step);
  };

  for (const type of INTENT) scroller.addEventListener(type, stop, { passive: true });
  // The first application is synchronous so the first paint is already in
  // place; the frames after it only chase what moves.
  step();
  return stop;
}
