import { useCallback } from "react";

import { uuid } from "~/client";
import type { NewThreadInput } from "~/components/ThreadDraft";
import { sendPayload, type Attachment } from "~/lib/attachments";
import { toast } from "~/lib/toast";

import { NEW_THREAD } from "./threadKeys";
import type { ComposerDrafts } from "./useComposerDrafts";
import type { ThreadCommands } from "./useThreadCommands";
import type { Wire } from "./useWire";

/**
 * Starting a thread. A plain message goes with the thread. Files cannot: they
 * upload to a thread, so the thread starts empty and the message waits on
 * them, as one sent from the thread's composer while its files go up does.
 * Scheduling starts it empty too, and opens the schedule on it once its
 * pictures are up.
 */
export function useNewThread({
  wire,
  store,
  select,
  openSchedule,
  sendWhenUploaded,
}: {
  wire: Wire;
  store: ComposerDrafts;
  select: (id: string) => void;
  openSchedule: (threadId: string, text: string, imageIds: string[]) => void;
  sendWhenUploaded: ThreadCommands["sendWhenUploaded"];
}) {
  const { clientRef } = wire;
  const { attachments, draftFiles, setDraft, setAttachments, uploadInto } = store;
  return useCallback(
    async (input: NewThreadInput, schedule = false) => {
      const pending = (attachments[NEW_THREAD] ?? []).flatMap((a) => {
        const file = draftFiles.current.get(a.key);
        return file ? [{ a, file }] : [];
      });
      const later = schedule || pending.length > 0;
      const res = await clientRef.current!.command(
        "create_thread",
        later ? { ...input, text: "" } : input,
      );
      const threadId: string = res.threadId;
      setDraft(NEW_THREAD, "");
      for (const { a } of pending) draftFiles.current.delete(a.key);
      const going = pending.map(({ a }) => ({
        ...a,
        status: "uploading" as const,
        ...(a.kind === "file" ? { progress: 0 } : {}),
      }));
      // A message waits on its files the way one sent from a thread's own
      // composer does: out of the composer, in the outbox, with the composer
      // saying so. Only a schedule shows them in the composer while they go
      // up, since there is nothing to send until it is set.
      setAttachments((all) => ({
        ...all,
        [NEW_THREAD]: [],
        ...(schedule && going.length ? { [threadId]: going } : {}),
      }));
      select(threadId);
      // The thread exists but the message did not go: it waits in the new
      // thread's composer rather than being lost.
      if (res.promptError) {
        setDraft(threadId, input.text);
        toast.error("The thread started, but the message did not send", {
          description: res.promptError,
        });
      }
      if (!later) return;

      const uploads = pending.map(({ a, file }) => uploadInto(threadId, a.key, file));
      if (!schedule) {
        for (const u of uploads) u.catch(() => {});
        sendWhenUploaded({ commandId: uuid(), threadId, text: input.text, attachments: going }, going);
        return;
      }

      // Only once they are up: before that, the text in the composer beside
      // its pictures would be sent by a tap on Send while it waits.
      const results = await Promise.allSettled(uploads);
      setDraft(threadId, input.text);
      const failed = results.filter((r): r is PromiseRejectedResult => r.status === "rejected");
      if (failed.length) {
        // Taken back mid-upload is a choice, not a failure; either way the
        // message stays in the composer for another go.
        if (failed.some((r) => r.reason?.name !== "AbortError")) {
          toast.error("The thread started, but a file did not upload", {
            description: "Your message is waiting in the thread's composer.",
          });
        }
        return;
      }
      const payload = sendPayload(
        going.map((a, i) => ({
          ...a,
          ...(results[i] as PromiseFulfilledResult<Partial<Attachment>>).value,
        })),
      );
      openSchedule(threadId, input.text, payload.imageIds);
    },
    [
      attachments,
      clientRef,
      draftFiles,
      openSchedule,
      select,
      sendWhenUploaded,
      setAttachments,
      setDraft,
      uploadInto,
    ],
  );
}
