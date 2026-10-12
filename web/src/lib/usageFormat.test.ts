import { describe, expect, it } from "vitest";
import {
  bucketFullLabel,
  bucketStarts,
  formatAge,
  formatCountdown,
  formatPace,
  formatCost,
  formatTokens,
  providerColor,
  quotaPace,
} from "./usageFormat";
import type { QuotaWindow } from "~/protocol";

describe("bucketStarts", () => {
 it("aligns a rolling window with server buckets", () => { expect(bucketStarts(1250, 3250, 1000)).toEqual([1000, 2000, 3000]); });
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
describe("quotaPace", () => {
  const now = 1_000_000_000_000;
  const MIN = 60_000;
  // A 5-hour session window, `elapsedMins` in, with `usedPercent` used.
  const session = (elapsedMins: number, usedPercent?: number, over: Partial<QuotaWindow> = {}): QuotaWindow => ({
    id: "five_hour",
    kind: "thread",
    label: "Session",
    usedPercent,
    windowDurationMins: 300,
    resetsAt: now + (300 - elapsedMins) * MIN,
    checkedAt: now - MIN,
    ...over,
  });

  it("is ahead of pace when usage outruns the clock, and projects when the limit hits", () => {
    // 60% used at the halfway mark: 10 points ahead, and at 0.4%/min the last
    // 40% goes in 100 minutes — 50 minutes before the reset.
    const pace = quotaPace(session(150, 60), now)!;
    expect(pace.elapsed).toBeCloseTo(0.5);
    expect(pace.ahead).toBeCloseTo(10);
    expect(pace.projection).toEqual({ kind: "hits", at: now + 100 * MIN });
  });

  it("is under pace with headroom, and projects where usage finishes", () => {
    const pace = quotaPace(session(150, 35), now)!;
    expect(pace.ahead).toBeCloseTo(-15);
    expect(pace.projection?.kind).toBe("finishes");
    expect(pace.projection?.kind === "finishes" && pace.projection.percent).toBeCloseTo(70);
  });

  it("keeps a reading over 100 as it is, and reports the limit reached", () => {
    const pace = quotaPace(session(150, 120), now)!;
    expect(pace.ahead).toBeCloseTo(70);
    expect(pace.projection).toEqual({ kind: "reached" });
  });

  it("clamps the marker inside the bar when the clock and the window disagree", () => {
    expect(quotaPace(session(-30, 0), now)!.elapsed).toBe(0);
  });

  it("measures pace but does not project very early in the window", () => {
    const pace = quotaPace(session(6, 20), now)!;
    expect(pace.elapsed).toBeCloseTo(0.02);
    expect(pace.ahead).toBeCloseTo(18);
    expect(pace.projection).toBeUndefined();
  });

  it("has nothing to measure against without a duration, a reset time, or a timed window", () => {
    expect(quotaPace(session(150, 50, { windowDurationMins: undefined }), now)).toBeUndefined();
    expect(quotaPace(session(150, 50, { resetsAt: undefined }), now)).toBeUndefined();
    expect(quotaPace(session(150, 50, { kind: "credits" }), now)).toBeUndefined();
  });

  it("drops the marker once the window has already reset", () => {
    expect(quotaPace(session(310, 50), now)).toBeUndefined();
  });

  it("keeps the marker but neither compares nor projects from a stale reading", () => {
    const pace = quotaPace(session(150, 60, { checkedAt: now - 20 * MIN }), now)!;
    expect(pace).toEqual({ elapsed: 0.5, stale: true });
  });

  it("keeps the marker with no reading to compare", () => {
    expect(quotaPace(session(150, undefined), now)).toEqual({ elapsed: 0.5, stale: false });
  });
});

describe("formatPace", () => {
  it("says which side of pace, rounded to whole points", () => {
    expect(formatPace(15.2)).toBe("Tracking 15% ahead of pace");
    expect(formatPace(-12)).toBe("12% under pace");
    expect(formatPace(0.3)).toBe("On pace");
  });
});
