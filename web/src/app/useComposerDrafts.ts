import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { uuid } from "~/client";
import { isImageAttachment, stageFile, uploadStaged, type Attachment } from "~/lib/attachments";
import type { ThreadMeta } from "~/protocol";

import {
  loadAttachments,
  loadDrafts,
  loadOutbox,
  persistDrafts,
  persistOutbox,
  saveAttachments,
} from "./composerStorage";
import { createDraftStore, PARAGRAPH, type Drafts } from "./draftStore";
import { createOutbox, type Outbox, type OutboxSend } from "./outbox";
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
  // whatever has been typed since, with the files it carried, and behind any
  // earlier send of the thread that failed too, so they read in the order
  // they were sent.
  const restoreFailedSend = useCallback(
    (entry: OutboxSend) => {
      const before = failedBefore(outbox, entry);
      draftStore.update((d) => restoreDraft(d, entry, before));
      setAttachments((all) => restoreAttachments(all, entry, before));
    },
    [draftStore, outbox],
  );
  // And out again once it has gone again, by a retry, leaving what was typed
  // after it. Text edited since is left alone.
  const withdrawFailedSend = useCallback(
    (entry: OutboxSend) => {
      const before = failedBefore(outbox, entry);
      draftStore.update((d) => withdrawDraft(d, entry, before));
      setAttachments((all) => withdrawAttachments(all, entry));
    },
    [draftStore, outbox],
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
  // The file behind each upload, from the moment it starts until it is up, so
  // a failed one can be tried again without picking it again: on a phone
  // that is a trip back through the photo library.
  const uploadFiles = useRef<Map<string, File>>(new Map());
  // The upload itself, so a message sent while it is still going can wait
  // for it and send what it resolves to.
  const uploads = useRef<Map<string, Promise<Partial<Attachment>>>>(new Map());

  // One file up to one thread. Resolves to what made it sendable; rejects when
  // it failed or was taken back, having already marked it in the composer.
  const uploadInto = useCallback(
    (threadId: string, key: string, file: File) => {
      const abort = new AbortController();
      uploadsInFlight.current.set(key, abort);
      uploadFiles.current.set(key, file);
      // A thumbnail draws no progress, and each report would redraw the app.
      const showsProgress = !isImageAttachment(file);
      const upload = uploadStaged(threadId, file, {
        signal: abort.signal,
        onProgress: showsProgress
          ? (progress) => patchAttachment(threadId, key, { progress })
          : undefined,
      })
        .then((patch) => {
          uploadFiles.current.delete(key);
          patchAttachment(threadId, key, patch);
          return patch;
        })
        .catch((e: Error) => {
          // An abort means the file was taken back; there is nothing left
          // to report it to. Anything else, a stall included, stays in the
          // composer marked failed, its file kept for a retry.
          if (e.name !== "AbortError")
            patchAttachment(threadId, key, { status: "error", error: e.message });
          throw e;
        })
        .finally(() => uploadsInFlight.current.delete(key));
      uploads.current.set(key, upload);
      return upload;
    },
    [patchAttachment],
  );

  const uploadOf = useCallback((key: string) => uploads.current.get(key), []);

  // A failed upload's retry: the same file, up again under the same chip.
  // Nothing happens for a chip whose file this page never had, one restored
  // from storage, say: it has to be picked again.
  const retryUpload = useCallback(
    (threadId: string, key: string) => {
      const file = uploadFiles.current.get(key);
      if (!file) return;
      setAttachments((all) => {
        const list = all[threadId];
        if (!list?.some((a) => a.key === key)) return all;
        return {
          ...all,
          [threadId]: list.map((a) =>
            a.key === key
              ? {
                  ...a,
                  status: "uploading" as const,
                  error: undefined,
                  progress: a.progress === undefined ? undefined : 0,
                }
              : a,
          ),
        };
      });
      uploadInto(threadId, key, file).catch(() => {});
    },
    [uploadInto],
  );

  // The files and uploads go once nothing can ask for them again: neither a
  // composer nor a message waiting on its uploads holds their chip. An upload
  // is kept only while its chip is still uploading, since a send reads the
  // finished ones from the chip itself. This covers a thread that went, and
  // a message that went with its files.
  useEffect(() => {
    const held = new Set<string>();
    const going = new Set<string>();
    for (const list of Object.values(attachments)) {
      for (const a of list) {
        held.add(a.key);
        if (a.status === "uploading") going.add(a.key);
      }
    }
    for (const e of outbox.get()) {
      for (const a of e.attachments) {
        held.add(a.key);
        if (e.status === "uploading") going.add(a.key);
      }
    }
    for (const key of uploadFiles.current.keys()) if (!held.has(key)) uploadFiles.current.delete(key);
    for (const key of uploads.current.keys()) if (!going.has(key)) uploads.current.delete(key);
  }, [attachments, outbox]);

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
    uploadFiles.current.delete(key);
    uploads.current.delete(key);
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
      uploadOf,
      retryUpload,
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
      uploadOf,
      retryUpload,
      attachFiles,
      attachToDraft,
      removeAttachment,
    ],
  );
}

