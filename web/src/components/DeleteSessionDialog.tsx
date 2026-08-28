import { useEffect, useRef, useState } from "react";

import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Label } from "~/components/ui/label";
import { Spinner } from "~/components/ui/spinner";
import type { SessionMeta } from "~/protocol";

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
export function useDeleteSession({
  sessions,
  onDelete,
  projectRoot,
  onStart,
  onRefused,
  onDeparted,
  onFailed,
}: {
  /** Every session omniplex knows of, to see who else is in the same checkout. */
  sessions: SessionMeta[];
  /** removeWorktree is the user's answer to the checkbox, never inferred. */
  onDelete: (id: string, removeWorktree: boolean) => void | Promise<unknown>;
  /** The project's own checkout, which is never a worktree omniplex may remove. */
  projectRoot: (id?: string) => string | undefined;
  /** Fired as the request goes, for a caller that must pin a list first. */
  onStart?: (target: SessionMeta) => void;
  /** Fired when the server would not take the request after all. */
  onRefused?: (target: SessionMeta) => void;
  /** Fired the moment the session leaves the list, for a caller with a row to
      animate out. The wait is already over by then — this is only the news. */
  onDeparted?: (target: SessionMeta) => void;
  /** Fired when teardown failed and the session is staying after all. */
  onFailed?: (target: SessionMeta) => void;
}) {
  // Deleting a session can take a checkout on disk with it, so a stray click
  // on the X must not be enough on its own — the X only opens this
  // confirmation, and the checkout only goes if it is asked for there.
  const [confirming, setConfirming] = useState<SessionMeta | null>(null);
  const [removeWorktree, setRemoveWorktree] = useState(false);
  // The delete the server is working on, which is not over when the click is:
  // the row only goes when the teardown finishes.
  const [deleting, setDeleting] = useState<SessionMeta | null>(null);
  // The delete this hook is currently living through, for the one thing that
  // arrives too late to read state: a refusal from the server.
  const latest = useRef<string | null>(null);

  const ask = (s: SessionMeta) => {
    // Defaulted on for a worktree omniplex provisioned, because that is what omniplex did
    // before and it is usually right; off for one it merely borrowed.
    setRemoveWorktree(s.workspaceMode === "managed");
    setConfirming(s);
  };

  const mode = confirming?.workspaceMode ?? "";
  // "The last session omniplex knows of" is a question the session list can already
  // answer: it holds every session's cwd. A closed session counts — it still
  // names that path, and omniplex still knows of it.
  const sharers = confirming
    ? sessions.filter((s) => s.id !== confirming.id && s.cwd === confirming.cwd)
    : [];
  // Only these two modes have a directory omniplex could remove. A local session is
  // the user's own checkout, and a session with no project has no lease at all
  // — offering a checkbox for either would be offering an action the server
  // will not perform. Nor does a managed session whose provisioning failed
  // before it got a directory: its cwd is still the project root, and the
  // server refuses to remove that whatever the dialog asked for.
  const hasWorktree =
    (mode === "managed" || mode === "borrowed") &&
    !!confirming?.cwd &&
    confirming.cwd !== projectRoot(confirming.projectId);
  const removable = hasWorktree && sharers.length === 0;
  // A turn open, or agents and shells running beside one that is over: the
  // delete cuts them off, which is worth a line before the button.
  const running =
    confirming?.attention === "working" || confirming?.attention === "background";

  // The dialog is only "busy" for the session it is currently asking about: it
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
  // about *this* session closes; the user may have dismissed it and opened
  // another meanwhile.
  const settle = (id: string) => {
    setDeleting((d) => (d?.id === id ? null : d));
    setConfirming((c) => (c?.id === id ? null : c));
  };

  // A delete is over when the session leaves the list. The request only
  // *starts* the teardown — its promise settles on acceptance, so nothing else
  // here can tell the difference between "still working" and "finished".
  //
  // This is done during the render that drops the session rather than in an
  // effect, so a caller with a row to animate still has its DOM node when it
  // hears about the departure. It lives here rather than in the sidebar
  // because every caller of this hook has to stop waiting; only the sidebar
  // has a row, and a caller without one was left spinning forever.
  if (deleting && !sessions.some((s) => s.id === deleting.id)) {
    settle(deleting.id);
    onDeparted?.(deleting);
  }

  // Teardown failed, so the session is staying. The wait is just as over as a
  // successful one: whoever asked is already being told what to do about it,
  // and a dialog still claiming to be deleting is in the way of doing it.
  const failed =
    deleting && sessions.some((s) => s.id === deleting.id && s.phase === "cleanup_failed")
      ? deleting
      : null;
  useEffect(() => {
    if (!failed) return;
    settle(failed.id);
    onFailed?.(failed);
    // settle and onFailed are recreated each render and only ever act on the
    // session they are given, so tracking them here would re-run this for no
    // change in what it does.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [failed]);

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

export type DeleteSession = ReturnType<typeof useDeleteSession>;

/**
 * The confirmation, and then the wait. Rendered above whatever opened it — in
 * the sidebar's case above both of its shapes, so that neither the sheet
 * closing nor a change of breakpoint can take it away mid-delete.
 */
export function DeleteSessionDialog({ flow }: { flow: DeleteSession }) {
  // It stays put while the delete runs: closing it on the click would be
  // claiming the session is gone at the moment the work starts. The one way
  // out is `stuck` — a teardown script that hangs must not take the window
  // with it — and taking it only hides the progress. The delete carries on,
  // and the row still leaves on its own.
  const held = flow.busy && !flow.stuck;
  return (
    <Dialog open={flow.confirming !== null} onOpenChange={(open) => !open && flow.dismiss()}>
      <DialogContent
        className="sm:max-w-sm"
        showCloseButton={!held}
        onEscapeKeyDown={(e) => held && e.preventDefault()}
        onInteractOutside={(e) => held && e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>Delete “{flow.confirming?.title || "Untitled"}”?</DialogTitle>
          {/* Whatever else it says, it says plainly whether anything on disk
              is at risk. The old copy promised a worktree removal that a
              borrowed session never performed. */}
          <DialogDescription>
            {flow.mode === "local"
              ? "This permanently deletes the session and its transcript. Your checkout is left untouched."
              : "This permanently deletes the session and its transcript."}
          </DialogDescription>
        </DialogHeader>

        {flow.confirming && flow.hasWorktree && (
          <div className="space-y-2 text-[12px]">
            {flow.removable ? (
              <>
                <div className="flex items-start gap-2">
                  {/* Settled the moment Delete was pressed: the request has
                      already gone with the answer that was ticked then, so
                      changing it now would only make the dialog lie about what
                      is happening on disk. */}
                  <Checkbox
                    id="delete-remove-worktree"
                    checked={flow.removeWorktree}
                    onCheckedChange={(v) => flow.setRemoveWorktree(v === true)}
                    disabled={flow.busy}
                    className="mt-0.5"
                  />
                  <div className="min-w-0">
                    <Label htmlFor="delete-remove-worktree" className="cursor-pointer">
                      Also delete the worktree
                    </Label>
                    <span className="text-muted-foreground block font-mono text-[11px] break-all">
                      {flow.confirming.cwd}
                    </span>
                  </div>
                </div>
                <p className="text-muted-foreground text-[11px]">
                  {flow.confirming.branch
                    ? `The branch ${flow.confirming.branch} is kept either way.`
                    : "Branches are never deleted."}
                  {flow.mode === "borrowed" && " omniplex did not create this worktree."}
                </p>
              </>
            ) : (
              <p className="text-muted-foreground text-[11px]">
                The worktree is left on disk: {flow.sharers.length} other session
                {flow.sharers.length === 1 ? "" : "s"} still
                {flow.sharers.length === 1 ? " uses" : " use"} it
                {flow.sharers[0]?.title ? ` (“${flow.sharers[0].title}”)` : ""}.
              </p>
            )}
          </div>
        )}

        {flow.confirming && flow.running && (
          <p className="text-attention-foreground text-[11px]">
            This session still has running jobs.
          </p>
        )}

        {flow.stuck && (
          <p className="text-muted-foreground text-[11px]">
            This is taking longer than usual. You can close this — the delete keeps running, and
            the session goes when it finishes.
          </p>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={flow.dismiss} disabled={flow.busy}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={flow.startDelete} disabled={flow.busy}>
            {flow.busy ? (
              <>
                <Spinner aria-hidden className="size-4" />
                {/* Named, because tearing a worktree down is the slow part
                    and the one worth waiting through. */}
                {flow.removable && flow.removeWorktree ? "Deleting worktree…" : "Deleting…"}
              </>
            ) : (
              "Delete"
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
