import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { Client } from "~/client";
import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import type { Wire } from "./useWire";

/**
 * A value shown ahead of the server. Once the server confirms it, it stays
 * only until the next threads broadcast: the confirmation can arrive before
 * the list that carries the value, and after that list the server's word goes.
 */
type Pending<T> = { value: T; confirmedOn?: ThreadMeta[] };

export type OverlayCommand<T> = {
  /** Sends the command that makes `value` the server's. */
  send: (client: Client, threadId: string, value: T) => Promise<unknown>;
  /** The toast's title when the server refuses it. */
  errorTitle: (value: T) => string;
};

const NONE: ReadonlyMap<string, never> = new Map<string, never>();

/**
 * One per-thread value the user has set, shown before the server has it.
 *
 * The value shows at once and stays until the server answers. On a phone the
 * round trip can be seconds, and any threads broadcast the server sent before
 * it got the command would otherwise put the old value back, which reads as
 * the action not having taken. A failure drops the pending value with a
 * toast, so the list shows whatever the server holds.
 *
 * Each thread has at most one command on the wire. The client resends every
 * unanswered command on reconnect and the server runs them concurrently, so
 * two sent together could land in either order. A value set while one is in
 * flight waits for it, and only the latest waiting one is sent. Values are
 * compared with `===`, so `T` should be a primitive.
 *
 * Confirmations are marked against the wire's own list, not whatever list the
 * caller lays `live` over, since the wire's is the one that changes only when
 * the server speaks.
 *
 * `command` is read when a value is sent; pass a module-level constant.
 */
export function useThreadOverlay<T>(wire: Wire, command: OverlayCommand<T>) {
  const { clientRef, threads } = wire;
  // The latest value asked for, per thread, until the server has answered it.
  const wanted = useRef(new Map<string, T>());
  const inFlight = useRef(new Set<string>());
  const [pending, setPending] = useState<ReadonlyMap<string, Pending<T>>>(new Map());
  // The list on screen, for marking which one a confirmation arrived over.
  const listRef = useRef(threads);
  useEffect(() => {
    listRef.current = threads;
    // A confirmed value is dead once a newer list arrives; drop it rather than
    // carry it forever.
    setPending((prev) => {
      let next: Map<string, Pending<T>> | null = null;
      for (const [id, entry] of prev) {
        if (!entry.confirmedOn || entry.confirmedOn === threads) continue;
        next ??= new Map(prev);
        next.delete(id);
      }
      return next ?? prev;
    });
  }, [threads]);

  const put = useCallback((threadId: string, entry: Pending<T> | null) => {
    setPending((prev) => {
      const next = new Map(prev);
      if (entry) next.set(threadId, entry);
      else next.delete(threadId);
      return next;
    });
  }, []);

  const send = useCallback(
    function send(threadId: string) {
      if (!wanted.current.has(threadId)) return;
      const value = wanted.current.get(threadId) as T;
      inFlight.current.add(threadId);
      const client = clientRef.current;
      const sent = client
        ? command.send(client, threadId, value)
        : Promise.reject(new Error("Not connected"));
      sent
        .then(
          () => true,
          (e: Error) => {
            toast.error(command.errorTitle(value), { description: e.message });
            return false;
          },
        )
        .then((saved) => {
          inFlight.current.delete(threadId);
          if (wanted.current.get(threadId) !== value) return send(threadId);
          wanted.current.delete(threadId);
          put(threadId, saved ? { value, confirmedOn: listRef.current } : null);
        });
    },
    [clientRef, command, put],
  );

  const set = useCallback(
    (threadId: string, value: T) => {
      wanted.current.set(threadId, value);
      put(threadId, { value });
      if (!inFlight.current.has(threadId)) send(threadId);
    },
    [send, put],
  );

  /** The values to show over the list, per thread. */
  const live = useMemo((): ReadonlyMap<string, T> => {
    if (pending.size === 0) return NONE;
    const out = new Map<string, T>();
    for (const [id, entry] of pending) {
      if (!entry.confirmedOn || entry.confirmedOn === threads) out.set(id, entry.value);
    }
    return out;
  }, [pending, threads]);

  return { live, set };
}
