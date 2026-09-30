import { describe, expect, it } from "vitest";

import type { Issue } from "~/protocol";

import { branchTemplate } from "./branchTemplate";

const issue = (number: number, title: string): Issue => ({ number, title, url: "" });

describe("branchTemplate", () => {
  it("fills in the number and a slug of the title", () => {
    const { format, error } = branchTemplate("fix/{number}/{title}");
    expect(error).toBeNull();
    expect(format(issue(482, "Token refresh 500s after 24h"))).toBe(
      "fix/482/token-refresh-500s-after-24h",
    );
  });

  it("slugs punctuation and edges away and cuts long titles without a trailing dash", () => {
    const { format } = branchTemplate("{title}");
    expect(format(issue(1, "  --[Bug] Can't   save!!  "))).toBe("bug-can-t-save");
    // The 40th character lands on a dash, which is trimmed after the cut.
    expect(format(issue(1, `${"a".repeat(39)} bbbb`))).toBe("a".repeat(39));
  });

  it("repeats a placeholder and leaves other text alone", () => {
    expect(branchTemplate("{number}-{number}_x").format(issue(7, "t"))).toBe("7-7_x");
  });

  it("uses the default template when empty", () => {
    for (const blank of ["", "   "]) {
      const { format, error } = branchTemplate(blank);
      expect(error).toBeNull();
      expect(format(issue(12, "Fix the thing"))).toBe("issue/12-fix-the-thing");
    }
  });

  it("names an unknown placeholder and falls back to the issue number", () => {
    // toString sits on every object's prototype, so it must not pass as known.
    expect(branchTemplate("{toString}").error).toMatch(/\{toString\}/);
    const { format, error } = branchTemplate("fix/{number}-{foo}");
    expect(error).toMatch(/\{foo\}/);
    expect(error).toMatch(/\{number\}/);
    expect(error).toMatch(/\{title\}/);
    expect(format(issue(9, "Anything"))).toBe("issue/9");
  });
});
