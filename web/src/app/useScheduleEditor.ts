import { useCallback, useState } from "react";

import { uuid } from "~/client";
import type { ScheduleInput } from "~/components/ScheduleDialog";
import { sendPayload } from "~/lib/attachments";
import type { ScheduledPrompt } from "~/protocol";

import type { ComposerDrafts } from "./useComposerDrafts";
import type { Wire } from "./useWire";

export type ScheduleEditor = ReturnType<typeof useScheduleEditor>;

type Editing = {
  id: string;
  threadId: string;
  text: string;
  imageIds: string[];
  schedule?: ScheduledPrompt;
};

/**
 * The schedule sheet: a new schedule for what is in a composer, or an edit to
 * one already set. It lives above the thread view because starting a new
 * thread can open it on a thread that is not attached yet.
 */
export function useScheduleEditor(wire: Wire, store: ComposerDrafts) {
  const { clientRef } = wire;
  const { drafts, setDrafts, attachments, setAttachments } = store;
  const [editing, setEditing] = useState<Editing | null>(null);

  const openNew = useCallback(
    (threadId: string, text: string, imageIds: string[]) =>
      setEditing({ id: uuid(), threadId, text, imageIds }),
    [],
  );
  // What the thread's composer holds right now becomes the schedule.
  const openFromComposer = useCallback(
    (threadId: string) =>
      openNew(threadId, drafts[threadId] ?? "", sendPayload(attachments[threadId] ?? []).imageIds),
    [attachments, drafts, openNew],
  );
  const openExisting = useCallback(
    (threadId: string, p: ScheduledPrompt) =>
      setEditing({
        id: uuid(),
        threadId,
        text: p.prompt,
        imageIds: (p.images ?? []).map((i) => i.id),
        schedule: p,
      }),
    [],
  );
  const close = useCallback(() => setEditing(null), []);

  const save = useCallback(
    async (input: ScheduleInput) => {
      const editor = editing;
      if (!editor || !clientRef.current) throw new Error("Reconnect before scheduling");
      await clientRef.current.command("schedule_prompt", {
        threadId: editor.threadId,
        id: editor.schedule?.id ?? editor.id,
        revision: editor.schedule?.revision ?? 0,
        ...input,
        imageIds: editor.imageIds,
      });
      if (!editor.schedule) {
        // Clear only the draft and images captured when this sheet opened.
        setDrafts((all) =>
          all[editor.threadId] === editor.text ? { ...all, [editor.threadId]: "" } : all,
        );
        const scheduled = new Set(editor.imageIds);
        setAttachments((all) => {
          const staged = all[editor.threadId] ?? [];
          for (const a of staged)
            if (a.id && a.previewUrl && scheduled.has(a.id)) URL.revokeObjectURL(a.previewUrl);
          return {
            ...all,
            [editor.threadId]: staged.filter((a) => !a.id || !scheduled.has(a.id)),
          };
        });
      }
    },
    [clientRef, editing, setAttachments, setDrafts],
  );

  // Sending one now or cancelling it, from the list above the composer.
  const runAction = useCallback(
    async (threadId: string | null, action: string, p: ScheduledPrompt) => {
      if (!clientRef.current) throw new Error("Reconnect first");
      await clientRef.current.command(action, { threadId, id: p.id, revision: p.revision });
    },
    [clientRef],
  );

  return { editing, openNew, openFromComposer, openExisting, close, save, runAction };
}
