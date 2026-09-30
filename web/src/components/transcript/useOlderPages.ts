import { useEffect, useLayoutEffect, useRef, type RefObject } from "react";

import type { ThreadState } from "~/protocol";
import { useLatest } from "~/useLatest";

/**
 * Paging older history in above the reader: asks for the page above the
 * loaded window as the top comes near, and holds the view still when it lands.
 * Returns the ref for the sentinel row at the top of the content.
 */
export function useOlderPages(
  scrollerRef: RefObject<HTMLDivElement | null>,
  contentRef: RefObject<HTMLDivElement | null>,
  state: ThreadState,
  hasOlder: boolean,
  onLoadOlder: (() => void) | undefined,
) {
  // Older pages prepend above the reader, and without correction the view
  // would stay at the same scrollTop — which is now a page higher in the
  // conversation than where they were reading. The correction needs the
  // scroller's height from *before* the prepend, and the tail can grow between
  // React commits (text streams outside React's knowledge), so the height is
  // tracked continuously by a ResizeObserver rather than sampled per render.
  // Observer callbacks fire after layout effects, so at the moment the prepend
  // effect below runs, lastHeight still holds the pre-prepend height.
  const lastHeight = useRef(0);
  useEffect(() => {
    const el = scrollerRef.current;
    const content = contentRef.current;
    if (!el || !content || typeof ResizeObserver === "undefined") return;
    lastHeight.current = el.scrollHeight;
    const ro = new ResizeObserver(() => {
      lastHeight.current = el.scrollHeight;
    });
    ro.observe(content, { box: "border-box" });
    return () => ro.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // A prepend is a drop in itemsBefore together with a new first item; a
  // snapshot replacing the state wholesale changes seq too and is left to the
  // pin/restore logic, not treated as reading history.
  const prevItemsBefore = useRef(state.itemsBefore ?? 0);
  const prevFirstItem = useRef(state.items[0]?.id);
  useLayoutEffect(() => {
    const el = scrollerRef.current;
    const before = state.itemsBefore ?? 0;
    const prepended =
      before < prevItemsBefore.current && state.items[0]?.id !== prevFirstItem.current;
    prevItemsBefore.current = before;
    prevFirstItem.current = state.items[0]?.id;
    if (!el || !prepended) return;
    const delta = el.scrollHeight - lastHeight.current;
    if (delta > 0) el.scrollTop += delta;
    lastHeight.current = el.scrollHeight;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.items, state.itemsBefore]);

  // The trigger: a sentinel at the top of the content, watched against the
  // scroller with a margin so the fetch starts before the reader hits the
  // edge. Reconnected each time a page lands (itemsBefore changes), because an
  // observer only reports crossings — after a short page the sentinel can
  // still be inside the margin, and re-observing is what re-fires it.
  const sentinelRef = useRef<HTMLDivElement>(null);
  const onLoadOlderRef = useLatest(onLoadOlder);
  useEffect(() => {
    const sentinel = sentinelRef.current;
    const root = scrollerRef.current;
    if (!hasOlder || !sentinel || !root || typeof IntersectionObserver === "undefined") return;
    let visible = false;
    const io = new IntersectionObserver(
      (entries) => {
        visible = entries.some((e) => e.isIntersecting);
        if (visible) onLoadOlderRef.current?.();
      },
      { root, rootMargin: "800px 0px 0px 0px" },
    );
    io.observe(sentinel);
    // An observer reports crossings, and a failed page is not one: the fetch
    // dies, itemsBefore never changes, the sentinel just sits there inside the
    // margin and the spinner spins forever. While it is visible, keep asking —
    // the client dedups in-flight requests, so on the happy path this is a
    // handful of no-ops and on the sad path it is the retry.
    const retry = window.setInterval(() => {
      if (visible) onLoadOlderRef.current?.();
    }, 3000);
    return () => {
      io.disconnect();
      window.clearInterval(retry);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasOlder, state.itemsBefore]);

  return sentinelRef;
}
