import { useEffect, useState } from "react";

import type { Chunk } from "~/lib/chunk";

/**
 * Fetches code-split chunks once the page has settled, so the first time one
 * is needed it is usually already in. Pass module-level chunks: the fetch
 * starts again whenever the list changes.
 */
export function useIdlePrefetch(...chunks: Chunk<unknown>[]) {
  useEffect(() => {
    const prefetch = () => {
      for (const c of chunks) void c.load();
    };
    if (typeof requestIdleCallback === "function") {
      const id = requestIdleCallback(prefetch, { timeout: 5000 });
      return () => cancelIdleCallback(id);
    }
    const id = setTimeout(prefetch, 2000);
    return () => clearTimeout(id);
  }, chunks);
}

/**
 * A chunk's value while `active`, fetching it if it is not in yet. `loaded`
 * is there on the first render once the code has arrived, so a prefetched
 * chunk never shows its placeholder. A failure is forgotten each time `active`
 * turns on again, and the fetch is retried.
 */
export function useChunk<T>(chunk: Chunk<T>, active = true) {
  const loaded = chunk.get();
  const [seenActive, setSeenActive] = useState(active);
  const [failed, setFailed] = useState(false);
  const [, setArrived] = useState(0);
  if (seenActive !== active) {
    setSeenActive(active);
    if (active) setFailed(false);
  }
  useEffect(() => {
    if (!active || loaded) return;
    let live = true;
    void chunk.load().then((c) => {
      if (!live) return;
      if (c) setArrived((n) => n + 1);
      else setFailed(true);
    });
    return () => {
      live = false;
    };
  }, [active, loaded, chunk]);
  return { loaded, failed };
}
