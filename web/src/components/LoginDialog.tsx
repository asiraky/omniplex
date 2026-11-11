import { useState } from "react";

import { uuid } from "~/client";
import { TerminalSurface } from "~/components/panel/TerminalSurface";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "~/components/ui/dialog";
import { endTerminal } from "~/lib/termSocket";

/**
 * A harness's own sign-in, run in a terminal the user can see and type into.
 * Nothing here knows how Claude (or anyone) logs in: the server runs the
 * adapter's login command under the instance's environment, and the flow is
 * whatever that command does — a URL to open, a code to paste back. Closing
 * the dialog is what triggers the recheck, so a finished login shows up as
 * "ready" without a second click.
 */
export function LoginDialog({
  instanceId,
  name,
  onEnded,
  onClose,
}: {
  instanceId: string;
  name: string;
  /** The sign-in process exited: whatever it did, the harness's state may have changed. */
  onEnded: () => void;
  onClose: () => void;
}) {
  // One sign-in per dialog: a dropped socket reattaches to it, and closing
  // the dialog ends it rather than leaving it to the server's TTL.
  const [termId] = useState(uuid);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (open) return;
        endTerminal(termId);
        onClose();
      }}
    >
      <DialogContent className="flex h-[80dvh] max-h-[80dvh] w-[calc(100vw-1rem)] max-w-3xl flex-col gap-3 p-4 sm:w-full">
        <DialogHeader>
          <DialogTitle>Sign in to {name}</DialogTitle>
          <DialogDescription>
            This is {name}&apos;s own sign-in, running on the server. Follow what it prints — usually a
            link to open and a code to paste back. Close this when it says you are signed in.
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-hidden rounded-md">
          <TerminalSurface termId={termId} target={{ login: instanceId }} onEnded={onEnded} />
        </div>
      </DialogContent>
    </Dialog>
  );
}
