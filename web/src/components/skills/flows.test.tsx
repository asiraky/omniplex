// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { pageSlots } from "./flows";
import { SkillsSurface, type SkillsCommand } from "./SkillsSurface";
import type { GitChange, Setup, Skill, SkillsList } from "~/lib/skills";
import { render } from "~/test/harness";

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

const fromAcme = (name: string, updatedAt = "2026-01-01T00:00:00Z") =>
  skill(name, { source: { method: "git", repo: "acme/skills", managed: true, updatedAt } });

const SETUP: Setup = {
  library: "~/dotfiles/skills",
  libraryDir: "/lib",
  exists: true,
  projectLibrary: ".agents/skills",
  cliVersion: "1.0.0",
  npx: true,
  links: [
    { harness: "claude", dir: "/home/u/.claude/skills", state: "direct" },
    { harness: "codex", dir: "/lib", state: "direct" },
    { harness: "pi", dir: "/lib", state: "direct" },
  ],
};

type Handler = (args: Record<string, unknown>) => unknown;

/**
 * A server whose git status follows its writes: every install or update
 * leaves a change behind, and a commit clears them.
 */
function mockServer(overrides: Record<string, Handler> = {}, setup: Setup = SETUP) {
  let skills = [skill("grilling"), fromAcme("pdf"), fromAcme("docx")];
  let changes: GitChange[] = [];
  const status = () => ({ root: "/home/u/dotfiles", branch: "main", changes });
  const handlers: Record<string, Handler> = {
    list_skills: (): SkillsList => ({ skills, subagents: [], setup }),
    read_skill: (args) => ({
      ...(skills.find((s) => s.dir === args.dir) ?? skill("none")),
      content: "---\nname: x\n---\n\nBody.\n",
      files: [],
    }),
    skills_git_status: () => ({ git: status() }),
    stage_skills: () => ({
      id: "stage-1",
      method: "git",
      repo: "other/repo",
      skills: [
        {
          name: "fresh",
          description: "",
          files: [{ path: "SKILL.md", size: 1 }],
          manual: false,
          picked: false,
          inUser: false,
          inProject: false,
        },
      ],
    }),
    install_staged: () => {
      const placed = skill("fresh", { source: { method: "git", repo: "other/repo", managed: true } });
      skills = [...skills, placed];
      changes = [...changes, { name: "fresh", status: "added", files: 1 }];
      return { skills: [placed] };
    },
    discard_staged: () => ({}),
    stage_update: () => ({
      id: "up-1",
      repo: "acme/skills",
      skills: [
        { name: "pdf", dir: "/lib/pdf", changed: true, files: [{ path: "SKILL.md", status: "modified" }] },
        { name: "docx", dir: "/lib/docx", changed: false, files: null },
      ],
    }),
    apply_update: (args) => {
      const dirs = args.dirs as string[];
      const updated = skills.filter((s) => dirs.includes(s.dir)).map((s) => fromAcme(s.name, "2026-02-02T00:00:00Z"));
      skills = skills.map((s) => updated.find((u) => u.dir === s.dir) ?? s);
      changes = [...changes, ...updated.map((s): GitChange => ({ name: s.name, status: "modified", files: 1 }))];
      return { skills: updated };
    },
    commit_skills: () => {
      changes = [];
      return { commit: "abc1234", git: status() };
    },
    ...overrides,
  };
  const fn = vi.fn(async (name: string, args: Record<string, unknown>) => {
    const handler = handlers[name];
    if (!handler) throw new Error(`unexpected command ${name}`);
    return handler(args);
  });
  return fn as typeof fn & SkillsCommand;
}

const calls = (command: ReturnType<typeof mockServer>, name: string) =>
  command.mock.calls.filter(([n]) => n === name).map(([, args]) => args);

async function renderPage(command = mockServer()) {
  render(<SkillsSurface command={command} threadId="t1" slots={pageSlots} />);
  await screen.findByRole("button", { name: /grilling/ });
  await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(1));
  return command;
}

// An open dialog hides the page behind it from the accessibility tree, and the
// bar is asked about while one is.
const commitBar = () => screen.queryByRole("region", { name: "Uncommitted changes", hidden: true });

async function chooseFromAdd(item: RegExp) {
  fireEvent.pointerDown(screen.getByRole("button", { name: "Add skill" }), { button: 0, ctrlKey: false });
  fireEvent.click(await screen.findByRole("menuitem", { name: item }));
  return screen.findByRole("dialog");
}

