import { useCallback } from "react";
import { toast } from "sonner";

import type { NewThreadInput } from "~/components/ThreadDraft";
import { sendPayload, type Attachment } from "~/lib/attachments";

import { NEW_THREAD } from "./threadKeys";
import type { ComposerDrafts } from "./useComposerDrafts";
import type { Wire } from "./useWire";

/**
 * Starting a thread. A plain message goes with the thread. Files cannot: they
 * upload to a thread, so the thread starts empty, the message waits in its
 * composer while they go up, and then it sends. Scheduling starts it empty
 * too, and opens the schedule on it.
 */
export function useNewThread({
  wire,
  store,
  select,
  openSchedule,
}: {
  wire: Wire;
  store: ComposerDrafts;
  select: (id: string) => void;
  openSchedule: (threadId: string, text: string, imageIds: string[]) => void;
}) {
  const { clientRef } = wire;
  const { attachments, draftFiles, setDraft, setDrafts, setAttachments, uploadInto } = store;
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
      setAttachments((all) => {
        const next: Record<string, Attachment[]> = { ...all, [NEW_THREAD]: [] };
        if (pending.length) {
          next[threadId] = pending.map(({ a }) => ({
            ...a,
            status: "uploading" as const,
            ...(a.kind === "file" ? { progress: 0 } : {}),
          }));
        }
        return next;
      });
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

      setDraft(threadId, input.text);
      const results = await Promise.allSettled(
        pending.map(({ a, file }) => uploadInto(threadId, a.key, file)),
      );
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
        pending.map(({ a }, i) => ({
          ...a,
          ...(results[i] as PromiseFulfilledResult<Partial<Attachment>>).value,
        })),
      );
      if (schedule) {
        openSchedule(threadId, input.text, payload.imageIds);
        return;
      }
      try {
        await clientRef.current!.command("prompt", {
          threadId,
          text: input.text,
          ...(payload.imageIds.length ? { imageIds: payload.imageIds } : {}),
          ...(payload.files.length ? { files: payload.files } : {}),
        });
      } catch (e) {
        toast.error("The thread started, but the message did not send", {
          description: e instanceof Error ? e.message : String(e),
        });
        return;
      }
      setDrafts((all) => (all[threadId] === input.text ? { ...all, [threadId]: "" } : all));
      setAttachments((all) => {
        for (const a of all[threadId] ?? []) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
        return { ...all, [threadId]: [] };
      });
    },
    [
      attachments,
      clientRef,
      draftFiles,
      openSchedule,
      select,
      setAttachments,
      setDraft,
      setDrafts,
      uploadInto,
    ],
  );
}
