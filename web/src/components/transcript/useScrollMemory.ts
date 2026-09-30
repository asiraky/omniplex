import { useEffect, useLayoutEffect, useRef, type RefObject } from "react";

import type { ThreadState } from "~/protocol";
import { saveResume } from "~/resume";
import { atBottom } from "~/useAutoScroll";
import { useLatest } from "~/useLatest";

/**
 * Where the reader was, kept across a thread switch and a discarded tab:
 * restores the parent's saved position once on mount, reports the position up
 * as it moves, and saves the whole state for resume.ts as the page goes to
 * background.
 */
export function useScrollMemory(
  scrollerRef: RefObject<HTMLDivElement | null>,
  state: ThreadState,
  initialScroll: { top: number; atBottom: boolean } | undefined,
  onScrollChange: ((threadId: string, top: number, atBottom: boolean) => void) | undefined,
) {
  // The one-shot restore, from the ref because the prop is cleared after
  // mount and later renders must not re-apply a stale position. A restore
  // that was at the tail needs no offset: the pin starts armed and the
  // stick lands it, exactly as if the reader had never left.
  const initialScrollRef = useRef(initialScroll);
  useLayoutEffect(() => {
    const init = initialScrollRef.current;
    const el = scrollerRef.current;
    if (!el || !init || init.atBottom) return;
    el.scrollTop = init.top;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The other half of resume.ts: as the page goes to background — the moment
  // a mobile browser may discard the tab — save the state and where it was
  // scrolled, so the reload that follows can paint this transcript instead of
  // "Attaching…". The state rides in a ref so the listeners subscribe once
  // rather than per event.
  const latestState = useLatest(state);
  useEffect(() => {
    const save = () => {
      const el = scrollerRef.current;
      if (!el) return;
      saveResume(latestState.current, el.scrollTop, atBottom(el));
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") save();
    };
    document.addEventListener("visibilitychange", onVisibility);
    window.addEventListener("pagehide", save);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pagehide", save);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // The same idea one scope smaller: where the reader is, reported up as it
  // moves, so that switching to another thread and back returns to the place
  // they were reading rather than the bottom. Held in a ref so a new callback
  // identity does not re-subscribe the listener.
  const onScrollChangeRef = useLatest(onScrollChange);
  // A layout effect, because its cleanup has to run while the scroller is
  // still in the document: a detached node reports a scrollTop of zero, and
  // the final read below is the one that catches movement whose scroll event
  // was still queued when the switch happened.
  useLayoutEffect(() => {
    const el = scrollerRef.current;
    if (!el) return;
    const report = () =>
      onScrollChangeRef.current?.(latestState.current.threadId, el.scrollTop, atBottom(el));
    el.addEventListener("scroll", report, { passive: true });
    // Mounting counts as a position too: a transcript left at the tail — or
    // one that just restored an offset above — has somewhere to come back to
    // even if the reader never touches it.
    report();
    return () => {
      el.removeEventListener("scroll", report);
      report();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
