// The app's half of the browser's history. Two kinds of entry:
//
// - One per place the user went: /thread/<id> for a thread, / for a new
//   thread's draft (flagged in the entry's state) or for nothing selected.
//   Back and Forward move between them, and Android's back gesture stays in
//   the app instead of leaving it.
// - On a phone, a throwaway entry under each sheet, dialog or full-page screen
//   opened over the thread, at the same URL. Back pops it, which closes the
//   sheet rather than changing thread.
//
// A sheet's entry carries its depth: how many sheets deep the page was when
// it opened. A popstate to a shallower entry closes every sheet above it. A
// sheet closed by its own button or Escape unwinds its entry with
// history.go, which pops nothing else.
//
// history.go lands a task later. Until it does, history.state still names the
// entry being left, and a push or replace would land beside the wrong one, so
// both wait for it.

import { useEffect } from "react";

import { useIsDesktop } from "~/useMediaQuery";
import { useLatest } from "~/useLatest";

const LAYER = "omniplexLayer";
// How long a sheet asked to close has before it counts as refusing.
const REFUSED_MS = 100;
// How long a history.go has to land before it counts as lost: one that only
// stepped an embedded page's frame back fires no popstate here.
const LOST_MS = 1000;
const DRAFT = "omniplexDraft";

interface Layer {
  depth: number;
  close: () => void;
}

// The sheets whose entries are in history, bottom first.
const live: Layer[] = [];
// Depths of entries whose sheets closed on their own since the last unwind.
const closed = new Set<number>();
let unwindQueued = false;
// The depth an in-flight unwind is going back to, and what waits on it.
let landing: number | null = null;
const afterLanding: (() => void)[] = [];
let lost: ReturnType<typeof setTimeout> | undefined;
let listening = false;
// A place renamed while sheets were open over it (the restore settling, a
// thread found gone): the sheets' entries take the new name now, the place's
// own when Back or an unwind gets down to it.
let renamed: { path: string; state: Record<string, unknown> | null } | null = null;

function stateOf(state: unknown = window.history.state): Record<string, unknown> {
  return state && typeof state === "object" ? (state as Record<string, unknown>) : {};
}

function depthOf(state: unknown = window.history.state): number {
  const d = stateOf(state)[LAYER];
  return typeof d === "number" && d > 0 ? d : 0;
}

// The current depth, counting an unwind still in flight as done.
function currentDepth(): number {
  return landing ?? depthOf();
}

function whenLanded(fn: () => void) {
  if (landing === null) fn();
  else afterLanding.push(fn);
}

/** Whether the current history entry is a new thread's draft. */
export function isDraftEntry(state: unknown = window.history.state): boolean {
  return stateOf(state)[DRAFT] === true;
}

/**
 * Go to a thread's path, or to / for a draft or for nothing. Pushes, unless
 * told to replace or the current entry is a sheet's or a draft's: the sheet
 * closes as the app moves on, and its entry becomes the new place, so Back
 * from there skips the sheet and the abandoned draft both. A replace with
 * sheets open renames the place under them and leaves them open.
 */
export function navigate(path: string, { draft = false, replace = false } = {}) {
  const state = draft ? { [DRAFT]: true } : null;
  if (replace && live.length) {
    renamed = { path, state };
    whenLanded(rename);
    return;
  }
  renamed = null;
  // Moving on: whatever covers the old place goes, and its entries are taken
  // over below rather than unwound. A sheet that will not close yet puts one
  // back over the new place.
  const covering = live.splice(0);
  closed.clear();
  for (const layer of covering.reverse()) layer.close();
  const write = () => {
    const takeOver = depthOf() > 0 || isDraftEntry();
    if (replace || takeOver) window.history.replaceState(state, "", path);
    else window.history.pushState(state, "", path);
  };
  whenLanded(() => {
    // From a sheet opened over another (the palette over the phone's sidebar),
    // only the bottom sheet's entry is taken over: the ones above it would be
    // dead steps behind the new place. Over a draft, the draft's own entry is,
    // as it would be with no sheet open.
    const depth = depthOf();
    const to = isDraftEntry() ? 0 : Math.min(depth, 1);
    if (depth === to) return write();
    afterLanding.push(write);
    goBack(depth, to);
  });
}

function goBack(at: number, to: number) {
  landing = to;
  window.history.go(to - at);
  clearTimeout(lost);
  lost = setTimeout(land, LOST_MS);
}

