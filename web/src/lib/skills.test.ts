import { describe, expect, it } from "vitest";

import {
  composerItemFor,
  copiesOf,
  fmtDate,
  groupSkills,
  harnessState,
  invocationSummary,
  matchesQuery,
  normalizeSkill,
  PROMPT_BUDGETS,
  promptReport,
  skillOrigin,
  skillsScope,
  type Skill,
  type Source,
} from "~/lib/skills";
import type { ComposerItem } from "~/protocol";

const skill = (name: string, extra: Partial<Skill> = {}): Skill => ({
  name,
  description: `${name} does things`,
  dir: `/lib/${name}`,
  scope: "user",
  paths: [`/lib/${name}`],
  harnesses: ["claude", "codex", "pi"],
  editable: true,
  ...extra,
});

const source = (repo: string, extra: Partial<Source> = {}): Source => ({ method: "git", repo, managed: true, ...extra });

const item = (name: string, extra: Partial<ComposerItem> = {}): ComposerItem => ({
  id: `id:${name}`,
  name,
  kind: "skill",
  trigger: "/",
  insertText: `/${name}`,
  behavior: "prompt",
  ...extra,
});

describe("normalizeSkill", () => {
  it("fills the arrays Go sends as null and leaves a whole skill alone", () => {
    const raw = { ...skill("a"), harnesses: null, paths: null } as unknown as Skill;
    expect(normalizeSkill(raw)).toMatchObject({ harnesses: [], paths: [] });
    const whole = skill("b");
    expect(normalizeSkill(whole)).toBe(whole);
  });
});

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

describe("skillOrigin", () => {
  it("sorts a skill by where it came from", () => {
    const kinds = (s: Skill) => skillOrigin(s).kind;
    expect(kinds(skill("a"))).toBe("yours");
    expect(kinds(skill("a", { scope: "project" }))).toBe("project");
    expect(kinds(skill("a", { scope: "plugin", plugin: "p" }))).toBe("plugins");
    expect(kinds(skill("a", { scope: "system" }))).toBe("system");
    expect(kinds(skill("a", { source: source("acme/skills") }))).toBe("source");
  });

  it("puts synced ahead of scope, and project ahead of source", () => {
    expect(skillOrigin(skill("a", { synced: true, scope: "plugin" })).kind).toBe("synced");
    expect(skillOrigin(skill("a", { scope: "project", source: source("acme/skills") })).kind).toBe("project");
  });

  it("keys each source repo apart", () => {
    const a = skillOrigin(skill("a", { source: source("acme/one") }));
    const b = skillOrigin(skill("b", { source: source("acme/two") }));
    expect(a.key).not.toBe(b.key);
  });
});

describe("groupSkills", () => {
  it("orders groups by origin whatever order the skills arrive in", () => {
    const groups = groupSkills([
      skill("sys", { scope: "system", editable: false }),
      skill("sync", { synced: true, editable: false }),
      skill("plug", { scope: "plugin", plugin: "p", editable: false }),
      skill("z-src", { source: source("zeta/skills") }),
      skill("a-src", { source: source("acme/skills") }),
      skill("mine"),
      skill("proj", { scope: "project" }),
    ]);
    expect(groups.map((g) => g.kind)).toEqual(["project", "yours", "source", "source", "plugins", "synced", "system"]);
    expect(groups.filter((g) => g.kind === "source").map((g) => g.repo)).toEqual(["acme/skills", "zeta/skills"]);
  });

  it("starts only the read-only groups folded", () => {
    const groups = groupSkills([
      skill("sys", { scope: "system" }),
      skill("sync", { synced: true }),
      skill("plug", { scope: "plugin", plugin: "p" }),
      skill("src", { source: source("acme/skills") }),
      skill("mine"),
      skill("proj", { scope: "project" }),
    ]);
    expect(Object.fromEntries(groups.map((g) => [g.kind, g.collapsed]))).toEqual({
      project: false,
      yours: false,
      source: false,
      plugins: true,
      synced: true,
      system: true,
    });
  });

  it("merges same-name copies in a group, the most widely seen first", () => {
    const narrow = skill("dup", { dir: "/a/dup", harnesses: ["claude"] });
    const wide = skill("dup", { dir: "/b/dup", harnesses: ["codex", "pi"] });
    const [group] = groupSkills([narrow, skill("other"), wide]);
    expect(group.entries.map((e) => e.name)).toEqual(["dup", "other"]);
    expect(group.entries[0].copies).toEqual([wide, narrow]);
  });

  it("does not merge a name across groups", () => {
    const groups = groupSkills([skill("dup", { dir: "/a/dup" }), skill("dup", { dir: "/p/dup", scope: "project" })]);
    expect(groups.map((g) => g.entries.map((e) => e.copies.length))).toEqual([[1], [1]]);
  });

  it("cuts plugins per plugin and merges a name only inside one", () => {
    const [group] = groupSkills([
      skill("deploy", { dir: "/z/deploy", scope: "plugin", plugin: "zeta" }),
      skill("deploy", { dir: "/a/deploy", scope: "plugin", plugin: "acme" }),
      skill("deploy", { dir: "/a2/deploy", scope: "plugin", plugin: "acme" }),
      skill("loose", { scope: "plugin" }),
    ]);
    expect(group.subgroups?.map((s) => [s.key, s.entries.map((e) => e.copies.length)])).toEqual([
      ["plugin:", [1]],
      ["plugin:acme", [2]],
      ["plugin:zeta", [1]],
    ]);
    expect(group.entries).toHaveLength(3);
  });

  it("returns nothing for no skills", () => {
    expect(groupSkills([])).toEqual([]);
  });
});

