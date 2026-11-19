import { describe, expect, it } from "vitest";

import { sidebarOrder, sidebarSections } from "./sidebarOrder";
import type { Project, ThreadMeta } from "~/protocol";

const project = (id: string, createdAt: number) =>
  ({ id, name: id, defaults: {}, folders: [], createdAt, updatedAt: 0 }) as unknown as Project;
const thread = (id: string, projectId: string | undefined, pinned?: boolean) =>
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
  describe("assistant chats", () => {
    const withChats = [thread("x", undefined), ...threads, thread("y", undefined), thread("z", undefined, true)];
    const sections = (v = view()) =>
      sidebarSections(withChats, projects, v).map((s) => [s.key, ids(s.threads)]);

    it("get a section of their own under Pinned, grouped or not", () => {
      expect(sections()).toEqual([
        ["pinned", ["c", "z"]],
        ["assistant", ["x", "y"]],
        ["p1", ["b", "d"]],
        ["p2", ["a", "e"]],
      ]);
      expect(sections(view({ grouped: false }))).toEqual([
        ["pinned", ["c", "z"]],
        ["assistant", ["x", "y"]],
        ["unpinned", ["a", "b", "d", "e"]],
      ]);
    });

    it("stay out of the one-project count, so one project still lists flat", () => {
      expect(sections(view({ hidden: ["p2"] }))).toEqual([
        ["pinned", ["z"]],
        ["assistant", ["x", "y"]],
        ["unpinned", ["b", "d"]],
      ]);
    });

    it("fold by their own key, and are walked in the order shown", () => {
      expect(ids(sidebarOrder(withChats, projects, view({ collapsed: ["assistant"] })))).toEqual([
        "c", "z", "b", "d", "a", "e",
      ]);
      expect(ids(sidebarOrder(withChats, projects, view()))).toEqual([
        "c", "z", "x", "y", "b", "d", "a", "e",
      ]);
    });

    it("stand alone when they are all there is", () => {
      expect(sidebarSections([thread("x", undefined)], projects, view()).map((s) => s.key)).toEqual([
        "assistant",
      ]);
    });
  });

  it("walks past delegated threads under a listed parent, but not orphans", () => {
    const withChildren = [
      ...threads,
      { ...thread("b1", "p1"), parentId: "b" },
      { ...thread("b2", "p1"), parentId: "b1" },
      { ...thread("o", "p1"), parentId: "gone" },
    ];
    expect(ids(sidebarOrder(withChildren, projects, view()))).toEqual(["c", "b", "d", "o", "a", "e"]);
  });
});
