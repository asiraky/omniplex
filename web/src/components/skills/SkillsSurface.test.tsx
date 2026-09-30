// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SkillsSurface, type SkillsCommand, type SkillsSurfaceProps } from "./SkillsSurface";
import type { HarnessState, Setup, Skill, SkillDetail, SkillHarness, SkillsList } from "~/lib/skills";
import { render } from "~/test/harness";

const ALL: SkillHarness[] = ["claude", "codex", "pi"];

const skill = (name: string, extra: Partial<Skill> = {}): Skill => ({
  name,
  description: `${name} does things`,
  dir: `/real/${name}`,
  scope: "user",
  paths: [`/home/u/.agents/skills/${name}`],
  harnesses: ALL,
  editable: true,
  ...extra,
});

const every = (mode: HarnessState["mode"]) => ({ claude: { mode }, codex: { mode }, pi: { mode } });

const SETUP: Setup = {
  library: "~/.agents/skills",
  libraryDir: "/home/u/.agents/skills",
  exists: true,
  projectLibrary: ".agents/skills",
  cliVersion: "1.2.3",
  npx: true,
  links: [
    { harness: "claude", dir: "/home/u/.claude/skills", state: "per-skill" },
    { harness: "codex", dir: "/home/u/.agents/skills", state: "direct" },
    { harness: "pi", dir: "/home/u/.pi/skills", state: "none" },
  ],
};

const SKILLS: Skill[] = [
  skill("dev", { scope: "project", dir: "/proj/.agents/skills/dev", description: "x".repeat(30) }),
  skill("grilling", { description: "x".repeat(20) }),
  skill("unslop", { harnesses: ["codex", "pi"], problem: "missing description", description: "" }),
  skill("secret", { invocation: every("manual") }),
  skill("pdf", { description: "x".repeat(10), source: { method: "git", repo: "acme/skills", managed: true } }),
  skill("wrangler", {
    scope: "plugin",
    plugin: "cloudflare",
    editable: false,
    harnesses: ["claude"],
    description: "x".repeat(40),
  }),
];

const CONTENT = "---\nname: grilling\ndescription: Grill the plan\n---\n\n# Grilling\n\nAsk hard questions.\n";

type Handler = (args: Record<string, unknown>) => unknown;

/** A server with just enough state that a change is visible on the next read. */
function mockCommand(overrides: Record<string, Handler> = {}, initial: Skill[] = SKILLS) {
  let content = CONTENT;
  let skills = initial;
  let setup = SETUP;
  const find = (dir: unknown) => skills.find((s) => s.dir === dir) ?? skill("new");
  const replace = (next: Skill) => {
    skills = skills.map((s) => (s.dir === next.dir ? next : s));
    return next;
  };
  const handlers: Record<string, Handler> = {
    list_skills: (): SkillsList => ({
      skills,
      subagents: [
        { name: "reviewer", description: "Reviews", path: "/home/u/.claude/agents/reviewer.md", scope: "user", harness: "claude" },
      ],
      setup,
    }),
    read_skill: (args): SkillDetail => ({ ...find(args.dir), content, files: [{ path: "notes.md", size: 12 }] }),
    read_skill_file: () => ({ content: "# Notes\n\nfile body", binary: false }),
    save_skill: (args) => {
      content = String(args.content);
      return {};
    },
    create_skill: (args) => {
      const created = skill(String(args.name), { scope: args.scope as Skill["scope"] });
      skills = [...skills, created];
      return created;
    },
    set_skill_invocation: (args) => {
      const s = find(args.dir);
      const mode = args.manual ? "manual" : "auto";
      return replace({ ...s, invocation: Object.fromEntries(s.harnesses.map((h) => [h, { mode }])) });
    },
    link_skill: (args) => {
      const s = find(args.dir);
      return replace({ ...s, harnesses: [...s.harnesses, args.harness as SkillHarness] });
    },
    remove_skill: (args) => {
      skills = skills.filter((s) => s.dir !== args.dir);
      return { ok: true };
    },
    save_skills_setup: (args) => {
      setup = {
        ...setup,
        library: String(args.library),
        projectLibrary: String(args.projectLibrary),
        cliVersion: String(args.cliVersion),
      };
      return { setup };
    },
    link_library: (args) => {
      setup = {
        ...setup,
        links: setup.links.map((l) => (l.harness === args.harness ? { ...l, state: "direct" as const } : l)),
      };
      return { setup };
    },
    ...overrides,
  };
  const fn = vi.fn(async (name: string, args: Record<string, unknown>) => handlers[name](args));
  return fn as typeof fn & SkillsCommand;
}

