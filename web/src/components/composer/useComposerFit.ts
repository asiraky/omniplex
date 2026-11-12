import { useLayoutEffect, useState, type RefObject } from "react";

// Measured against the box, not the viewport: the composer is as narrow on an
// iPad with the sidebar and the panel open (~230px) as on a phone, at a
// viewport that clears every breakpoint.

/** Below this the attach, meter, mic, stop and send buttons alone can be wider
    than the toolbar (a running turn with a message typed shows stop and send
    both), so the model trigger takes a line of its own above them. */
const TRIGGER_OWN_LINE_PX = 300;
/** Below this the model trigger names the model and nothing else: the logo,
    the effort, the chevron and the word "Fast" are what it gives up first. */
const COMPACT_TRIGGER_PX = 420;
/** Below this a draft's settings (permissions, 1M, pin) move out of the
    toolbar into the drawer under the box, so the model's name keeps the room. */
const TOOLS_IN_ROW_PX = 560;

export interface ComposerFit {
  /** The model trigger sits on a line of its own, where it has room to say
      everything, and the toolbar wraps. */
  triggerOwnLine: boolean;
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

/** The arrangement for a box this wide. Undefined is unmeasured, and so is 0:
    a box that has not been laid out, as in a test, rather than one that is
    really that narrow. */
function fitFor(width: number | undefined): ComposerFit {
  if (!width) return { triggerOwnLine: false, compactTrigger: false, toolsInRow: true };
  const triggerOwnLine = width < TRIGGER_OWN_LINE_PX;
  return {
    triggerOwnLine,
    compactTrigger: !triggerOwnLine && width < COMPACT_TRIGGER_PX,
    toolsInRow: width >= TOOLS_IN_ROW_PX,
  };
}
