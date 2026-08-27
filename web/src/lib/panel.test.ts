// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";

import { closeSurface, defaultPanel, fileSurface, loadPanel, openSurface, savePanel } from "./panel";

describe("panel surfaces", () => {
  it("reuses a singleton and a repeated file path", () => {
    let p = defaultPanel();
    p = openSurface(p, { id: "files", kind: "files" });
    p = openSurface(p, fileSurface("a/b.ts"));
    p = openSurface(p, fileSurface("a/b.ts"));
    expect(p.surfaces.map((s) => s.id)).toEqual(["diff", "files", "file:a/b.ts"]);
    expect(p.active).toBe("file:a/b.ts");
  });

  it("closing the active tab lands on its left neighbour", () => {
    let p = defaultPanel();
    p = openSurface(p, { id: "files", kind: "files" });
    p = openSurface(p, { id: "jobs", kind: "jobs" });
    p = closeSurface(p, "jobs");
    expect(p.active).toBe("files");
    // Closing an inactive tab leaves the active one alone.
    p = closeSurface(p, "diff");
    expect(p.active).toBe("files");
  });
});

describe("panel persistence", () => {
  beforeEach(() => localStorage.clear());

  it("round-trips and prunes malformed surfaces", () => {
    const p = openSurface(defaultPanel(), fileSurface("x.go"));
    savePanel("s1", p);
    expect(loadPanel("s1")).toEqual(p);

    localStorage.setItem("omniplex.panel.v1:s2", JSON.stringify({ surfaces: [{ id: "file:y", kind: "file" }], active: "file:y" }));
    // A file surface without a path is unusable and dropped; empty falls back.
    expect(loadPanel("s2")).toEqual(defaultPanel());
  });

  it("falls back cleanly on garbage", () => {
    localStorage.setItem("omniplex.panel.v1:s3", "not json");
    expect(loadPanel("s3")).toEqual(defaultPanel());
  });
});
