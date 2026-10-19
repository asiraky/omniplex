import { describe, expect, it } from "vitest";

import { splitPinned } from "./pinned";
import { groupThreads, visibleByProject } from "./projectGroups";
import type { Project, ThreadMeta } from "~/protocol";

const project = (id: string, createdAt: number) =>
  ({ id, name: id, defaults: {}, folders: [], createdAt, updatedAt: 0 }) as unknown as Project;
const thread = (id: string, projectId: string, pinned?: boolean) =>
  ({ id, projectId, pinned, cwd: "/src/x" }) as ThreadMeta;
const ids = (list: ThreadMeta[]) => list.map((s) => s.id);

const projects = [project("p1", 2), project("p2", 1)];
// In the user's order, the way the server sends them.
const threads = [
  thread("a", "p1"),
  thread("b", "p2", true),
  thread("c", "p1", true),
  thread("d", "p2"),
  thread("e", "p1", false),
];

describe("splitPinned", () => {
  it("takes the pinned threads out of the list, both halves in the list's order", () => {
    const { pinned, rest } = splitPinned(threads);
    expect(ids(pinned)).toEqual(["b", "c"]);
    expect(ids(rest)).toEqual(["a", "d", "e"]);
  });

  it("hands the list back untouched when nothing is pinned", () => {
    const plain = [thread("a", "p1"), thread("b", "p2")];
    const { pinned, rest } = splitPinned(plain);
    expect(pinned).toEqual([]);
    expect(rest).toBe(plain);
  });

  it("pins nothing the project filter has hidden", () => {
    const shown = visibleByProject(threads, projects, new Set(["p2"]));
    const { pinned, rest } = splitPinned(shown);
    expect(ids(pinned)).toEqual(["c"]);
    expect(ids(rest)).toEqual(["a", "e"]);
  });

  it("leaves only the unpinned threads to be grouped", () => {
    const { rest } = splitPinned(threads);
    expect(groupThreads(rest, projects).map((g) => [g.key, ids(g.threads)])).toEqual([
      ["p1", ["a", "e"]],
      ["p2", ["d"]],
    ]);
  });

  it("follows whatever order it is handed, so a frozen list keeps its departing row in place", () => {
    // The delete flow's frozen order, with the departing pinned row put back
    // at its old index.
    const frozen = [thread("c", "p1", true), thread("a", "p1"), thread("b", "p2", true)];
    expect(ids(splitPinned(frozen).pinned)).toEqual(["c", "b"]);
  });
});