async function renderSurface(command = mockCommand(), props: Partial<SkillsSurfaceProps> = { threadId: "s1" }) {
  render(<SkillsSurface command={command} {...props} />);
  await screen.findByRole("button", { name: /grilling/ });
  return command;
}

const row = (name: RegExp) => screen.getByRole("button", { name });
const calls = (command: ReturnType<typeof mockCommand>, name: string) =>
  command.mock.calls.filter(([n]) => n === name).map(([, args]) => args);

async function openSkill(name: RegExp) {
  fireEvent.click(row(name));
  await screen.findByRole("button", { name: "Back to skills" });
  // The document arriving is what enables Edit and settles the view.
  await screen.findByRole("heading", { name: "Grilling" });
}

describe("SkillsSurface list", () => {
  it("groups skills by where they came from", async () => {
    const command = await renderSurface();
    expect(command).toHaveBeenCalledWith("list_skills", { threadId: "s1" });

    expect(within(screen.getByRole("region", { name: "Project" })).getByRole("button", { name: /dev/ })).toBeTruthy();
    const yours = within(screen.getByRole("region", { name: "Yours" }));
    for (const name of [/grilling/, /unslop/, /secret/]) expect(yours.getByRole("button", { name })).toBeTruthy();
    expect(yours.queryByRole("button", { name: /pdf/ })).toBeNull();
    expect(within(screen.getByRole("region", { name: "acme/skills" })).getByRole("button", { name: /pdf/ })).toBeTruthy();
  });

  it("scopes the listing to a project when there is no thread", async () => {
    const command = await renderSurface(mockCommand(), { projectId: "p1" });
    expect(command).toHaveBeenCalledWith("list_skills", { projectId: "p1" });
  });

  it("keeps a read-only group folded until it is opened", async () => {
    await renderSurface();
    expect(screen.queryByRole("button", { name: /wrangler/ })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /^Plugins/ }));
    expect(within(screen.getByRole("region", { name: "cloudflare" })).getByRole("button", { name: /wrangler/ })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Plugins/ }));
    expect(screen.queryByRole("button", { name: /wrangler/ })).toBeNull();
  });

  it("lets an editable group be folded away", async () => {
    await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /^Yours/ }));
    expect(screen.queryByRole("button", { name: /grilling/ })).toBeNull();
    expect(screen.getByRole("button", { name: /dev/ })).toBeTruthy();
  });

  it("reveals a match inside a folded group, and folds it again when the search is cleared", async () => {
    await renderSurface();
    const search = screen.getByRole("searchbox", { name: "Search skills" });

    fireEvent.change(search, { target: { value: "wrang" } });
    expect(screen.getByRole("button", { name: /wrangler/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /grilling/ })).toBeNull();
    expect(screen.queryByRole("region", { name: "Yours" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
    expect(screen.queryByRole("button", { name: /wrangler/ })).toBeNull();
    expect(screen.getByRole("button", { name: /grilling/ })).toBeTruthy();
  });

  it("finds skills by their source repo and says when nothing matches", async () => {
    await renderSurface();
    const search = screen.getByRole("searchbox", { name: "Search skills" });

    fireEvent.change(search, { target: { value: "acme" } });
    expect(screen.getByRole("button", { name: /pdf/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /grilling/ })).toBeNull();

    fireEvent.change(search, { target: { value: "zzzz" } });
    expect(screen.queryByRole("region")).toBeNull();
    expect(screen.getByText(/zzzz/)).toBeTruthy();
  });

  it("puts a skill's problem in its row as text", async () => {
    await renderSurface();
    expect(within(row(/unslop/)).getByText("missing description")).toBeTruthy();
    expect(within(row(/grilling/)).queryByText("missing description")).toBeNull();
  });

  it("shows same-name copies as one row and lets the detail switch between them", async () => {
    const copies = [
      skill("grilling", { dir: "/a/grilling", paths: ["/a/grilling"] }),
      skill("grilling", { dir: "/b/grilling", paths: ["/b/grilling"], harnesses: ["claude"] }),
    ];
    const command = await renderSurface(mockCommand({}, copies));
    expect(screen.getAllByRole("button", { name: /grilling/ })).toHaveLength(1);

    await openSkill(/grilling/);
    expect(calls(command, "read_skill")).toEqual([{ threadId: "s1", dir: "/a/grilling" }]);
    const switcher = within(screen.getByRole("region", { name: "Copies" }));
    expect(switcher.getAllByRole("button")).toHaveLength(2);

    fireEvent.click(switcher.getByRole("button", { name: /\/b\/grilling/ }));
    await waitFor(() => expect(calls(command, "read_skill")).toContainEqual({ threadId: "s1", dir: "/b/grilling" }));
    expect(
      within(screen.getByRole("region", { name: "Copies" }))
        .getByRole("button", { name: /\/b\/grilling/ })
        .getAttribute("aria-current"),
    ).toBe("true");
  });

  it("shows an error with a retry when listing fails", async () => {
    let fail = true;
    const command = mockCommand({
      list_skills: () => {
        if (fail) throw new Error("socket closed");
        return { skills: SKILLS, subagents: [] };
      },
    });
    render(<SkillsSurface command={command} />);
    expect(await screen.findByText(/socket closed/)).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("button", { name: /grilling/ })).toBeTruthy();
  });

  it("takes an older server's response: no invocation, source or setup, and nulls for empty lists", async () => {
    const old = { ...skill("grilling"), harnesses: null, paths: null } as unknown as Skill;
    await renderSurface(mockCommand({ list_skills: () => ({ skills: [old], subagents: null }) }));

    fireEvent.click(screen.getByRole("tab", { name: "In prompt" }));
    fireEvent.click(screen.getByRole("tab", { name: "Skills" }));
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("button", { name: "Back to skills" }));

    fireEvent.click(screen.getByRole("button", { name: "Skills setup" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("textbox")).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "Save" })).toBeNull();
  });
});

