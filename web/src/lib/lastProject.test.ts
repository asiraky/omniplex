// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

import { initialProject, loadLastProject, saveLastProject } from "./lastProject";

const projects = [{ id: "p1" }, { id: "p2" }];

describe("last project", () => {
  beforeEach(() => localStorage.clear());

  it("round-trips and opens on the remembered project", () => {
    saveLastProject("p2");
    expect(loadLastProject()).toBe("p2");
    expect(initialProject(projects)).toBe("p2");
  });

  it("falls back to the first project when nothing is remembered", () => {
    expect(initialProject(projects)).toBe("p1");
    expect(initialProject([])).toBe("");
  });

  it("falls back when the remembered project is gone", () => {
    saveLastProject("deleted");
    expect(initialProject(projects)).toBe("p1");
  });

  it("survives storage being denied", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => saveLastProject("p2")).not.toThrow();
    expect(initialProject(projects)).toBe("p1");
    vi.restoreAllMocks();
  });
});
