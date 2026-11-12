import { ChevronDownIcon, ListTodoIcon } from "lucide-react";
import { useState } from "react";

import { cn } from "~/lib/utils";
import type { Item, PlanStep } from "~/protocol";

/** "Tasks 2/5 · Writing tests": how far along the plan is, and the step it is
    on — the running one, else the next one waiting. A plan with nothing left
    names no step. */
export function planSummary(steps: PlanStep[]): { label: string; current?: PlanStep } {
  const done = steps.filter((s) => s.status === "completed").length;
  const current = steps.find((s) => s.status === "running") ?? steps.find((s) => s.status === "pending");
  const label = `Tasks ${done}/${steps.length}${current ? ` · ${current.text}` : ""}`;
  return { label, current };
}

const GLYPH: Record<PlanStep["status"], string> = { completed: "✓", running: "◉", pending: "○" };

// The agent's task list, as one quiet line where it wrote it, opening to the
// steps. A TodoWrite list a later one replaced is dimmed: still history, no
// longer the plan.
export function PlanRow({ item }: { item: Item }) {
  const [open, setOpen] = useState(false);
  const steps = item.steps ?? [];
  const { label } = planSummary(steps);

  return (
    <div className={cn("fade-in", item.superseded && "opacity-60")}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="hover:text-foreground focus-visible:ring-ring text-muted-foreground flex min-h-11 w-full min-w-0 items-center gap-1.5 rounded-md px-1 py-0.5 text-left text-[13px] transition-colors outline-none focus-visible:ring-2 md:min-h-0"
      >
        <ListTodoIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 truncate">{label}</span>
        <ChevronDownIcon className={cn("size-3.5 shrink-0 transition-transform", !open && "-rotate-90")} />
      </button>

      {open && (
        <div className="mt-1 space-y-1 border-l pl-3">
          {item.explanation && <p className="text-muted-foreground text-[12px] break-words">{item.explanation}</p>}
          <ul className="space-y-1">
            {steps.map((step, i) => (
              <li
                key={i}
                className={cn(
                  "flex gap-2 text-[13px]",
                  step.status === "running" ? "text-foreground font-medium" : "text-muted-foreground",
                )}
              >
                <span aria-hidden className="w-3 shrink-0 text-center">
                  {GLYPH[step.status] ?? GLYPH.pending}
                </span>
                <span className="min-w-0 break-words">{step.text}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