describe("SkillsSurface in prompt", () => {
  const names = (list: HTMLElement) =>
    within(list)
      .getAllByRole("button")
      .map((b) => b.textContent?.match(/^(dev|grilling|unslop|secret|pdf|wrangler)/)?.[1]);

  it("lists what one harness carries, longest first, and follows the harness picked", async () => {
    await renderSurface();
    fireEvent.click(screen.getByRole("tab", { name: "In prompt" }));

    // Claude: not unslop (it cannot see it), not secret (manual).
    expect(names(screen.getByRole("list", { name: /Claude/ }))).toEqual(["wrangler", "dev", "grilling", "pdf"]);

    fireEvent.click(screen.getByRole("tab", { name: "Codex" }));
    // Codex: no plugin skill, and unslop with its empty description last.
    expect(names(screen.getByRole("list", { name: /Codex/ }))).toEqual(["dev", "grilling", "pdf", "unslop"]);
  });

  it("narrows to the search and opens a skill from its row", async () => {
    const command = await renderSurface();
    fireEvent.click(screen.getByRole("tab", { name: "In prompt" }));
    fireEvent.change(screen.getByRole("searchbox", { name: "Search skills" }), { target: { value: "gril" } });
    const list = screen.getByRole("list", { name: /Claude/ });
    expect(names(list)).toEqual(["grilling"]);

    fireEvent.click(within(list).getByRole("button", { name: /grilling/ }));
    await screen.findByRole("button", { name: "Back to skills" });
    expect(command).toHaveBeenCalledWith("read_skill", { threadId: "s1", dir: "/real/grilling" });
  });
});

describe("SkillsSurface detail", () => {
  it("drills into a skill and comes back to the list", async () => {
    const command = await renderSurface();
    fireEvent.click(row(/grilling/));

    expect(await screen.findByRole("heading", { name: "Grilling" })).toBeTruthy();
    expect(command).toHaveBeenCalledWith("read_skill", { threadId: "s1", dir: "/real/grilling" });
    // The frontmatter is a key/value block, not part of the markdown body.
    expect(screen.getByText("Grill the plan")).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Source" }));
    expect(screen.getByText((_, el) => el?.tagName === "PRE" && el.textContent === CONTENT)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /notes\.md/ }));
    expect(await screen.findByText(/file body/)).toBeTruthy();
    expect(command).toHaveBeenCalledWith("read_skill_file", { threadId: "s1", dir: "/real/grilling", path: "notes.md" });

    fireEvent.click(screen.getByRole("button", { name: "Back to skills" }));
    expect(row(/grilling/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Back to skills" })).toBeNull();
  });

  it("saves the edited content and re-reads the skill", async () => {
    const command = await renderSurface();
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));

    const edited = CONTENT.replace("Ask hard questions.", "Ask harder questions.");
    fireEvent.change(screen.getByRole("textbox", { name: "SKILL.md source" }), { target: { value: edited } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("textbox", { name: "SKILL.md source" })).toBeNull());
    expect(command).toHaveBeenCalledWith("save_skill", { threadId: "s1", dir: "/real/grilling", content: edited });
    expect(calls(command, "read_skill")).toHaveLength(2);
    expect(screen.getByText("Ask harder questions.")).toBeTruthy();
  });

  it("keeps the editor open with the error when a save is refused", async () => {
    await renderSurface(
      mockCommand({
        save_skill: () => {
          throw new Error("invalid skill: frontmatter needs a name");
        },
      }),
    );
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "SKILL.md source" }), { target: { value: "no frontmatter" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("needs a name"));
    expect(screen.getByRole("textbox", { name: "SKILL.md source" })).toBeTruthy();
  });

  it("offers nothing that writes for a read-only skill", async () => {
    await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /^Plugins/ }));
    await openSkill(/wrangler/);
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("button", { name: /^Link for/ })).toBeNull();
  });
});

