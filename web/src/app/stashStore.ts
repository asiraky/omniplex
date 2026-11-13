import { useCallback, useSyncExternalStore } from "react";

import type { Attachment } from "~/lib/attachments";

/** A draft set aside to send something else first: its text and the files
    that were ready to go with it. */
export interface StashEntry {
  id: string;
  text: string;
  attachments: Attachment[];
  /** When it was stashed, in ms. */
  at: number;
}

/** Per thread, newest first. */
export type Stashes = Readonly<Record<string, readonly StashEntry[]>>;

/** Per thread. Past it the oldest goes: a stash is for a few drafts parked
    for a while, not an archive. */
export const MAX_STASH = 20;

export type StashStore = ReturnType<typeof createStashStore>;

// One array for every thread with nothing stashed, so a reader of one sees
// the same snapshot each time and React skips the render.
const NONE: readonly StashEntry[] = Object.freeze([]);

/** The thread's stash with the entry at its head, and whatever that pushed
    past the limit. */
export function pushStash(
  all: Stashes,
  threadId: string,
  entry: StashEntry,
): { next: Stashes; evicted: StashEntry[] } {
  const list = [entry, ...(all[threadId] ?? [])];
  return { next: { ...all, [threadId]: list.slice(0, MAX_STASH) }, evicted: list.slice(MAX_STASH) };
}

/** The thread's stash without the entry, and the entry. Nothing changes when
    it is not there. A thread left with none is dropped, so its key goes. */
export function takeStash(
  all: Stashes,
  threadId: string,
  entryId: string,
): { next: Stashes; taken?: StashEntry } {
  const list = all[threadId];
  const taken = list?.find((e) => e.id === entryId);
  if (!list || !taken) return { next: all };
  const kept = list.filter((e) => e !== taken);
  const next: Record<string, readonly StashEntry[]> = { ...all, [threadId]: kept };
  if (!kept.length) delete next[threadId];
  return { next, taken };
}

/**
 * Each thread's stashed drafts, held outside React state like the drafts are,
 * so a stash or an unstash rerenders the composers that show it and not the
 * app around them.
 */
export function createStashStore(initial: Stashes = {}) {
  let stashes: Stashes = initial;
  const listeners = new Set<() => void>();

  return {
    get: (threadId: string): readonly StashEntry[] => stashes[threadId] ?? NONE,
    all: () => stashes,
    update: (fn: (all: Stashes) => Stashes) => {
      const next = fn(stashes);
      // Same object back means nothing changed, so nobody needs to look.
      if (next === stashes) return;
      stashes = next;
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

/** One thread's stash, rerendering the caller only when that list changes. No
    thread reads as an empty stash. */
export function useStash(store: StashStore, threadId: string | null): readonly StashEntry[] {
  const snapshot = useCallback(() => (threadId ? store.get(threadId) : NONE), [store, threadId]);
  return useSyncExternalStore(store.subscribe, snapshot, snapshot);
}
