import { describe, expect, it } from "vitest";

import {
  destinationsOf,
  matchesQuery,
  originText,
  repoSectionTitle,
  sectionSkills,
  skillsScope,
  type Destination,
  type Skill,
  type SkillsList,
  type Source,
} from "~/lib/skills";

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

  it("keeps the project's own skills apart from the repos'", () => {
    const out = sectionSkills([
      skill("ours", { scope: "project", private: true, folder: "/home/p" }),
      skill("shared", { scope: "project", folder: "/code/p" }),
    ]);
    expect(names(out.private)).toEqual(["ours"]);
    expect(names(out.project)).toEqual(["shared"]);
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

const destinations: Destination[] = [
  { kind: "project", folder: "/home/p", label: "This project" },
  { kind: "repo", folder: "/code/api", label: "api repo", main: true },
  { kind: "repo", folder: "/code/web", label: "web repo" },
  { kind: "personal", folder: "", label: "Personal" },
];

describe("destinationsOf", () => {
  const list = (extra: Partial<SkillsList>): SkillsList => ({ skills: [], claudeSync: true, codexBundled: true, ...extra });

  it("takes the server's list and default", () => {
    expect(destinationsOf(list({ destinations, defaultDestination: "/code/web" }))).toEqual({
      destinations,
      defaultDestination: "/code/web",
    });
  });

  it("offers only personal when the server sends none", () => {
    for (const l of [null, list({}), list({ destinations: null })]) {
      expect(destinationsOf(l)).toEqual({
        destinations: [{ kind: "personal", folder: "", label: "Personal" }],
        defaultDestination: "",
      });
    }
  });

  it("starts on the first destination when the default is not one of them", () => {
    expect(destinationsOf(list({ destinations, defaultDestination: "/gone" })).defaultDestination).toBe("/home/p");
    expect(destinationsOf(list({ destinations: destinations.slice(0, 2) })).defaultDestination).toBe("/home/p");
  });
});

describe("repoSectionTitle", () => {
  const repo = (name: string, folder?: string) => skill(name, { scope: "project", folder });

  it("names the one folder its skills share", () => {
    expect(repoSectionTitle([repo("a", "/code/api"), repo("b", "/code/api")], destinations)).toEqual({
      title: "api repo",
      perRow: false,
    });
  });

  it("names each row's folder when the skills are in several", () => {
    expect(repoSectionTitle([repo("a", "/code/api"), repo("b", "/code/web")], destinations)).toEqual({
      title: "Repos",
      perRow: true,
    });
  });

  it("leaves the title to the caller when the folder is not a destination", () => {
    expect(repoSectionTitle([repo("a", "/elsewhere")], destinations).title).toBeUndefined();
    expect(repoSectionTitle([repo("a")], destinations).title).toBeUndefined();
  });
});

describe("originText", () => {
  it("names a project skill by where it lives", () => {
    expect(originText(skill("a", { scope: "project", private: true, folder: "/home/p" }), destinations)).toBe(
      "This project",
    );
    expect(originText(skill("a", { scope: "project", folder: "/code/web" }), destinations)).toBe("web repo");
    expect(originText(skill("a", { scope: "project", folder: "/elsewhere", editable: false }), destinations)).toBe(
      "Project, read-only",
    );
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
    ).toEqual({ kind: "thread", threadId: "t1", projectName: "site" });
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
