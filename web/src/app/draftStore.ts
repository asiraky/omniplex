import { useCallback, useSyncExternalStore } from "react";

export type Drafts = Readonly<Record<string, string>>;

export type DraftStore = ReturnType<typeof createDraftStore>;

/**
 * The composers' half-typed text, per thread, held outside React state.
 *
 * Every keystroke writes here. Were this state in the app, each one would
 * rerender the app and everything under it: the sidebar, every thread row,
 * every transcript row. Here a write tells only the subscribers, and each
 * subscriber reads one thread's string, so typing rerenders the composer that
 * shows it and nothing else.
 */
export function createDraftStore() {
  let drafts: Drafts = {};
  const listeners = new Set<() => void>();

  const update = (fn: (all: Drafts) => Drafts) => {
    const next = fn(drafts);
    // Same object back means nothing changed, so nobody needs to look.
    if (next === drafts) return;
    drafts = next;
    for (const listener of listeners) listener();
  };

  return {
    get: (id: string) => drafts[id] ?? "",
    set: (id: string, text: string) =>
      update((all) => ((all[id] ?? "") === text ? all : { ...all, [id]: text })),
    update,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

/**
 * One thread's draft, rerendering the caller only when that string changes:
 * a write to another thread's draft notifies, but the snapshot compares equal
 * and React skips the render. No thread reads as the empty draft.
 */
export function useDraft(store: DraftStore, id: string | null) {
  const snapshot = useCallback(() => (id ? store.get(id) : ""), [store, id]);
  return useSyncExternalStore(store.subscribe, snapshot, snapshot);
}
