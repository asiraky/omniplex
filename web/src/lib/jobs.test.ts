import { describe, expect, it } from "vitest";

import type { Job } from "../protocol";
import { classifyJob, jobTree, liveJobCount, liveJobCounts, liveJobsLabel } from "./jobs";

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
  it("treats an orphan as a root", () => {
    expect(jobTree([job({ id: "o", parentJobId: "gone", depth: 1 })]).map((j) => j.id)).toEqual(["o"]);
  });
});
