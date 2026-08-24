import { describe, expect, it } from "vitest";

import { groupSessions, visibleByProject } from "./projectGroups";
import type { Project, SessionMeta } from "~/protocol";

const project = (id: string, name: string): Project =>
  ({ id, root: `/src/${name}`, config: { name } }) as Project;

const session = (id: string, projectId?: string, cwd = "/src/somewhere/here") =>
  ({ id, projectId, cwd }) as SessionMeta;

const projects = [project("p1", "omniplex"), project("p2", "worksauce")];
const ids = (list: SessionMeta[]) => list.map((s) => s.id);
const shape = (sessions: SessionMeta[], list = projects) =>
  groupSessions(sessions, list).map((g) => [g.name, ids(g.sessions)]);

describe("visibleByProject", () => {
  const sessions = [session("a", "p1"), session("b", "p2"), session("c", "p1")];

  it("shows everything when nothing is switched off", () => {
    expect(visibleByProject(sessions, projects, new Set())).toBe(sessions);
  });

  it("drops the sessions belonging to a hidden project", () => {
    expect(ids(visibleByProject(sessions, projects, new Set(["p1"])))).toEqual(["b"]);
  });

  it("ignores hidden ids whose project is gone, rather than stranding sessions", () => {
    expect(visibleByProject(sessions, projects, new Set(["deleted"]))).toBe(sessions);
  });

  it("can hide everything, which the sidebar renders as its own empty state", () => {
    expect(visibleByProject(sessions, projects, new Set(["p1", "p2"]))).toEqual([]);
  });
});

describe("groupSessions", () => {
  it("groups by project, most recently used project first", () => {
    // The list arrives most-recently-updated first, so "worksauce" leads on
    // the strength of session "a" alone.
    expect(shape([session("a", "p2"), session("b", "p1"), session("c", "p2")])).toEqual([
      ["worksauce", ["a", "c"]],
      ["omniplex", ["b"]],
    ]);
  });

  it("keeps the order the list already had inside each group", () => {
    // Which is what lets a departing row fold away where it stands: grouping
    // never reorders, so the delete flow's frozen ordering survives it.
    expect(shape([session("a", "p1"), session("b", "p1"), session("c", "p1")])).toEqual([
      ["omniplex", ["a", "b", "c"]],
    ]);
  });

  it("gives one group when only one project has sessions", () => {
    // The caller's whole test is groups.length > 1, so this is the "four
    // projects selected, one of them populated" case: nothing to group.
    expect(groupSessions([session("a", "p1"), session("b", "p1")], projects)).toHaveLength(1);
  });

  it("has no group, and so no header, for a project with no sessions here", () => {
    expect(shape([session("a", "p1")])).toEqual([["omniplex", ["a"]]]);
  });

  it("gives nothing at all for an empty list", () => {
    expect(groupSessions([], projects)).toEqual([]);
  });

  it("falls back to the cwd for a session whose project cannot be resolved", () => {
    // The pre-project shape. It cannot be reached from the UI — a session
    // cannot be created without a project, and a project owning sessions
    // cannot be deleted — but it must not vanish if it ever appears.
    expect(shape([session("a", undefined, "/home/me/code/loose")])).toEqual([
      ["code/loose", ["a"]],
    ]);
  });

  it("keeps two unresolvable sessions apart when their checkouts differ", () => {
    expect(
      shape([session("a", undefined, "/a/one"), session("b", "gone", "/b/two")]),
    ).toEqual([
      ["a/one", ["a"]],
      ["b/two", ["b"]],
    ]);
  });
});
