import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { useThread, useThreadStore } from "~/app/threadStore";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "~/components/ui/sheet";
import { Spinner } from "~/components/ui/spinner";
import type { FilesSince, RestoreBlocker, RewindTarget } from "~/lib/rewind";
import { toast } from "~/lib/toast";
import type { ThreadState } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

function removesLine(later: number): string {
  return later === 0
    ? "Removes this message from the thread and from the agent's memory."
    : `Removes this message and ${later} after it from the thread and from the agent's memory.`;
}

function filesLine({ count, atLeast }: FilesSince): string {
  if (count === 0) {
    return atLeast
      ? "Puts back the files changed since this message."
      : "Puts the files back as they were before this message.";
  }
  const files = count === 1 && !atLeast ? "1 file" : `${atLeast ? "at least " : ""}${count} files`;
  return `Puts back ${files} changed since this message.`;
}

const NO_RESTORE: Record<RestoreBlocker, string> = {
  project: "Files can't be restored: this thread works in the project folder.",
  shared: "Files can't be restored: another open thread uses this folder.",
  unmanaged: "Files can't be restored: this thread isn't in a worktree Omniplex made.",
};

/**
 * The confirmation behind "Edit from here": cut the thread back to before a
 * prompt, with or without putting the files back, and hand the prompt to the
 * composer. A bottom sheet on a phone, a dialog with a pointer.
 *
 * It holds shut while the rewind runs, and stays open with the reason when the
 * server refuses. A rewind can land without its answer (a slow link times the
 * command out, or the files part failed after the conversation was cut): the
 * turn leaving the thread is what says it happened, so that closes it too.
 */
export default function RewindSheet({
  target,
  rewind,
  onRewound,
  focusAfter,
  onClose,
}: {
  target: RewindTarget;
  /** Resolves null once done, else why not. */
  rewind: (restoreFiles: boolean) => Promise<string | null>;
  /** The thread was cut back: give the prompt back and close. */
  onRewound: () => void;
  /** Once it has gone after a rewind, where the focus goes instead of back to
      the button that opened it, which has gone with its message. Only then: a
      focus moved earlier is pulled back into the sheet by its focus trap. */
  focusAfter?: () => void;
  onClose: () => void;
}) {
  const isDesktop = useIsDesktop();
  const [busy, setBusy] = useState<"keep" | "restore" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const attempted = useRef(false);
  const done = useRef(false);
  const lastError = useRef<string | null>(null);

  const { threadId, turnId } = target;
  const gone = useThread(
    useThreadStore(),
    useCallback(
      (s: ThreadState | null) =>
        !!s && s.threadId === threadId && !s.turns.some((t) => t.id === turnId),
      [threadId, turnId],
    ),
  );

  const finish = useCallback(() => {
    if (done.current) return;
    done.current = true;
    // The conversation went, but the server also said something failed:
    // the files restore, as the only part that runs after the cut.
    if (lastError.current) {
      toast.error("Rewound, but something went wrong", { description: lastError.current });
    }
    onRewound();
  }, [onRewound]);

  useEffect(() => {
    if (!gone) return;
    if (attempted.current) finish();
    // Rewound from somewhere else before anything was asked here.
    else onClose();
  }, [gone, finish, onClose]);

  const run = async (restoreFiles: boolean) => {
    if (busy) return;
    attempted.current = true;
    setBusy(restoreFiles ? "restore" : "keep");
    setError(null);
    const err = await rewind(restoreFiles);
    if (done.current) {
      if (err) toast.error("Rewound, but something went wrong", { description: err });
      return;
    }
    lastError.current = err;
    if (err === null) {
      finish();
      return;
    }
    setBusy(null);
    setError(err);
  };

  const onOpenChange = (open: boolean) => {
    if (!open && !busy) onClose();
  };
  const hold = (e: Event) => {
    if (busy) e.preventDefault();
  };
  const closeFocus = (e: Event) => {
    if (!done.current) return;
    e.preventDefault();
    focusAfter?.();
  };

  const title = "Edit from here?";
  const description = removesLine(target.later);
  const body: ReactNode = (
    <div className="flex flex-col gap-3">
      <p className="text-muted-foreground text-sm">
        Its text goes back in the composer to edit and send.
      </p>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-col gap-2">
        <Button className="w-full" disabled={!!busy} onClick={() => void run(false)}>
          {busy === "keep" && <Spinner aria-hidden />}
          Rewind, keep files
        </Button>
        {target.restore === null ? (
          <div className="flex flex-col gap-1">
            <Button
              variant="destructive"
              className="w-full"
              disabled={!!busy}
              onClick={() => void run(true)}
            >
              {busy === "restore" && <Spinner aria-hidden />}
              Rewind and restore files
            </Button>
            <p className="text-muted-foreground text-center text-[12px]">
              {filesLine(target.files)}
            </p>
          </div>
        ) : (
          <p className="text-muted-foreground text-[12px]">{NO_RESTORE[target.restore]}</p>
        )}
        <Button variant="outline" className="w-full" disabled={!!busy} onClick={onClose}>
          Cancel
        </Button>
      </div>
    </div>
  );

  if (!isDesktop) {
    return (
      <Sheet open onOpenChange={onOpenChange}>
        <SheetContent
          side="bottom"
          showCloseButton={!busy}
          onEscapeKeyDown={hold}
          onInteractOutside={hold}
          onCloseAutoFocus={closeFocus}
          className="scroll-thin mx-auto max-h-[85dvh] max-w-lg overflow-y-auto rounded-t-xl pb-[calc(1rem+env(safe-area-inset-bottom))]"
        >
          <SheetHeader className="pr-12">
            <SheetTitle>{title}</SheetTitle>
            <SheetDescription>{description}</SheetDescription>
          </SheetHeader>
          <div className="px-4">{body}</div>
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent
        className="sm:max-w-sm"
        showCloseButton={!busy}
        onEscapeKeyDown={hold}
        onInteractOutside={hold}
        onCloseAutoFocus={closeFocus}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {body}
      </DialogContent>
    </Dialog>
  );
}
