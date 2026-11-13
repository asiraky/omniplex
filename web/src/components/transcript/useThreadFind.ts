import { useCallback, useEffect, useRef, useState } from "react";

import { SEARCH_MIN_LENGTH, type SearchThread, type ThreadMatch } from "~/lib/threadFind";
import { useLatest } from "~/useLatest";

/** How long typing must pause before the query goes to the server. */
export const FIND_DEBOUNCE_MS = 250;

export type FindStatus = "idle" | "short" | "loading" | "ready" | "error";

export type ThreadFind = {
  query: string;
  setQuery: (q: string) => void;
  status: FindStatus;
  /** The last result, oldest first. */
  matches: ThreadMatch[];
  total: number;
  current: ThreadMatch | null;
  /** 1-based position of `current` among all `total` matches; 0 when none. */
  ordinal: number;
  /** Bumped on every selection, a repeat of the same match included, so the
      consumer jumps again. */
  step: number;
  /** Toward newer. On the newest, re-runs the query to pick up new messages. */
  next: () => void;
  /** Toward older, wrapping from the oldest to the newest. */
  prev: () => void;
  /** Run the query again now, opening on the newest match. */
  rerun: () => void;
};

type Found = {
  status: FindStatus;
  matches: ThreadMatch[];
  total: number;
  /** Into `matches`; -1 for none. */
  idx: number;
  step: number;
};

const NONE: Found = { status: "idle", matches: [], total: 0, idx: -1, step: 0 };

// Counted in code points, as the server counts runes: one emoji is two UTF-16
// units but a single character, and too short. All-blank text would match
// nearly every message, so it waits for something to look for.
function searchable(q: string): boolean {
  return [...q].length >= SEARCH_MIN_LENGTH && q.trim() !== "";
}

/**
 * Find in a thread over a server-side search. The result is a snapshot of
 * when the query ran: typing re-runs it, and so does stepping past the newest
 * match, which is how messages that arrived since get found.
 */
export function useThreadFind(search: SearchThread): ThreadFind {
  const searchRef = useLatest(search);
  const [query, setQueryState] = useState("");
  const [found, setFound] = useState<Found>(NONE);
  // Mirrors for the handlers, which decide what to do from the current
  // result and must not wait on a render to see it.
  const foundRef = useRef(found);
  const queryRef = useRef("");
  const request = useRef<AbortController | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const update = useCallback((f: (prev: Found) => Found) => {
    foundRef.current = f(foundRef.current);
    setFound(foundRef.current);
  }, []);

  const cancel = useCallback(() => {
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = null;
    request.current?.abort();
    request.current = null;
  }, []);

  /** Run `q` now. With `after` (the index of the newest match a next()
      stepped off) select the first newer match, else wrap to the oldest;
      without, open on the newest. */
  const run = useCallback(
    (q: string, after?: number) => {
      cancel();
      const controller = new AbortController();
      request.current = controller;
      update((f) => ({ ...f, status: "loading" }));
      searchRef.current(q, controller.signal).then(
        ({ matches, total }) => {
          if (controller.signal.aborted) return;
          request.current = null;
          let idx = matches.length - 1;
          if (after !== undefined && matches.length) {
            const newer = matches.findIndex((m) => m.index > after);
            idx = newer === -1 ? 0 : newer;
          }
          update((f) => ({ status: "ready", matches, total, idx, step: idx >= 0 ? f.step + 1 : f.step }));
        },
        () => {
          if (controller.signal.aborted) return;
          request.current = null;
          update((f) => ({ ...NONE, status: "error", step: f.step }));
        },
      );
    },
    [cancel, update, searchRef],
  );

  const setQuery = useCallback(
    (q: string) => {
      if (q === queryRef.current) return;
      queryRef.current = q;
      setQueryState(q);
      cancel();
      if (!searchable(q)) {
        update((f) => ({ ...NONE, status: q ? "short" : "idle", step: f.step }));
        return;
      }
      // The old result belongs to the old query.
      update((f) => ({ ...NONE, status: "loading", step: f.step }));
      timer.current = setTimeout(() => {
        timer.current = null;
        run(q);
      }, FIND_DEBOUNCE_MS);
    },
    [cancel, update, run],
  );

  const select = useCallback(
    (idx: number) => update((f) => ({ ...f, idx, step: f.step + 1 })),
    [update],
  );

  const next = useCallback(() => {
    const f = foundRef.current;
    if (f.status !== "ready") return;
    const last = f.matches.length - 1;
    // Nothing found yet may be something said since: look again.
    if (last < 0) run(queryRef.current);
    else if (f.idx < last) select(f.idx + 1);
    else run(queryRef.current, f.matches[last].index);
  }, [run, select]);

  const prev = useCallback(() => {
    const f = foundRef.current;
    if (f.status !== "ready" || !f.matches.length) return;
    select(f.idx > 0 ? f.idx - 1 : f.matches.length - 1);
  }, [select]);

  const rerun = useCallback(() => {
    if (searchable(queryRef.current)) run(queryRef.current);
  }, [run]);

  useEffect(() => cancel, [cancel]);

  const current = found.idx >= 0 ? (found.matches[found.idx] ?? null) : null;
  return {
    query,
    setQuery,
    status: found.status,
    matches: found.matches,
    total: found.total,
    current,
    ordinal: current ? found.total - found.matches.length + found.idx + 1 : 0,
    step: found.step,
    next,
    prev,
    rerun,
  };
}
