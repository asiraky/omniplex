import { useCallback, useEffect, useRef, useState } from "react";

// Following the tail of a transcript is two behaviours pretending to be one:
// the view snaps to the bottom as content arrives, and the reader is allowed
// to leave. A distance-from-the-bottom heuristic on its own cannot tell the
// two apart — a snap and a scroll-up look identical to the scroll event — so
// while text streams the reader loses every argument with it.
//
// This tracks the pin as intent instead. It is broken by anything that reads
// as "the reader moved the view upwards", and re-armed only by arriving at the
// bottom, which either the reader does themselves or the button does for them.

// Arriving at the bottom re-arms the pin. The tolerance is small on purpose:
// a generous band would re-pin a reader who nudged the view a little and
// expected it to stay put. Sub-pixel scroll positions (fractional zoom,
// high-DPI layouts) are why it is not zero.
const AT_BOTTOM_PX = 8;

// Exported for the resume cache, which records "was at the tail" with the
// same tolerance the pin itself uses.
export const atBottom = (el: HTMLElement) =>
  el.scrollHeight - el.scrollTop - el.clientHeight <= AT_BOTTOM_PX;

// Nothing to leave, so nothing to read as leaving: a transcript that fits its
// window answers a wheel or a swipe with no movement at all, and taking that
// as intent would strand the pin off and the button on for a view that is
// already showing everything there is.
const canScroll = (el: HTMLElement) => el.scrollHeight - el.clientHeight > AT_BOTTOM_PX;

// The strip of view left above a message the transcript was asked to anchor —
// enough that it reads as "at the top" without sitting on the edge.
const ANCHOR_GAP_PX = 16;

// How far an element sits down the scroller's content, in layout terms.
//
// Deliberately not `getBoundingClientRect`: a rect is the *painted* box, and
// the row this is asked about has only just mounted, so its `fade-in` is still
// playing and the paint is up to 3px below where the row is going to settle.
// Anchoring to that parks the view 3px off and then snaps it back the first
// time anything resizes — a small, very visible flick a moment after every
// prompt is sent. `offsetTop` is the layout box, which is where the animation
// is heading and where the row will be for the rest of its life.
//
// Walking to the document and subtracting, rather than reading one offset,
// because `offsetTop` is relative to the nearest positioned ancestor and
// neither the scroller nor the content is required to be one — whatever that
// ancestor turns out to be, it is on both walks and cancels.
const layoutTop = (el: HTMLElement) => {
  let y = 0;
  for (let node: HTMLElement | null = el; node; node = node.offsetParent as HTMLElement | null)
    y += node.offsetTop;
  return y;
};

/**
 * Keeps a scroller pinned to its own bottom while content grows, and gives up
 * the pin the moment the reader scrolls up.
 *
 * `scrollerRef` goes on the scrolling element and `contentRef` on the element
 * inside it whose height changes — text revealed between renders grows the
 * content without React knowing, so the pin has to watch the box, not state.
 * `stick()` is for the cases React does drive; it is a no-op while unpinned.
 *
 * `initialPinned` is for a scroller restoring a saved position: mounting
 * pinned would snap it straight to the bottom before the restore could land.
 *
 * `anchorTo(el)` is the other half: it lifts one element up near the top of the
 * view and holds it there while the space below fills. The room to do that
 * rarely exists — an element at the bottom of the transcript cannot rise
 * without something underneath it — so the hook makes it, publishing the
 * shortfall as `--anchor-reserve` on the content element for the caller's
 * padding to consume. The reserve shrinks as real content takes its place, and
 * once the new content fills the view on its own the anchor gives way to the
 * pin, so a long answer ends up followed rather than frozen.
 */
