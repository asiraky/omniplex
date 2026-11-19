import { describe, expect, it } from "vitest";

import type { ThreadMeta } from "~/protocol";
import {
  childStatus,
  delegatedCounts,
  descendants,
  rowMarks,
  sidebarThreads,
  stripLabel,
  threadTree,
} from "./threadTree";

const t = (id: string, over: Partial<ThreadMeta> = {}) =>
  ({ id, title: id, createdAt: 0, ...over }) as ThreadMeta;
const ids = (list: { id: string }[]) => list.map((x) => x.id);

describe("sidebarThreads", () => {
  it("hides children at any depth under a listed parent, keeping order", () => {
    const list = [
      t("a"),
      t("child", { parentId: "a" }),
      t("b"),
      t("grandchild", { parentId: "child" }),
    ];
    expect(ids(sidebarThreads(list))).toEqual(["a", "b"]);
  });

  it("shows an orphan whose parent is gone as its own row", () => {
    const list = [t("a"), t("orphan", { parentId: "gone" })];
    expect(ids(sidebarThreads(list))).toEqual(["a", "orphan"]);
  });

  it("shows the topmost listed thread when the root has gone", () => {
    const list = [t("mid", { parentId: "gone" }), t("leaf", { parentId: "mid" })];
    expect(ids(sidebarThreads(list))).toEqual(["mid"]);
  });

  it("does not hide threads whose parent chain loops", () => {
    const list = [t("x", { parentId: "y" }), t("y", { parentId: "x" }), t("self", { parentId: "self" })];
    expect(ids(sidebarThreads(list))).toEqual(["x", "y", "self"]);
  });
});

describe("descendants", () => {
  it("lists every level, parents before their children, siblings oldest first", () => {
    const tree = threadTree([
      t("root"),
      t("late", { parentId: "root", createdAt: 2 }),
      t("early", { parentId: "root", createdAt: 1 }),
      t("deep", { parentId: "early" }),
      t("other"),
    ]);
    expect(descendants(tree, "root").map((d) => [d.thread.id, d.depth])).toEqual([
      ["early", 0],
      ["deep", 1],
      ["late", 0],
    ]);
  });

  it("terminates on a cycle", () => {
    const tree = threadTree([t("x", { parentId: "y" }), t("y", { parentId: "x" })]);
    expect(descendants(tree, "x").map((d) => d.thread.id)).toEqual(["y"]);
  });
});

describe("rowMarks", () => {
  const list = [
    t("p"),
    t("c", { parentId: "p" }),
    t("g", { parentId: "c", attention: "needs_answer" }),
    t("q"),
    t("qc", { parentId: "q", attention: "working" }),
  ];
  const tree = threadTree(list);

  it("marks the row holding the open thread, at any depth", () => {
    expect([...rowMarks(tree, "g").childActive]).toEqual(["p"]);
    expect([...rowMarks(tree, "p").childActive]).toEqual([]);
  });

  it("marks rows with a descendant that needs the user", () => {
    expect([...rowMarks(tree, null).childNeedsYou]).toEqual(["p"]);
  });

  it("counts a failed turn only once the thread has stopped", () => {
    const failed = threadTree([t("p"), t("c", { parentId: "p", turnFailed: true })]);
    expect([...rowMarks(failed, null).childNeedsYou]).toEqual(["p"]);
    const retrying = threadTree([t("p"), t("c", { parentId: "p", turnFailed: true, attention: "working" })]);
    expect([...rowMarks(retrying, null).childNeedsYou]).toEqual([]);
  });
});

describe("delegatedCounts", () => {
  it("counts live and needs-you descendants, each once", () => {
    const tree = threadTree([
      t("p"),
      t("a", { parentId: "p", attention: "working" }),
      t("b", { parentId: "a", attention: "background" }),
      t("c", { parentId: "p", attention: "needs_permission" }),
      t("d", { parentId: "p" }),
      t("e", { parentId: "p", attention: "failed" }),
    ]);
    expect(delegatedCounts(tree, "p")).toEqual({ live: 2, needsYou: 2 });
    expect(delegatedCounts(tree, null)).toEqual({ live: 0, needsYou: 0 });
  });
});

describe("stripLabel", () => {
  it("puts what needs the user first, then jobs, then working threads", () => {
    expect(stripLabel("2 agents", { live: 1, needsYou: 0 })).toBe("2 agents, 1 thread");
    expect(stripLabel("", { live: 2, needsYou: 1 })).toBe("1 thread needs you, 2 threads");
    expect(stripLabel("1 agent", { live: 0, needsYou: 3 })).toBe("3 threads need you, 1 agent");
    expect(stripLabel("", { live: 0, needsYou: 0 })).toBe("");
  });
});

describe("childStatus", () => {
  it("names the state, falling back on the last turn's outcome", () => {
    expect(childStatus(t("a", { attention: "needs_permission" }))).toBe("Needs approval");
    expect(childStatus(t("a", { attention: "needs_answer" }))).toBe("Needs answer");
    expect(childStatus(t("a", { attention: "background" }))).toBe("Working");
    expect(childStatus(t("a", { attention: "closed" }))).toBe("Stopped");
    expect(childStatus(t("a", { turnFailed: true }))).toBe("Failed");
    expect(childStatus(t("a"))).toBe("Done");
  });
});
