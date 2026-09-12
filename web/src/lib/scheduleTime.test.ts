import { describe, expect, it } from "vitest";
import {
  absoluteCandidates,
  day,
  localDateTime,
  scheduleLabel,
  validateSchedule,
  waitLabel,
  zoneOr,
} from "./scheduleTime";

describe("scheduled local times", () => {
  it("resolves Brisbane independently of the host timezone", () => {
    expect(
      absoluteCandidates("2026-09-08T01:30", "Australia/Brisbane"),
    ).toEqual([Date.parse("2026-09-07T15:30:00Z")]);
  });
  it("rejects a spring DST gap and offers both autumn occurrences", () => {
    expect(absoluteCandidates("2026-03-08T02:30", "America/New_York")).toEqual(
      [],
    );
    expect(absoluteCandidates("2026-11-01T01:30", "America/New_York")).toEqual([
      Date.parse("2026-11-01T05:30:00Z"),
      Date.parse("2026-11-01T06:30:00Z"),
    ]);
  });
  it("handles half-hour DST changes", () => {
    expect(
      absoluteCandidates("2026-04-05T01:45", "Australia/Lord_Howe"),
    ).toHaveLength(2);
    expect(
      absoluteCandidates("2026-10-04T02:15", "Australia/Lord_Howe"),
    ).toEqual([]);
  });
  it("rejects invalid dates and keeps midnight on the right day", () => {
    expect(absoluteCandidates("2026-02-30T12:00", "UTC")).toEqual([]);
    expect(
      localDateTime(Date.parse("2026-09-07T14:00:00Z"), "Australia/Brisbane"),
    ).toBe("2026-09-08T00:00");
  });
  // A card left open on a phone says how long the wait still is, not just a
  // clock time the reader would have to do arithmetic against.
  it("states a wait as a clock time and a distance", () => {
    const now = Date.parse("2026-09-08T09:00:00Z");
    expect(waitLabel(now + 30_000, now)).toMatch(/any moment now/);
    expect(waitLabel(now + 25 * 60_000, now)).toMatch(/in 25 min/);
    expect(waitLabel(now + 2 * 3_600_000, now)).toMatch(/in 2h/);
    expect(waitLabel(now + 2 * 3_600_000 + 10 * 60_000, now)).toMatch(/in 2h 10m/);
    // Tomorrow is named, because "3:00" alone reads as today.
    expect(waitLabel(now + 20 * 3_600_000, now)).toMatch(/^[A-Z][a-z]{2}/);
  });

  // A schedule the server armed carries no zone, and an old one may carry
  // something Intl refuses. Neither may throw: this used to white-screen the
  // scheduled list the moment an auto-resume was armed.
  it("falls back to the device zone for a missing or unusable one", () => {
    const here = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(zoneOr("Australia/Brisbane")).toBe("Australia/Brisbane");
    expect(zoneOr("")).toBe(here);
    expect(zoneOr(undefined)).toBe(here);
    expect(zoneOr("Local")).toBe(here);
    expect(() => scheduleLabel(Date.now(), "")).not.toThrow();
  });

  it("accepts only future instants within 24 elapsed hours", () => {
    const now = 1000;
    expect(validateSchedule(now, now)).toBeTruthy();
    expect(validateSchedule(now + 1, now)).toBeUndefined();
    expect(validateSchedule(now + day, now)).toBeUndefined();
    expect(validateSchedule(now + day + 1, now)).toBeTruthy();
    expect(validateSchedule(NaN, now)).toBeTruthy();
  });
});