export function useAutoScroll<S extends HTMLElement, C extends HTMLElement>(
  initialPinned = true,
) {
  const scrollerRef = useRef<S>(null);
  const contentRef = useRef<C>(null);

  // The pin is state because a button hangs off it, and a ref because the
  // scroll and resize handlers below read it outside of React's world.
  const [pinned, setPinned] = useState(initialPinned);
  const pinnedRef = useRef(initialPinned);

  // The last scroll position we saw. A drop from it is the one signal that
  // catches scrollbar drags, which produce no other event of their own.
  const lastTop = useRef(0);

  const setPin = useCallback((next: boolean) => {
    if (pinnedRef.current === next) return;
    pinnedRef.current = next;
    setPinned(next);
  }, []);

  const jumpToBottom = useCallback(() => {
    const el = scrollerRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
    lastTop.current = el.scrollTop;
  }, []);

  // The element the view is holding high, the reserve currently making room
  // for it, and whether that hold is still ours to enforce — a reader who
  // scrolls takes the view back, but the room they can see stays until content
  // grows into it, so it is not thrown away with the hold.
  const anchorEl = useRef<HTMLElement | null>(null);
  const anchored = useRef(false);
  const reserve = useRef(0);
  // Whether the hold has yet been acted on — the first application is the one
  // that lifts, and it is judged differently from the ones that follow.
  const fresh = useRef(false);

  const setReserve = useCallback((px: number) => {
    if (reserve.current === px) return;
    reserve.current = px;
    contentRef.current?.style.setProperty("--anchor-reserve", `${px}px`);
  }, []);

  const clearAnchor = useCallback(() => {
    anchored.current = false;
    anchorEl.current = null;
    setReserve(0);
  }, [setReserve]);

  const stick = useCallback(() => {
    const el = scrollerRef.current;
    if (!pinnedRef.current || !el) return;
    // A scrollbar drag moves the view and says nothing else about it: its
    // scroll event is queued, and if content arrives first this snap would
    // overwrite the position before anyone read it — the exact fight this
    // hook exists to end. So the position is checked against the one we last
    // wrote before it is written over. A drop that ends at the bottom is not
    // a reader: that is the view being clamped by content going away.
    if (el.scrollTop < lastTop.current - 1 && !atBottom(el)) {
      setPin(false);
      return;
    }
    jumpToBottom();
  }, [jumpToBottom, setPin]);

  // The button's action: animate down and take the pin back. Re-arming the pin
  // before the animation finishes is deliberate — content arriving mid-flight
  // should be followed, not chased.
  const scrollToBottom = useCallback(() => {
    const el = scrollerRef.current;
    if (!el) return;
    clearAnchor();
    setPin(true);
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduced || typeof el.scrollTo !== "function") {
      jumpToBottom();
      return;
    }
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [clearAnchor, jumpToBottom, setPin]);

  // Size the reserve to whatever the anchored element still needs to sit high
  // in the view, and — while the hold is ours — put it there. Both halves run
  // on every content change: the measurement is what retires the reserve as
  // the answer grows into it.
  const applyAnchor = useCallback(() => {
    const el = scrollerRef.current;
    const target = anchorEl.current;
    if (!el || !target?.isConnected) return clearAnchor();

    const offset = layoutTop(target) - layoutTop(el);
    const desired = Math.max(0, offset - ANCHOR_GAP_PX);
    // What lies below the anchor on its own merits: the reserve is discounted,
    // or it would keep justifying itself.
    const natural = el.scrollHeight - reserve.current - desired;
    const need = Math.max(0, Math.ceil(el.clientHeight - natural));
    setReserve(need);

    // Needing no room means the content below the anchor already fills the
    // view — which on the first application is not a turn outgrowing anything,
    // it is a tall prompt under a tall composer that had the room all along.
    // Reading it as "outgrown" there would hand the view straight back to the
    // tail and undo the lift before it happened.
    if (need === 0 && !fresh.current) {
      // The turn has outgrown the room made for it, so there is nothing left
      // to hold up. Someone watching output arrive wants the tail from here.
      const takeOver = anchored.current;
      clearAnchor();
      if (takeOver) {
        setPin(true);
        jumpToBottom();
      }
      return;
    }
    fresh.current = false;
    if (!anchored.current) return;
    el.scrollTop = desired;
    lastTop.current = el.scrollTop;
  }, [clearAnchor, jumpToBottom, setPin, setReserve]);

  /**
   * Lift `el` up near the top of the view and keep it there while the space
   * beneath it fills. Unpinning is the point rather than a side effect: the
   * tail is exactly where this is asking not to be.
   */
  const anchorTo = useCallback(
    (el: HTMLElement | null) => {
      if (!el) return;
      anchorEl.current = el;
      anchored.current = true;
      fresh.current = true;
      setPin(false);
      applyAnchor();
    },
    [applyAnchor, setPin],
  );

  useEffect(() => {
    const el = scrollerRef.current;
    if (!el) return;

    // A scroller can mount already scrolled — a restored position, a browser
    // restoring one for us — and the first movement has to be measured from
    // where it actually is rather than from the top.
    lastTop.current = el.scrollTop;

    const onScroll = () => {
      const top = el.scrollTop;
      // Every position this hook writes is recorded as it is written, so a
      // position that is not the recorded one came from the reader — and a
      // reader who moves the view has taken it back from the anchor.
      // Unless it is a drop that ends at the bottom: the reserve is sized so
      // the anchored position *is* the last screenful, so the view arriving
      // there from below is the browser clamping as content above the anchor
      // shrinks (a card collapsing, a run folding), not a hand on the wheel.
      // Reading that as intent lost the hold to every collapse that happened
      // to land mid-turn, and left the reserve behind with nothing holding it
      // up. It has to be a drop: clamping can only ever move the view up, so
      // a move *down* to the bottom is a reader who has gone looking for the
      // tail, and yanking them back to the prompt is the whole complaint.
      const clamped = top < lastTop.current && atBottom(el);
      if (anchored.current && Math.abs(top - lastTop.current) > 1 && !clamped)
        anchored.current = false;
      // A position that went up — under the reader's hand or the scrollbar's
      // — ends the pin, and it does so even a few pixels from the bottom:
      // re-arming inside the tolerance would undo a small deliberate nudge
      // and hand the view straight back to the stream. Movement up that ends
      // at the bottom is not the reader at all, it is the view being clamped
      // by content going away, and it leaves the pin as it found it. Arriving
      // at the bottom any other way re-arms: that is the position the pin
      // itself would hold, so there is nothing left to preserve.
      // While an anchor holds, "at the bottom" is an artefact of the room the
      // anchor reserved: the reserve is sized so the anchored position *is*
      // the last screenful, so the hook's own scroll would otherwise re-arm
      // the pin against the hold it just took — and the pin's snap would then
      // fight the anchor for the rest of the turn.
      const movedUp = top < lastTop.current;
      if (movedUp && !atBottom(el)) setPin(false);
      else if (!movedUp && atBottom(el) && !anchored.current) setPin(true);
      lastTop.current = top;
    };

    // Wheel, touch and keys are read as intent directly rather than waiting to
    // see the movement. During a fast stream the snap can land in the same
    // frame as the reader's scroll and cancel it out, leaving nothing for the
    // scroll handler to notice; the intent was still real.
    const onWheel = (e: WheelEvent) => {
      if (e.deltaY < 0 && canScroll(el)) {
        anchored.current = false;
        setPin(false);
      }
    };

    let touchY = 0;
    const onTouchStart = (e: TouchEvent) => {
      touchY = e.touches[0]?.clientY ?? 0;
    };
    const onTouchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY ?? 0;
      // A finger travelling down the screen drags the content down, which is
      // to say it scrolls the view up.
      if (y > touchY && canScroll(el)) {
        anchored.current = false;
        setPin(false);
      }
      touchY = y;
    };

    const UP_KEYS = new Set(["ArrowUp", "PageUp", "Home"]);
    const onKeyDown = (e: KeyboardEvent) => {
      if (UP_KEYS.has(e.key) && canScroll(el)) {
        anchored.current = false;
        setPin(false);
      }
    };

    el.addEventListener("scroll", onScroll, { passive: true });
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchstart", onTouchStart, { passive: true });
    el.addEventListener("touchmove", onTouchMove, { passive: true });
    el.addEventListener("keydown", onKeyDown);
    return () => {
      el.removeEventListener("scroll", onScroll);
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchstart", onTouchStart);
      el.removeEventListener("touchmove", onTouchMove);
      el.removeEventListener("keydown", onKeyDown);
    };
  }, [setPin]);

  // Text is revealed between event ticks, so following the tail has to watch
  // the content's size rather than React state.
  useEffect(() => {
    const scroller = scrollerRef.current;
    const content = contentRef.current;
    if (!scroller || !content || typeof ResizeObserver === "undefined") return;
    // Watch the border box, not the content box: the tail's reserved room is
    // bottom padding that tracks the floating composer, and a composer growing
    // past that headroom would otherwise creep over the tail without ever
    // resizing the content box — so the pin would never re-snap to clear it.
    const ro = new ResizeObserver(() => {
      // Order matters: the anchor gets to retire its reserve first, so a turn
      // that has outgrown it hands the view to the pin in the same tick rather
      // than one frame late.
      if (anchorEl.current) applyAnchor();
      if (!anchored.current) stick();
    });
    ro.observe(content, { box: "border-box" });
    return () => ro.disconnect();
  }, [applyAnchor, stick]);

  return { scrollerRef, contentRef, pinned, stick, scrollToBottom, anchorTo };
}
