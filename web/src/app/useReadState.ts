import { useCallback, useEffect, useRef } from "react";

import { toast } from "~/lib/toast";

import type { Wire } from "./useWire";

/**
 * Read and unread, reported to the server so paired devices agree: read on
 * open, and the explicit flag either way from a row's context menu.
 */
export function useReadState(wire: Wire, activeId: string | null) {
  const { clientRef, state, threads } = wire;
  // Read-on-open. The report carries the seq this page has actually rendered,
  // not the server's head: events landing mid-report stay unread. Gated on
  // the phase: while a turn streams, every event bumps seq, and re-reporting
  // each one would chatter on exactly the connections we care about. The turn
  // finishing flips the phase and sends one report for the whole turn.
  const viewedReported = useRef<Record<string, number>>({});
  // One chain of read-state commands per thread. The server runs each
  // connection's commands in independent goroutines, so two frames sent
  // back-to-back can execute in either order, and "mark unread" losing to an
  // in-flight read-on-open report would silently undo the user's click.
  // Sending each command only after the previous one's ack pins the order.
  const readStateQueue = useRef<Record<string, Promise<unknown>>>({});
  const sendReadState = useCallback(
    (threadId: string, command: string, args: object) => {
      const next = (readStateQueue.current[threadId] ?? Promise.resolve()).then(() =>
        clientRef.current?.command(command, args),
      );
      // Swallowed here so the chain survives a failure; callers hang their own
      // error handling off the returned promise.
      readStateQueue.current[threadId] = next.catch(() => {});
      return next;
    },
    [clientRef],
  );
  useEffect(() => {
    if (!state || state.threadId !== activeId) return;
    if (state.phase === "turn" || state.phase === "provisioning" || state.phase === "cleaning")
      return;
    if (state.seq <= (viewedReported.current[state.threadId] ?? 0)) return;
    viewedReported.current[state.threadId] = state.seq;
    sendReadState(state.threadId, "mark_thread_viewed", {
      threadId: state.threadId,
      seq: state.seq,
    }).catch(() => {
      // Nothing to tell the user: the dot clears next time this succeeds.
    });
  }, [activeId, state, sendReadState]);

  // The explicit flag back the other way, from the row's context menu.
  // Fire-and-forget like the label mutations: the threads broadcast is the
  // authoritative answer.
  const setThreadUnread = useCallback(
    (threadId: string, unread: boolean) => {
      if (unread) {
        // Forget what this page reported, or the effect above would treat the
        // current head as already-sent and never re-mark it read.
        delete viewedReported.current[threadId];
        sendReadState(threadId, "mark_thread_unread", { threadId }).catch((e) => {
          toast.error("Could not mark that thread unread", { description: e.message });
        });
        return;
      }
      const head = threads.find((s) => s.id === threadId)?.headSeq ?? 0;
      sendReadState(threadId, "mark_thread_viewed", { threadId, seq: head }).catch((e) => {
        toast.error("Could not mark that thread read", { description: e.message });
      });
    },
    [threads, sendReadState],
  );

  return setThreadUnread;
}
