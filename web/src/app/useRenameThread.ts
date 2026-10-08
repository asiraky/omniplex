import { useCallback } from "react";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import type { Wire } from "./useWire";

/** The list with one thread's title replaced. */
function withTitle(threads: ThreadMeta[], threadId: string, title: string) {
  return threads.map((s) => (s.id === threadId ? { ...s, title } : s));
}

/**
 * Renaming a thread. The new name shows at once: on a slow connection the
 * threads broadcast that confirms it can be seconds away, and the old name
 * coming back in the meantime reads as the edit not having taken. A failure
 * puts the old name back, unless something else has renamed it since.
 */
export function useRenameThread(wire: Wire) {
  const { clientRef, threads, setThreads } = wire;
  return useCallback(
    (threadId: string, title: string) => {
      const before = threads.find((s) => s.id === threadId)?.title ?? "";
      setThreads((list) => withTitle(list, threadId, title));
      clientRef.current?.command("rename_thread", { threadId, title }).catch((e) => {
        setThreads((list) =>
          list.find((s) => s.id === threadId)?.title === title
            ? withTitle(list, threadId, before)
            : list,
        );
        toast.error("Could not rename that thread", { description: e.message });
      });
    },
    [clientRef, threads, setThreads],
  );
}