describe("SkillsSurface invocation", () => {
  it("turns manual only on and off through the server, and re-reads the files it rewrote", async () => {
    const command = await renderSurface();
    await openSkill(/grilling/);
    const toggle = () => screen.getByRole("switch", { name: /Manual only/ });
    expect(toggle().getAttribute("aria-checked")).toBe("false");

    fireEvent.click(toggle());
    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("true"));
    expect(calls(command, "set_skill_invocation")).toEqual([{ threadId: "s1", dir: "/real/grilling", manual: true }]);
    await waitFor(() => expect(calls(command, "read_skill")).toHaveLength(2));

    await waitFor(() => expect(toggle()).toHaveProperty("disabled", false));
    fireEvent.click(toggle());
    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("false"));
    expect(calls(command, "set_skill_invocation")[1]).toEqual({ threadId: "s1", dir: "/real/grilling", manual: false });
  });

  it("starts the switch on for a skill that is already manual", async () => {
    await renderSurface();
    await openSkill(/secret/);
    expect(screen.getByRole("switch", { name: /Manual only/ }).getAttribute("aria-checked")).toBe("true");
  });

  it("leaves the switch where it was and says why when the server refuses", async () => {
    await renderSurface(
      mockCommand({
        set_skill_invocation: () => {
          throw new Error("skill is not editable");
        },
      }),
    );
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("switch", { name: /Manual only/ }));
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("not editable"));
    expect(screen.getByRole("switch", { name: /Manual only/ }).getAttribute("aria-checked")).toBe("false");
  });

  it("offers one fix when a skill's files disagree between harnesses", async () => {
    const split = skill("grilling", {
      invocation: { claude: { mode: "manual" }, codex: { mode: "auto" }, pi: { mode: "auto" } },
    });
    const command = await renderSurface(mockCommand({}, [split]));
    await openSkill(/grilling/);

    fireEvent.click(within(screen.getByRole("region", { name: "Invocation" })).getByRole("button", { name: /manual everywhere/ }));
    await waitFor(() => expect(screen.queryByRole("button", { name: /manual everywhere/ })).toBeNull());
    expect(calls(command, "set_skill_invocation")).toEqual([{ threadId: "s1", dir: "/real/grilling", manual: true }]);
  });

  it("offers no fix when the disagreement is a harness setting", async () => {
    const overridden = skill("grilling", {
      invocation: { claude: { mode: "off", by: "settings" }, codex: { mode: "auto" }, pi: { mode: "auto" } },
    });
    await renderSurface(mockCommand({}, [overridden]));
    await openSkill(/grilling/);
    expect(screen.queryByRole("button", { name: /manual everywhere/ })).toBeNull();
    // The files still say auto, so the switch is off whatever Claude's settings do.
    expect(screen.getByRole("switch", { name: /Manual only/ }).getAttribute("aria-checked")).toBe("false");
  });

  it("links a skill for a harness that cannot see it", async () => {
    const command = await renderSurface();
    await openSkill(/unslop/);
    const invocation = () => within(screen.getByRole("region", { name: "Invocation" }));
    // Codex and pi already see it.
    expect(invocation().getAllByRole("button", { name: /^Link for/ })).toHaveLength(1);

    fireEvent.click(invocation().getByRole("button", { name: "Link for Claude" }));
    await waitFor(() => expect(invocation().queryByRole("button", { name: /^Link for/ })).toBeNull());
    expect(calls(command, "link_skill")).toEqual([{ threadId: "s1", dir: "/real/unslop", harness: "claude" }]);
  });
});

