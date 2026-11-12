import { describe, expect, it } from "vitest";

import { sidebarOrder } from "./sidebarOrder";
import type { Project, ThreadMeta } from "~/protocol";

const project = (id: string, createdAt: number) =>
  ({ id, name: id, defaults: {}, folders: [], createdAt, updatedAt: 0 }) as unknown as Project;
const thread = (id: string, projectId: string, pinned?: boolean) =>
  ({ id, projectId, pinned, cwd: "/src/x" }) as ThreadMeta;
const ids = (list: ThreadMeta[]) => list.map((s) => s.id);
const view = (over: { hidden?: string[]; collapsed?: string[]; grouped?: boolean } = {}) => ({
  hidden: new Set(over.hidden),
  collapsed: new Set(over.collapsed),
  grouped: over.grouped ?? true,
});

// p1 is the newer project, so its group leads.
const projects = [project("p1", 2), project("p2", 1)];
// In the user's order, the way the server sends them.
const threads = [
  thread("a", "p2"),
  thread("b", "p1"),
  thread("c", "p2", true),
  thread("d", "p1"),
  thread("e", "p2"),
];

describe("sidebarOrder", () => {
  it("puts pinned first, then each project's rows under it, newest project first", () => {
    expect(ids(sidebarOrder(threads, projects, view()))).toEqual(["c", "b", "d", "a", "e"]);
  });

  it("keeps the user's order under Pinned when grouping is off", () => {
    expect(ids(sidebarOrder(threads, projects, view({ grouped: false })))).toEqual([
      "c",
      "a",
      "b",
      "d",
      "e",
    ]);
  });

  it("lists flat when only one project is left to group", () => {
    // p2 hidden, pinned thread and all: what is left is p1's, in list order.
    expect(ids(sidebarOrder(threads, projects, view({ hidden: ["p2"] })))).toEqual(["b", "d"]);
  });

  it("skips a folded group's rows, unless asked for them", () => {
    const folded = view({ collapsed: ["p1"] });
    expect(ids(sidebarOrder(threads, projects, folded))).toEqual(["c", "a", "e"]);
    expect(ids(sidebarOrder(threads, projects, folded, { includeCollapsed: true }))).toEqual([
      "c",
      "b",
      "d",
      "a",
      "e",
    ]);
  });

  it("folds Pinned, and the flat rest under it, by their own keys", () => {
    expect(ids(sidebarOrder(threads, projects, view({ collapsed: ["pinned"] })))).toEqual([
      "b",
      "d",
      "a",
      "e",
    ]);
    expect(
      ids(sidebarOrder(threads, projects, view({ grouped: false, collapsed: ["unpinned"] }))),
    ).toEqual(["c"]);
  });

  it("cannot fold the bare list: with nothing pinned it has no header", () => {
    const unpinned = threads.filter((s) => !s.pinned);
    expect(
      ids(sidebarOrder(unpinned, projects, view({ grouped: false, collapsed: ["unpinned"] }))),
    ).toEqual(["a", "b", "d", "e"]);
  });

  it("is empty when the filter hides everything", () => {
    expect(sidebarOrder(threads, projects, view({ hidden: ["p1", "p2"] }))).toEqual([]);
  });
});