/**
 * What the last page of this browser left: drafts, staged files and the
 * outbox. A send that lapsed while the page was gone goes back into its
 * composer, as a failed one does.
 */
function restoreSaved() {
  const { entries, lapsed } = loadOutbox();
  const outbox = createOutbox(entries);
  let drafts: Drafts = loadDrafts();
  let attachments = loadAttachments();
  for (const entry of lapsed) {
    const before = failedBefore(outbox, entry);
    drafts = restoreDraft(drafts, entry, before);
    attachments = restoreAttachments(attachments, entry, before);
  }
  const threadIds = new Set([
    ...Object.keys(drafts),
    ...Object.keys(attachments),
    ...entries.map((e) => e.threadId),
  ]);
  // The new-thread composer is not a thread, and is never in the list.
  threadIds.delete(NEW_THREAD);
  return { draftStore: createDraftStore(drafts), outbox, attachments, threadIds };
}

/** The thread's failed sends from before this one, oldest first: what a
    restore of it should already find at the head of the composer. */
function failedBefore(outbox: Outbox, entry: OutboxSend): OutboxSend[] {
  const all = outbox.get();
  const at = all.findIndex((e) => e.commandId === entry.commandId);
  return all
    .slice(0, at < 0 ? all.length : at)
    .filter((e) => e.threadId === entry.threadId && e.status === "failed");
}

/** Where the text of earlier failed sends, still there as they went, ends in a
    draft: past each one and the breaks that follow it. */
function afterEarlier(draft: string, before: readonly OutboxSend[]): number {
  let at = 0;
  for (const { text } of before) {
    if (!text) continue;
    if (!draft.startsWith(text, at)) break;
    const next = at + text.length;
    if (next < draft.length && draft[next] !== "\n") break;
    at = next;
    while (draft[at] === "\n") at++;
  }
  return at;
}

function splitAt(draft: string, at: number) {
  return { head: draft.slice(0, at).replace(/\n+$/, ""), tail: draft.slice(at) };
}

function restoreDraft(drafts: Drafts, entry: OutboxSend, before: readonly OutboxSend[]): Drafts {
  const current = drafts[entry.threadId] ?? "";
  const { head, tail } = splitAt(current, afterEarlier(current, before));
  const text = [head, entry.text, tail].filter(Boolean).join(PARAGRAPH);
  return { ...drafts, [entry.threadId]: text };
}

function withdrawDraft(drafts: Drafts, entry: OutboxSend, before: readonly OutboxSend[]): Drafts {
  const current = drafts[entry.threadId] ?? "";
  const { head, tail } = splitAt(current, afterEarlier(current, before));
  if (!tail.startsWith(entry.text)) return drafts;
  // What follows the message has to start on a line of its own, however many
  // breaks down: the user may have retyped the gap the restore left.
  const rest = tail.slice(entry.text.length);
  if (rest && !rest.startsWith("\n")) return drafts;
  const text = [head, rest.replace(/^\n+/, "")].filter(Boolean).join(PARAGRAPH);
  return text === current ? drafts : { ...drafts, [entry.threadId]: text };
}

function restoreAttachments(
  all: Record<string, Attachment[]>,
  entry: OutboxSend,
  before: readonly OutboxSend[],
): Record<string, Attachment[]> {
  if (entry.attachments.length === 0) return all;
  const list = all[entry.threadId] ?? [];
  const back = entry.attachments.filter((a) => !list.some((b) => b.key === a.key));
  // Behind the files of earlier failed sends still at the front.
  const earlier = new Set(before.flatMap((e) => e.attachments.map((a) => a.key)));
  let at = 0;
  while (at < list.length && earlier.has(list[at].key)) at++;
  return { ...all, [entry.threadId]: [...list.slice(0, at), ...back, ...list.slice(at)] };
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
