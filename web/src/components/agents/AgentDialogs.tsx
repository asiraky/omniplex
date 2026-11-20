import { useState } from "react";

import { ConfirmDialog, ErrorLine } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Spinner } from "~/components/ui/spinner";
import type { AgentApi, Command } from "~/lib/agentApi";
import { toast } from "~/lib/toast";
import { errorText } from "~/lib/utils";
import type { Agent } from "~/protocol";

/**
 * Restart an agent's live thread on its newest prompt. Nothing is lost but the
 * running shells, so the button is the plain primary one, not a red one; it
 * still asks, because killing a shell mid-run is not something to do by a
 * stray tap. Mount it while open.
 */
export function RestartAgentDialog({
  agent,
  command,
  onClose,
}: {
  agent: Agent;
  command: Command;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = async () => {
    if (!agent.threadId || busy) return;
    setBusy(true);
    setError(null);
    try {
      await command("restart_thread", { threadId: agent.threadId });
      onClose();
      toast.success("Thread restarted");
    } catch (e) {
      setError(errorText(e));
      setBusy(false);
    }
  };
  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="sm:max-w-sm" showCloseButton={!busy}>
        <DialogHeader>
          <DialogTitle>Restart {agent.name}?</DialogTitle>
          <DialogDescription>
            The thread keeps its transcript and resumes with prompt r{agent.promptRev}. Shells that are still running
            are killed. No compaction happens.
          </DialogDescription>
        </DialogHeader>
        {error && <ErrorLine message={error} />}
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy} className="min-h-11 md:min-h-9">
            Cancel
          </Button>
          <Button onClick={() => void run()} disabled={busy} className="min-h-11 md:min-h-9">
            {busy && <Spinner aria-hidden className="size-4" />}
            Restart
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Archive an agent. Its thread stops and it leaves the sidebar; onArchived
 * takes the user off its page, which no longer has an agent to show.
 */
export function ArchiveAgentDialog({
  agent,
  api,
  open,
  onOpenChange,
  onArchived,
}: {
  agent: Agent;
  api: AgentApi;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onArchived: () => void;
}) {
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Archive ${agent.name}?`}
      description="Stops its thread and hides the agent. Revisions, the log and the archived threads are kept."
      confirmLabel="Archive"
      onConfirm={async () => {
        await api.archive(agent.id);
        toast.success(`Archived ${agent.name}`);
        onArchived();
      }}
    />
  );
}
