import { useCallback, useEffect, useMemo, useRef, type RefObject } from "react";

import { CommandTimeout, uuid, type Client } from "~/client";
import type { ComposerAttachments } from "~/components/Composer";
import { overPromptLimit, sendPayload, type Attachment } from "~/lib/attachments";
import { toast } from "~/lib/toast";
import { errorText } from "~/lib/utils";
import type { CardEdits, CardOutcome, ComposerItem, Delivery, ThreadState } from "~/protocol";

import { prependDraft } from "./draftStore";
import type { OutboxSend } from "./outbox";
import type { ComposerDrafts } from "./useComposerDrafts";
import type { Wire } from "./useWire";

export type ThreadCommands = ReturnType<typeof useThreadCommands>;

// The revision is not sent anywhere. It is a parameter so that a new revision
// makes a new loader, and a new loader is what tells the Composer and the
// recent-skills list to ask for the catalogue again.
function composerItemsLoader(
  clientRef: RefObject<Client | null>,
  threadId: string | null,
  _revision: number,
) {
  return async (): Promise<ComposerItem[]> => {
    if (!threadId) return [];
    const result = await clientRef.current!.command("list_composer_items", { threadId });
    return result.items ?? [];
  };
}

/**
 * What the attached thread's composer, transcript and prompts send to the
 * server: prompts, stops, answers to permission and elicitation requests, and
 * the slash-command catalogue and actions.
 */
