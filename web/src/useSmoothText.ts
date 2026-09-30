import { useEffect, useRef, useState } from "react";

// Harnesses do not stream at the same granularity. The claude CLI coalesces
// text into ~75–125 character deltas at 2–4 per second — roughly a line at a
// time — while codex emits a delta per word. Rendering deltas as they land
// therefore looks jerky on one harness and smooth on the other.
//
// This paces the reveal instead: arriving text is buffered and released at a
// rate derived from how much is waiting, so output appears continuously at
// whatever speed the model is actually producing it. Nothing is invented — only
// the timing of already-received characters is smoothed.

// Backlog is drained over roughly this window, so the reveal rate tracks the
// production rate rather than being a fixed typing speed. Tuned against the
// measured claude pattern: continuous output, ~120 characters behind at worst.
const DRAIN_WINDOW_MS = 200;
const MIN_CHARS_PER_SEC = 80;
// Once the turn ends there is nothing left to pace against, so the remaining
// buffer is flushed briskly instead of snapping the last line into place.
const FINISH_CHARS_PER_SEC = 250;
const MAX_CHARS_PER_SEC = 1500;
// Beyond this the reveal would be a distraction rather than an effect: a
// replayed turn or a pasted block should simply appear.
const SNAP_BACKLOG = 1500;

/**
 * Returns a progressively revealed prefix of `text` while it is still
 * arriving, and the whole of it once the reveal has caught up.
 *
 * Text already present when the component mounts is shown immediately, so
 * attaching to a thread mid-turn does not retype the transcript; only text
 * arriving afterwards is paced.
 */
export function useSmoothText(text: string, active: boolean): string {
  const [revealed, setRevealed] = useState(() => text.length);
  const revealedRef = useRef(revealed);

  // The animation loop below reads the latest text and active flag through
  // refs. They are written after commit rather than during render: the loop
  // only ever runs on a frame boundary, so it never needs a value from a
  // render that React went on to discard.
  const textRef = useRef(text);
  const activeRef = useRef(active);

  // frameRef is the pending animation frame, 0 when the loop is dormant.
  // kickRef restarts a dormant loop; it exists so text arriving after the
  // loop has stopped can wake it without tearing the effect down — cancelling
  // and rescheduling the frame on every chunk would starve the loop entirely
  // when chunks arrive faster than frames.
  const frameRef = useRef(0);
  const kickRef = useRef(() => {});

  useEffect(() => {
    let prev = performance.now();

    const tick = (now: number) => {
      // Clamp dt so a backgrounded tab does not dump the whole buffer on the
      // first frame after it wakes.
      const dt = Math.min(now - prev, 200);
      prev = now;

      const total = textRef.current.length;
      const backlog = total - revealedRef.current;

      if (backlog <= 0) {
        // Caught up. While inactive the loop goes dormant — not permanently:
        // text can still arrive after a block goes inactive (a lifecycle
        // desync, or a block that mounts between turns), and the effect below
        // kicks the loop awake when it does.
        if (!activeRef.current) {
          frameRef.current = 0;
          return;
        }
        frameRef.current = requestAnimationFrame(tick);
        return;
      }

      if (backlog > SNAP_BACKLOG) {
        revealedRef.current = total;
      } else {
        const floor = activeRef.current ? MIN_CHARS_PER_SEC : FINISH_CHARS_PER_SEC;
        const cps = Math.min(
          MAX_CHARS_PER_SEC,
          Math.max(floor, (backlog / DRAIN_WINDOW_MS) * 1000),
        );
        revealedRef.current = Math.min(total, revealedRef.current + (cps * dt) / 1000);
      }

      // Re-render only when a whole new character becomes visible.
      setRevealed((r) =>
        Math.floor(revealedRef.current) > Math.floor(r) ? revealedRef.current : r,
      );

      frameRef.current = requestAnimationFrame(tick);
    };

    kickRef.current = () => {
      if (frameRef.current) return; // already running
      prev = performance.now();
      frameRef.current = requestAnimationFrame(tick);
    };

    kickRef.current();
    return () => {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = 0;
    };
  }, [active]);

  useEffect(() => {
    textRef.current = text;
    activeRef.current = active;
    // A block that mounts inactive and then grows — its first chunk landed
    // before the turn was announced — has a dormant loop and nothing else to
    // restart it: `active` never changes, so the effect above never re-runs.
    // Waking the loop here is what keeps such a block from freezing at its
    // first chunk forever.
    if (text.length > revealedRef.current) kickRef.current();
  });

  const shown = Math.min(Math.floor(revealed), text.length);
  // Once the buffer is drained the full string is returned, so a settled
  // message never depends on this hook's state.
  if (!active && shown >= text.length) return text;
  return text.slice(0, shown);
}
