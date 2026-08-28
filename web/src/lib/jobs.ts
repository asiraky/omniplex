// Jobs: work running beside the conversation. Mirrors internal/proto's job
// vocabulary and the derived helpers the UI needs from a SessionState.

import type { Job, JobKind, JobStatus } from "../protocol";

/** Denylist classifier: the SDK's task_type names are an open set, so
    everything not explicitly a shell, monitor, or housekeeping is an agent.
    Mirrors proto.ClassifyJob. */
export function classifyJob(taskType: string | undefined): JobKind {
  switch (taskType) {
    case "local_bash":
    case "shell":
      return "shell";
    case "monitor":
    case "monitor_mcp":
      return "monitor";
    case "plan":
    case "dream":
      return "inert";
    default:
      return "agent";
  }
}

export function jobDone(status: JobStatus): boolean {
  return status !== "running" && status !== "paused";
}

export function isLive(j: Job): boolean {
  return !jobDone(j.status) && j.kind !== "inert";
}

export interface JobCounts {
  agents: number;
  shells: number;
  monitors: number;
}

export function liveJobCounts(jobs: Job[]): JobCounts {
  const c: JobCounts = { agents: 0, shells: 0, monitors: 0 };
  for (const j of jobs) {
    if (jobDone(j.status)) continue;
    if (j.kind === "agent") c.agents++;
    else if (j.kind === "shell") c.shells++;
    else if (j.kind === "monitor") c.monitors++;
  }
  return c;
}

export function liveJobCount(jobs: Job[]): number {
  const c = liveJobCounts(jobs);
  return c.agents + c.shells + c.monitors;
}

/** Visible jobs, in a parent-before-children order, so a flat list renders
    as a tree by indenting on `depth`. */
export function jobTree(jobs: Job[]): Job[] {
  const out: Job[] = [];
  const byParent = new Map<string | undefined, Job[]>();
  for (const j of jobs) {
    if (j.hidden || j.kind === "inert") continue;
    const k = j.parentJobId && jobs.some((x) => x.id === j.parentJobId) ? j.parentJobId : undefined;
    byParent.set(k, [...(byParent.get(k) ?? []), j]);
  }
  const walk = (parent: string | undefined) => {
    for (const j of byParent.get(parent) ?? []) {
      out.push(j);
      walk(j.id);
    }
  };
  walk(undefined);
  return out;
}

/** Children of a job, direct only. */
export function childJobs(jobs: Job[], id: string): Job[] {
  return jobs.filter((j) => j.parentJobId === id && !j.hidden);
}

/** "2 agents, 1 shell" — the strip label. Empty when nothing is live. */
export function liveJobsLabel(jobs: Job[]): string {
  const c = liveJobCounts(jobs);
  const parts: string[] = [];
  if (c.agents) parts.push(`${c.agents} agent${c.agents === 1 ? "" : "s"}`);
  if (c.shells) parts.push(`${c.shells} shell${c.shells === 1 ? "" : "s"}`);
  if (c.monitors) parts.push(`${c.monitors} monitor${c.monitors === 1 ? "" : "s"}`);
  return parts.join(", ");
}

export function jobLabel(j: Job): string {
  return j.name || j.workflowName || j.role || j.taskType || j.id;
}