async function installFresh() {
  const dialog = within(await chooseFromAdd(/Install/));
  fireEvent.change(dialog.getByLabelText("Source"), { target: { value: "other/repo" } });
  fireEvent.click(dialog.getByRole("button", { name: "Fetch" }));
  await dialog.findByRole("checkbox", { name: /fresh/ });
  fireEvent.click(dialog.getByRole("button", { name: /^Install/ }));
}

describe("skills page flows", () => {
  it("shows an installed skill at once and brings up the commit bar, without listing again", async () => {
    const command = await renderPage();
    expect(commitBar()).toBeNull();

    await installFresh();

    expect(await screen.findByRole("region", { name: "other/repo" })).toBeTruthy();
    await waitFor(() => expect(commitBar()?.textContent).toContain("1 uncommitted"));
    expect(calls(command, "install_staged")[0]).toMatchObject({ threadId: "t1", id: "stage-1", skills: ["fresh"] });
    expect(calls(command, "list_skills")).toHaveLength(1);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("lists again after the install that creates the library", async () => {
    const command = await renderPage(mockServer({}, { ...SETUP, exists: false }));
    await installFresh();
    await waitFor(() => expect(calls(command, "list_skills")).toHaveLength(2));
  });

  it("starts each install at the paste box", async () => {
    await renderPage();
    let dialog = within(await chooseFromAdd(/Install/));
    fireEvent.change(dialog.getByLabelText("Source"), { target: { value: "other/repo" } });
    fireEvent.click(dialog.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    dialog = within(await chooseFromAdd(/Install/));
    expect((dialog.getByLabelText("Source") as HTMLTextAreaElement).value).toBe("");
  });

  it("still reaches the surface's own new skill dialog", async () => {
    await renderPage();
    const dialog = within(await chooseFromAdd(/new skill/));
    expect(dialog.getByLabelText("Name")).toBeTruthy();
  });

  it("checks a whole source from its group, and an update brings up the commit bar", async () => {
    const command = await renderPage();
    const group = within(screen.getByRole("region", { name: "acme/skills" }));
    fireEvent.click(group.getByRole("button", { name: "Check for updates" }));

    fireEvent.click(await screen.findByRole("button", { name: "Update pdf" }));
    await waitFor(() => expect(commitBar()?.textContent).toContain("1 uncommitted"));
    // Any one skill from the source stands for it; the server finds the rest.
    expect(["/lib/pdf", "/lib/docx"]).toContain((calls(command, "stage_update")[0] as { dir: string }).dir);
    expect(calls(command, "apply_update")).toEqual([{ threadId: "t1", id: "up-1", dirs: ["/lib/pdf"] }]);
  });

  it("checks from a skill's own page and reads the skill again once it is updated", async () => {
    const command = await renderPage();
    fireEvent.click(screen.getByRole("button", { name: /docx/ }));
    await screen.findByRole("button", { name: "Back to skills" });
    await waitFor(() => expect(calls(command, "read_skill")).toHaveLength(1));

    fireEvent.click(screen.getByRole("button", { name: "Check for updates" }));
    await screen.findByRole("button", { name: "Update pdf" });
    expect(calls(command, "stage_update")).toEqual([{ threadId: "t1", dir: "/lib/docx" }]);

    // Another skill from the source changing is no reason to read this one again.
    fireEvent.click(screen.getByRole("button", { name: "Update pdf" }));
    await waitFor(() => expect(calls(command, "apply_update")).toHaveLength(1));
    await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(2));
    expect(calls(command, "read_skill")).toHaveLength(1);
  });

  it("reads the open skill again when it is the one updated", async () => {
    const command = await renderPage();
    fireEvent.click(screen.getByRole("button", { name: /pdf/ }));
    await screen.findByRole("button", { name: "Back to skills" });
    await waitFor(() => expect(calls(command, "read_skill")).toHaveLength(1));

    fireEvent.click(screen.getByRole("button", { name: "Check for updates" }));
    fireEvent.click(await screen.findByRole("button", { name: "Update pdf" }));
    await waitFor(() => expect(calls(command, "read_skill")).toHaveLength(2));
  });

  it("clears the commit bar's entries once they are committed", async () => {
    const command = await renderPage();
    await installFresh();
    await waitFor(() => expect(commitBar()).toBeTruthy());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    fireEvent.click(within(commitBar()!).getByRole("button", { expanded: false }));
    fireEvent.click(within(commitBar()!).getByRole("button", { name: "Commit" }));
    await waitFor(() => expect(commitBar()?.textContent).toContain("abc1234"));
    expect(calls(command, "commit_skills")).toEqual([{ threadId: "t1", names: ["fresh"], message: "skills: add fresh" }]);
    expect(within(commitBar()!).queryByRole("checkbox")).toBeNull();
  });
});
