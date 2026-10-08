import { XIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

import type { AuthWires } from "~/components/AuthFlowDialog";
import { McpTab } from "~/components/connections/McpTab";
import { SignInsTab } from "~/components/connections/SignInsTab";
import { useConnections } from "~/components/connections/useConnections";
import { IconButton } from "~/components/IconButton";
import { SkillsTab } from "~/components/skills/SkillsTab";
import { clisNeedAttention, serversNeedAttention } from "~/lib/connections";
import type { SkillsScope } from "~/lib/skills";
import { loadToolsTab, saveToolsTab, type ToolsTab } from "~/lib/toolsTab";
import { cn } from "~/lib/utils";

import { Segmented, type PageCommand } from "./parts";

/**
 * Everything the agents are given besides the prompt: skills, MCP servers and
 * the command-line sign-ins, one tab each. A tab is built the first time it
 * is shown and kept while hidden, so moving between them asks nothing again.
 */
export function ToolsPage({
  wires,
  scope,
  projects = [],
  tab: asked,
  onClose,
}: {
  wires: AuthWires;
  scope: SkillsScope;
  /** For naming the projects MCP servers belong to. */
  projects?: { id: string; name: string }[];
  /** Open on this tab rather than the last one used. */
  tab?: ToolsTab;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<ToolsTab>(() => asked ?? loadToolsTab());
  const [seen, setSeen] = useState<ReadonlySet<ToolsTab>>(() => new Set([tab]));
  const command = wires.command as PageCommand;
  const store = useConnections(command, tab !== "skills", scope.projectId);

  const choose = (next: ToolsTab) => {
    setTab(next);
    setSeen((s) => (s.has(next) ? s : new Set([...s, next])));
    saveToolsTab(next);
  };

  const conn = store.conn;
  const tabs: { id: ToolsTab; label: string; attention?: boolean }[] = [
    { id: "skills", label: "Skills" },
    { id: "mcp", label: "MCP", attention: !!conn && serversNeedAttention(conn.servers) },
    { id: "signins", label: "Sign-ins", attention: !!conn && clisNeedAttention(conn.clis) },
  ];

  const panel = (id: ToolsTab, label: string, body: () => ReactNode) =>
    seen.has(id) && (
      <div
        role="tabpanel"
        aria-label={label}
        hidden={tab !== id}
        className={cn("h-full min-h-0", tab !== id && "hidden")}
      >
        {body()}
      </div>
    );

  return (
    <div className="bg-background fixed inset-0 z-50 flex flex-col pb-[env(safe-area-inset-bottom)]">
      <header className="flex items-center gap-2 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-4">
        <IconButton label="Close" onClick={onClose}>
          <XIcon />
        </IconButton>
        <h1 className="sr-only">Skills, MCP servers and sign-ins</h1>
        <div className="mx-auto flex w-full max-w-3xl min-w-0 flex-1 md:pr-10">
          <Segmented label="Show" value={tab} options={tabs} onChange={choose} className="w-full md:w-auto" />
        </div>
      </header>
      <div className="min-h-0 flex-1 border-t">
        {/* A list of names and sentences: past this width the lines only get
            harder to follow. */}
        <div className="relative mx-auto h-full w-full max-w-3xl min-h-0">
          {panel("skills", "Skills", () => (
            <SkillsTab command={command} scope={scope} />
          ))}
          {panel("mcp", "MCP servers", () => (
            <McpTab
              wires={wires}
              store={store}
              threadId={scope.threadId}
              projectId={scope.projectId}
              projects={projects}
              shown={tab === "mcp"}
            />
          ))}
          {panel("signins", "Sign-ins", () => (
            <SignInsTab wires={wires} store={store} />
          ))}
        </div>
      </div>
    </div>
  );
}
