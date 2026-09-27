// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";

import { loadRecentSkills, recordRecentSkill, resolveRecentSkills } from "~/lib/recentSkills";
import type { ComposerItem } from "~/protocol";

function item(name: string, extra: Partial<ComposerItem> = {}): ComposerItem {
  return {
    id: `id:${name}`,
    name,
    kind: "skill",
    trigger: "/",
    insertText: `/${name}`,
    behavior: "prompt",
    ...extra,
  };
}

describe("recentSkills", () => {
  beforeEach(() => localStorage.clear());

  it("remembers most recent first, without duplicates", () => {
    recordRecentSkill("p1", "/one");
    recordRecentSkill("p1", "/two");
    recordRecentSkill("p1", "/one");
    expect(loadRecentSkills("p1")).toEqual(["/one", "/two"]);
  });

  it("keeps projects apart", () => {
    recordRecentSkill("p1", "/one");
    recordRecentSkill("p2", "/two");
    expect(loadRecentSkills("p1")).toEqual(["/one"]);
    expect(loadRecentSkills("p2")).toEqual(["/two"]);
    expect(loadRecentSkills(undefined)).toEqual([]);
  });

  it("survives a corrupt entry", () => {
    localStorage.setItem("hy.recentSkills.v1:p1", "{not json");
    expect(loadRecentSkills("p1")).toEqual([]);
  });

  it("never offers a skill this thread's catalogue lacks", () => {
    const catalogue = [item("alpha"), item("beta")];
    const resolved = resolveRecentSkills(["/gone", "/beta"], catalogue, 5);
    expect(resolved.map((i) => i.insertText)).toEqual(["/beta", "/alpha"]);
  });

  it("orders by recency and caps at the limit", () => {
    const catalogue = [item("a"), item("b"), item("c"), item("d")];
    const resolved = resolveRecentSkills(["/c", "/a"], catalogue, 2);
    expect(resolved.map((i) => i.insertText)).toEqual(["/c", "/a"]);
  });

  it("seeds from the catalogue when there is no history, skipping client actions", () => {
    const catalogue = [
      item("model", { behavior: "client-action", action: "model", kind: "command" }),
      item("alpha"),
      item("beta"),
    ];
    const resolved = resolveRecentSkills([], catalogue, 5);
    expect(resolved.map((i) => i.insertText)).toEqual(["/alpha", "/beta"]);
  });

  it("still resolves a remembered client action", () => {
    const catalogue = [
      item("model", { behavior: "client-action", action: "model", kind: "command" }),
      item("alpha"),
    ];
    const resolved = resolveRecentSkills(["/model"], catalogue, 5);
    expect(resolved.map((i) => i.insertText)).toEqual(["/model", "/alpha"]);
  });
});

describe("seeding prefers skills", () => {
  it("puts skills ahead of plain commands when there is no history", () => {
    const catalogue = [
      item("compact", { kind: "command", insertText: "/compact" }),
      item("review", { kind: "command", insertText: "/review" }),
      item("work-issue"),
    ];
    const resolved = resolveRecentSkills([], catalogue, 2);
    expect(resolved.map((i) => i.insertText)).toEqual(["/work-issue", "/compact"]);
  });

  it("keeps a remembered command above any seed", () => {
    const catalogue = [item("alpha"), item("compact", { kind: "command", insertText: "/compact" })];
    const resolved = resolveRecentSkills(["/compact"], catalogue, 2);
    expect(resolved.map((i) => i.insertText)).toEqual(["/compact", "/alpha"]);
  });
});
