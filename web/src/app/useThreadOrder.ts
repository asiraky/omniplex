import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";
import { withPositions } from "~/threadOrder";

import type { Wire } from "./useWire";

/**
 * A move shown ahead of the server. Once the server confirms it, it stays only
 * until the next threads broadcast: the confirmation can arrive before the
 * list that carries the new position, and after that list the server's word
 * goes.
 */
type Pending = { position: number; confirmedOn?: ThreadMeta[] };

/**
 * The thread list in the user's order, with their moves applied before the
 * server has seen them.
 *
 * A dropped row has to land where the finger let go, not a round trip later:
 * on a phone that is seconds, and a row that snaps back and then jumps reads
 * as the drag having failed. So the move overlays the list at once and holds
 * over any broadcast the server sent before it got the move, the same way a
 * rename does. A failure drops the overlay, and the row goes back to where
 * the server has it.
 *
 * Each thread has at most one move on the wire, for the rename's reason: the
 * client resends unanswered commands on reconnect and the server runs them
 * concurrently, so two moves sent together could land in either order. A move
 * made while one is in flight waits for it, and only the latest waiting one
 * is sent.
 *
 * `threads` is the list to order — App's, with pending renames already on it.
 * Confirmations are marked against the wire's own list, since that is the one
 * that changes only when the server speaks.
 */
export function useThreadOrder(wire: Wire, threads: ThreadMeta[]) {
  const { clientRef } = wire;
  // The latest position asked for, per thread, until the server has answered.
  const wanted = useRef(new Map<string, number>());
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
      const position = wanted.current.get(threadId);
      if (position === undefined) return;
      inFlight.current.add(threadId);
      const client = clientRef.current;
      const sent = client
        ? client.command("set_thread_position", { threadId, position })
        : Promise.reject(new Error("Not connected"));
      sent
        .then(
          () => true,
          (e: Error) => {
            toast.error("Could not move that thread", { description: e.message });
            return false;
          },
        )
        .then((saved) => {
          inFlight.current.delete(threadId);
          if (wanted.current.get(threadId) !== position) return send(threadId);
          wanted.current.delete(threadId);
          put(threadId, saved ? { position, confirmedOn: listRef.current } : null);
        });
    },
    [clientRef, put],
  );

  const move = useCallback(
    (threadId: string, position: number) => {
      wanted.current.set(threadId, position);
      put(threadId, { position });
      if (!inFlight.current.has(threadId)) send(threadId);
    },
    [send, put],
  );

  const shown = useMemo(() => {
    const live = new Map<string, number>();
    for (const [id, entry] of pending) {
      if (!entry.confirmedOn || entry.confirmedOn === wire.threads) live.set(id, entry.position);
    }
    return withPositions(threads, live);
  }, [threads, wire.threads, pending]);
  return { threads: shown, move };
}
