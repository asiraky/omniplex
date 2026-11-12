import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { uuid } from "~/client";
import { stageFile, uploadStaged, type Attachment } from "~/lib/attachments";
import type { ThreadMeta } from "~/protocol";

import {
  loadAttachments,
  loadDrafts,
  loadOutbox,
  persistDrafts,
  persistOutbox,
  saveAttachments,
} from "./composerStorage";
import { createDraftStore, type Drafts } from "./draftStore";
import { createOutbox, type OutboxSend } from "./outbox";
import { NEW_THREAD } from "./threadKeys";

export type ComposerDrafts = ReturnType<typeof useComposerDrafts>;

/**
 * What each thread's composer holds: the half-typed text and the files staged
 * to go with it, kept per thread up here rather than inside the Composer.
 *
 * Switching threads nulls `state`, which unmounts the whole content subtree
 * (Composer included) and remounts it for the next thread, so a draft owned by
 * the Composer would be destroyed on every switch. Holding it in the parent,
 * keyed by thread id, lets a half-typed message survive the swap and still be
 * there when you come back. The maps are pruned as threads go away.
 *
 * All of it is kept in localStorage too, along with the prompts still waiting
 * for their ack (the outbox): a phone discards a backgrounded tab without
 * warning, and the reload that follows must bring back what was being typed
 * and send again what had not yet been confirmed sent.
 *
 * The text is not React state: it changes on every keystroke, and state up
 * here would rerender the whole app each time. It lives in a store the
 * composers subscribe to themselves (`useDraft`), and the callbacks that need
 * it read it when they run (`getDraft`) rather than closing over a render's
 * copy. Attachments change rarely, so they stay ordinary state.
 */
