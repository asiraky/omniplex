import { describe, expect, it } from "vitest";

import type { Job } from "../protocol";
import { childJobs, classifyJob, jobTree, jobVisible, liveJobCount, liveJobCounts, liveJobsLabel } from "./jobs";

function job(over: Partial<Job> & { id: string }): Job {
  return { depth: 0, kind: "agent", status: "running", usage: {}, ...over };
}

describe("classifyJob", () => {
  it("names shells, monitors and housekeeping; everything else is an agent", () => {
    expect(classifyJob("local_bash")).toBe("shell");
    expect(classifyJob("shell")).toBe("shell");
    expect(classifyJob("monitor")).toBe("monitor");
    expect(classifyJob("monitor_mcp")).toBe("monitor");
    expect(classifyJob("plan")).toBe("inert");
    expect(classifyJob("dream")).toBe("inert");
    expect(classifyJob("Explore")).toBe("agent");
    expect(classifyJob(undefined)).toBe("agent");
  });
});

describe("liveJobCounts", () => {
  const jobs = [
    job({ id: "a" }),
    job({ id: "b", status: "paused" }),
    job({ id: "c", status: "completed" }),
    job({ id: "s", kind: "shell" }),
    job({ id: "m", kind: "monitor", status: "failed" }),
    job({ id: "i", kind: "inert" }),
  ];
  it("counts running and paused, per kind, ignoring inert", () => {
    expect(liveJobCounts(jobs)).toEqual({ agents: 2, shells: 1, monitors: 0 });
    expect(liveJobCount(jobs)).toBe(3);
  });
  it("labels the live set", () => {
    expect(liveJobsLabel(jobs)).toBe("2 agents, 1 shell");
    expect(liveJobsLabel([job({ id: "x", kind: "monitor" })])).toBe("1 monitor");
    expect(liveJobsLabel([])).toBe("");
  });
});

describe("jobVisible", () => {
  it("keeps live jobs, drops hidden and inert", () => {
    expect(jobVisible(job({ id: "a" }))).toBe(true);
    expect(jobVisible(job({ id: "h", hidden: true }))).toBe(false);
    expect(jobVisible(job({ id: "i", kind: "inert" }))).toBe(false);
  });
  it("drops a finished shell but keeps a finished agent or monitor", () => {
    expect(jobVisible(job({ id: "s", kind: "shell" }))).toBe(true);
    expect(jobVisible(job({ id: "s2", kind: "shell", status: "paused" }))).toBe(true);
    expect(jobVisible(job({ id: "s3", kind: "shell", status: "completed" }))).toBe(false);
    expect(jobVisible(job({ id: "s4", kind: "shell", status: "failed" }))).toBe(false);
    expect(jobVisible(job({ id: "s5", kind: "shell", status: "stopped" }))).toBe(false);
    expect(jobVisible(job({ id: "a", status: "completed" }))).toBe(true);
    expect(jobVisible(job({ id: "m", kind: "monitor", status: "completed" }))).toBe(true);
  });
});

describe("childJobs", () => {
  it("lists direct children, minus hidden and finished shells", () => {
    const jobs = [
      job({ id: "p" }),
      job({ id: "a", parentJobId: "p" }),
      job({ id: "sh-live", kind: "shell", parentJobId: "p" }),
      job({ id: "sh-done", kind: "shell", status: "completed", parentJobId: "p" }),
      job({ id: "h", hidden: true, parentJobId: "p" }),
    ];
    expect(childJobs(jobs, "p").map((j) => j.id)).toEqual(["a", "sh-live"]);
  });
});

describe("jobTree", () => {
  it("orders parents before children and drops hidden/inert", () => {
    const jobs = [
      job({ id: "c1", parentJobId: "p", depth: 1 }),
      job({ id: "p" }),
      job({ id: "q" }),
      job({ id: "c2", parentJobId: "p", depth: 1 }),
      job({ id: "gc", parentJobId: "c2", depth: 2 }),
      job({ id: "h", hidden: true }),
      job({ id: "i", kind: "inert" }),
    ];
    const tree = jobTree(jobs);
    expect(tree.map((j) => j.id)).toEqual(["p", "c1", "c2", "gc", "q"]);
    expect(tree.map((j) => j.depth)).toEqual([0, 1, 1, 2, 0]);
  });
  it("drops finished shells and keeps their children as roots", () => {
    const jobs = [
      job({ id: "sh", kind: "shell", status: "completed" }),
      job({ id: "sh2", kind: "shell" }),
      job({ id: "c", parentJobId: "sh", depth: 1 }),
    ];
    expect(jobTree(jobs).map((j) => j.id)).toEqual(["sh2", "c"]);
  });
  it("treats an orphan as a root", () => {
    expect(jobTree([job({ id: "o", parentJobId: "gone", depth: 1 })]).map((j) => j.id)).toEqual(["o"]);
  });
});
