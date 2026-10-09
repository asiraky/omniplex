import { useEffect, useRef, useState } from "react";

import type { ThreadMeta } from "~/protocol";

import type { DeleteThreadConfirm } from "./DeleteThreadConfirm";

// How long a delete may take before the dialog stops holding the window shut.
const STUCK_MS = 10_000;

/**
 * Everything a delete needs to be asked for and waited through: the guards on
 * what may be removed from disk, the confirmation's own state, and the wait
 * while the server tears the workspace down.
 *
 * What is deliberately *not* here is anything about a list — the row's exit
 * animation and the ordering it is pinned to belong to the sidebar, which is
 * the only place a row exists. That split is what lets the transcript's
 * "this landed" prompt open the very same confirmation, with the very same
 * guards, without inheriting a row it does not have.
 */
export function useDeleteThread({
  threads,
  onDelete,
  projectFolders,
  onStart,
  onRefused,
  onDeparted,
  onFailed,
}: {
  /** Every thread omniplex knows of, to see who else is in the same checkout. */
  threads: ThreadMeta[];
  /** removeWorktree is the user's answer to the checkbox, never inferred. */
  onDelete: (id: string, removeWorktree: boolean) => void | Promise<unknown>;
  /** The project's own checkout, which is never a worktree omniplex may remove. */
  projectFolders: (id?: string) => string[];
  /** Fired as the request goes, for a caller that must pin a list first. */
  onStart?: (target: ThreadMeta) => void;
  /** Fired when the server would not take the request after all. */
  onRefused?: (target: ThreadMeta) => void;
  /** Fired the moment the thread leaves the list, for a caller with a row to
      animate out. The wait is already over by then — this is only the news. */
  onDeparted?: (target: ThreadMeta) => void;
  /** Fired when teardown failed and the thread is staying after all. */
  onFailed?: (target: ThreadMeta) => void;
}) {
  // Deleting a thread can take a checkout on disk with it, so a stray click
  // on the X must not be enough on its own — the X only opens this
  // confirmation, and the checkout only goes if it is asked for there.
  const [confirming, setConfirming] = useState<ThreadMeta | null>(null);
  const [removeWorktree, setRemoveWorktree] = useState(false);
  // The delete the server is working on, which is not over when the click is:
  // the row only goes when the teardown finishes.
  const [deleting, setDeleting] = useState<ThreadMeta | null>(null);
  // The delete this hook is currently living through, for the one thing that
  // arrives too late to read state: a refusal from the server.
  const latest = useRef<string | null>(null);

  const ask = (s: ThreadMeta) => {
    // Defaulted on for a worktree omniplex provisioned, because that is what omniplex did
    // before and it is usually right; off for one it merely borrowed.
    setRemoveWorktree(s.workspaceMode === "managed");
    setConfirming(s);
  };

  const mode = confirming?.workspaceMode ?? "";
  // "The last thread omniplex knows of" is a question the thread list can already
  // answer: it holds every thread's cwd. A closed thread counts — it still
  // names that path, and omniplex still knows of it.
  const sharers = confirming
    ? threads.filter((s) => s.id !== confirming.id && s.cwd === confirming.cwd)
    : [];
  // Only these two modes have a directory omniplex could remove. A local thread is
  // the user's own checkout, and a thread with no project has no lease at all
  // — offering a checkbox for either would be offering an action the server
  // will not perform. Nor does a managed thread whose provisioning failed
  // before it got a directory: its cwd is still the project folder, and the
  // server refuses to remove that whatever the dialog asked for.
  const hasWorktree =
    (mode === "managed" || mode === "borrowed") &&
    !!confirming?.cwd &&
    !projectFolders(confirming.projectId).includes(confirming.cwd);
  const removable = hasWorktree && sharers.length === 0;
  // A turn open, or agents and shells running beside one that is over: the
  // delete cuts them off, which is worth a line before the button.
  const running =
    confirming?.attention === "working" || confirming?.attention === "background";

  // The dialog is only "busy" for the thread it is currently asking about: it
  // can be dismissed once the wait has gone long and reopened on another row,
  // and that row's Delete button must still be a live button.
  const busy = !!deleting && deleting.id === confirming?.id;

  // A deprovision hook is a user's own script and can hang forever. The dialog
  // holds the window while a delete is running, so it has to admit when the
  // wait has stopped being normal and let go.
  const [stuck, setStuck] = useState(false);
  useEffect(() => {
    if (!deleting) {
      setStuck(false);
      return;
    }
    const t = setTimeout(() => setStuck(true), STUCK_MS);
    return () => clearTimeout(t);
  }, [deleting]);

  // Stop claiming a delete is still happening. Only the dialog that was asking
  // about *this* thread closes; the user may have dismissed it and opened
  // another meanwhile.
  const settle = (id: string) => {
    setDeleting((d) => (d?.id === id ? null : d));
    setConfirming((c) => (c?.id === id ? null : c));
  };

  // A delete is over when the thread leaves the list. The request only
  // *starts* the teardown — its promise settles on acceptance, so nothing else
  // here can tell the difference between "still working" and "finished".
  //
  // This is done during the render that drops the thread rather than in an
  // effect, so a caller with a row to animate still has its DOM node when it
  // hears about the departure. It lives here rather than in the sidebar
  // because every caller of this hook has to stop waiting; only the sidebar
  // has a row, and a caller without one was left spinning forever.
  if (deleting && !threads.some((s) => s.id === deleting.id)) {
    settle(deleting.id);
    onDeparted?.(deleting);
  }

  // Teardown failed, so the thread is staying. The wait is just as over as a
  // successful one: whoever asked is already being told what to do about it,
  // and a dialog still claiming to be deleting is in the way of doing it.
  // Settled during render for the same reason as the departure above, and
  // settling clears `deleting`, so this runs once per failure.
  if (deleting && threads.some((s) => s.id === deleting.id && s.phase === "cleanup_failed")) {
    settle(deleting.id);
    onFailed?.(deleting);
  }

  const startDelete = () => {
    if (!confirming || busy) return;
    const target = confirming;
    latest.current = target.id;
    onStart?.(target);
    setDeleting(target);
    Promise.resolve(onDelete(target.id, removable && removeWorktree)).catch(() => {
      // The failure has already been reported where it was raised; all that is
      // left here is to stop claiming the delete is still happening. Written
      // against the current state, not the state at the click: a slow refusal
      // must not clear a delete the user has since started on another row.
      if (latest.current !== target.id) return;
      settle(target.id);
      onRefused?.(target);
    });
  };

  return {
    confirming,
    ask,
    mode,
    sharers,
    hasWorktree,
    removable,
    running,
    removeWorktree,
    setRemoveWorktree,
    deleting,
    busy,
    stuck,
    startDelete,
    settle,
    dismiss: () => setConfirming(null),
  };
}

