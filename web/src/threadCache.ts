// The last few threads this page looked at, kept in memory so switching back
// to one paints its transcript straight away. The entry's seq is the cursor:
// the client attaches with afterSeq and the server sends only what happened
// while we were looking elsewhere (or a snapshot, when that gap has outgrown
// replay range — see Client).
//
// In memory only. resume.ts covers the one thread that must survive a tab
// discard; this covers the hop back to the one you were on a minute ago.

import type { ThreadState } from "./protocol";

const THREAD_CACHE_SIZE = 5;

export class ThreadCache {
  // A Map iterates in insertion order, so re-inserting on every touch keeps
  // the least recently used entry first.
  private entries = new Map<string, ThreadState>();

  constructor(private capacity = THREAD_CACHE_SIZE) {}

  /** The cached state, marking it most recently used. */
  get(threadId: string): ThreadState | undefined {
    const state = this.entries.get(threadId);
    if (state) {
      this.entries.delete(threadId);
      this.entries.set(threadId, state);
    }
    return state;
  }

  put(state: ThreadState) {
    this.entries.delete(state.threadId);
    this.entries.set(state.threadId, state);
    while (this.entries.size > this.capacity) {
      this.entries.delete(this.entries.keys().next().value!);
    }
  }
}
