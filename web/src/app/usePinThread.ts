import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import type { Wire } from "./useWire";

/**
 * A pin shown ahead of the server. Once the server confirms it, it stays only
 * until the next threads broadcast: the confirmation can arrive before the
 * list that carries the flag, and after that list the server's word goes.
 */
type Pending = { pinned: boolean; confirmedOn?: ThreadMeta[] };

/**
 * Pinning a thread, and the thread list as the user should see it meanwhile.
 *
 * The row jumps into (or out of) the Pinned section at once. On a phone the
 * round trip is seconds, and a tap that does nothing for that long reads as a
 * tap that missed. The flag holds over any broadcast the server sent before
 * it got the command, the same way a rename or a move does. A failure drops
 * it, so the row goes back to wherever the server has it.
 *
 * Each thread has at most one pin on the wire, for the rename's reason: the
 * client resends unanswered commands on reconnect and the server runs them
 * concurrently, so pin-then-unpin sent together could land in either order. A
 * toggle made while one is in flight waits for it, and only the latest
 * waiting one is sent.
 *
 * `threads` is the list to flag — App's, with pending renames already on it.
 * Confirmations are marked against the wire's own list, since that is the one
 * that changes only when the server speaks.
 */
export function usePinThread(wire: Wire, threads: ThreadMeta[]) {
  const { clientRef } = wire;
  const wanted = useRef(new Map<string, boolean>());
  const inFlight = useRef(new Set<string>());
  const [pending, setPending] = useState<ReadonlyMap<string, Pending>>(new Map());
  const listRef = useRef(wire.threads);
  useEffect(() => {
    listRef.current = wire.threads;
  }, [wire.threads]);

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
      const pinned = wanted.current.get(threadId);
      if (pinned === undefined) return;
      inFlight.current.add(threadId);
      const client = clientRef.current;
      const sent = client
        ? client.command("set_thread_pinned", { threadId, pinned })
        : Promise.reject(new Error("Not connected"));
      sent
        .then(
          () => true,
          (e: Error) => {
            toast.error(pinned ? "Could not pin that thread" : "Could not unpin that thread", {
              description: e.message,
            });
            return false;
          },
        )
        .then((saved) => {
          inFlight.current.delete(threadId);
          if (wanted.current.get(threadId) !== pinned) return send(threadId);
          wanted.current.delete(threadId);
          put(threadId, saved ? { pinned, confirmedOn: listRef.current } : null);
        });
    },
    [clientRef, put],
  );

  const setPinned = useCallback(
    (threadId: string, pinned: boolean) => {
      wanted.current.set(threadId, pinned);
      put(threadId, { pinned });
      if (!inFlight.current.has(threadId)) send(threadId);
    },
    [send, put],
  );

  const shown = useMemo(() => {
    if (pending.size === 0) return threads;
    return threads.map((s) => {
      const entry = pending.get(s.id);
      if (!entry || (entry.confirmedOn && entry.confirmedOn !== wire.threads)) return s;
      return !!s.pinned === entry.pinned ? s : { ...s, pinned: entry.pinned };
    });
  }, [threads, wire.threads, pending]);
  return { threads: shown, setPinned };
}
