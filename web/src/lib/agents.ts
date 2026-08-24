import type { Item } from "~/protocol";

export interface SubagentRow {
  /** The Task/Agent tool call that is the agent. */
  task: Item;
  /** Everything that ran inside it, in log order. */
  children: Item[];
}

function isTaskCall(it: Item): boolean {
  if (it.toolKind !== "think") return false;
  const title = it.title ?? "";
  if (title === "Task" || title === "Agent" || title.startsWith("Task:") || title.startsWith("Agent:")) return true;
  const input = it.input as Record<string, unknown> | undefined;
  return !!input && typeof input === "object" && ("subagent_type" in input || "prompt" in input);
}

export function collectSubagents(items: Item[]): SubagentRow[] {
  const children = new Map<string, Item[]>();
  for (const it of items) {
    if (!it.parentId) continue;
    const list = children.get(it.parentId) ?? [];
    list.push(it);
    children.set(it.parentId, list);
  }
  return items
    .filter((it) => it.kind === "tool" && (children.has(it.id) || isTaskCall(it)))
    .map((task) => ({ task, children: children.get(task.id) ?? [] }));
}

/** How many subagents are running right now — the panel toggle's badge. */
export function liveAgentCount(items: Item[]): number {
  return collectSubagents(items).filter(
    ({ task }) => task.status === "pending" || task.status === "in_progress",
  ).length;
}
