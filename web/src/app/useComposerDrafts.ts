import { useCallback, useEffect, useRef, useState } from "react";

import { uuid } from "~/client";
import { stageFile, uploadStaged, type Attachment } from "~/lib/attachments";
import type { ThreadMeta } from "~/protocol";

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
 * there when you come back. Thread scope only: no persistence, and the maps are
 * pruned as threads go away.
 */
export function useComposerDrafts(threads: ThreadMeta[], activeId: string | null) {
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  // Images staged for the next message, per thread and for the same reason as
  // the drafts: switching away and back must not lose what you attached. The
  // upload starts as soon as a picture is picked, so by send time this is a
  // list of ids the server already holds.
  const [attachments, setAttachments] = useState<Record<string, Attachment[]>>({});
  const setDraft = useCallback(
    (id: string, text: string) => setDrafts((d) => (d[id] === text ? d : { ...d, [id]: text })),
    [],
  );
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
  const seenThreads = useRef<Set<string>>(new Set());
  useEffect(() => {
    for (const s of threads) seenThreads.current.add(s.id);
    setDrafts((d) => {
      const live = new Set(threads.map((s) => s.id));
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
      const live = new Set(threads.map((s) => s.id));
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
  }, [threads]);

  return {
    drafts,
    setDraft,
    setDrafts,
    attachments,
    setAttachments,
    draftFiles,
    uploadInto,
    attachFiles,
    attachToDraft,
    removeAttachment,
  };
}