export function useComposerDrafts(threads: ThreadMeta[], activeId: string | null) {
  const [restored] = useState(restoreSaved);
  const { draftStore, outbox } = restored;
  // Images staged for the next message, per thread and for the same reason as
  // the drafts: switching away and back must not lose what you attached. The
  // upload starts as soon as a picture is picked, so by send time this is a
  // list of ids the server already holds.
  const [attachments, setAttachments] = useState(restored.attachments);

  useEffect(() => persistDrafts(draftStore), [draftStore]);
  useEffect(() => persistOutbox(outbox), [outbox]);
  // From the last write: saving compares each thread's list by reference and
  // writes only the ones that moved.
  const savedAttachments = useRef(restored.attachments);
  useEffect(() => {
    saveAttachments(savedAttachments.current, attachments);
    savedAttachments.current = attachments;
  }, [attachments]);

  // A prompt that did not go, back in its thread's composer: in front of
  // whatever has been typed since, with the files it carried.
  const restoreFailedSend = useCallback(
    (entry: OutboxSend) => {
      draftStore.update((d) => restoreDraft(d, entry));
      setAttachments((all) => restoreAttachments(all, entry));
    },
    [draftStore],
  );
  // And out again once it has gone again, by a retry,
  // leaving what was typed after it. Text edited since is left alone.
  const withdrawFailedSend = useCallback(
    (entry: OutboxSend) => {
      draftStore.update((d) => withdrawDraft(d, entry));
      setAttachments((all) => withdrawAttachments(all, entry));
    },
    [draftStore],
  );
  const getDraft = draftStore.get;
  const setDraft = draftStore.set;
  // The updater form, for writes that must land against the draft as it is
  // when they run: a reply arriving seconds after its request, say.
  const setDrafts = draftStore.update;
  const patchAttachment = useCallback(
    (threadId: string, key: string, patch: Partial<Attachment>) => {
      setAttachments((all) => {
        const list = all[threadId];
        if (!list?.some((a) => a.key === key)) return all;
        return { ...all, [threadId]: list.map((a) => (a.key === key ? { ...a, ...patch } : a)) };
      });
    },
    [],
  );

  // Picked, dropped, or pasted images and PDFs. Each is uploaded on its own the moment
  // it arrives: the composer stays usable, and a slow picture on a slow
  // connection never blocks typing the question that goes with it.
  // The in-flight upload behind each staged image, so removing one can stop it.
  const uploadsInFlight = useRef<Map<string, AbortController>>(new Map());

  // One file up to one thread. Resolves to what made it sendable; rejects when
  // it failed or was taken back, having already marked it in the composer.
  const uploadInto = useCallback(
    (threadId: string, key: string, file: File) => {
      const abort = new AbortController();
      uploadsInFlight.current.set(key, abort);
      return uploadStaged(threadId, file, {
        signal: abort.signal,
        onProgress: (progress) => patchAttachment(threadId, key, { progress }),
      })
        .then((patch) => {
          patchAttachment(threadId, key, patch);
          return patch;
        })
        .catch((e: Error) => {
          // An abort means the file was taken back; there is nothing left
          // to report it to.
          if (e.name !== "AbortError")
            patchAttachment(threadId, key, { status: "error", error: e.message });
          throw e;
        })
        .finally(() => uploadsInFlight.current.delete(key));
    },
    [patchAttachment],
  );

  const attachFiles = useCallback(
    (files: File[]) => {
      const threadId = activeId;
      if (!threadId) return;
      for (const file of files) {
        // Not `crypto.randomUUID`: that exists only in a secure context, and
        // the origins a phone reaches this server on are not one.
        const key = uuid();
        const staged = stageFile(file, key);
        setAttachments((all) => ({ ...all, [threadId]: [...(all[threadId] ?? []), staged] }));
        // A picture goes up as an image, shrunk first; anything else goes up
        // as an artefact the agent reads from disk.
        uploadInto(threadId, key, file).catch(() => {});
      }
    },
    [activeId, uploadInto],
  );

  // Files picked for a thread that does not exist yet. Uploads belong to a
  // thread, so these wait here as they were picked and go up once it does.
  const draftFiles = useRef<Map<string, File>>(new Map());
  const attachToDraft = useCallback((files: File[]) => {
    for (const file of files) {
      const key = uuid();
      draftFiles.current.set(key, file);
      const staged: Attachment = { ...stageFile(file, key), status: "staged", progress: undefined };
      setAttachments((all) => ({ ...all, [NEW_THREAD]: [...(all[NEW_THREAD] ?? []), staged] }));
    }
  }, []);

  const removeAttachment = useCallback((threadId: string, key: string) => {
    uploadsInFlight.current.get(key)?.abort();
    draftFiles.current.delete(key);
    setAttachments((all) => {
      const list = all[threadId] ?? [];
      const going = list.find((a) => a.key === key);
      if (going?.previewUrl) URL.revokeObjectURL(going.previewUrl);
      return { ...all, [threadId]: list.filter((a) => a.key !== key) };
    });
  }, []);

  // Drop drafts for threads that have left the list, so a deleted thread does
  // not leave its text behind for the life of the tab. "Absent from the list"
  // only means gone if the thread was ever *in* the list: a freshly created
  // thread is attached, and can be typed into, before the broadcast listing
  // it arrives, and treating that gap as a disappearance would prune its draft.
  //
  // What the last page left behind counts as seen once the list has arrived:
  // a thread deleted while the tab was away must not keep its draft forever.
  // Not before, because until then the list is empty rather than complete.
  const seenThreads = useRef<Set<string>>(new Set());
  const seededSaved = useRef(false);
  useEffect(() => {
    if (!seededSaved.current && threads.length > 0) {
      seededSaved.current = true;
      for (const id of restored.threadIds) seenThreads.current.add(id);
    }
    for (const s of threads) seenThreads.current.add(s.id);
    const live = new Set(threads.map((s) => s.id));
    draftStore.update((d) => {
      const next: Record<string, string> = {};
      let changed = false;
      for (const [id, text] of Object.entries(d)) {
        if (live.has(id) || !seenThreads.current.has(id)) next[id] = text;
        else changed = true;
      }
      return changed ? next : d;
    });
    // Staged images go the same way, releasing their preview URLs as they do:
    // a deleted thread must not leak blobs for the life of the tab.
    setAttachments((all) => {
      const next: Record<string, Attachment[]> = {};
      let changed = false;
      for (const [id, list] of Object.entries(all)) {
        if (live.has(id) || !seenThreads.current.has(id)) next[id] = list;
        else {
          for (const a of list) URL.revokeObjectURL(a.previewUrl);
          changed = true;
        }
      }
      return changed ? next : all;
    });
    outbox.prune((id) => live.has(id) || !seenThreads.current.has(id));
  }, [draftStore, outbox, restored, threads]);

  // One object while nothing in it changes, so typing (which changes nothing
  // here) hands every consumer the same store it had.
  return useMemo(
    () => ({
      draftStore,
      outbox,
      restoreFailedSend,
      withdrawFailedSend,
      getDraft,
      setDraft,
      setDrafts,
      attachments,
      setAttachments,
      draftFiles,
      uploadInto,
      attachFiles,
      attachToDraft,
      removeAttachment,
    }),
    [
      draftStore,
      outbox,
      restoreFailedSend,
      withdrawFailedSend,
      getDraft,
      setDraft,
      setDrafts,
      attachments,
      uploadInto,
      attachFiles,
      attachToDraft,
      removeAttachment,
    ],
  );
}

