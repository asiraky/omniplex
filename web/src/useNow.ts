import { useSyncExternalStore } from "react";

/**
 * One clock for every "5m ago" on the page. A label that reads Date.now() as
 * it renders is only as fresh as the last unrelated re-render, so a sidebar
 * left open says "now" for an hour. A timer per label would fix that at the
 * cost of dozens of intervals; this is one, started by the first reader and
 * stopped by the last.
 *
 * It ticks on the minute boundary, because minutes are the finest unit any
 * label shows, and catches up as the page comes back to the foreground: a
 * phone freezes timers in a background tab, and the first thing you see on
 * returning is exactly the label that went stale.
 */
const MINUTE = 60_000;

let now = Date.now();
let timer: ReturnType<typeof setTimeout> | undefined;
const listeners = new Set<() => void>();

function tick() {
  now = Date.now();
  for (const l of listeners) l();
}

function schedule() {
  timer = setTimeout(
    () => {
      tick();
      schedule();
    },
    MINUTE - (Date.now() % MINUTE),
  );
}

function onVisibility() {
  if (document.visibilityState === "visible") tick();
}

function subscribe(l: () => void) {
  if (listeners.size === 0) {
    // The clock stood still while nobody was reading it.
    now = Date.now();
    schedule();
    document.addEventListener("visibilitychange", onVisibility);
  }
  listeners.add(l);
  return () => {
    listeners.delete(l);
    if (listeners.size > 0) return;
    clearTimeout(timer);
    timer = undefined;
    document.removeEventListener("visibilitychange", onVisibility);
  };
}

/** The current time, to the minute, re-rendering the caller as it moves on. */
export function useNow(): number {
  return useSyncExternalStore(subscribe, () => now);
}