describe("SkillsSurface remove", () => {
  it("does nothing until the removal is confirmed", async () => {
    const command = await renderSurface();
    await openSkill(/grilling/);

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls(command, "remove_skill")).toEqual([]);
    expect(screen.getByRole("button", { name: "Back to skills" })).toBeTruthy();
  });

  it("removes the skill and drops it from the list", async () => {
    const command = await renderSurface();
    await openSkill(/grilling/);

    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove skill" }));

    await waitFor(() => expect(screen.queryByRole("button", { name: "Back to skills" })).toBeNull());
    expect(calls(command, "remove_skill")).toEqual([{ threadId: "s1", dir: "/real/grilling" }]);
    expect(screen.queryByRole("button", { name: /grilling/ })).toBeNull();
    expect(screen.getByRole("button", { name: /secret/ })).toBeTruthy();
  });

  it("keeps the skill and shows the error when the server refuses", async () => {
    await renderSurface(
      mockCommand({
        remove_skill: () => {
          throw new Error("permission denied");
        },
      }),
    );
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Remove skill" }));

    expect(await within(dialog).findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("permission denied"));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    fireEvent.click(screen.getByRole("button", { name: "Back to skills" }));
    expect(row(/grilling/)).toBeTruthy();
  });
});

describe("SkillsSurface use", () => {
  it("offers Use only where there is a thread to use it in", async () => {
    await renderSurface();
    await openSkill(/grilling/);
    expect(screen.queryByRole("button", { name: "Use" })).toBeNull();
  });

  it("hands the skill to the thread", async () => {
    const onUse = vi.fn();
    await renderSurface(mockCommand(), { threadId: "s1", onUse });
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("button", { name: "Use" }));
    await waitFor(() => expect(onUse).toHaveBeenCalledTimes(1));
    expect(onUse.mock.calls[0][0]).toMatchObject({ name: "grilling", dir: "/real/grilling" });
  });

  it("says so when the thread's harness cannot run the skill", async () => {
    const onUse = vi.fn().mockRejectedValue(new Error("harness does not list it"));
    await renderSurface(mockCommand(), { threadId: "s1", onUse });
    await openSkill(/grilling/);
    fireEvent.click(screen.getByRole("button", { name: "Use" }));
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("does not list it"));
    expect(screen.getByRole("button", { name: "Use" })).toHaveProperty("disabled", false);
  });
});

