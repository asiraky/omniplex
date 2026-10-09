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

import type { DeleteThread } from "./DeleteThreadDialog";

/**
 * The delete confirmation's markup, apart from `DeleteThreadDialog` so it can
 * load after the first paint. Only that wrapper renders it.
 */
export function DeleteThreadConfirm({ flow }: { flow: DeleteThread }) {
  // It stays put while the delete runs: closing it on the click would be
  // claiming the thread is gone at the moment the work starts. The one way
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
              borrowed thread never performed. */}
          <DialogDescription>
            {flow.mode === "local"
              ? "This permanently deletes the thread and its transcript. Your checkout is left untouched."
              : "This permanently deletes the thread and its transcript."}
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
                The worktree is left on disk: {flow.sharers.length} other thread
                {flow.sharers.length === 1 ? "" : "s"} still
                {flow.sharers.length === 1 ? " uses" : " use"} it
                {flow.sharers[0]?.title ? ` (“${flow.sharers[0].title}”)` : ""}.
              </p>
            )}
          </div>
        )}

        {flow.confirming && flow.running && (
          <p className="text-attention-foreground text-[11px]">
            This thread still has running jobs.
          </p>
        )}

        {flow.stuck && (
          <p className="text-muted-foreground text-[11px]">
            This is taking longer than usual. You can close this — the delete keeps running, and
            the thread goes when it finishes.
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