/**
 * What the last page of this browser left: drafts, staged files and the
 * outbox. A prompt still waiting for its ack when that page died is about to
 * go again; its text comes out of the draft if it is still there (the page
 * died before the cleared draft reached storage), or it would be sent twice.
 */
function restoreSaved() {
  const entries = loadOutbox();
  let drafts: Drafts = loadDrafts();
  for (const entry of entries) if (entry.status === "pending") drafts = withdrawDraft(drafts, entry);
  const attachments = loadAttachments();
  const threadIds = new Set([
    ...Object.keys(drafts),
    ...Object.keys(attachments),
    ...entries.map((e) => e.threadId),
  ]);
  // The new-thread composer is not a thread, and is never in the list.
  threadIds.delete(NEW_THREAD);
  return {
    draftStore: createDraftStore(drafts),
    outbox: createOutbox(entries),
    attachments,
    threadIds,
  };
}

/** Between a restored message and what was typed after it. */
const PARAGRAPH = "\n\n";

function restoreDraft(drafts: Drafts, entry: OutboxSend): Drafts {
  const current = drafts[entry.threadId] ?? "";
  return { ...drafts, [entry.threadId]: [entry.text, current].filter(Boolean).join(PARAGRAPH) };
}

function withdrawDraft(drafts: Drafts, entry: OutboxSend): Drafts {
  const current = drafts[entry.threadId] ?? "";
  if (current === entry.text) return { ...drafts, [entry.threadId]: "" };
  // What follows the message has to start on a line of its own, however many
  // breaks down: the user may have retyped the gap the restore left.
  const rest = current.startsWith(entry.text) ? current.slice(entry.text.length) : "";
  if (!rest.startsWith("\n")) return drafts;
  return { ...drafts, [entry.threadId]: rest.replace(/^\n+/, "") };
}

function restoreAttachments(
  all: Record<string, Attachment[]>,
  entry: OutboxSend,
): Record<string, Attachment[]> {
  if (entry.attachments.length === 0) return all;
  const list = all[entry.threadId] ?? [];
  const back = entry.attachments.filter((a) => !list.some((b) => b.key === a.key));
  return { ...all, [entry.threadId]: [...back, ...list] };
}

function withdrawAttachments(
  all: Record<string, Attachment[]>,
  entry: OutboxSend,
): Record<string, Attachment[]> {
  const list = all[entry.threadId];
  if (!list || entry.attachments.length === 0) return all;
  const sent = new Set(entry.attachments.map((a) => a.key));
  const kept = list.filter((a) => !sent.has(a.key));
  return kept.length === list.length ? all : { ...all, [entry.threadId]: kept };
}