describe("SkillsSurface setup", () => {
  async function openSetup() {
    fireEvent.click(screen.getByRole("button", { name: "Skills setup" }));
    return screen.findByRole("dialog");
  }

  it("saves only what was changed from what the server reported, then lists again", async () => {
    const command = await renderSurface();
    const dialog = await openSetup();
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toHaveProperty("disabled", true);

    fireEvent.change(within(dialog).getByLabelText("Library"), { target: { value: "  ~/skills  " } });
    expect(save).toHaveProperty("disabled", false);
    fireEvent.click(save);

    await waitFor(() => expect(calls(command, "list_skills")).toHaveLength(2));
    expect(calls(command, "save_skills_setup")).toEqual([
      { threadId: "s1", library: "~/skills", projectLibrary: SETUP.projectLibrary, cliVersion: SETUP.cliVersion },
    ]);
    // What the server answered is now the baseline, so there is nothing left to save.
    await waitFor(() => expect(within(dialog).getByRole("button", { name: "Save" })).toHaveProperty("disabled", true));
    expect(within(dialog).getByLabelText("Library")).toHaveProperty("value", "~/skills");
  });

  it("keeps the typed values and shows the error when a save is refused", async () => {
    await renderSurface(
      mockCommand({
        save_skills_setup: () => {
          throw new Error("library must be a directory");
        },
      }),
    );
    const dialog = await openSetup();
    fireEvent.change(within(dialog).getByLabelText("Skills CLI version"), { target: { value: "9.9.9" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("must be a directory"));
    expect(within(dialog).getByLabelText("Skills CLI version")).toHaveProperty("value", "9.9.9");
  });

  it("offers a link only for a harness with no skills folder, and links it", async () => {
    const command = await renderSurface();
    const dialog = await openSetup();
    const links = () => within(within(dialog).getByRole("region", { name: "Harness links" }));
    expect(links().getAllByRole("button")).toHaveLength(1);

    fireEvent.click(links().getByRole("button", { name: "Link pi" }));
    await waitFor(() => expect(links().queryByRole("button")).toBeNull());
    expect(calls(command, "link_library")).toEqual([{ threadId: "s1", harness: "pi" }]);
  });

  it("starts each opening from the saved values", async () => {
    await renderSurface();
    let dialog = await openSetup();
    fireEvent.change(within(dialog).getByLabelText("Library"), { target: { value: "/half/typed" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    dialog = await openSetup();
    expect(within(dialog).getByLabelText("Library")).toHaveProperty("value", SETUP.library);
  });
});

describe("SkillsSurface slots", () => {
  it("renders the install and commit slots with a way to reach the server", async () => {
    const command = mockCommand();
    await renderSurface(command, {
      threadId: "s1",
      slots: {
        install: (ctx) => (
          <button type="button" onClick={() => void ctx.command("install_probe", ctx.scopeArgs)}>
            slot install
          </button>
        ),
        commitBar: (ctx) => <p>commit bar for {ctx.setup?.library}</p>,
      },
    });
    expect(screen.getByText(`commit bar for ${SETUP.library}`)).toBeTruthy();
    command.mockImplementationOnce(async () => ({}));
    fireEvent.click(screen.getByRole("button", { name: "slot install" }));
    expect(command).toHaveBeenCalledWith("install_probe", { threadId: "s1" });
  });

  it("asks for a group action only on source groups, and an update only for a skill with a source", async () => {
    await renderSurface(mockCommand(), {
      threadId: "s1",
      slots: {
        groupAction: (group) => <button type="button">group {group.repo ?? group.key}</button>,
        update: (s) => <button type="button">update {s.name}</button>,
      },
    });
    expect(screen.getAllByRole("button", { name: /^group / }).map((b) => b.textContent)).toEqual(["group acme/skills"]);

    await openSkill(/pdf/);
    expect(screen.getByRole("button", { name: "update pdf" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to skills" }));

    await openSkill(/grilling/);
    expect(screen.queryByRole("button", { name: /^update / })).toBeNull();
  });

  it("folds a slot's new skills into the list", async () => {
    await renderSurface(mockCommand(), {
      threadId: "s1",
      slots: {
        install: (ctx) => (
          <button type="button" onClick={() => ctx.upsert([skill("fresh")])}>
            slot install
          </button>
        ),
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "slot install" }));
    expect(within(screen.getByRole("region", { name: "Yours" })).getByRole("button", { name: /fresh/ })).toBeTruthy();
  });
});

describe("SkillsSurface new skill", () => {
  it("blocks invalid names, then creates and opens the skill in the editor", async () => {
    const command = await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /New skill/ }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("Name");
    const create = within(dialog).getByRole("button", { name: "Create" });
    fireEvent.change(within(dialog).getByLabelText("Description"), { target: { value: "Does a thing" } });

    for (const [bad, message] of [
      ["Bad", /Lowercase/],
      ["-bad", /start or end/],
      ["a--b", /double hyphens/],
      ["x".repeat(65), /64/],
    ] as const) {
      fireEvent.change(name, { target: { value: bad } });
      expect(within(dialog).getByText(message)).toBeTruthy();
      expect(create).toHaveProperty("disabled", true);
    }
    fireEvent.submit(create.closest("form") ?? within(dialog).getByRole("textbox", { name: "Name" }));
    expect(calls(command, "create_skill")).toEqual([]);

    fireEvent.change(name, { target: { value: "my-skill" } });
    expect(create).toHaveProperty("disabled", false);
    fireEvent.click(create);

    expect(await screen.findByRole("textbox", { name: "SKILL.md source" })).toBeTruthy();
    expect(command).toHaveBeenCalledWith("create_skill", {
      threadId: "s1",
      scope: "project",
      name: "my-skill",
      description: "Does a thing",
    });
  });

  it("starts each opening with a blank form", async () => {
    await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /New skill/ }));
    let dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "half-typed" } });
    fireEvent.click(within(dialog).getByRole("radio", { name: /Personal/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: /New skill/ }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Name")).toHaveProperty("value", "");
    expect(within(dialog).getByRole("radio", { name: /Project/ }).getAttribute("aria-checked")).toBe("true");
  });

  it("only offers personal scope without a thread or project", async () => {
    await renderSurface(mockCommand(), {});
    fireEvent.click(screen.getByRole("button", { name: /New skill/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: /Project/ })).toHaveProperty("disabled", true);
    expect(within(dialog).getByRole("radio", { name: /Personal/ }).getAttribute("aria-checked")).toBe("true");
  });
});
