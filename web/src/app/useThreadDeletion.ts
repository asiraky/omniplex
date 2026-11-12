import { useCallback, useEffect, useRef } from "react";

import { useDeleteThread } from "~/components/DeleteThreadDialog";
import { toast } from "~/lib/toast";
import { useLatest } from "~/useLatest";

import type { ThreadState } from "~/protocol";

import { useThread } from "./threadStore";
import type { Wire } from "./useWire";

/**
 * Deleting a thread: the confirmation flow shared by the sidebar row and the
 * transcript's "finish with this thread", and the force delete offered when a
 * teardown fails.
 */
export function useThreadDeletion({
  wire,
  activeId,
  select,
}: {
  wire: Wire;
  activeId: string | null;
  select: (id: string) => void;
}) {
  const { clientRef, activeRef, threads, projects, threadStore } = wire;

  // The returned promise settles when the server has *accepted* the delete,
  // not when it is done: the thread is gone when it leaves the list, which
  // is what the sidebar waits on. Rejecting it is the sidebar's cue to stop
  // waiting, so the error is re-thrown after it has been reported.
  const remove = useCallback(
    (id: string, removeWorktree: boolean) => {
      if (id !== activeRef.current) select(id);
      const client = clientRef.current;
      if (!client) {
        toast.error("Could not delete that thread", { description: "Not connected." });
        return Promise.reject(new Error("not connected"));
      }
      return client.command("delete_thread", { threadId: id, removeWorktree }).catch((e) => {
        toast.error("Could not delete that thread", { description: e.message });
        throw e;
      });
    },
    [activeRef, clientRef, select],
  );

  // The list is read through a ref, at the moment of asking: the transcript
  // takes this, and a callback that changed with every broadcast would redraw
  // the memoised transcript for each one.
  const threadsRef = useLatest(threads);
  const forceDelete = useCallback(
    (id: string) => {
      // Only a worktree omniplex provisioned is omniplex's to destroy, so only that case may
      // promise it. The old copy promised it to every thread and kept the
      // promise for one of them.
      const removes = threadsRef.current.find((s) => s.id === id)?.workspaceMode === "managed";
      const accepted = window.confirm(
        removes
          ? "Tear down failed. Would you like to force delete?\n\nThis skips the teardown script, removes the recorded Git worktree, and permanently deletes the thread."
          : "Tear down failed. Would you like to force delete?\n\nThis skips the teardown script and permanently deletes the thread. The checkout is left on disk — omniplex did not create it.",
      );
      if (!accepted) return;
      clientRef.current
        ?.command("force_delete_thread", { threadId: id })
        .catch((e) => toast.error("Force delete failed", { description: e.message }));
    },
    [clientRef, threadsRef],
  );

  // A delete whose teardown failed asks once whether to force it, per failure.
  // What it watches is selected down to the seq of a failed delete, and null
  // otherwise, so the frames of a thread that is not failing to delete (all
  // of them, nearly) rerender nothing. Still an effect rather than a store
  // listener: the confirm blocks, and it should block over the failure the
  // page has painted, not over the frame before it.
  const failedDeleteSeq = useThread(threadStore, failedDeleteSeqOf);
  const forcePromptedRef = useRef<string | null>(null);
  useEffect(() => {
    if (!activeId || failedDeleteSeq === null) return;
    const key = `${activeId}:${failedDeleteSeq}`;
    if (forcePromptedRef.current === key) return;
    forcePromptedRef.current = key;
    forceDelete(activeId);
  }, [activeId, failedDeleteSeq, forceDelete]);

  // Whether the work in this thread has landed, and the confirmation the
  // transcript's prompt opens. The dialog and its guards are the sidebar's
  // own, so "finish with this thread" and the row's X are the same action
  // reached from two places; only the sidebar's row animation is not shared,
  // because the transcript has no row.
  const deleteFlow = useDeleteThread({
    threads,
    onDelete: remove,
    projectFolders: (id) => projects.find((p) => p.id === id)?.folders.map((f) => f.path) ?? [],
  });

  return { remove, forceDelete, deleteFlow };
}

function failedDeleteSeqOf(state: ThreadState | null) {
  return state?.phase === "cleanup_failed" && state.workspace.deleteAfterCleanup ? state.seq : null;
}
