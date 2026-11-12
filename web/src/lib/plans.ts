import type { Item, PlanStep } from "~/protocol";

/** "Tasks 2/5 · Writing tests": how far along the plan is, and the step it is
    on — the running one, else the next one waiting. A plan with nothing left
    names no step. */
export function planSummary(steps: PlanStep[]): string {
  const done = steps.filter((s) => s.status === "completed").length;
  const current = steps.find((s) => s.status === "running") ?? steps.find((s) => s.status === "pending");
  return `Tasks ${done}/${steps.length}${current ? ` · ${current.text}` : ""}`;
}

const TODO_PREFIX = "todo:";

/** The item id of a plan: one per TodoWrite call, else one per turn (codex). */
export function planItemId(turnId: string, toolCallId?: string): string {
  return toolCallId ? `${TODO_PREFIX}${toolCallId}` : `turn-plan:${turnId}`;
}

/** A TodoWrite list: the kind of plan a newer one supersedes. */
export function isTodoPlan(it: Item): boolean {
  return it.kind === "plan" && it.id.startsWith(TODO_PREFIX);
}
