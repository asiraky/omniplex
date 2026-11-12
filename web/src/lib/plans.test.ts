import { describe, expect, it } from "vitest";

import type { PlanStep } from "~/protocol";

import { planSummary } from "./plans";

const step = (text: string, status: PlanStep["status"]): PlanStep => ({ text, status });

describe("planSummary", () => {
  it("names the running step over an earlier pending one", () => {
    expect(planSummary([step("a", "completed"), step("b", "pending"), step("c", "running")])).toBe("Tasks 1/3 · c");
  });

  it("names the first pending step when nothing is running", () => {
    expect(planSummary([step("a", "completed"), step("b", "pending"), step("c", "pending")])).toBe("Tasks 1/3 · b");
  });

  it("names no step once every step is done", () => {
    expect(planSummary([step("a", "completed"), step("b", "completed")])).toBe("Tasks 2/2");
  });
});