export function useThreadCommands({
  wire,
  activeId,
  store,
  openDiff,
}: {
  wire: Wire;
  activeId: string | null;
  store: ComposerDrafts;
  openDiff: (path?: string) => void;
}) {
  // The thread's state is read when a command runs, not subscribed to: these
  // callbacks only need it at that moment, and holding it would remake them,
  // and rerender whatever takes them, on every frame.
  const { clientRef, threadStore, composerRevision } = wire;
  const {
    getDraft,
    setDraft,
    setDrafts,
    attachments,
    setAttachments,
    outbox,
    restoreFailedSend,
    withdrawFailedSend,
    uploadOf,
  } = store;

  // The prompt waits in the outbox until the server acks it. If it fails, or
  // the connection is found dead before an answer, its text and files go back
  // into the composer with the reason, rather than a toast over a composer
  // that has already emptied.
  const dispatch = useCallback(
    (send: OutboxSend) => {
      const { commandId, threadId, text, attachments: sent, delivery } = send;
      outbox.add(send);
      const { imageIds, files } = sendPayload(sent);
      // Left out entirely when there are none: the overwhelming majority of
      // prompts carry nothing, and the frame is persisted for retry.
      const args = {
        threadId,
        text,
        ...(imageIds.length ? { imageIds } : {}),
        ...(files.length ? { files } : {}),
        ...(delivery ? { delivery } : {}),
      };
      // The previews are kept until the ack: a failure puts the pictures back.
      const settle = () => {
        const entry = outbox.ack(commandId);
        if (!entry) return undefined;
        for (const a of entry.attachments) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
        return entry;
      };
      const client = clientRef.current;
      const reply = client
        ? client.command("prompt", args, commandId)
        : Promise.reject(new Error("not connected"));
      reply.then(settle, (e) => {
        // A connection torn down for another says nothing about the send: the
        // one that replaced it has it now.
        if (client && clientRef.current !== client) return;
        // The timeout's own message is written for a toast; the note already
        // says what to do about it.
        const reason =
          e instanceof CommandTimeout
            ? "no answer from the server"
            : e instanceof Error
              ? e.message
              : String(e);
        const entry = outbox.fail(commandId, reason);
        if (entry) restoreFailedSend(entry);
      });
    },
    [clientRef, outbox, restoreFailedSend],
  );

  // A send the last page never heard back about goes again, under its id:
  // the server's ledger replays its result if it did arrive, and runs it if
  // it did not. Only if this one fails too does the text come back. Once per
  // connection, not per mount: a dev remount closes the first before it is
  // up, and what was sent on it went nowhere.
  const resentOn = useRef<Client | null>(null);
  useEffect(() => {
    const client = clientRef.current;
    if (!client || resentOn.current === client) return;
    resentOn.current = client;
    for (const e of outbox.get()) if (e.status === "pending") dispatch(e);
  }, [clientRef, dispatch, outbox]);

  // A message whose files are still going up: it waits in the outbox until
  // they are all up, then goes. If any of them fails, or together they come
  // to more than a message may carry, it never goes, and comes back to the
  // composer with every file it carried, the failed ones marked for a retry.
  // Its chips are no longer in any composer, so the uploads' own marks land
  // on nothing and what each one came to is read from its promise instead.
  const sendWhenUploaded = useCallback(
    (waiting: OutboxSend) => {
      const uploading = waiting.attachments.filter((a) => a.status === "uploading");
      // Read before anything else runs: the files are kept only while
      // something still waits on them.
      const done = uploading.map(
        (a) => uploadOf(a.key) ?? Promise.reject(new Error("the upload was lost")),
      );
      outbox.wait(waiting);
      void Promise.allSettled(done).then((results) => {
        // Gone with its thread while it waited.
        const entry = outbox.get().find((e) => e.commandId === waiting.commandId);
        if (entry?.status !== "waiting") return;
        const outcome = new Map(uploading.map((a, i) => [a.key, results[i]]));
        const settled = waiting.attachments.map((a): Attachment => {
          const r = outcome.get(a.key);
          if (!r) return a;
          if (r.status === "fulfilled") return { ...a, ...r.value };
          return { ...a, status: "error", error: errorText(r.reason) };
        });
        const failed = settled.filter((a) => a.status === "error");
        // Checked again now the sizes are known: the composer could only count
        // what was already up when the message left it.
        const reason = failed.length
          ? `${listNames(failed.map((a) => a.name))} did not upload`
          : overPromptLimit(settled)
            ? "the images and PDFs come to more than 20 MB"
            : undefined;
        if (!reason) {
          dispatch({ ...waiting, attachments: settled });
          return;
        }
        const back = outbox.fail(waiting.commandId, reason, {
          cannotRetry: true,
          attachments: settled,
        });
        if (back) restoreFailedSend(back);
      });
    },
    [dispatch, outbox, restoreFailedSend, uploadOf],
  );

  // The composer passes what it was handed alongside the text, but the
  // attachments it sends are the staged ones held here; delivery, when the
  // person picked one, overrides the harness's default for a prompt sent
  // mid-turn.
  //
  // A message may go while some of its files are still uploading: it leaves
  // the composer at once, with them, and waits for them (sendWhenUploaded). A
  // file that had already failed when it was sent does not go with it, and
  // stays in the composer for the person to retry or remove: it must not
  // vanish without a word.
  const send = useCallback(
    (text: string, _attached?: ComposerAttachments, delivery?: Delivery) => {
      if (!activeId) return;
      const threadId = activeId;
      // A second message would overtake the one waiting on its uploads. The
      // composer holds its send while one is, so this is only a backstop;
      // the composer empties itself once this returns, so the text goes back
      // after that rather than being lost.
      if (outbox.isWaiting(threadId)) {
        queueMicrotask(() => setDrafts((d) => prependDraft(d, threadId, text)));
        return;
      }
      const staged = attachments[threadId] ?? [];
      const sent = staged.filter((a) => a.status === "ready" || a.status === "uploading");
      const uploading = sent.filter((a) => a.status === "uploading");
      const message = { threadId, text, attachments: sent, ...(delivery ? { delivery } : {}) };

      if (uploading.length === 0) {
        // Whatever failed before is superseded by this send: its text was in
        // the composer, and this is what the composer now holds. Sent
        // unchanged it is a retry, under the same id, so the server's command
        // ledger replays the first one's result if that did arrive after all.
        const payload = sendPayload(sent);
        const retry = outbox
          .takeFailed(threadId)
          .find((e) => e.text === text && samePayload(sendPayload(e.attachments), payload));
        dispatch({ commandId: retry?.commandId ?? uuid(), ...message });
      } else {
        // Not a retry: a send that waits has files the earlier one never had.
        outbox.takeFailed(threadId);
        sendWhenUploaded({ commandId: uuid(), ...message });
      }
      // Cleared optimistically, like the draft: the message is on its way, and
      // the transcript is about to show the same pictures back from the server.
      const leaving = new Set(sent.map((a) => a.key));
      setAttachments((all) => {
        const list = all[threadId];
        const kept = list?.filter((a) => !leaving.has(a.key));
        return list && kept && kept.length !== list.length ? { ...all, [threadId]: kept } : all;
      });
    },
    [activeId, attachments, dispatch, outbox, sendWhenUploaded, setAttachments, setDrafts],
  );

  // The note's Retry: the failed message exactly as it went, under its own id,
  // whatever the composer has gained since. What came back to the composer
  // with it comes out again, so a second failure puts it back only once.
  const retry = useCallback(
    (entry: OutboxSend) => {
      withdrawFailedSend(entry);
      dispatch(entry);
    },
    [dispatch, withdrawFailedSend],
  );

  const cancel = useCallback(() => {
    if (!activeId) return;
    // Stop drops whatever was queued behind the turn; the text comes back to
    // the composer rather than vanishing, in front of anything already typed.
    const queued = threadStore.get()?.queuedPrompts ?? [];
    if (queued.length > 0) {
      const restored = queued.map((q) => q.prompt).filter(Boolean);
      const current = getDraft(activeId);
      setDraft(activeId, [...restored, current].filter(Boolean).join("\n\n"));
    }
    clientRef.current?.command("cancel", { threadId: activeId }).catch((e) => {
      const message = e instanceof Error ? e.message : String(e);
      toast.error("Could not stop the turn", { description: message });
    });
  }, [activeId, clientRef, getDraft, setDraft, threadStore]);

  const dequeue = useCallback(
    (queueId: string) => {
      if (!activeId) return;
      const text =
        threadStore.get()?.queuedPrompts?.find((q) => q.queueId === queueId)?.prompt ?? "";
      clientRef.current?.command("dequeue_prompt", { threadId: activeId, queueId }).then(
        () => {
          // Against the draft as it is when the reply lands, not as it was
          // when the request left: on a slow link that is seconds apart.
          if (text) setDrafts((d) => prependDraft(d, activeId, text));
        },
        (e) => toast.error("Could not remove that prompt", { description: e.message }),
      );
    },
    [activeId, clientRef, setDrafts, threadStore],
  );

  // An edit whose prompt started before it was saved: the words go to the
  // composer as a removed prompt's do, so they can still be sent.
  const keepLostEdit = useCallback(
    (text: string) => {
      if (!activeId) return;
      setDrafts((d) => prependDraft(d, activeId, text));
      toast.info("That prompt already went out", { description: "Your edit is in the composer." });
    },
    [activeId, setDrafts],
  );

  // Text only: leaving imageIds out keeps the pictures the prompt already
  // has. Resolves false on failure so the card stays open with the edit in it
  // rather than dropping what was typed.
  const editQueued = useCallback(
    async (queueId: string, text: string) => {
      if (!activeId || !clientRef.current) return false;
      try {
        await clientRef.current.command("edit_queued_prompt", { threadId: activeId, queueId, text });
        return true;
      } catch (e) {
        const message = e instanceof Error ? e.message : String(e);
        toast.error("Could not edit that prompt", { description: message });
        return false;
      }
    },
    [activeId, clientRef],
  );

  // Steers it into the running turn, or starts a turn with it when the
  // thread is idle; the server decides which from the thread's phase.
  const sendQueued = useCallback(
    (queueId: string) => {
      if (!activeId) return;
      clientRef.current?.command("send_queued_prompt", { threadId: activeId, queueId }).catch((e) => {
        const message = e instanceof Error ? e.message : String(e);
        toast.error("Could not send that prompt", { description: message });
      });
    },
    [activeId, clientRef],
  );

  const resumeQueue = useCallback(() => {
    if (!activeId) return;
    clientRef.current?.command("resume_queue", { threadId: activeId }).catch((e) => {
      const message = e instanceof Error ? e.message : String(e);
      toast.error("Could not resume the queue", { description: message });
    });
  }, [activeId, clientRef]);

  const resolvePermission = useCallback(
    (requestId: string, outcome: string, optionId: string) => {
      if (activeId) {
        clientRef.current
          ?.command("resolve_permission", { threadId: activeId, requestId, outcome, optionId })
          .catch((e) => toast.error("Could not send that answer", { description: e.message }));
      }
    },
    [activeId, clientRef],
  );

  const resolveElicitation = useCallback(
    (requestId: string, action: string, value: unknown) => {
      if (activeId) {
        clientRef.current
          ?.command("resolve_elicitation", { threadId: activeId, requestId, action, value })
          .catch((e) => toast.error("Could not send that answer", { description: e.message }));
      }
    },
    [activeId, clientRef],
  );

  // Unlike the two above, the card waits on the answer: applying it can fail
  // (a bad value, the network) and the card stays up to say why. Edits can
  // carry secret values, which is why it skips the command ledger on the
  // server, and why they are only ever in this frame.
  const resolveCard = useCallback(
    async (requestId: string, action: "accept" | "decline", edits?: CardEdits) => {
      if (!activeId || !clientRef.current) throw new Error("Not connected");
      const result = await clientRef.current.command("resolve_card", {
        threadId: activeId,
        requestId,
        action,
        ...(edits ? { edits } : {}),
      });
      return result?.outcome as CardOutcome | undefined;
    },
    [activeId, clientRef],
  );

  // After a sign-in, so a server the live session was turned away by
  // reconnects now the proxy has a token for it. Best effort: a session that
  // has gone reaches it when it starts again.
  const reconnectMcp = useCallback(
    (name: string, project?: string) => {
      if (!activeId) return;
      clientRef.current
        ?.command("thread_mcp_reconnect", { threadId: activeId, name, ...(project ? { project } : {}) })
        .catch(() => {});
    },
    [activeId, clientRef],
  );

  const loadComposerItems = useMemo(
    () => composerItemsLoader(clientRef, activeId, composerRevision),
    [clientRef, activeId, composerRevision],
  );

  const runComposerAction = useCallback(
    async (action: string, args: string, invocation: string) => {
      if (!activeId) return;
      try {
        await clientRef.current!.command("run_composer_action", {
          threadId: activeId,
          action,
          args,
          invocation,
        });
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        toast.error("Could not run that command", { description: message });
        throw error;
      }
    },
    [activeId, clientRef],
  );

  const runClientComposerAction = useCallback(
    (action: string) => {
      if (action === "diff") {
        openDiff();
        return;
      }
      const state = threadStore.get();
      if (action === "status" && state)
        toast.info("Thread status", { description: statusLine(state) });
    },
    [openDiff, threadStore],
  );

  // The transcript asking for the page above its window. Fire-and-forget: the
  // client dedups concurrent asks and publishes the merged state through the
  // same onState path every other update takes.
  const loadOlderItems = useCallback(() => {
    void clientRef.current?.loadOlder();
  }, [clientRef]);

  // The transcript's own buttons for a thread that stopped: carry on, retry
  // the workspace, or tear it down.
  const threadCommand = useCallback(
    (command: string) => {
      if (!activeId) return;
      clientRef.current?.command(command, { threadId: activeId }).catch((e) => {
        toast.error("That didn't go through", { description: e.message });
      });
    },
    [activeId, clientRef],
  );

  return {
    send,
    sendWhenUploaded,
    retry,
    cancel,
    dequeue,
    editQueued,
    keepLostEdit,
    sendQueued,
    resumeQueue,
    resolvePermission,
    resolveElicitation,
    resolveCard,
    reconnectMcp,
    loadComposerItems,
    runComposerAction,
    runClientComposerAction,
    loadOlderItems,
    threadCommand,
  };
}

function statusLine(state: ThreadState) {
  const used = state.usage?.contextUsed;
  return [
    state.model || "Default model",
    state.mode || "Default approvals",
    used !== undefined ? `${used.toLocaleString()} context tokens` : "Token usage unavailable",
  ].join(" · ");
}

/** "a.png", "a.png and b.txt", "a.png, b.txt and c.pdf". */
function listNames(names: string[]) {
  return names.length < 2 ? names.join("") : `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`;
}

function samePayload(a: ReturnType<typeof sendPayload>, b: ReturnType<typeof sendPayload>) {
  return JSON.stringify(a) === JSON.stringify(b);
}
