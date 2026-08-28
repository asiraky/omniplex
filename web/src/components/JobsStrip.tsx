import type { Job } from "~/protocol";
import { liveJobCounts, liveJobsLabel } from "~/lib/jobs";
import { cn } from "~/lib/utils";

/**
 * One line above the composer naming what is running beside the conversation:
 * "2 agents, 1 shell". It is about liveness, not the turn — it stays while a
 * backgrounded agent works on after the turn that started it has ended. Agents
 * pulse; a lone shell or monitor is a steady dot. Clicking opens the jobs panel.
 */
export function JobsStrip({ jobs, onOpen }: { jobs: Job[]; onOpen: () => void }) {
  const counts = liveJobCounts(jobs);
  if (counts.agents + counts.shells + counts.monitors === 0) return null;
  const pulsing = counts.agents > 0;
  return (
    <div className="flex justify-center px-3 pb-1.5">
      <button
        type="button"
        onClick={onOpen}
        className="bg-card/90 text-muted-foreground hover:text-foreground focus-visible:ring-ring flex max-w-full items-center gap-2 rounded-full border px-3 py-1 text-[12px] shadow-sm backdrop-blur outline-none focus-visible:ring-2"
      >
        <span className="relative flex size-2 shrink-0" aria-hidden>
          {pulsing && (
            <span className="bg-primary absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none" />
          )}
          <span
            className={cn("relative inline-flex size-2 rounded-full", pulsing ? "bg-primary" : "bg-primary/60")}
          />
        </span>
        <span className="truncate">{liveJobsLabel(jobs)}</span>
      </button>
    </div>
  );
}
