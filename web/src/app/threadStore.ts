import { createContext, useContext, useEffect, useMemo, useRef, useSyncExternalStore } from "react";

import type { ThreadState } from "~/protocol";

export type ThreadStore = ReturnType<typeof createThreadStore>;

/**
 * The attached thread's state, held outside React state.
 *
 * Every frame off the wire is a new state, and most of them are a job's
 * progress or a usage count ticking. Were the state in the app, each one would
 * rerender the app and everything under it: the header, the transcript, the
 * composer, the panel. Here a frame tells only the subscribers, each of which
 * selects the slice it draws, so a job tick rerenders what shows jobs and a
 * usage tick what shows usage, and nothing else.
 */
export function createThreadStore(initial: ThreadState | null) {
  let state = initial;
  const listeners = new Set<() => void>();
  return {
    get: () => state,
    set: (next: ThreadState | null) => {
      // apply.ts hands back the same object for a frame it discards, so the
      // same object means nothing changed and nobody needs to look.
      if (next === state) return;
      state = next;
      for (const listener of listeners) listener();
    },
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

/**
 * Equal when both are the same value, or objects (arrays included) whose own
 * fields are each the same value. The comparer for a selector that picks
 * several fields into a fresh object every time it runs.
 */
export function shallow<T>(a: T, b: T): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const ka = Object.keys(a);
  if (ka.length !== Object.keys(b).length) return false;
  const ra = a as Record<string, unknown>;
  const rb = b as Record<string, unknown>;
  return ka.every((k) => Object.prototype.hasOwnProperty.call(rb, k) && Object.is(ra[k], rb[k]));
}

/**
 * A slice of the attached thread, rerendering the caller only when the slice
 * changes: every frame notifies, but a selection equal to the last one hands
 * back the last one, and React skips the render.
 *
 * Handing back the last one is what lets `select` build a fresh object each
 * time: useSyncExternalStore compares snapshots by identity, so a fresh pick
 * returned as-is would read as a change on every call and never settle. The
 * memo lives in the snapshot function's closure, not in a ref written during
 * render, and it only ever returns a value equal (by `isEqual`) to selecting
 * afresh, so whatever it holds cannot render anything wrong. The last rendered
 * value is carried across a new selector (an inline arrow is new every render)
 * from after commit, so a value a discarded render computed is never it.
 */
export function useThread<T>(
  store: ThreadStore,
  select: (s: ThreadState | null) => T,
  isEqual: (a: T, b: T) => boolean = Object.is,
): T {
  const rendered = useRef<{ value: T } | null>(null);
  const snapshot = useMemo(() => {
    let memo: { state: ThreadState | null; value: T } | null = null;
    return () => {
      const state = store.get();
      if (memo && memo.state === state) return memo.value;
      const next = select(state);
      const last = memo ?? rendered.current;
      const value = last && isEqual(last.value, next) ? last.value : next;
      memo = { state, value };
      return value;
    };
  }, [store, select, isEqual]);
  const value = useSyncExternalStore(store.subscribe, snapshot, snapshot);
  useEffect(() => {
    rendered.current = { value };
  }, [value]);
  return value;
}

/**
 * The store the app attaches threads into, for components that select from it
 * without the app handing each one the store. Outside a provider no thread is
 * ever attached: the default store holds null and nothing writes to it.
 */
export const ThreadStoreContext = createContext<ThreadStore>(createThreadStore(null));

export function useThreadStore() {
  return useContext(ThreadStoreContext);
}
