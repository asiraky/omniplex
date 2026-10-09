import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import type { Wire } from "./useWire";

/**
 * A name shown ahead of the server. Once the server confirms it, it stays only
 * until the next threads broadcast: the confirmation can arrive before the
 * list that carries the new name, and after that list the server's word goes.
 */
type Pending = { title: string; confirmedOn?: ThreadMeta[] };

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
  const [pending, setPending] = useState<ReadonlyMap<string, Pending>>(new Map());
  // The list on screen, for marking which one a confirmation arrived over.
  const listRef = useRef(threads);
  useEffect(() => {
    listRef.current = threads;
  }, [threads]);

  const put = useCallback((threadId: string, entry: Pending | null) => {
    setPending((prev) => {
      const next = new Map(prev);
      if (entry) next.set(threadId, entry);
      else next.delete(threadId);
      return next;
    });
  }, []);

  const send = useCallback(
    function send(threadId: string) {
      const title = wanted.current.get(threadId);
      if (title === undefined) return;
      inFlight.current.add(threadId);
      const client = clientRef.current;
      const sent = client
        ? client.command("rename_thread", { threadId, title })
        : Promise.reject(new Error("Not connected"));
      sent
        .then(
          () => true,
          (e: Error) => {
            toast.error("Could not rename that thread", { description: e.message });
            return false;
          },
        )
        .then((saved) => {
          inFlight.current.delete(threadId);
          if (wanted.current.get(threadId) !== title) return send(threadId);
          wanted.current.delete(threadId);
          put(threadId, saved ? { title, confirmedOn: listRef.current } : null);
        });
    },
    [clientRef, put],
  );

  const rename = useCallback(
    (threadId: string, title: string) => {
      wanted.current.set(threadId, title);
      put(threadId, { title });
      if (!inFlight.current.has(threadId)) send(threadId);
    },
    [send, put],
  );

  const shown = useMemo(
    () =>
      pending.size === 0
        ? threads
        : threads.map((t) => withPending(t, pending.get(t.id), threads)),
    [threads, pending],
  );
  return { threads: shown, rename };
}

function withPending(thread: ThreadMeta, entry: Pending | undefined, list: ThreadMeta[]) {
  if (!entry || (entry.confirmedOn && entry.confirmedOn !== list)) return thread;
  return { ...thread, title: entry.title };
}
