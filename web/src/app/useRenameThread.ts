import { useMemo } from "react";

import type { Wire } from "./useWire";
import { useThreadOverlay, type OverlayCommand } from "./useThreadOverlay";

const RENAME: OverlayCommand<string> = {
  send: (client, threadId, title) => client.command("rename_thread", { threadId, title }),
  errorTitle: () => "Could not rename that thread",
};

/**
 * Renaming a thread, and the wire's thread list as the user should see it
 * meanwhile: a new name shows at once and holds over stale broadcasts until
 * the server answers (see `useThreadOverlay`).
 */
export function useRenameThread(wire: Wire) {
  const { live, set } = useThreadOverlay(wire, RENAME);
  const threads = useMemo(() => {
    if (live.size === 0) return wire.threads;
    return wire.threads.map((t) => {
      const title = live.get(t.id);
      return title === undefined ? t : { ...t, title };
    });
  }, [wire.threads, live]);
  return { threads, rename: set };
}
