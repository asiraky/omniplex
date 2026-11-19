import { BotIcon } from "lucide-react";
import { memo } from "react";

import { HarnessBadge } from "~/components/HarnessBadge";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "~/components/ui/context-menu";
import { fmtAgo } from "~/lib/format";
import type { AgentTab } from "~/lib/agentNav";
import { cn } from "~/lib/utils";
import type { Agent, ThreadMeta } from "~/protocol";
import { rowStatus } from "~/threadStatus";
import { useNow } from "~/useNow";

/** What the row's menu asks App to do with an agent's thread. */
export type AgentAction = "restart" | "newThread";

/** Waiting on the user for a card: an approval or a question. */
function needsYou(t: ThreadMeta | undefined): boolean {
  return t?.attention === "needs_permission" || t?.attention === "needs_answer";
}

/**
 * One agent in the sidebar. Tapping it goes to its live thread, the thing it
 * is for; with no thread, to its page. It has no X and no pin: an agent is
 * archived from its page, and it already sits in a section of its own. The
 * long-press menu has the rest.
 *
 * Memoised like ThreadRow, for the same reason: App re-renders on every frame.
 */
export const AgentRow = memo(function AgentRow({
  agent,
  thread,
  active,
  accentOf,
  onOpen,
  onOpenAgent,
  onAction,
}: {
  agent: Agent;
  /** The agent's live thread, as the list has it; absent with none. */
  thread: ThreadMeta | undefined;
  active: boolean;
  accentOf: (harness: string) => string | undefined;
  onOpen: (agent: Agent) => void;
  onOpenAgent: (agentId: string, tab?: AgentTab) => void;
  onAction: (agent: Agent, action: AgentAction) => void;
}) {
  const now = useNow();
  const live = !!agent.threadId;
  const pending = needsYou(thread);
  const busy = !!thread && rowStatus(thread) === "busy";
  // Quiet unless it wants something or is working, like a read thread's row.
  const recede = !active && !pending && !busy;
  return (
    <div className="mb-0.5">
      <ContextMenu modal={false}>
        <ContextMenuTrigger asChild>
          <div
            className={cn(
              "group relative min-w-0 rounded-lg transition-colors",
              active ? "bg-sidebar-accent text-sidebar-accent-foreground" : "hover:bg-sidebar-accent/60",
              recede && "opacity-60 hover:opacity-100 focus-within:opacity-100",
            )}
          >
            <button
              type="button"
              onClick={() => onOpen(agent)}
              aria-current={active ? "true" : undefined}
              aria-label={`Agent ${agent.name}${pending ? ", needs you" : ""}`}
              className="focus-visible:ring-ring block w-full min-w-0 cursor-pointer rounded-lg px-2.5 py-2 text-left outline-none focus-visible:ring-2"
            >
              <span className="flex items-center gap-1.5">
                <BotIcon aria-hidden className="text-primary size-3.5 shrink-0" />
                <span className={cn("min-w-0 truncate text-[13px]", pending && !active && "font-semibold")}>
                  {agent.name}
                </span>
                {thread && (
                  <span className="text-muted-foreground ml-auto shrink-0 font-mono text-[10px]">
                    {fmtAgo(thread.updatedAt, now)}
                  </span>
                )}
              </span>
              <span className="text-muted-foreground mt-1 flex min-w-0 items-center gap-1 font-mono text-[10px]">
                <span className="min-w-0 truncate">
                  {live ? `r${agent.promptRev} · epoch ${agent.epoch}` : "no thread"}
                </span>
                <span className="ml-auto flex shrink-0 items-center gap-1.5 pl-1.5">
                  {pending && (
                    <span className="bg-attention rounded-full px-1.5 font-sans text-[10px] leading-[15px] font-semibold text-[#201400]">
                      needs you
                    </span>
                  )}
                  <span className="relative flex">
                    <HarnessBadge harness={agent.harness} accent={accentOf(agent.harness)} className="size-3.5" />
                    {busy && (
                      <span
                        role="status"
                        aria-label="Working"
                        className="border-muted-foreground/25 border-t-muted-foreground pointer-events-none absolute -inset-[3px] animate-spin rounded-full border-[1.5px] [animation-duration:1.6s] motion-reduce:animate-none"
                      />
                    )}
                  </span>
                </span>
              </span>
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="min-w-48">
          {live && (
            <>
              <ContextMenuItem onSelect={() => onOpen(agent)}>
                <BotIcon />
                Open thread
              </ContextMenuItem>
              <MenuSeparator />
            </>
          )}
          <div className="text-muted-foreground truncate px-2 py-1 text-[11px] font-medium">{agent.name}</div>
          <MenuLink label="Prompt" hint={`r${agent.promptRev}`} onSelect={() => onOpenAgent(agent.id, "prompt")} />
          <MenuLink label="Compaction" hint={`c${agent.compactRev}`} onSelect={() => onOpenAgent(agent.id, "compact")} />
          <MenuLink
            label="Log"
            hint={agent.epoch > 0 ? `${agent.epoch} compaction${agent.epoch === 1 ? "" : "s"}` : undefined}
            onSelect={() => onOpenAgent(agent.id, "log")}
          />
          <MenuLink label="Settings" onSelect={() => onOpenAgent(agent.id, "settings")} />
          <MenuSeparator />
          {live && (
            <MenuLink label="Restart thread" hint="Keeps the transcript" onSelect={() => onAction(agent, "restart")} />
          )}
          <MenuLink
            label={live ? "New thread…" : "Start thread…"}
            hint={live ? "Archives this one" : undefined}
            onSelect={() => onAction(agent, "newThread")}
          />
        </ContextMenuContent>
      </ContextMenu>
    </div>
  );
});

function MenuLink({ label, hint, onSelect }: { label: string; hint?: string; onSelect: () => void }) {
  return (
    <ContextMenuItem onSelect={onSelect}>
      <span className="flex-1">{label}</span>
      {hint && <span className="text-muted-foreground font-mono text-[11px]">{hint}</span>}
    </ContextMenuItem>
  );
}

// The context menu has no separator of its own; this is the dropdown's.
function MenuSeparator() {
  return <div role="separator" className="bg-border -mx-1 my-1 h-px" />;
}
