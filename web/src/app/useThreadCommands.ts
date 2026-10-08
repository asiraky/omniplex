import { useCallback, useMemo, type RefObject } from "react";

import type { Client } from "~/client";
import { sendPayload } from "~/lib/attachments";
import { toast } from "~/lib/toast";
import type { CardEdits, CardOutcome, ComposerItem, ThreadState } from "~/protocol";

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
  const { clientRef, state, composerRevision } = wire;
  const { drafts, setDraft, setDrafts, attachments, setAttachments } = store;

  const send = useCallback(
    (text: string) => {
      if (!activeId) return;
      const staged = attachments[activeId] ?? [];
      const { imageIds, files } = sendPayload(staged);
      // Left out entirely when there are none: the overwhelming majority of
      // prompts carry nothing, and the frame is persisted for retry.
      const args = {
        threadId: activeId,
        text,
        ...(imageIds.length ? { imageIds } : {}),
        ...(files.length ? { files } : {}),
      };
      clientRef.current?.command("prompt", args).catch((e) => {
        toast.error("Could not send that prompt", { description: e.message });
      });
      // Cleared optimistically, like the draft: the message is on its way, and
      // the transcript is about to show the same pictures back from the server.
      for (const a of staged) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
      setAttachments((all) => (all[activeId]?.length ? { ...all, [activeId]: [] } : all));
    },
    [activeId, attachments, clientRef, setAttachments],
  );

  const cancel = useCallback(() => {
    if (!activeId) return;
    // Stop drops whatever was queued behind the turn; the text comes back to
    // the composer rather than vanishing, in front of anything already typed.
    const queued = state?.queuedPrompts ?? [];
    if (queued.length > 0) {
      const restored = queued.map((q) => q.prompt).filter(Boolean);
      const current = drafts[activeId] ?? "";
      setDraft(activeId, [...restored, current].filter(Boolean).join("\n\n"));
    }
    clientRef.current?.command("cancel", { threadId: activeId }).catch((e) => {
      const message = e instanceof Error ? e.message : String(e);
      toast.error("Could not stop the turn", { description: message });
    });
  }, [activeId, clientRef, drafts, setDraft, state]);

  const dequeue = useCallback(
    (queueId: string) => {
      if (!activeId) return;
      const text = state?.queuedPrompts?.find((q) => q.queueId === queueId)?.prompt ?? "";
      clientRef.current?.command("dequeue_prompt", { threadId: activeId, queueId }).then(
        () => {
          if (!text) return;
          // Against the draft as it is when the reply lands, not as it was
          // when the request left: on a slow link that is seconds apart.
          setDrafts((d) => ({
            ...d,
            [activeId]: [text, d[activeId] ?? ""].filter(Boolean).join("\n\n"),
          }));
        },
        (e) => toast.error("Could not remove that prompt", { description: e.message }),
      );
    },
    [activeId, clientRef, setDrafts, state],
  );

  const resolvePermission = useCallback(
    (requestId: string, outcome: string, optionId: string) => {
      if (activeId) {
        clientRef.current?.command("resolve_permission", {
          threadId: activeId,
          requestId,
          outcome,
          optionId,
        });
      }
    },
    [activeId, clientRef],
  );

  const resolveElicitation = useCallback(
    (requestId: string, action: string, value: unknown) => {
      if (activeId) {
        clientRef.current?.command("resolve_elicitation", {
          threadId: activeId,
          requestId,
          action,
          value,
        });
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
      if (action === "status" && state)
        toast.info("Thread status", { description: statusLine(state) });
    },
    [openDiff, state],
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
    (command: string) => activeId && clientRef.current?.command(command, { threadId: activeId }),
    [activeId, clientRef],
  );

  return {
    send,
    cancel,
    dequeue,
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