describe("copiesOf", () => {
  const a = skill("dup", { dir: "/a/dup" });
  const b = skill("dup", { dir: "/b/dup" });
  const groups = groupSkills([a, b, skill("solo")]);

  it("finds every copy sharing a row from any one of them", () => {
    expect(copiesOf(groups, "/b/dup").map((s) => s.dir)).toEqual(["/a/dup", "/b/dup"]);
    expect(copiesOf(groups, "/lib/solo")).toHaveLength(1);
  });

  it("is empty for a dir that is not listed", () => {
    expect(copiesOf(groups, "/nowhere")).toEqual([]);
  });
});

describe("harnessState", () => {
  it("is null for a harness that cannot see the skill", () => {
    expect(harnessState(skill("a", { harnesses: ["codex"] }), "claude")).toBeNull();
  });

  it("reads as auto when an older server sends no invocation", () => {
    expect(harnessState(skill("a"), "claude")).toEqual({ mode: "auto" });
  });

  it("reports what the server said", () => {
    const s = skill("a", { invocation: { claude: { mode: "off", by: "settings" } } });
    expect(harnessState(s, "claude")).toEqual({ mode: "off", by: "settings" });
    // Seen, but not mentioned in a partial map.
    expect(harnessState(s, "codex")).toEqual({ mode: "auto" });
  });
});

describe("invocationSummary", () => {
  it("is quiet when every harness that sees it has it in the prompt", () => {
    const s = skill("a", { invocation: { claude: { mode: "auto" }, codex: { mode: "name-only" }, pi: { mode: "auto" } } });
    expect(invocationSummary(s)).toEqual({ state: "auto", missing: [], overridden: [], manual: false, fixable: false });
  });

  it("is manual when none has it, and the files say so", () => {
    const s = skill("a", { invocation: { claude: { mode: "manual" }, codex: { mode: "manual" }, pi: { mode: "manual" } } });
    expect(invocationSummary(s)).toMatchObject({ state: "manual", manual: true, fixable: false });
  });

  it("is off only when every harness has it off", () => {
    const off = skill("a", { harnesses: ["claude"], invocation: { claude: { mode: "off", by: "settings" } } });
    expect(invocationSummary(off).state).toBe("off");
    const part = skill("a", { invocation: { claude: { mode: "off" }, codex: { mode: "manual" }, pi: { mode: "manual" } } });
    expect(invocationSummary(part).state).toBe("manual");
  });

  it("is mixed and fixable when the files disagree between harnesses", () => {
    const s = skill("a", { invocation: { claude: { mode: "manual" }, codex: { mode: "auto" }, pi: { mode: "manual" } } });
    expect(invocationSummary(s)).toMatchObject({ state: "mixed", manual: false, fixable: true });
  });

  it("does not offer a fix for a read-only skill", () => {
    const s = skill("a", {
      editable: false,
      invocation: { claude: { mode: "manual" }, codex: { mode: "auto" }, pi: { mode: "auto" } },
    });
    expect(invocationSummary(s)).toMatchObject({ state: "mixed", fixable: false });
  });

  it("does not offer a fix when only a harness setting disagrees", () => {
    const s = skill("a", {
      invocation: { claude: { mode: "off", by: "settings" }, codex: { mode: "auto" }, pi: { mode: "auto" } },
    });
    expect(invocationSummary(s)).toMatchObject({ state: "mixed", overridden: ["claude"], manual: false, fixable: false });
  });

  it("reads the switch from the files alone when a setting overrides one harness", () => {
    const s = skill("a", {
      invocation: { claude: { mode: "auto", by: "settings" }, codex: { mode: "manual" }, pi: { mode: "manual" } },
    });
    expect(invocationSummary(s)).toMatchObject({ state: "mixed", overridden: ["claude"], manual: true, fixable: false });
  });

  it("lists the harnesses that cannot see it", () => {
    expect(invocationSummary(skill("a", { harnesses: ["pi"] }))).toMatchObject({ state: "auto", missing: ["claude", "codex"] });
  });

  it("is unseen when no harness reads it", () => {
    expect(invocationSummary(skill("a", { harnesses: [] }))).toMatchObject({
      state: "unseen",
      missing: ["claude", "codex", "pi"],
      manual: false,
      fixable: false,
    });
  });
});

