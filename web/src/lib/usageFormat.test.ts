import { describe, expect, it } from "vitest";
import {
  bucketFullLabel,
  bucketStarts,
  formatAge,
  formatCountdown,
  formatCost,
  formatTokens,
  providerColor,
} from "./usageFormat";

describe("bucketStarts", () => {
  it("fills the gaps so idle time stays on the axis", () => {
    const starts = bucketStarts(0, 5 * 1000, 1000);
    expect(starts).toEqual([0, 1000, 2000, 3000, 4000]);
  });

  it("is empty for a degenerate window", () => {
    expect(bucketStarts(0, 0, 1000)).toEqual([]);
    expect(bucketStarts(0, 100, 0)).toEqual([]);
  });
});

describe("formatCost", () => {
  it("gives cents above a dollar", () => {
    expect(formatCost(12.345)).toBe("$12.35");
  });

  it("keeps significant digits below a dollar", () => {
    expect(formatCost(0.0042)).toBe("$0.0042");
    expect(formatCost(0.5)).toBe("$0.50");
  });

  it("handles zero and the unrepresentable", () => {
    expect(formatCost(0)).toBe("$0");
    expect(formatCost(Number.NaN)).toBe("—");
  });
});

describe("formatTokens", () => {
  it("compacts", () => {
    expect(formatTokens(1_234_000)).toBe("1.2M");
    expect(formatTokens(840_000)).toBe("840k");
    expect(formatTokens(12)).toBe("12");
  });
});

describe("formatCountdown", () => {
  const now = 1_000_000_000_000;

  it("formats days, hours and minutes coarsely", () => {
    expect(formatCountdown(now + 3 * 24 * 3600 * 1000 + 5 * 3600 * 1000, now)).toBe("3d 5h");
    expect(formatCountdown(now + 3 * 24 * 3600 * 1000, now)).toBe("3d");
    expect(formatCountdown(now + 3 * 3600 * 1000 + 20 * 60 * 1000, now)).toBe("3h 20m");
    expect(formatCountdown(now + 12 * 60 * 1000, now)).toBe("12m");
  });

  it("says now for the past", () => {
    expect(formatCountdown(now - 1000, now)).toBe("now");
  });
});

describe("formatAge", () => {
  it("reads as time since", () => {
    const now = 1_000_000_000_000;
    expect(formatAge(now - 5 * 60 * 1000, now)).toBe("5m ago");
    expect(formatAge(now, now)).toBe("just now");
  });
});

describe("providerColor", () => {
  it("is stable for the named providers", () => {
    expect(providerColor("claude", 3)).toBe(providerColor("claude", 0));
    expect(providerColor("codex", 2)).toBe(providerColor("codex", 4));
    expect(providerColor("claude", 0)).not.toBe(providerColor("codex", 0));
  });

  it("falls through to the palette for unknown providers", () => {
    expect(providerColor("other", 0)).toBe("var(--chart-1)");
    expect(providerColor("other", 1)).toBe("var(--chart-2)");
  });
});

describe("bucketFullLabel", () => {
  it("labels hourly buckets with the time", () => {
    const label = bucketFullLabel(new Date("2026-04-12T14:00:00").getTime(), 3600 * 1000);
    expect(label).toMatch(/14/);
  });

  it("labels daily buckets with the date only", () => {
    const label = bucketFullLabel(new Date("2026-04-12T00:00:00").getTime(), 24 * 3600 * 1000);
    expect(label).not.toMatch(/:/);
  });
});