import { shallow, useThread, useThreadStore } from "~/app/threadStore";
import type { ThreadState } from "~/protocol";
import { liveJobCounts, liveJobsLabel } from "~/lib/jobs";
import { cn } from "~/lib/utils";
import { stripLabel } from "~/threadTree";

/**
 * One line above the composer naming what is running beside the conversation:
 * "2 agents, 1 shell", and the threads this one delegated, "1 thread". It is
 * about liveness, not the turn — it stays while a backgrounded agent works on
 * after the turn that started it has ended. Agents and threads pulse; a lone
 * shell or monitor is a steady dot. A delegated thread waiting on the user
 * says so first, in the warning colour. Clicking opens the jobs panel.
 *
 * It selects what it says rather than the jobs, so a job's progress (its
 * activity line, its token count) rerenders it only when the line changes.
 * The delegated counts come in as two numbers for the same reason.
 */
export function JobsStrip({
  onOpen,
  delegatedLive = 0,
  delegatedNeedsYou = 0,
}: {
  onOpen: () => void;
  /** Delegated threads still working. */
  delegatedLive?: number;
  /** Delegated threads waiting on the user. */
  delegatedNeedsYou?: number;
}) {
  const { agents, shells, monitors, label: jobs } = useThread(useThreadStore(), stripFields, shallow);
  if (agents + shells + monitors + delegatedLive + delegatedNeedsYou === 0) return null;
  const label = stripLabel(jobs, { live: delegatedLive, needsYou: delegatedNeedsYou });
  const warn = delegatedNeedsYou > 0;
  const pulsing = warn || agents > 0 || delegatedLive > 0;
  const tone = warn ? "bg-attention" : "bg-primary";
  return (
    <div className="flex justify-center px-3 pb-1.5">
      <button
        type="button"
        onClick={onOpen}
        className={cn(
          "bg-card/90 hover:text-foreground focus-visible:ring-ring flex max-w-full items-center gap-2 rounded-full border px-3 py-1 text-[12px] shadow-sm backdrop-blur outline-none focus-visible:ring-2",
          warn ? "border-attention/50 text-attention-foreground" : "text-muted-foreground",
        )}
      >
        <span className="relative flex size-2 shrink-0" aria-hidden>
          {pulsing && (
            <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none", tone)} />
          )}
          <span className={cn("relative inline-flex size-2 rounded-full", pulsing ? tone : "bg-primary/60")} />
        </span>
        <span className="truncate">{label}</span>
      </button>
    </div>
  );
}

function stripFields(state: ThreadState | null) {
  const jobs = state?.jobs ?? [];
  return { ...liveJobCounts(jobs), label: liveJobsLabel(jobs) };
}
