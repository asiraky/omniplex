import { BotIcon, ChevronRightIcon } from "lucide-react";

import { Spinner } from "~/components/ui/spinner";
import { fmtTokens } from "~/lib/format";
import { isLive, jobLabel } from "~/lib/jobs";
import type { Item, Job, ToolStatus } from "~/protocol";

import { StatusMark } from "./ToolCards";

// A batch of subagents, as one card that stays in the transcript: unlike a
// tool call, a spawned agent keeps running after the turn that started it, so
// the card reads from the live job rather than the tool item where it can.
// Clicking anywhere on it opens the jobs panel.
export function JobsCard({
  items,
  jobs,
  onOpen,
}: {
  items: Item[];
  jobs: Job[];
  onOpen?: () => void;
}) {
  const rows = items.map((item) => ({ item, job: jobs.find((j) => j.toolCallId === item.id) }));
  const live = rows.some((r) =>
    r.job ? isLive(r.job) : r.item.status === "in_progress" || r.item.status === "pending",
  );
  return (
    <button
      type="button"
      onClick={onOpen}
      className="fade-in bg-card/60 hover:bg-accent/40 focus-visible:ring-ring w-full rounded-lg border text-left transition-colors outline-none focus-visible:ring-2"
    >
      <div className="text-muted-foreground flex items-center gap-2 px-3 pt-2 font-mono text-[10px] tracking-wide uppercase">
        <BotIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate">
          {items.length === 1 ? "1 agent" : `${items.length} agents`}
        </span>
        {live ? <Spinner className="text-primary size-3.5" /> : null}
        <ChevronRightIcon className="size-3.5" />
      </div>
      <ul className="space-y-1 px-3 py-2">
        {rows.map(({ item, job }) => {
          const label = job ? jobLabel(job) : item.title || "agent";
          const status: ToolStatus | undefined = job
            ? isLive(job)
              ? "in_progress"
              : job.status === "completed"
                ? "completed"
                : job.status === "failed"
                  ? "failed"
                  : "cancelled"
            : item.status;
          const tokens = job?.usage.totalTokens;
          return (
            <li key={item.id} className="flex min-w-0 items-center gap-2 text-[13px]">
              <span className="min-w-0 flex-1 truncate font-mono">{label}</span>
              {job?.activity && isLive(job) && (
                <span className="text-muted-foreground hidden min-w-0 max-w-[40%] truncate text-[11px] sm:inline">
                  {job.activity}
                </span>
              )}
              {tokens ? (
                <span className="text-muted-foreground shrink-0 font-mono text-[10px]">
                  {fmtTokens(tokens)}
                </span>
              ) : null}
              <StatusMark status={status} />
            </li>
          );
        })}
      </ul>
    </button>
  );
}
