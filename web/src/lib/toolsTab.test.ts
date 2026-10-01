// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";

import { loadToolsTab, saveToolsTab } from "./toolsTab";

describe("toolsTab", () => {
  beforeEach(() => localStorage.clear());

  it("reopens on the last tab and falls back to skills for anything else", () => {
    expect(loadToolsTab()).toBe("skills");
    saveToolsTab("signins");
    expect(loadToolsTab()).toBe("signins");
    localStorage.setItem("omniplex.tools.tab", "connections");
    expect(loadToolsTab()).toBe("skills");
  });
});
