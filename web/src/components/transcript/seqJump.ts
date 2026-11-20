import { useSyncExternalStore } from "react";

import type { Item } from "~/protocol";

// A thread opened at an event seq rather than where it was last read: a
// compaction log row's "Before" jumping to its boundary. Opening a thread and
// drawing it are a navigation and a mount apart, and the thread may not even
// be attached yet (an archived one), so the ask waits here, by thread, for
// the transcript of that thread to take it.

/** A fresh object per ask, so asking twice for the same place jumps twice. */
export type SeqJump = { threadId: string; seq: number };

let pending: SeqJump | null = null;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());

/** Ask for `threadId` to open at `seq`; with no seq, drop any earlier ask so
    a plain open lands where the thread was left. */
export function requestSeqJump(threadId: string, seq?: number) {
  const next = seq === undefined ? null : { threadId, seq };
  if (next === null && pending === null) return;
  pending = next;
  emit();
}

/** Settle `jump`, if it is still the one standing. */
export function settleSeqJump(jump: SeqJump) {
  if (pending !== jump) return;
  pending = null;
  emit();
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};
const snapshot = () => pending;

/** The standing ask for `threadId`, if there is one. */
export function useSeqJump(threadId: string): SeqJump | null {
  const jump = useSyncExternalStore(subscribe, snapshot, snapshot);
  return jump?.threadId === threadId ? jump : null;
}

// A compaction's notice is keyed on its event's seq (projection/state.go).
const COMPACT = "compact:";
const compactSeq = (id: string) => (id.startsWith(COMPACT) ? Number(id.slice(COMPACT.length)) : NaN);

/**
 * The item a jump to `seq` lands on within the loaded window: the notice the
 * event made, else the first compaction notice after it (the boundary gone
 * with a rewind). "more" while the window may still hold it above; null when
 * the whole thread is loaded, or the window reaches back past `seq`, and
 * nothing fits. Items carry no seq of their own, so compaction notices, whose
 * ids do, are the only landmarks.
 */
export function seqTarget(items: readonly Item[], itemsBefore: number, seq: number): string | "more" | null {
  let after: string | null = null;
  let passed = false;
  for (const it of items) {
    const n = compactSeq(it.id);
    if (Number.isNaN(n)) continue;
    if (n === seq) return it.id;
    if (n < seq) passed = true;
    else if (after === null) after = it.id;
  }
  if (itemsBefore > 0 && !passed) return "more";
  return after;
}
