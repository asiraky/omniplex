import { useCallback, useEffect, useRef } from "react";

import type { ThreadMeta } from "~/protocol";
import type { ResumeSnapshot } from "~/resume";

export type ScrollPosition = { top: number; atBottom: boolean };

/**
 * Where each thread's transcript was scrolled, kept above the Transcript for
 * the same reason as the drafts: switching threads unmounts it, so a position
 * it owned would be lost every time, and you would come back to a thread you
 * were reading half-way up and find yourself at the bottom.
 *
 * A ref rather than state: the transcript reports every scroll, and nothing
 * on the page renders from this, so re-rendering the app on each one would be
 * pure cost. Seeded from the resume cache so the boot restore and the switch
 * restore are one path. Thread scope only, pruned as threads leave the list.
 */
export function useScrollMemory(resume: ResumeSnapshot | null) {
  const positions = useRef<Record<string, ScrollPosition>>(
    resume ? { [resume.state.threadId]: { top: resume.scrollTop, atBottom: resume.atBottom } } : {},
  );
  // Threads the list has taken away. A deleted thread's transcript reports
  // one last position as it unmounts, and that unmount happens after the prune
  // below has already dropped it, so the id is refused outright rather than
  // being written straight back in.
  const goneThreads = useRef<Set<string>>(new Set());
  const record = useCallback((id: string, top: number, atBottom: boolean) => {
    if (goneThreads.current.has(id)) return;
    positions.current[id] = { top, atBottom };
  }, []);
  const positionOf = useCallback(
    (id: string): ScrollPosition | undefined => positions.current[id],
    [],
  );
  // For a thread that is gone without ever having been in a list, which the
  // prune below cannot see.
  const forget = useCallback((id: string) => {
    delete positions.current[id];
    goneThreads.current.add(id);
  }, []);

  // A deleted thread's offset means nothing, and a new id reusing it would be
  // handed a stranger's place in the transcript. "Absent from the list" only
  // means gone if the thread was ever in the list: a freshly created thread
  // is attached before the broadcast listing it arrives.
  const seenThreads = useRef<Set<string>>(new Set());
  const prune = useCallback((threads: ThreadMeta[]) => {
    for (const s of threads) seenThreads.current.add(s.id);
    for (const s of threads) goneThreads.current.delete(s.id);
    const live = new Set(threads.map((s) => s.id));
    for (const id of Object.keys(positions.current)) {
      if (live.has(id) || !seenThreads.current.has(id)) continue;
      delete positions.current[id];
      goneThreads.current.add(id);
    }
  }, []);

  return { record, positionOf, forget, prune };
}

/**
 * Prunes the scroll memory as threads leave the list. Its own hook so the app
 * can run it after the selection's restore, which may forget a thread first.
 */
export function usePruneScrollMemory(
  memory: { prune: (threads: ThreadMeta[]) => void },
  threads: ThreadMeta[],
) {
  const { prune } = memory;
  useEffect(() => prune(threads), [prune, threads]);
}
