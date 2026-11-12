import { useEffect, useRef, type MouseEvent, type PointerEvent } from "react";

const HOLD_MS = 450;
// A finger drifts while it holds; past this it is a drag or a scroll.
const SLOP_PX = 8;

/**
 * Holding a button down for a moment, as a phone's stand-in for a right click.
 * Spread the handlers on the button and pass its click through `click`: a hold
 * that fired swallows the click its release would otherwise send, so holding
 * Send opens the menu without also sending.
 */
export function useLongPress(enabled: boolean, onLongPress: () => void) {
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const start = useRef<{ x: number; y: number } | null>(null);
  const fired = useRef(false);

  const cancel = () => {
    clearTimeout(timer.current);
    timer.current = undefined;
    start.current = null;
  };
  useEffect(() => cancel, []);
  // Disarmed mid-hold — the turn ended under the finger — the hold is void.
  useEffect(() => {
    if (!enabled) cancel();
  }, [enabled]);

  const handlers = {
    onPointerDown: (e: PointerEvent) => {
      // A fired hold whose release never clicked (the menu caught it) must
      // not swallow the next real tap.
      fired.current = false;
      if (!enabled || e.button !== 0) return;
      cancel();
      start.current = { x: e.clientX ?? 0, y: e.clientY ?? 0 };
      timer.current = setTimeout(() => {
        timer.current = undefined;
        start.current = null;
        fired.current = true;
        onLongPress();
      }, HOLD_MS);
    },
    onPointerMove: (e: PointerEvent) => {
      const s = start.current;
      if (!s) return;
      if (Math.hypot((e.clientX ?? 0) - s.x, (e.clientY ?? 0) - s.y) > SLOP_PX) cancel();
    },
    onPointerUp: cancel,
    onPointerLeave: cancel,
    onPointerCancel: cancel,
    // Android answers a hold with the context menu, which would land on top of
    // ours.
    onContextMenu: (e: MouseEvent) => {
      if (enabled) e.preventDefault();
    },
  };

  /** Wraps the button's click so the one ending a fired hold does nothing. */
  const click = (action: () => void) => () => {
    if (fired.current) {
      fired.current = false;
      return;
    }
    action();
  };

  return { handlers, click };
}
