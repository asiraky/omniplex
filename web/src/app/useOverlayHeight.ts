import { useEffect, useRef } from "react";

/**
 * The floating overlay (composer plus any permission/elicitation prompt
 * stacked above it) and the column it floats over. We measure the first and
 * publish its height on the second, so the transcript can reserve exactly
 * that much room beneath its content.
 *
 * Nothing measures the composer on its own, so a fixed padding could only
 * ever guess at its height, and it grows (a tall draft, a permission prompt
 * appearing above it) well past any guess. A ResizeObserver on the whole
 * overlay keeps `--composer-h` exactly right, and the transcript reserves
 * `that + headroom` below its tail. Grow the overlay and the content above it
 * visibly rises: it reads as the composer pushing the transcript up, even
 * though it is floating.
 *
 * The same observer publishes the transcript's scrollbar width as
 * `--scrollbar-w`, so the fades and the composer stop short of the scrollbar
 * instead of painting over it. It is 0 on a phone, whose scrollbars float, and
 * whatever the browser makes it on a desktop. Observing the scroller's content
 * box catches the scrollbar coming and going as the transcript grows.
 */
export function useOverlayHeight(threadId: string | null) {
  const layoutRef = useRef<HTMLDivElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (typeof ResizeObserver === "undefined") return;
    const overlay = overlayRef.current;
    const layout = layoutRef.current;
    const scroller = layout?.querySelector<HTMLElement>("[data-transcript-scroller]");
    if (!overlay || !layout) return;
    const apply = () => {
      layout.style.setProperty(
        "--composer-h",
        `${Math.ceil(overlay.getBoundingClientRect().height)}px`,
      );
      layout.style.setProperty(
        "--scrollbar-w",
        `${scroller ? scroller.offsetWidth - scroller.clientWidth : 0}px`,
      );
    };
    apply();
    const ro = new ResizeObserver(apply);
    ro.observe(overlay);
    if (scroller) ro.observe(scroller);
    return () => {
      ro.disconnect();
      layout.style.removeProperty("--composer-h");
      layout.style.removeProperty("--scrollbar-w");
    };
    // The transcript is keyed by thread, so a new thread is a new scroller.
  }, [threadId]);
  return { layoutRef, overlayRef };
}
