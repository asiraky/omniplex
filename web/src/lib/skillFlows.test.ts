import { describe, expect, it } from "vitest";

import {
  baseName,
  defaultCommitMessage,
  defaultStagedTicks,
  installLinks,
  installPath,
  normalizeGitStatus,
  normalizeStaged,
  normalizeUpdateStage,
  pendingUpdates,
  RECORD_FILE,
  sourceLabel,
  stagedClashes,
} from "~/lib/skillFlows";
import type { GitChange, Setup, Staged, StagedSkill, UpdateSkill, UpdateStage } from "~/lib/skills";

const staged = (name: string, extra: Partial<StagedSkill> = {}): StagedSkill => ({
  name,
  description: "",
  files: [{ path: "SKILL.md", size: 1 }],
  manual: false,
  picked: false,
  inUser: false,
  inProject: false,
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
    expect(defaultStagedTicks([staged("pdf")])).toEqual(["pdf"]);
  });

  it("ticks just the named ones out of many", () => {
    expect(defaultStagedTicks([staged("a"), staged("b", { picked: true }), staged("c", { picked: true })])).toEqual([
      "b",
      "c",
    ]);
  });

  it("ticks nothing out of many when none was named, and nothing out of none", () => {
    expect(defaultStagedTicks([staged("a"), staged("b")])).toEqual([]);
    expect(defaultStagedTicks([])).toEqual([]);
  });
});

describe("stagedClashes", () => {
  const skills = [staged("a", { inUser: true }), staged("b", { inProject: true }), staged("c", { inUser: true })];

  it("reports only ticked skills already in the library being installed into", () => {
    expect(stagedClashes(skills, ["a", "b"], "user")).toEqual(["a"]);
    expect(stagedClashes(skills, ["a", "b"], "project")).toEqual(["b"]);
  });

  it("is empty when the taken names are not ticked", () => {
    expect(stagedClashes(skills, new Set(["b"]), "user")).toEqual([]);
    expect(stagedClashes(skills, [], "user")).toEqual([]);
  });
});

describe("installLinks and installPath", () => {
  const setup: Setup = {
    library: "~/dotfiles/skills",
    libraryDir: "/home/u/dotfiles/skills",
    exists: true,
    projectLibrary: ".agents/skills",
    cliVersion: "1.0.0",
  npx: true,
    links: [
      { harness: "claude", dir: "/home/u/.claude/skills", state: "per-skill" },
      { harness: "codex", dir: "/home/u/.agents/skills", state: "direct" },
      { harness: "pi", dir: "/home/u/.agents/skills", state: "none" },
    ],
  };
  const byHarness = (links: ReturnType<typeof installLinks>) =>
    Object.fromEntries(links.map((l) => [l.harness, l] as const));

  it("follows what the server detected for the personal library", () => {
    const links = byHarness(installLinks(setup, "user"));
    expect(links.codex.direct).toBe(true);
    expect(links.claude).toMatchObject({ direct: false, dir: "/home/u/.claude/skills" });
    // No folder yet is still a symlink to add: the server makes the folder.
    expect(links.pi.direct).toBe(false);
    expect(links.claude.text).toContain("/home/u/.claude/skills");
    expect(links.codex.text).not.toBe(links.claude.text);
  });

  it("leaves out a harness the server did not report", () => {
    const links = installLinks({ ...setup, links: setup.links.filter((l) => l.harness !== "pi") }, "user");
    expect(links.map((l) => l.harness)).toEqual(["claude", "codex"]);
  });

  it("works a project out from its library: direct only for a harness that looks there", () => {
    const links = byHarness(installLinks(setup, "project"));
    expect(links.codex.direct).toBe(true);
    expect(links.pi.direct).toBe(true);
    expect(links.claude.direct).toBe(false);

    const claudeFirst = byHarness(installLinks({ ...setup, projectLibrary: "./.claude/skills/" }, "project"));
    expect(claudeFirst.claude.direct).toBe(true);
    expect(claudeFirst.codex.direct).toBe(false);
    expect(claudeFirst.codex.dir).toBe(claudeFirst.pi.dir);
  });

  it("has nothing to say without a setup", () => {
    expect(installLinks(undefined, "user")).toEqual([]);
    expect(installPath("user", undefined)).toBe("");
  });

  it("names the real folder, joined to the project root when there is one", () => {
    expect(installPath("user", setup)).toBe("/home/u/dotfiles/skills");
    expect(installPath("user", { ...setup, libraryDir: "" })).toBe("~/dotfiles/skills");
    expect(installPath("project", setup, "/proj/")).toBe("/proj/.agents/skills");
    expect(installPath("project", setup)).toBe(".agents/skills");
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

describe("baseName", () => {
  it("is the last segment, whatever trails it", () => {
    expect(baseName("/home/u/dotfiles")).toBe("dotfiles");
    expect(baseName("/home/u/dotfiles/")).toBe("dotfiles");
    expect(baseName("dotfiles")).toBe("dotfiles");
  });
});

describe("pendingUpdates", () => {
  const entry = (name: string, extra: Partial<UpdateSkill> = {}): UpdateSkill => ({
    name,
    dir: `/lib/${name}`,
    changed: false,
    files: [],
    ...extra,
  });
  const stage: UpdateStage = {
    id: "u1",
    repo: "acme/skills",
    skills: [
      entry("same"),
      entry("newer", { changed: true }),
      entry("other", { changed: true }),
      entry("dropped", { changed: true, gone: true }),
    ],
  };

  it("is the changed skills still in the source", () => {
    expect(pendingUpdates(stage, new Set()).map((s) => s.name)).toEqual(["newer", "other"]);
  });

  it("drops what has been updated already", () => {
    expect(pendingUpdates(stage, new Set(["/lib/newer"])).map((s) => s.name)).toEqual(["other"]);
  });
});

describe("normalizing what Go sends as null", () => {
  it("fills a staged source's lists", () => {
    const raw = { id: "s", method: "git", repo: "r", skills: [{ ...staged("a"), files: null }] } as unknown as Staged;
    expect(normalizeStaged(raw).skills[0].files).toEqual([]);
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
