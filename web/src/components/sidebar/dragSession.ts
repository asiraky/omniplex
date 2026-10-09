import type { PointerEvent as ReactPointerEvent } from "react";

import { dropIndex, edgeScroll, rowShift } from "~/rowDrag";

/** How far a pointer travels before a press on a row becomes a drag. Below
    it, the press is a click and selects the thread as it always did. */
const THRESHOLD = 5;

/** Lifts the row being dragged off the list. Literal, so the stylesheet
    keeps them. */
const LIFTED = [
  "bg-sidebar",
  "ring-border",
  "relative",
  "z-20",
  "rounded-lg",
  "shadow-lg",
  "ring-1",
];

/**
 * One press on a row, from pointerdown until it is released, cancelled or
 * torn down. Its own module so `SortableRows` can load it after first paint:
 * nobody drags a row in the first second, and the initial bundle is what a
 * phone on 4G waits for.
 *
 * The rows slide by transforms written straight to the DOM, not through
 * React state: a phone re-rendering a whole run of rows — menus, tooltips and
 * all — on every pointer move is the jank this would otherwise be. On the
 * drop the transforms and their transition come off together, and the list
 * reorders (the move is applied ahead of the server) before the next paint,
 * so nothing animates from its old slot to its new one: it is simply there.
 *
 * `list` is the run's container, one child per row; `from` is the pressed
 * row's index in it. `onDrop` gets the index among the other rows it was
 * released at. The returned function tears the press down without a drop.
 */
export function beginDrag(
  e: ReactPointerEvent,
  list: HTMLElement,
  from: number,
  onDrop: (to: number) => void,
  onEnd: () => void,
): () => void {
  const pointerId = e.pointerId;
  const source = e.currentTarget;
  const startX = e.clientX;
  const startY = e.clientY;
  const rows = () => Array.from(list.children as HTMLCollectionOf<HTMLElement>);
  const scroller = list.closest<HTMLElement>("[data-reorder-scroll]");
  let lastY = startY;
  let live = false;
  let startScroll = 0;
  let tops: number[] = [];
  let centers: number[] = [];
  let slot = 0;
  let to = from;
  let frame = 0;

  const measure = () => {
    const boxes = rows().map((c) => c.getBoundingClientRect());
    tops = boxes.map((b) => b.top);
    centers = boxes.map((b) => b.top + b.height / 2);
    // The gap to the next row counts as part of the slot, or the rows
    // making room would close up a couple of pixels short.
    slot =
      from + 1 < boxes.length
        ? tops[from + 1] - tops[from]
        : from > 0
          ? tops[from] - tops[from - 1]
          : boxes[from].height;
    startScroll = scroller?.scrollTop ?? 0;
  };

  const update = () => {
    // Measured in the scroller's content, so a list scrolling under a
    // still finger carries the row along with it.
    const scrolled = (scroller?.scrollTop ?? 0) - startScroll;
    // Held inside its own run: the row can reach the first and last slot
    // and no further, so it never appears to be headed for another group.
    const dy = Math.min(
      tops[tops.length - 1] - tops[from],
      Math.max(tops[0] - tops[from], lastY - startY + scrolled),
    );
    to = dropIndex(centers, from, centers[from] + dy);
    rows().forEach((row, i) => {
      const by = i === from ? dy : rowShift(i, from, to, slot);
      row.style.transform = by ? `translateY(${by}px)` : "";
    });
  };

  const lift = () => {
    // Others slide aside; the dragged row follows the pointer exactly, so
    // it gets no transition of its own. Reduced motion slides nothing.
    const still = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    rows().forEach((row, i) => {
      if (i === from) row.classList.add(...LIFTED);
      else if (!still) row.style.transition = "transform 150ms ease-out";
    });
  };

  const settle = () => {
    rows().forEach((row) => {
      row.classList.remove(...LIFTED);
      row.style.transform = "";
      row.style.transition = "";
    });
  };

  const tick = () => {
    frame = requestAnimationFrame(tick);
    if (!scroller) return;
    const box = scroller.getBoundingClientRect();
    const by = edgeScroll(lastY, box.top, box.bottom);
    if (by === 0) return;
    const before = scroller.scrollTop;
    scroller.scrollTop += by;
    if (scroller.scrollTop !== before) update();
  };

  const onMove = (m: PointerEvent) => {
    if (m.pointerId !== pointerId) return;
    lastY = m.clientY;
    if (!live) {
      if (Math.hypot(m.clientX - startX, m.clientY - startY) < THRESHOLD) return;
      live = true;
      measure();
      lift();
      document.documentElement.classList.add("select-none");
      // Held by the row, so a release outside the window still ends it.
      try {
        source.setPointerCapture(pointerId);
      } catch {
        // Already released: the pointerup is on its way regardless.
      }
      window.getSelection()?.removeAllRanges();
      frame = requestAnimationFrame(tick);
    }
    m.preventDefault();
    update();
  };

  const end = (commit: boolean) => {
    const wasLive = live;
    cleanup();
    if (!wasLive) return;
    // The release lands on the row's button and would select the thread
    // the user was only moving. Swallowed once, in capture, before React
    // sees it; dropped on the next tick in case the release missed the row.
    const swallow = (c: Event) => {
      c.stopPropagation();
      c.preventDefault();
    };
    window.addEventListener("click", swallow, { capture: true, once: true });
    setTimeout(() => window.removeEventListener("click", swallow, { capture: true }), 0);
    if (commit) onDrop(to);
  };
  const onUp = (u: PointerEvent) => {
    if (u.pointerId === pointerId) end(true);
  };
  const onCancel = (c: PointerEvent) => {
    if (c.pointerId === pointerId) end(false);
  };
  const onKey = (k: KeyboardEvent) => {
    if (k.key === "Escape" && live) {
      k.preventDefault();
      k.stopPropagation();
      end(false);
    }
  };
  // A finger on the handle must not scroll the page: touch-action covers
  // the start of the gesture, this covers browsers that read it late.
  const onTouchMove = (t: TouchEvent) => {
    if (live) t.preventDefault();
  };

  function cleanup() {
    cancelAnimationFrame(frame);
    if (live) settle();
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onUp);
    window.removeEventListener("pointercancel", onCancel);
    window.removeEventListener("keydown", onKey, { capture: true });
    window.removeEventListener("touchmove", onTouchMove);
    document.documentElement.classList.remove("select-none");
    onEnd();
  }

  window.addEventListener("pointermove", onMove);
  window.addEventListener("pointerup", onUp);
  window.addEventListener("pointercancel", onCancel);
  window.addEventListener("keydown", onKey, { capture: true });
  window.addEventListener("touchmove", onTouchMove, { passive: false });
  return cleanup;
}
