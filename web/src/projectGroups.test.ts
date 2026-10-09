import { describe, expect, it } from "vitest";

import { groupThreads, visibleByProject } from "./projectGroups";
import type { Project, ThreadMeta } from "~/protocol";

const project = (id: string, name: string, createdAt = 0, updatedAt = 0): Project =>
  ({ id, name, defaults: {}, folders: [], createdAt, updatedAt }) as unknown as Project;

const thread = (id: string, projectId?: string, cwd = "/src/somewhere/here") =>
  ({ id, projectId, cwd }) as ThreadMeta;

// omniplex is the newer project, so its group comes first.
const projects = [project("p1", "omniplex", 2), project("p2", "worksauce", 1)];
const ids = (list: ThreadMeta[]) => list.map((s) => s.id);
const shape = (threads: ThreadMeta[], list = projects) =>
  groupThreads(threads, list).map((g) => [g.name, ids(g.threads)]);

describe("visibleByProject", () => {
  const threads = [thread("a", "p1"), thread("b", "p2"), thread("c", "p1")];

  it("shows everything when nothing is switched off", () => {
    expect(visibleByProject(threads, projects, new Set())).toBe(threads);
  });

  it("drops the threads belonging to a hidden project", () => {
    expect(ids(visibleByProject(threads, projects, new Set(["p1"])))).toEqual(["b"]);
  });

  it("ignores hidden ids whose project is gone, rather than stranding threads", () => {
    expect(visibleByProject(threads, projects, new Set(["deleted"]))).toBe(threads);
  });

  it("can hide everything, which the sidebar renders as its own empty state", () => {
    expect(visibleByProject(threads, projects, new Set(["p1", "p2"]))).toEqual([]);
  });
});

describe("groupThreads", () => {
  it("orders groups newest project first, not by which thread is on top", () => {
    // "worksauce" holds the top thread, but moving a thread to the top of the
    // list must not carry its whole project up past the others.
    expect(shape([thread("a", "p2"), thread("b", "p1"), thread("c", "p2")])).toEqual([
      ["omniplex", ["b"]],
      ["worksauce", ["a", "c"]],
    ]);
  });

  it("keeps its group order when a project is edited and the registry reorders", () => {
    const edited = [project("p2", "worksauce", 1, 99), project("p1", "omniplex", 2, 5)];
    expect(shape([thread("a", "p1"), thread("b", "p2")], edited)).toEqual([
      ["omniplex", ["a"]],
      ["worksauce", ["b"]],
    ]);
  });

  it("puts threads with no resolvable project after every real project", () => {
    expect(
      shape([thread("a", undefined, "/x/loose"), thread("b", "p2"), thread("c", "gone", "/y/other")]),
    ).toEqual([
      ["worksauce", ["b"]],
      ["x/loose", ["a"]],
      ["y/other", ["c"]],
    ]);
  });

  it("keeps the order the list already had inside each group", () => {
    // Which is what lets a departing row fold away where it stands: grouping
    // never reorders, so the delete flow's frozen ordering survives it.
    expect(shape([thread("a", "p1"), thread("b", "p1"), thread("c", "p1")])).toEqual([
      ["omniplex", ["a", "b", "c"]],
    ]);
  });

  it("gives one group when only one project has threads", () => {
    // The caller's whole test is groups.length > 1, so this is the "four
    // projects selected, one of them populated" case: nothing to group.
    expect(groupThreads([thread("a", "p1"), thread("b", "p1")], projects)).toHaveLength(1);
  });

  it("has no group, and so no header, for a project with no threads here", () => {
    expect(shape([thread("a", "p1")])).toEqual([["omniplex", ["a"]]]);
  });

  it("gives nothing at all for an empty list", () => {
    expect(groupThreads([], projects)).toEqual([]);
  });

  it("falls back to the cwd for a thread whose project cannot be resolved", () => {
    // The pre-project shape. It cannot be reached from the UI — a thread
    // cannot be created without a project, and a project owning threads
    // cannot be deleted — but it must not vanish if it ever appears.
    expect(shape([thread("a", undefined, "/home/me/code/loose")])).toEqual([
      ["code/loose", ["a"]],
    ]);
  });

  it("keeps two unresolvable checkouts apart even when their tails match", () => {
    expect(
      groupThreads(
        [thread("a", undefined, "/srv/one/app/repo"), thread("b", undefined, "/srv/two/app/repo")],
        projects,
      ),
    ).toHaveLength(2);
  });

  it("keeps two unresolvable threads apart when their checkouts differ", () => {
    expect(
      shape([thread("a", undefined, "/a/one"), thread("b", "gone", "/b/two")]),
    ).toEqual([
      ["a/one", ["a"]],
      ["b/two", ["b"]],
    ]);
  });
});
