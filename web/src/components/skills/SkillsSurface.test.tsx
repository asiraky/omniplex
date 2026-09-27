// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SkillsSurface, type SkillsCommand } from "./SkillsSurface";
import type { Skill, SkillDetail, SkillsList } from "~/lib/skills";
import { render } from "~/test/harness";

const skill = (name: string, extra: Partial<Skill> = {}): Skill => ({
  name,
  description: `${name} does things`,
  dir: `/real/${name}`,
  scope: "user",
  paths: [`/home/u/.agents/skills/${name}`],
  harnesses: ["claude", "codex", "pi"],
  editable: true,
  ...extra,
});

const LIST: SkillsList = {
  skills: [
    skill("dev", { scope: "project", dir: "/proj/.agents/skills/dev" }),
    skill("grilling"),
    skill("unslop", { harnesses: ["codex", "pi"], problem: "missing description", description: "" }),
    skill("wrangler", { scope: "plugin", plugin: "cloudflare", editable: false, harnesses: ["claude"] }),
  ],
  subagents: [{ name: "reviewer", description: "Reviews", path: "/home/u/.claude/agents/reviewer.md", scope: "user", harness: "claude" }],
};

const CONTENT = "---\nname: grilling\ndescription: Grill the plan\n---\n\n# Grilling\n\nAsk hard questions.\n";

function detail(s: Skill, content = CONTENT): SkillDetail {
  return { ...s, content, files: [{ path: "notes.md", size: 12 }] };
}

function mockCommand(overrides: Record<string, (args: Record<string, unknown>) => unknown> = {}) {
  let content = CONTENT;
  const handlers: Record<string, (args: Record<string, unknown>) => unknown> = {
    list_skills: () => LIST,
    read_skill: (args) => detail(LIST.skills.find((s) => s.dir === args.dir) ?? skill("new"), content),
    read_skill_file: () => ({ content: "# Notes\n\nfile body", binary: false }),
    save_skill: (args) => {
      content = String(args.content);
      return {};
    },
    create_skill: (args) => skill(String(args.name), { dir: `/real/${String(args.name)}`, scope: args.scope as Skill["scope"] }),
    ...overrides,
  };
  const fn = vi.fn(async (name: string, args: Record<string, unknown>) => handlers[name](args));
  return fn as typeof fn & SkillsCommand;
}

async function renderSurface(command = mockCommand(), props: { threadId?: string; projectId?: string } = { threadId: "s1" }) {
  render(<SkillsSurface command={command} {...props} />);
  await screen.findByRole("button", { name: /grilling/ });
  return command;
}

describe("SkillsSurface list", () => {
  it("groups skills by scope with counts and filters by segment", async () => {
    const command = await renderSurface();
    expect(command).toHaveBeenCalledWith("list_skills", { threadId: "s1" });

    const project = screen.getByRole("region", { name: "Project" });
    expect(within(project).getByRole("button", { name: /dev/ })).toBeTruthy();
    const personal = screen.getByRole("region", { name: "Personal" });
    expect(within(personal).getAllByRole("button")).toHaveLength(2);
    expect(screen.getByRole("region", { name: "cloudflare plugin" })).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: /Plugins/ }));
    expect(screen.queryByRole("button", { name: /grilling/ })).toBeNull();
    expect(screen.getByRole("button", { name: /wrangler/ })).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: /All/ }));
    fireEvent.change(screen.getByRole("searchbox", { name: "Search skills" }), { target: { value: "unsl" } });
    expect(screen.getAllByRole("button", { name: /unslop|dev|grilling|wrangler/ }).map((b) => b.textContent)).toEqual([
      expect.stringContaining("unslop"),
    ]);
  });

  it("shows an error with a retry when listing fails", async () => {
    let fail = true;
    const command = mockCommand({
      list_skills: () => {
        if (fail) throw new Error("socket closed");
        return LIST;
      },
    });
    render(<SkillsSurface command={command} />);
    expect(await screen.findByText(/socket closed/)).toBeTruthy();
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("button", { name: /grilling/ })).toBeTruthy();
  });
});

describe("SkillsSurface detail", () => {
  it("drills into a skill and comes back to the list", async () => {
    const command = await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /grilling/ }));

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
    expect(screen.getByRole("button", { name: /grilling/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Back to skills" })).toBeNull();
  });

  it("saves the edited content and re-reads the skill", async () => {
    const command = await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /grilling/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));

    const edited = CONTENT.replace("Ask hard questions.", "Ask harder questions.");
    fireEvent.change(screen.getByRole("textbox", { name: "SKILL.md source" }), { target: { value: edited } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.queryByRole("textbox", { name: "SKILL.md source" })).toBeNull());
    expect(command).toHaveBeenCalledWith("save_skill", { threadId: "s1", dir: "/real/grilling", content: edited });
    expect(command.mock.calls.filter(([name]) => name === "read_skill")).toHaveLength(2);
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
    fireEvent.click(screen.getByRole("button", { name: /grilling/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "SKILL.md source" }), { target: { value: "no frontmatter" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("needs a name"));
    expect(screen.getByRole("textbox", { name: "SKILL.md source" })).toBeTruthy();
  });

  it("offers no editor for a plugin skill", async () => {
    await renderSurface();
    fireEvent.click(screen.getByRole("button", { name: /wrangler/ }));
    await screen.findByRole("heading", { name: "Grilling" });
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
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
    expect(command.mock.calls.some(([n]) => n === "create_skill")).toBe(false);

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

  it("only offers personal scope without a thread or project", async () => {
    render(<SkillsSurface command={mockCommand()} />);
    await screen.findByRole("button", { name: /grilling/ });
    fireEvent.click(screen.getByRole("button", { name: /New skill/ }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: /Project/ })).toHaveProperty("disabled", true);
    expect(within(dialog).getByRole("radio", { name: /Personal/ }).getAttribute("aria-checked")).toBe("true");
  });
});
