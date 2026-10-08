import { describe, expect, it } from "vitest";

import { titleToSave } from "./threadTitle";

describe("titleToSave", () => {
  it("folds whitespace onto one line", () => {
    expect(titleToSave("  New\n  name\t", "Old")).toBe("New name");
  });
  it("keeps the old name for a blank or untouched field", () => {
    expect(titleToSave("   ", "Old")).toBeNull();
    expect(titleToSave(" Old ", "Old")).toBeNull();
  });
});
