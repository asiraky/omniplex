import { useState } from "react";

import { ErrorLine } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
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
import { Textarea } from "~/components/ui/textarea";
import { agentApi, type Command } from "~/lib/agentApi";
import { useAgentNav } from "~/lib/agentNav";
import { errorText } from "~/lib/utils";
import type { Agent } from "~/protocol";

/**
 * New thread for an agent: archives its live thread and starts a fresh one on
 * the current revisions. It is the reset for an epoch gone bad, so it says
 * what goes, and offers the agent's saved first message to edit for this
 * start only. Mount it while open, so each opening starts from the kickoff.
 */
export function NewAgentThreadDialog({
  agent,
  command,
  onClose,
}: {
  agent: Agent;
  command: Command;
  onClose: () => void;
}) {
  const { openThread } = useAgentNav();
  const [message, setMessage] = useState(agent.kickoff ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const live = !!agent.threadId;

  const start = async () => {
    const text = message.trim();
    if (!text || busy) return;
    setBusy(true);
    setError(null);
    try {
      const { threadId } = await agentApi(command).newThread(agent.id, text);
      onClose();
      openThread(threadId);
    } catch (e) {
      setError(errorText(e));
      setBusy(false);
    }
  };

  return (
    // Held open while the server swaps threads: closing on the click would
    // leave the user on a thread that is about to be archived under them.
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="sm:max-w-md" showCloseButton={!busy}>
        <DialogHeader>
          <DialogTitle>New thread for {agent.name}</DialogTitle>
          <DialogDescription>
            {live
              ? `This archives the current thread${agent.epoch > 0 ? ` (${agent.epoch} compaction${agent.epoch === 1 ? "" : "s"})` : ""} and starts a fresh session. Nothing carries over except the brief.`
              : "This starts a fresh session on the agent's brief."}
          </DialogDescription>
        </DialogHeader>

        <p className="text-muted-foreground rounded-lg border px-3 py-2 text-[12px] leading-relaxed">
          Starts with prompt <b className="text-foreground font-mono">r{agent.promptRev}</b> and compaction
          instructions <b className="text-foreground font-mono">c{agent.compactRev}</b>.
          {live && " The archived thread and its log stay readable from the Log tab."}
        </p>

        <div className="space-y-1.5">
          <Label htmlFor="agent-first-message">First message</Label>
          <Textarea
            id="agent-first-message"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            disabled={busy}
            className="max-h-[40dvh] min-h-24"
          />
          <p className="text-muted-foreground text-[11.5px]">
            {agent.kickoff ? "Saved on the agent. Edit it here for this start only." : "What the agent starts on."}
          </p>
        </div>

        {error && <ErrorLine message={error} />}

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy} className="min-h-11 md:min-h-9">
            Cancel
          </Button>
          <Button onClick={() => void start()} disabled={busy || !message.trim()} className="min-h-11 md:min-h-9">
            {busy && <Spinner aria-hidden className="size-4" />}
            {live ? "Archive and start" : "Start"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
