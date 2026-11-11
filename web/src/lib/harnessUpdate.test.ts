import { describe, expect, it } from "vitest";

import { describeHarnessUpdate } from "./harnessUpdate";

const now = 10 * 60 * 60_000;
const threeHoursAgo = now - 3 * 60 * 60_000;

describe("describeHarnessUpdate", () => {
  it("says nothing before any update has run", () => {
    expect(describeHarnessUpdate(undefined, now)).toBeNull();
  });

  it("reports a version change from and to, with its age", () => {
    const line = describeHarnessUpdate({ at: threeHoursAgo, from: "2.1.252", to: "2.1.258" }, now);
    expect(line?.tone).toBe("muted");
    expect(line?.text).toContain("2.1.252 → 2.1.258");
    expect(line?.text).toContain("3h ago");
  });

  it("names only the new version when the old one is unknown", () => {
    const line = describeHarnessUpdate({ at: threeHoursAgo, to: "2.1.258" }, now);
    expect(line?.text).toContain("to 2.1.258");
    expect(line?.text).not.toContain("→");
  });

  it("reads equal versions as already up to date", () => {
    const line = describeHarnessUpdate({ at: threeHoursAgo, from: "2.1.258", to: "2.1.258" }, now);
    expect(line?.tone).toBe("muted");
    expect(line?.text).not.toContain("→");
    expect(line?.text).toContain("3h ago");
  });

  it("flags a failure for attention, carrying the error, even when versions are present", () => {
    const line = describeHarnessUpdate(
      { at: threeHoursAgo, from: "2.1.252", to: "2.1.258", error: "network unreachable" },
      now,
    );
    expect(line?.tone).toBe("attention");
    expect(line?.text).toContain("network unreachable");
    expect(line?.text).not.toContain("→");
  });
});
