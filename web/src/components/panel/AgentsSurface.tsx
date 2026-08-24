import { BotIcon } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";

import { cn } from "~/lib/utils";
import { collectSubagents, type SubagentRow } from "~/lib/agents";
import type { Item } from "~/protocol";
import { formatDuration } from "~/rows";

function running(it: Item): boolean {
  return it.status === "pending" || it.status === "in_progress";
}

// A live elapsed timer written straight to the DOM: re-rendering the roster
// once a second to move a clock would fight the fixed-row design it sits in.
function Elapsed({ since, live }: { since?: number; live: boolean }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!since) return;
    const el = ref.current;
    if (!el) return;
    const tick = () => {
      el.textContent = formatDuration(Date.now() - since);
    };
    tick();
    if (!live) return;
    const t = window.setInterval(tick, 1000);
    return () => window.clearInterval(t);
  }, [since, live]);
  if (!since) return null;
  return <span ref={ref} className="tabular-nums" />;
}

function AgentRow({ row }: { row: SubagentRow }) {
  const { task, children } = row;
  const live = running(task);
  const failed = task.status === "failed";

  const input = (task.input ?? {}) as Record<string, unknown>;
  const description =
    (typeof input.description === "string" && input.description) ||
    (task.title ?? "subagent").replace(/^(Task|Agent):\s*/, "");
  const agentType = typeof input.subagent_type === "string" ? input.subagent_type : "";

  const tools = children.filter((c) => c.kind === "tool");
  // What it is doing right now — the newest child with something to say.
  const latest = [...children].reverse().find((c) => (c.kind === "tool" ? c.title : c.text));
  const activity =
    latest?.kind === "tool"
      ? latest.title
      : latest?.text
        ? latest.text.trim().split("\n").pop()
        : "";

  return (
    // Fixed-height rows on purpose: three reserved text lines, so a streaming
    // agent never reflows the roster under the reader's eyes.
    <div className="bg-card/60 flex h-[4.75rem] items-start gap-2.5 overflow-hidden rounded-lg border px-3 py-2">
      <span
        className={cn(
          "mt-1 size-2 shrink-0 rounded-full",
          live ? "bg-primary animate-pulse" : failed ? "bg-destructive" : "bg-success",
        )}
        title={task.status}
      />
      <div className="min-w-0 flex-1">
        <p className="truncate text-[12.5px] font-medium">{description}</p>
        <p className="text-muted-foreground h-[1.25rem] truncate font-mono text-[11px]">
          {live ? (activity ?? "working…") : (activity ?? "")}
        </p>
        <p className="text-muted-foreground truncate text-[10px]">
          {[
            agentType,
            `${tools.length} tool${tools.length === 1 ? "" : "s"}`,
            <Elapsed key="t" since={task.receivedAt} live={live} />,
          ]
            .filter(Boolean)
            .map((part, i) => (
              <span key={i}>
                {i > 0 && " · "}
                {part}
              </span>
            ))}
        </p>
      </div>
    </div>
  );
}

/** The fleet roster: one fixed-height row per subagent, newest last. */
export function AgentsSurface({ items }: { items: Item[] }) {
  const agents = useMemo(() => collectSubagents(items), [items]);

  if (agents.length === 0) {
    return (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
        <BotIcon className="size-5 opacity-60" />
        <p className="text-[13px]">No subagents yet.</p>
        <p className="text-[12px]">When the agent delegates work with Task, the fleet shows up here.</p>
      </div>
    );
  }

  return (
    <div className="scroll-thin h-full space-y-2 overflow-y-auto overscroll-contain p-2">
      {agents.map((row) => (
        <AgentRow key={row.task.id} row={row} />
      ))}
    </div>
  );
}