describe("promptReport", () => {
  const cap = PROMPT_BUDGETS.claude.perDescription!;
  const budget = PROMPT_BUDGETS.claude.chars!;

  it("lists what lands in the prompt, longest first, ties by name", () => {
    const report = promptReport(
      [
        skill("short", { description: "abc" }),
        skill("b-same", { description: "12345" }),
        skill("a-same", { description: "12345" }),
        skill("long", { description: "x".repeat(40) }),
      ],
      "pi",
    );
    expect(report.entries.map((e) => e.skill.name)).toEqual(["long", "a-same", "b-same", "short"]);
    expect(report.total).toBe(53);
  });

  it("leaves out skills the harness cannot see and counts the ones it keeps out", () => {
    const report = promptReport(
      [
        skill("seen"),
        skill("elsewhere", { harnesses: ["codex"] }),
        skill("by-hand", { invocation: { claude: { mode: "manual" } } }),
        skill("disabled", { invocation: { claude: { mode: "off" } } }),
      ],
      "claude",
    );
    expect(report.entries.map((e) => e.skill.name)).toEqual(["seen"]);
    expect(report).toMatchObject({ manual: 1, off: 1 });
  });

  it("counts a name-only skill as an entry with no description characters", () => {
    const report = promptReport([skill("quiet", { invocation: { codex: { mode: "name-only" } } })], "codex");
    expect(report.entries).toEqual([expect.objectContaining({ mode: "name-only", chars: 0, cut: 0 })]);
    expect(report.total).toBe(0);
  });

  it("cuts a description at the harness's per-description cap", () => {
    const long = skill("long", { description: "x".repeat(cap + 100) });
    const [claude] = promptReport([long], "claude").entries;
    expect(claude).toMatchObject({ chars: cap, cut: 100 });
    // Codex has an overall budget but no per-description cap.
    const [codex] = promptReport([long], "codex").entries;
    expect(codex).toMatchObject({ chars: cap + 100, cut: 0 });
  });

  it("reports how far the total runs past the budget, and nothing inside it", () => {
    const each = cap;
    const n = Math.floor(budget / each) + 1;
    const many = Array.from({ length: n }, (_, i) => skill(`s${i}`, { dir: `/lib/s${i}`, description: "x".repeat(each) }));
    expect(promptReport(many, "claude").over).toBe(n * each - budget);
    expect(promptReport(many.slice(0, 1), "claude").over).toBe(0);
  });

  it("is never over for a harness with no published budget", () => {
    const huge = [skill("huge", { description: "x".repeat(budget * 3) })];
    const report = promptReport(huge, "pi");
    expect(report.budget).toBeUndefined();
    expect(report.over).toBe(0);
  });
});

describe("composerItemFor", () => {
  it("finds the harness's own entry for the skill", () => {
    const items = [item("other"), item("grilling", { trigger: "$", insertText: "$grilling" })];
    expect(composerItemFor(items, { name: "grilling" })?.insertText).toBe("$grilling");
  });

  it("prefers a plugin's qualified name over a bare one of the same name", () => {
    const items = [item("deploy"), item("acme:deploy", { insertText: "/acme:deploy" })];
    expect(composerItemFor(items, { name: "deploy", plugin: "acme" })?.insertText).toBe("/acme:deploy");
    expect(composerItemFor(items, { name: "deploy" })?.insertText).toBe("/deploy");
  });

  it("falls back to the bare name for a plugin skill listed without its plugin", () => {
    expect(composerItemFor([item("deploy")], { name: "deploy", plugin: "acme" })?.insertText).toBe("/deploy");
  });

  it("matches an alias and ignores case", () => {
    const items = [item("skill:grilling", { insertText: "/skill:grilling", aliases: ["Grilling"] })];
    expect(composerItemFor(items, { name: "grilling" })?.insertText).toBe("/skill:grilling");
  });

  it("takes a skill over a command of the same name, and a prompt command when that is all there is", () => {
    const command = item("review", { kind: "command", insertText: "/review-cmd" });
    expect(composerItemFor([command, item("review")], { name: "review" })?.insertText).toBe("/review");
    expect(composerItemFor([command], { name: "review" })?.insertText).toBe("/review-cmd");
  });

  it("never picks a command that acts instead of prompting", () => {
    const action = item("clear", { kind: "command", behavior: "client-action" });
    expect(composerItemFor([action], { name: "clear" })).toBeUndefined();
  });

  it("is undefined when the harness does not list the skill", () => {
    expect(composerItemFor([item("other")], { name: "grilling" })).toBeUndefined();
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

describe("fmtDate", () => {
  it("is empty for a missing or unparseable timestamp", () => {
    expect(fmtDate(undefined)).toBe("");
    expect(fmtDate("")).toBe("");
    expect(fmtDate("not a date")).toBe("");
  });

  it("gives something for a real one", () => {
    expect(fmtDate("2026-03-04T10:00:00Z")).not.toBe("");
  });
});
