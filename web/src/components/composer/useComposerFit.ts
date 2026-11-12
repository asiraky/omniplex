import { useLayoutEffect, useState, type RefObject } from "react";

// Measured against the box, not the viewport: the composer is as narrow on an
// iPad with the sidebar and the panel open (~230px) as on a phone, at a
// viewport that clears every breakpoint.

/** Below this the model trigger names the model and nothing else: the logo,
    the effort, the chevron and the word "Fast" are what it gives up first. */
export const COMPACT_TRIGGER_PX = 420;
/** Below this a draft's settings (permissions, 1M, pin) move out of the
    toolbar into the drawer under the box, so the model's name keeps the room. */
export const TOOLS_IN_ROW_PX = 560;

export interface ComposerFit {
  /** The model trigger shows only the model's name. */
  compactTrigger: boolean;
  /** Extra tools sit in the toolbar rather than the drawer. */
  toolsInRow: boolean;
}

/**
 * How much the composer's toolbar can hold at the box's current width.
 *
 * Unmeasured (no ResizeObserver, or before layout) it assumes room for
 * everything: that is the desktop the layout was drawn for.
 */
export function useComposerFit(box: RefObject<HTMLElement | null>): ComposerFit {
  const [width, setWidth] = useState<number>();
  // Layout effect, so the first paint is already the right arrangement rather
  // than the wide one snapping to the narrow.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    setWidth(el.clientWidth);
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => setWidth(el.clientWidth));
    observer.observe(el);
    return () => observer.disconnect();
  }, [box]);
  return fitFor(width);
}

/** The arrangement for a box this wide; undefined is unmeasured. */
export function fitFor(width: number | undefined): ComposerFit {
  if (width === undefined || width === 0) return { compactTrigger: false, toolsInRow: true };
  return { compactTrigger: width < COMPACT_TRIGGER_PX, toolsInRow: width >= TOOLS_IN_ROW_PX };
}
