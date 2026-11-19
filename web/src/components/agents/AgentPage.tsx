import {
  ArchiveIcon,
  ArrowLeftIcon,
  BotIcon,
  EllipsisIcon,
  PanelLeftIcon,
  RotateCwIcon,
  SquarePenIcon,
} from "lucide-react";
import { useState } from "react";

import { IconButton } from "~/components/IconButton";
import { Loading, Segmented } from "~/components/tools/parts";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import type { AgentApi } from "~/lib/agentApi";
import type { AgentFocus, AgentTab } from "~/lib/agentNav";
import type { Agent, HarnessMeta, Project } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";
import { AgentSettingsTab } from "./AgentSettingsTab";
import { CompactionTab } from "./CompactionTab";
import { LogTab } from "./LogTab";
import { PromptTab } from "./PromptTab";
import { RevisionDrafts } from "./revisionDrafts";

/** What the page asks App to do with the agent; App holds the dialogs, which the sidebar opens too. */
export type AgentPageAction = "restart" | "newThread" | "archive";

const TABS: { id: AgentTab; label: string }[] = [
  { id: "prompt", label: "Prompt" },
  { id: "compact", label: "Compaction" },
  { id: "log", label: "Log" },
  { id: "settings", label: "Settings" },
];

/**
 * An agent's page, in the main column beside the sidebar: its brief, its
 * compaction instructions, its log and its settings, one tab each. It is the
 * place the agent is configured; the thread is where it works, one tap away.
 * Each tab sets its own width and padding, so the page adds none around them.
 */
export function AgentPage({
  agent,
  loaded,
  tab,
  focus,
  api,
  projects,
  harnesses,
  sidebarOpen,
  onShowSidebar,
  onBack,
  onOpenThread,
  onTab,
  onAction,
}: {
  /** Absent when no live agent has this id: archived, or not in yet. */
  agent: Agent | undefined;
  /** Whether the agents list has arrived, so absent means gone. */
  loaded: boolean;
  tab: AgentTab;
  focus?: AgentFocus;
  api: AgentApi;
  projects: Project[];
  harnesses: HarnessMeta[];
  sidebarOpen: boolean;
  onShowSidebar: () => void;
  onBack: () => void;
  onOpenThread: (threadId: string) => void;
  onTab: (tab: AgentTab) => void;
  onAction: (agent: Agent, action: AgentPageAction) => void;
}) {
  const isDesktop = useIsDesktop();
  const project = projects.find((p) => p.id === agent?.projectId);
  const live = !!agent?.threadId;
  // Unsaved Prompt and Compaction edits, kept while the page is up so another tab and back finds them.
  const [drafts] = useState(() => new Map());

  return (
    <section aria-label={agent ? `Agent ${agent.name}` : "Agent"} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex items-center gap-1 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:gap-2 md:px-3">
        {isDesktop && !sidebarOpen && (
          <IconButton label="Show threads" shortcut="toggleSidebar" onClick={onShowSidebar}>
            <PanelLeftIcon className="size-3.5" />
          </IconButton>
        )}
        <IconButton label="Back to the thread" onClick={onBack}>
          <ArrowLeftIcon />
        </IconButton>
        {agent && (
          <>
            <BotIcon aria-hidden className="text-primary size-4 shrink-0" />
            <h1 className="min-w-0 truncate text-[14px] font-semibold">{agent.name}</h1>
            <span className="text-muted-foreground hidden min-w-0 truncate text-[12px] md:inline">
              {project?.name ?? "No project"} · {live ? `live thread, epoch ${agent.epoch}` : "no thread"}
            </span>
            <span className="flex-1" />
            {live ? (
              <Button variant="outline" size="sm" className="h-11 md:h-8" onClick={() => onOpenThread(agent.threadId!)}>
                Open thread
              </Button>
            ) : (
              <Button size="sm" className="h-11 md:h-8" onClick={() => onAction(agent, "newThread")}>
                Start thread
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <IconButton label="More" className="text-muted-foreground hover:text-foreground">
                  <EllipsisIcon />
                </IconButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-52">
                {live && (
                  <>
                    <DropdownMenuItem onSelect={() => onAction(agent, "restart")}>
                      <RotateCwIcon />
                      <MenuText title="Restart thread" sub="Keeps the transcript" />
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => onAction(agent, "newThread")}>
                      <SquarePenIcon />
                      <MenuText title="New thread…" sub="Archives the current one" />
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                  </>
                )}
                <DropdownMenuItem variant="destructive" onSelect={() => onAction(agent, "archive")}>
                  <ArchiveIcon />
                  Archive agent
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        )}
      </header>

      {!agent ? (
        loaded ? (
          <div className="border-t px-4 py-10 text-center">
            <p className="text-[14px] font-medium">No agent here</p>
            <p className="text-muted-foreground mt-1 text-[12.5px]">It was archived, or the link is wrong.</p>
          </div>
        ) : (
          <div className="border-t">
            <Loading>Loading the agent</Loading>
          </div>
        )
      ) : (
        <>
          <div className="border-b px-4 pb-2.5">
            <div className="mx-auto max-w-[68rem]">
              <Segmented label="Agent page" value={tab} options={TABS} onChange={onTab} className="md:w-fit" />
            </div>
          </div>
          {/* Keyed by tab so each one opens at its top, not at the last one's scroll. */}
          <RevisionDrafts value={drafts}>
            <div
              key={tab}
              role="tabpanel"
              aria-label={TABS.find((t) => t.id === tab)?.label}
              className="scroll-thin min-h-0 flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]"
            >
              {tab === "prompt" ? (
                <PromptTab agent={agent} api={api} focus={focus} />
              ) : tab === "compact" ? (
                <CompactionTab agent={agent} api={api} focus={focus} />
              ) : tab === "log" ? (
                // Keyed by agent: a thread picked on one agent's log is not the next one's.
                <LogTab key={agent.id} agent={agent} api={api} focus={focus} />
              ) : (
                <AgentSettingsTab
                  agent={agent}
                  api={api}
                  projects={projects}
                  harnesses={harnesses}
                  onOpenTab={onTab}
                  onNewThread={() => onAction(agent, "newThread")}
                  onArchive={() => onAction(agent, "archive")}
                />
              )}
            </div>
          </RevisionDrafts>
        </>
      )}
    </section>
  );
}

function MenuText({ title, sub }: { title: string; sub: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span>{title}</span>
      <span className="text-muted-foreground text-[11px]">{sub}</span>
    </span>
  );
}
