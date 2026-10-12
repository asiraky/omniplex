import {
  type FocusEvent as ReactFocusEvent,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  useEffect,
  useRef,
  useState,
} from "react";

/**
 * Open state for a popover that opens on hover or focus with a mouse, and on
 * tap with a finger. Spread `trigger` on the PopoverTrigger and `content` on
 * the PopoverContent, and pass `open`/`setOpen` to the Popover.
 */
export function useHoverPopover() {
  const [open, setOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  // Whether the press that is about to click came from a pointer that hovers.
  const hoverPress = useRef(false);
  useEffect(() => () => clearTimeout(timer.current), []);

  const openSoon = () => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setOpen(true), 150);
  };
  // A grace period, not an immediate close: the pointer has to cross the gap
  // between the trigger and the popover, and moving onto the popover cancels it.
  const closeSoon = () => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setOpen(false), 120);
  };
  const cancelClose = () => clearTimeout(timer.current);
  // Touch has no hover: after a tap the pointer immediately leaves, and a
  // finger cannot move onto the portalled popover to cancel a scheduled close.
  // So hover-open/close is mouse-only; touch falls through to the trigger's
  // click, which Radix toggles, and an outside tap closes it.
  const hoverOnly = (fn: () => void) => (e: ReactPointerEvent) => {
    if (e.pointerType !== "touch") fn();
  };

  return {
    open,
    setOpen,
    trigger: {
      onPointerEnter: hoverOnly(openSoon),
      onPointerLeave: hoverOnly(closeSoon),
      onPointerDown: (e: ReactPointerEvent) => {
        hoverPress.current = e.pointerType !== "touch";
      },
      // Radix toggles on click. A mouse has usually opened it by hovering
      // first, so a click would shut what the user is reaching for: swallow it.
      // A tap toggles as normal, and so does Enter or Space from the keyboard.
      onClick: (e: ReactMouseEvent) => {
        if (open && hoverPress.current) e.preventDefault();
        hoverPress.current = false;
      },
      // Keyboard focus only. A tap focuses the button just before its click,
      // and opening here would have that click close it again.
      onFocus: (e: ReactFocusEvent<HTMLElement>) => {
        if (e.currentTarget.matches(":focus-visible")) setOpen(true);
      },
      onBlur: closeSoon,
    },
    content: {
      onOpenAutoFocus: (e: Event) => e.preventDefault(),
      onPointerEnter: hoverOnly(cancelClose),
      onPointerLeave: hoverOnly(closeSoon),
    },
  };
}
