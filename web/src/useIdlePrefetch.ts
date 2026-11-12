import { useEffect } from "react";

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
