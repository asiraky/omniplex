import { BotIcon, ChevronRightIcon } from "lucide-react";
import { memo, useCallback } from "react";

import { shallow, useThread, useThreadStore } from "~/app/threadStore";
import { Elapsed } from "~/components/Elapsed";
import { ProviderLogo } from "~/components/ProviderLogo";
import { Spinner } from "~/components/ui/spinner";
import { fmtTokens } from "~/lib/format";
import { agentStatus, jobLabel, type AgentStatus } from "~/lib/jobs";
import { PROVIDER_LOGOS } from "~/lib/providerLogos";
import { cn } from "~/lib/utils";
import type { Item, Job, ThreadState } from "~/protocol";

// A batch of subagents, as one card that stays in the transcript: unlike a
// tool call, a spawned agent keeps running after the turn that started it, so
// the card reads from the live job rather than the tool item where it can.
// Each agent is its own row and its own tap target: it opens the jobs panel on
// that agent.
//
// It selects its own agents' jobs from the store, one per item, rather than
// being handed the thread's: jobs tick far more often than anything else in
// the transcript, and this way a tick redraws the card whose agent it is and
// not the transcript, nor the other cards.
//
// A recap is the same agents again under the turn's answer, one line each, so
// a long answer does not leave them a scroll away.
export function JobsCard({
  items,
  recap,
  onOpen,
}: {
  items: Item[];
  recap?: boolean;
  onOpen?: (jobId?: string) => void;
}) {
  const jobsOf = useCallback(
    (s: ThreadState | null) =>
      items.map((item) => s?.jobs.find((j) => j.toolCallId === item.id)),
    [items],
  );
  const jobs = useThread(useThreadStore(), jobsOf, shallow);
  const harness = useThread(useThreadStore(), harnessOf);
  const rows = items.map((item, i) => ({ item, job: jobs[i], status: agentStatus(jobs[i], item) }));
  const live = rows.some((r) => r.status === "running" || r.status === "queued");
  const count = items.length === 1 ? "1 agent" : `${items.length} agents`;
  return (
    <div className="fade-in bg-card/60 w-full overflow-hidden rounded-lg border">
      <div className="text-muted-foreground flex items-center gap-2 px-3 pt-2 font-mono text-[10px] tracking-wide uppercase">
        <BotIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate">{recap ? `${count} from this turn` : count}</span>
        {live ? <Spinner className="text-primary size-3.5" /> : null}
      </div>
      <ul className="py-1">
        {rows.map(({ item, job, status }) => (
          <AgentRow
            key={item.id}
            item={item}
            job={job}
            status={status}
            harness={harness}
            compact={recap}
            onOpen={onOpen}
          />
        ))}
      </ul>
    </div>
  );
}

const harnessOf = (s: ThreadState | null) => s?.harness ?? "";

const STATUS_LABEL: Record<AgentStatus, string> = {
  queued: "queued",
  running: "running",
  waiting: "waiting",
  completed: "done",
  failed: "failed",
  stopped: "stopped",
};

const DOT: Record<AgentStatus, string> = {
  queued: "bg-muted-foreground/60",
  running: "bg-primary animate-pulse",
  waiting: "bg-amber-400",
  completed: "bg-success",
  failed: "bg-destructive",
  stopped: "bg-muted-foreground",
};

const WORD: Record<AgentStatus, string> = {
  queued: "text-muted-foreground",
  running: "text-primary",
  waiting: "text-amber-500 dark:text-amber-400",
  completed: "text-muted-foreground",
  failed: "text-destructive",
  stopped: "text-muted-foreground",
};

/** The harness's mark in a disc, with the agent's status as a dot on its rim. */
function AgentAvatar({ harness, status }: { harness: string; status: AgentStatus }) {
  return (
    <span className="bg-muted relative flex size-6 shrink-0 items-center justify-center rounded-full">
      {PROVIDER_LOGOS[harness] ? (
        <ProviderLogo provider={harness} className="size-3.5" />
      ) : (
        <BotIcon className="text-muted-foreground size-3.5" />
      )}
      <span
        aria-hidden
        className={cn("ring-card absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full ring-2", DOT[status])}
      />
    </span>
  );
}

// One agent: avatar, task, status and time on the first line, what it is doing
// (or last did, or why it failed) on the second, so a phone reads it all
// without the activity being dropped for width.
const AgentRow = memo(function AgentRow({
  item,
  job,
  status,
  harness,
  compact,
  onOpen,
}: {
  item: Item;
  job: Job | undefined;
  status: AgentStatus;
  harness: string;
  compact?: boolean;
  onOpen?: (jobId?: string) => void;
}) {
  const label = job ? jobLabel(job) : item.title || "agent";
  const live = status === "running" || status === "queued" || status === "waiting";
  const activity = job?.error || job?.activity || (live ? "working…" : "");
  const tokens = job?.usage.totalTokens;
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen?.(job?.id)}
        aria-label={`${label}, ${STATUS_LABEL[status]}`}
        className="hover:bg-accent/40 focus-visible:ring-ring flex min-h-11 w-full min-w-0 items-center gap-2.5 px-3 py-1.5 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset"
      >
        <AgentAvatar harness={harness} status={status} />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-baseline gap-2">
            <span className="min-w-0 flex-1 truncate font-mono text-[13px]">{label}</span>
            <span className={cn("shrink-0 text-[11px]", WORD[status])}>{STATUS_LABEL[status]}</span>
            {/* A settled job the server never stamped would otherwise tick on forever. */}
            <Elapsed
              since={job && (live || job.finishedAt) ? job.startedAt : undefined}
              finishedAt={job?.finishedAt}
              className="text-muted-foreground shrink-0 font-mono text-[11px] tabular-nums"
            />
          </span>
          {!compact && (activity || tokens) ? (
            <span className="flex min-w-0 items-baseline gap-2">
              <span
                className={cn(
                  "min-w-0 flex-1 truncate text-[11px]",
                  job?.error ? "text-destructive" : "text-muted-foreground",
                )}
              >
                {activity}
              </span>
              {tokens ? (
                <span className="text-muted-foreground shrink-0 font-mono text-[10px]">
                  {fmtTokens(tokens)} tok
                </span>
              ) : null}
            </span>
          ) : null}
        </span>
        <ChevronRightIcon className="text-muted-foreground size-3.5 shrink-0" />
      </button>
    </li>
  );
});
