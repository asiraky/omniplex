import { describe, expect, it } from "vitest";
import {
  absoluteCandidates,
  day,
  localDateTime,
  validateSchedule,
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
  it("accepts only future instants within 24 elapsed hours", () => {
    const now = 1000;
    expect(validateSchedule(now, now)).toBeTruthy();
    expect(validateSchedule(now + 1, now)).toBeUndefined();
    expect(validateSchedule(now + day, now)).toBeUndefined();
    expect(validateSchedule(now + day + 1, now)).toBeTruthy();
    expect(validateSchedule(NaN, now)).toBeTruthy();
  });
});
