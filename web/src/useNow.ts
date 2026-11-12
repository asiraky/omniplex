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
 *
 * The tick only decides when to re-render; what a render reads is the real
 * time. A render driven by new data can land 59 seconds after the last tick,
 * and a countdown worked from the tick would overstate by that much: a
 * schedule made for five minutes from now would say "in 6 min".
 */
const MINUTE = 60_000;

let ticks = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
const listeners = new Set<() => void>();

function tick() {
  ticks++;
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

/** The current time, re-rendering the caller as each minute begins. */
export function useNow(): number {
  useSyncExternalStore(subscribe, () => ticks);
  return Date.now();
}
