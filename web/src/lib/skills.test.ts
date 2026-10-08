import { describe, expect, it } from "vitest";

import { matchesQuery, sectionSkills, skillsScope, type Skill, type Source } from "~/lib/skills";

const skill = (name: string, extra: Partial<Skill> = {}): Skill => ({
  name,
  description: `${name} does things`,
  dir: `/lib/${name}`,
  scope: "user",
  editable: true,
  mode: "on",
  ...extra,
});

const source = (repo: string, extra: Partial<Source> = {}): Source => ({ method: "git", repo, managed: true, ...extra });

describe("matchesQuery", () => {
  const s = skill("Grilling", {
    description: "Ask hard questions",
    plugin: "cloudflare",
    source: source("acme/skills"),
  });

  it("matches name, description, plugin and source repo, ignoring case and padding", () => {
    for (const q of ["grill", "HARD", "cloudfl", "acme/", "  grilling  "]) expect(matchesQuery(s, q)).toBe(true);
  });

  it("matches everything on a blank query and nothing on a miss", () => {
    expect(matchesQuery(s, "   ")).toBe(true);
    expect(matchesQuery(s, "wrangler")).toBe(false);
  });
});

describe("sectionSkills", () => {
  const names = (list: Skill[]) => list.map((s) => s.name);

  it("puts each skill in the section for where it came from", () => {
    const out = sectionSkills([
      skill("synced", { synced: true, scope: "user", editable: false }),
      skill("plug", { scope: "plugin", plugin: "cf", editable: false }),
      skill("builtin", { scope: "system", editable: false }),
      skill("proj", { scope: "project" }),
      skill("mine"),
      skill("installed", { source: source("acme/skills") }),
    ]);
    expect(names(out.yours)).toEqual(["installed", "mine"]);
    expect(names(out.project)).toEqual(["proj"]);
    expect(names(out.synced)).toEqual(["synced"]);
    expect(names(out.system)).toEqual(["builtin"]);
    expect(names(out.plugins)).toEqual(["plug"]);
  });

  it("keeps same-name copies as separate rows, in a stable order", () => {
    const out = sectionSkills([skill("pdf", { dir: "/b/pdf" }), skill("pdf", { dir: "/a/pdf" })]);
    expect(out.yours.map((s) => s.dir)).toEqual(["/a/pdf", "/b/pdf"]);
  });

  it("sorts plugins by plugin, then by name", () => {
    const out = sectionSkills([
      skill("a", { scope: "plugin", plugin: "zed", dir: "/z/a" }),
      skill("c", { scope: "plugin", plugin: "acme", dir: "/a/c" }),
      skill("b", { scope: "plugin", plugin: "acme", dir: "/a/b" }),
    ]);
    expect(out.plugins.map((s) => `${s.plugin}/${s.name}`)).toEqual(["acme/b", "acme/c", "zed/a"]);
  });

  it("returns every section empty for no skills", () => {
    expect(Object.values(sectionSkills([])).every((list) => list.length === 0)).toBe(true);
  });
});

describe("skillsScope", () => {
  const projects = [
    { id: "p1", name: "omniplex" },
    { id: "p2", name: "site" },
  ];

  it("follows the open thread and names its project", () => {
    expect(
      skillsScope({ threadId: "t1", threadProjectId: "p2", draftProjectId: "p1", lastProjectId: "p1", projects }),
    ).toEqual({ kind: "thread", threadId: "t1", projectId: "p2", projectName: "site" });
  });

  it("falls back to the project being drafted in, then the last one used", () => {
    expect(skillsScope({ threadId: null, draftProjectId: "p2", lastProjectId: "p1", projects })).toEqual({
      kind: "project",
      projectId: "p2",
      projectName: "site",
    });
    expect(skillsScope({ lastProjectId: "p1", projects })).toMatchObject({ kind: "project", projectId: "p1" });
  });

  it("skips a remembered project that no longer exists", () => {
    expect(skillsScope({ draftProjectId: "gone", lastProjectId: "p1", projects })).toMatchObject({ projectId: "p1" });
    expect(skillsScope({ lastProjectId: "gone", projects })).toEqual({ kind: "personal" });
  });

  it("is personal with nothing to go on", () => {
    expect(skillsScope({ projects: [] })).toEqual({ kind: "personal" });
  });
});
