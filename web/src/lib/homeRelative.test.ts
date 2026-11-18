import { describe, expect, it } from "vitest";

import { homeRelative } from "./homeRelative";

describe("homeRelative", () => {
  it.each([
    ["/home/me", "/home/me", "~"],
    ["/home/me/code/x", "/home/me", "~/code/x"],
    ["/home/me/code", "/home/me/", "~/code"],
    ["/home/meadow", "/home/me", "/home/meadow"],
    ["/etc", "/home/me", "/etc"],
  ])("writes %s under %s as %s", (cwd, home, want) => {
    expect(homeRelative(cwd, home)).toBe(want);
  });
});