export type DeleteThread = ReturnType<typeof useDeleteThread>;

// The dialog itself, fetched once something that can delete is on screen
// rather than with the first paint. Nobody deletes a thread in the first
// second, and the initial bundle is what a phone on 4G waits for; by the time
// an X is tapped it has long arrived.
let Confirm: typeof DeleteThreadConfirm | undefined;
let loading: Promise<void> | undefined;
export function loadDeleteThreadDialog(): Promise<void> {
  loading ??= import("./DeleteThreadConfirm").then(
    (m) => {
      Confirm = m.DeleteThreadConfirm;
    },
    () => {
      // Offline, most likely: the next mount tries again.
      loading = undefined;
    },
  );
  return loading;
}

/**
 * The confirmation, and then the wait. Rendered above whatever opened it — in
 * the sidebar's case above both of its shapes, so that neither the sheet
 * closing nor a change of breakpoint can take it away mid-delete.
 *
 * Nothing until its code has arrived. A delete asked for before then is not
 * lost: `confirming` is held in the flow, and the dialog opens on it the
 * moment it can.
 */
export function DeleteThreadDialog({ flow }: { flow: DeleteThread }) {
  const [ready, setReady] = useState(Confirm !== undefined);
  useEffect(() => {
    if (!ready) void loadDeleteThreadDialog().then(() => setReady(Confirm !== undefined));
  }, [ready]);
  return ready && Confirm ? <Confirm flow={flow} /> : null;
}
