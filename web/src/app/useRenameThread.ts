import { useCallback, useMemo, useRef, useState } from "react";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import type { Wire } from "./useWire";

/**
 * Renaming a thread, and the thread list as the user should see it meanwhile.
 *
 * The new name shows at once and stays until the server answers. On a slow
 * connection that can be seconds, and any threads broadcast the server sent
 * before it got the rename would otherwise put the old name back, which reads
 * as the edit not having taken. A failure drops the pending name, so the list
 * shows whatever the server holds.
 *
 * Each thread has at most one rename on the wire. The client resends every
 * unanswered command on reconnect and the server runs them concurrently, so
 * two renames sent together could land in either order. A rename made while
 * one is in flight waits for it, and only the latest waiting one is sent.
 */
export function useRenameThread(wire: Wire) {
  const { clientRef, threads } = wire;
  // The latest name asked for, per thread, until the server has answered it.
  const wanted = useRef(new Map<string, string>());
  const inFlight = useRef(new Set<string>());
  const [pending, setPending] = useState<ReadonlyMap<string, string>>(new Map());

  const send = useCallback(
    function send(threadId: string) {
      const title = wanted.current.get(threadId);
      if (title === undefined) return;
      inFlight.current.add(threadId);
      Promise.resolve(clientRef.current?.command("rename_thread", { threadId, title }))
        .catch((e: Error) => toast.error("Could not rename that thread", { description: e.message }))
        .finally(() => {
          inFlight.current.delete(threadId);
          if (wanted.current.get(threadId) !== title) return send(threadId);
          wanted.current.delete(threadId);
          setPending(new Map(wanted.current));
        });
    },
    [clientRef],
  );

  const rename = useCallback(
    (threadId: string, title: string) => {
      wanted.current.set(threadId, title);
      setPending(new Map(wanted.current));
      if (!inFlight.current.has(threadId)) send(threadId);
    },
    [send],
  );

  const shown = useMemo(
    () => (pending.size === 0 ? threads : threads.map((t) => withPending(t, pending))),
    [threads, pending],
  );
  return { threads: shown, rename };
}

function withPending(thread: ThreadMeta, pending: ReadonlyMap<string, string>) {
  const title = pending.get(thread.id);
  return title === undefined ? thread : { ...thread, title };
}