function land() {
  clearTimeout(lost);
  landing = null;
  rename();
  // A queued unwind may set off another go; what is left waits for that.
  while (landing === null && afterLanding.length) afterLanding.shift()!();
}

function rename() {
  if (!renamed) return;
  const depth = depthOf();
  window.history.replaceState({ ...renamed.state, ...(depth > 0 && { [LAYER]: depth }) }, "", renamed.path);
  if (depth === 0) renamed = null;
}

/** Put an entry under a sheet that just opened; Back calls `close`. */
function pushLayer(close: () => void): Layer {
  listen();
  const depth = currentDepth();
  // A sheet that closed and opened again before its entry was unwound (React
  // StrictMode's double effect, or a quick close-and-reopen) keeps the entry
  // rather than leaving it behind as a dead step.
  if (closed.has(depth) && !live.some((l) => l.depth === depth)) {
    closed.delete(depth);
    const layer = { depth, close };
    live.push(layer);
    return layer;
  }
  const layer = { depth: depth + 1, close };
  live.push(layer);
  whenLanded(() => {
    // Closed again, or taken over by a navigate, while it waited.
    if (!live.includes(layer)) return;
    window.history.pushState({ ...stateOf(), [LAYER]: layer.depth }, "", window.location.href);
  });
  return layer;
}

/** The sheet closed on its own: unwind its entry, if no one else has. */
function dropLayer(layer: Layer) {
  const i = live.indexOf(layer);
  if (i < 0) return;
  live.splice(i, 1);
  closed.add(layer.depth);
  // Sheets closing together (one opened from another) unwind in one step.
  if (unwindQueued) return;
  unwindQueued = true;
  queueMicrotask(unwind);
}

function unwind() {
  // One go at a time: the next is measured from where the last one lands.
  if (landing !== null) {
    afterLanding.push(unwind);
    return;
  }
  unwindQueued = false;
  const at = depthOf();
  let to = at;
  while (to > 0 && closed.has(to) && !live.some((l) => l.depth === to)) to--;
  // A sheet below one still open that closed first leaves its entry where it
  // is: going back to it would close the open one.
  closed.clear();
  if (to < at) goBack(at, to);
}

const moves = new Set<() => void>();

function onPopState(e: PopStateEvent) {
  if (landing !== null) {
    // The unwind arriving. Every sheet above it closed already; one open
    // since sits above it too but has no entry yet, so nothing closes here.
    // Nor is it the user going anywhere, so onMove's listeners, which would
    // read the URL a waiting navigate has yet to write, never hear it.
    land();
    return;
  }
  rename();
  const depth = depthOf(e.state);
  let closedSheet = false;
  for (let i = live.length - 1; i >= 0; i--) {
    const layer = live[i];
    if (layer.depth <= depth) continue;
    live.splice(i, 1);
    layer.close();
    closedSheet = true;
  }
  // A Back that closed a sheet stayed at the place under it: nothing for the
  // app to follow, even where its state and the URL have yet to agree (the
  // restore still waiting on the thread list).
  if (closedSheet) return;
  for (const fn of moves) fn();
}

function listen() {
  if (listening) return;
  listening = true;
  window.addEventListener("popstate", onPopState);
}

/**
 * Back or Forward, after any sheet it closed has closed. Unlike popstate,
 * which a sheet's own unwinding fires too, this hears only the user moving.
 */
export function onMove(fn: () => void): () => void {
  listen();
  moves.add(fn);
  return () => moves.delete(fn);
}

/**
 * On a phone, Back closes this while `open`. Desktop is left alone: there the
 * sheets are small and Back is for moving between threads.
 */
export function useBackDismiss(open: boolean, close: () => void) {
  const isDesktop = useIsDesktop();
  const active = open && !isDesktop;
  const closeRef = useLatest(close);
  useEffect(() => {
    if (!active) return;
    let done = false;
    const put = (): Layer =>
      pushLayer(() => {
        closeRef.current();
        // One that will not close yet (busy saving) has lost its entry all
        // the same, to Back or to a navigate, and the next Back would change
        // thread: put it back. One
        // that did close is cleaned up by then; if React is slower than
        // that, the entry put back is unwound when it does.
        setTimeout(() => {
          if (!done) layer = put();
        }, REFUSED_MS);
      });
    let layer = put();
    return () => {
      done = true;
      dropLayer(layer);
    };
  }, [active, closeRef]);
}
