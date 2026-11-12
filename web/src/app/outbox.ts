import { useCallback, useSyncExternalStore } from "react";

import type { Attachment } from "~/lib/attachments";
import type { Delivery } from "~/protocol";

/** What a send carries: enough to send it again under the same id. */
export interface OutboxSend {
  commandId: string;
  threadId: string;
  /** The trimmed text that was sent. */
  text: string;
  /** The ready attachments that went with it. */
  attachments: Attachment[];
  /** Steer or queue, when the person picked one for a prompt sent mid-turn. */
  delivery?: Delivery;
}

interface Sent extends OutboxSend {
  /** When it was last sent, in ms. Orders the entries again after a reload,
      and tells a send another tab is still waiting on from an abandoned one. */
  sentAt: number;
}

export interface PendingSend extends Sent {
  status: "pending";
}

export interface FailedSend extends Sent {
  status: "failed";
  reason: string;
  /** The note was put away. The entry stays, so sending the same message
      again is still a retry under its id rather than a second prompt. */
  dismissed?: boolean;
}

export type OutboxEntry = PendingSend | FailedSend;

export type Outbox = ReturnType<typeof createOutbox>;

/**
 * The prompts sent but not yet acked, keyed by commandId.
 *
 * Sending clears the composer straight away, so until the server confirms the
 * prompt this is the only copy of what was typed. A send that fails, or whose
 * connection is found dead before it is answered, is marked failed here
 * rather than dropped, and its text goes back to the composer.
 *
 * Held outside React state like the drafts: it changes once per send and once
 * per ack, and only the thread showing a failure needs to rerender for it.
 */
export function createOutbox(initial: readonly OutboxEntry[] = []) {
  let entries: readonly OutboxEntry[] = initial.slice();
  const listeners = new Set<() => void>();

  const set = (next: readonly OutboxEntry[]) => {
    entries = next;
    for (const listener of listeners) listener();
  };

  const replace = (found: OutboxEntry, next: OutboxEntry) =>
    set(entries.map((e) => (e === found ? next : e)));

  const remove = (commandId: string) => {
    const found = entries.find((e) => e.commandId === commandId);
    if (found) set(entries.filter((e) => e !== found));
    return found;
  };

  return {
    get: () => entries,
    subscribe: (listener: () => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    /** A retry sends under the same commandId, so it replaces the failed entry
        and moves to the end: it is now the latest send. */
    add: ({ commandId, threadId, text, attachments, delivery }: OutboxSend) => {
      const fresh: PendingSend = {
        commandId,
        threadId,
        text,
        attachments,
        ...(delivery ? { delivery } : {}),
        status: "pending",
        sentAt: Date.now(),
      };
      set([...entries.filter((e) => e.commandId !== commandId), fresh]);
    },
    ack: (commandId: string) => remove(commandId),
    /** Returns the entry only on the pending to failed step. Two sends under
        the same id can both report a failure; the text must go back to the
        composer once, not twice. */
    fail: (commandId: string, reason: string) => {
      const found = entries.find((e) => e.commandId === commandId);
      if (!found || found.status !== "pending") return undefined;
      const failed: FailedSend = { ...found, status: "failed", reason };
      replace(found, failed);
      return failed;
    },
    dismiss: (commandId: string) => {
      const found = entries.find((e) => e.commandId === commandId);
      if (found?.status !== "failed" || found.dismissed) return;
      replace(found, { ...found, dismissed: true });
    },
    /** A new send from the thread supersedes the note about the old one: its
        text is already back in the composer, which is what got sent. */
    takeFailed: (threadId: string) => {
      const isTaken = (e: OutboxEntry): e is FailedSend =>
        e.threadId === threadId && e.status === "failed";
      const taken = entries.filter(isTaken);
      if (taken.length) set(entries.filter((e) => !isTaken(e)));
      return taken;
    },
    prune: (live: (threadId: string) => boolean) => {
      const kept = entries.filter((e) => live(e.threadId));
      if (kept.length !== entries.length) set(kept);
    },
  };
}

/**
 * The thread's most recent failure still showing its note, rerendering only
 * when it changes.
 * Entries are replaced rather than mutated, so an unrelated ack or send hands
 * back the same object and React skips the render.
 */
export function useFailedSend(outbox: Outbox, threadId: string | null) {
  const snapshot = useCallback((): FailedSend | undefined => {
    if (!threadId) return undefined;
    const all = outbox.get();
    for (let i = all.length - 1; i >= 0; i--) {
      const e = all[i];
      if (e.threadId === threadId && e.status === "failed" && !e.dismissed) return e;
    }
    return undefined;
  }, [outbox, threadId]);
  return useSyncExternalStore(outbox.subscribe, snapshot, snapshot);
}
