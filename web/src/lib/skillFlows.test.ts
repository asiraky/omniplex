import { describe, expect, it } from "vitest";

import {
  defaultCommitMessage,
  defaultStagedTicks,
  normalizeGitStatus,
  normalizeStaged,
  normalizeUpdateStage,
  RECORD_FILE,
  retickStaged,
  sourceLabel,
} from "~/lib/skillFlows";
import type { GitChange, Staged, StagedSkill, UpdateStage } from "~/lib/skills";

const staged = (name: string, extra: Partial<StagedSkill> = {}): StagedSkill => ({
  name,
  description: "",
  files: [{ path: "SKILL.md", size: 1 }],
  picked: false,
  installedIn: [],
  ...extra,
});

describe("sourceLabel", () => {
  it("names the repo in whatever was pasted", () => {
    expect(sourceLabel("  acme/skills  ")).toBe("acme/skills");
    expect(sourceLabel("acme/skills#v2")).toBe("acme/skills");
    expect(sourceLabel("https://github.com/acme/skills/tree/main/pdf")).toBe("acme/skills");
    expect(sourceLabel("git@github.com:acme/skills.git")).toBe("acme/skills");
  });

  it("picks the source out of an npx skills add line, past its flags", () => {
    expect(sourceLabel("npx -y skills@1.2.3 add -g acme/skills --skill pdf")).toBe("acme/skills");
    expect(sourceLabel("npx skills add https://github.com/acme/skills")).toBe("acme/skills");
  });

  it("leaves a folder as it was typed, even one with add in it", () => {
    expect(sourceLabel("/home/u/code/add skills")).toBe("/home/u/code/add skills");
    expect(sourceLabel("~/dotfiles/skills")).toBe("~/dotfiles/skills");
  });
});

describe("defaultStagedTicks", () => {
  it("ticks the only skill whether or not it was named", () => {
    expect(defaultStagedTicks([staged("pdf")], "")).toEqual(["pdf"]);
  });

  it("ticks just the named ones out of many", () => {
    expect(
      defaultStagedTicks([staged("a"), staged("b", { picked: true }), staged("c", { picked: true })], ""),
    ).toEqual(["b", "c"]);
  });

  it("ticks nothing out of many when none was named, and nothing out of none", () => {
    expect(defaultStagedTicks([staged("a"), staged("b")], "")).toEqual([]);
    expect(defaultStagedTicks([], "")).toEqual([]);
  });

  it("leaves a skill that would replace one in the destination unticked, named or alone", () => {
    expect(defaultStagedTicks([staged("pdf", { installedIn: [""] })], "")).toEqual([]);
    expect(
      defaultStagedTicks([staged("a", { picked: true, installedIn: ["/home/p"] }), staged("b", { picked: true })], "/home/p"),
    ).toEqual(["b"]);
  });

  it("ticks a skill that is only installed somewhere else", () => {
    expect(defaultStagedTicks([staged("pdf", { installedIn: [""] })], "/home/p")).toEqual(["pdf"]);
  });
});

describe("retickStaged", () => {
  const skills = [
    staged("a", { picked: true, installedIn: [""] }),
    staged("b", { picked: true, installedIn: ["/repo"] }),
    staged("c", { installedIn: [""] }),
  ];

  it("unticks what would now replace a skill, and ticks what was held back only by the old one", () => {
    // In personal: a and c would replace yours, so only b starts ticked.
    expect(retickStaged(new Set(["b"]), skills, "", "/repo")).toEqual(["a"]);
  });

  it("does not tick a skill that was never wanted", () => {
    expect(retickStaged(new Set<string>(), skills, "", "/home/p")).toEqual(["a"]);
  });

  it("keeps the reader's own choices where nothing changed", () => {
    expect(retickStaged(new Set(["b", "c"]), skills, "/repo", "/home/p")).toEqual(["b", "c"]);
    expect(retickStaged(new Set(["a", "c"]), skills, "/home/p", "/home/p")).toEqual(["a", "c"]);
  });
});

describe("defaultCommitMessage", () => {
  const change = (name: string, status: GitChange["status"] = "modified"): GitChange => ({ name, status, files: 1 });

  it("names one verb's skills in order", () => {
    expect(defaultCommitMessage([change("pdf", "added")])).toBe("skills: add pdf");
    expect(defaultCommitMessage([change("b"), change("a")])).toBe("skills: update a, b");
    expect(defaultCommitMessage([change("old", "removed")])).toBe("skills: remove old");
  });

  it("joins mixed verbs, adds first and removals last", () => {
    expect(defaultCommitMessage([change("z", "removed"), change("m"), change("a", "added")])).toBe(
      "skills: add a; update m; remove z",
    );
  });

  it("does not name the source record beside the skills it describes", () => {
    expect(defaultCommitMessage([change(RECORD_FILE), change("pdf", "added")])).toBe("skills: add pdf");
  });

  it("still says something when the record is all there is", () => {
    const alone = defaultCommitMessage([change(RECORD_FILE)]);
    expect(alone).not.toBe("");
    expect(alone).not.toContain(RECORD_FILE);
  });

  it("counts a long run of names instead of listing it", () => {
    const many = ["a", "b", "c", "d", "e"].map((n) => change(n, "added"));
    expect(defaultCommitMessage(many)).toBe("skills: add 5 skills");
    expect(defaultCommitMessage(many.slice(0, 4))).toBe("skills: add a, b, c, d");
  });

  it("is empty for no changes", () => {
    expect(defaultCommitMessage([])).toBe("");
  });
});

describe("normalizing what Go sends as null", () => {
  it("fills a staged source's lists", () => {
    const raw = { id: "s", repo: "r", skills: [{ ...staged("a"), files: null }] } as unknown as Staged;
    expect(normalizeStaged(raw).skills[0].files).toEqual([]);
    const noInstalls = { id: "s", repo: "r", skills: [{ ...staged("a"), installedIn: null }] } as unknown as Staged;
    expect(normalizeStaged(noInstalls).skills[0].installedIn).toEqual([]);
    expect(normalizeStaged({ ...raw, skills: null } as unknown as Staged).skills).toEqual([]);
  });

  it("fills an update's lists", () => {
    const raw = { id: "u", repo: "r", skills: [{ name: "a", dir: "/a", changed: false, files: null }] };
    expect(normalizeUpdateStage(raw as unknown as UpdateStage).skills[0].files).toEqual([]);
    expect(normalizeUpdateStage({ id: "u", repo: "r", skills: null } as unknown as UpdateStage).skills).toEqual([]);
  });

  it("keeps no repo as no repo, and a repo with no changes as an empty list", () => {
    expect(normalizeGitStatus(null)).toBeNull();
    expect(normalizeGitStatus({ root: "/r", branch: "main", changes: null as never })?.changes).toEqual([]);
  });
});
