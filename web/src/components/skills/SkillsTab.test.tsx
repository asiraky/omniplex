// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ClaudeBuiltin, Destination, GitChange, Skill, SkillDetail, SkillsList } from "~/lib/skills";
import { render } from "~/test/harness";

import type { PageCommand } from "~/components/tools/parts";
import { SkillsTab } from "./SkillsTab";

const skill = (name: string, extra: Partial<Skill> = {}): Skill => ({
  name,
  description: `${name} does things`,
  dir: `/lib/${name}`,
  scope: "user",
  editable: true,
  mode: "on",
  ...extra,
});

const projectDestinations: Destination[] = [
  { kind: "project", folder: "/home/p", label: "This project" },
  { kind: "repo", folder: "/code/api", label: "api repo", main: true },
  { kind: "repo", folder: "/code/web", label: "web repo" },
  { kind: "personal", folder: "", label: "Personal" },
];

type Handler = (args: Record<string, unknown>) => unknown;

/** A server with just enough state that a write shows on the next read. */
function server(overrides: Record<string, Handler> = {}, initial: Skill[] = [skill("alpha"), skill("beta")]) {
  const state = {
    skills: initial,
    claudeSync: true,
    codexBundled: true,
    claudeBundled: true,
    // Each built-in's own switch, under the group's.
    claudeOff: new Set<string>(),
    claudeNames: ["loop", "simplify"],
    changes: [] as GitChange[],
    destinations: undefined as Destination[] | undefined,
    defaultDestination: undefined as string | undefined,
  };
  const find = (dir: unknown) => state.skills.find((s) => s.dir === dir)!;
  const builtin = (name: string): ClaudeBuiltin => ({
    name,
    mode: !state.claudeBundled || state.claudeOff.has(name) ? "off" : "on",
  });
  const handlers: Record<string, Handler> = {
    list_skills: (): SkillsList => ({
      skills: state.skills,
      claudeSync: state.claudeSync,
      codexBundled: state.codexBundled,
      claudeBundled: state.claudeBundled,
      claudeBuiltins: state.claudeNames.map(builtin),
      destinations: state.destinations,
      defaultDestination: state.defaultDestination,
    }),
    read_skill: (args): SkillDetail => ({ ...find(args.dir), content: `# ${find(args.dir).name}\n`, files: [] }),
    set_skill_mode: (args) => {
      const next = { ...find(args.dir), mode: args.mode as Skill["mode"] };
      state.skills = state.skills.map((s) => (s.dir === next.dir ? next : s));
      return next;
    },
    set_claude_sync: (args) => {
      state.claudeSync = Boolean(args.on);
      return { claudeSync: state.claudeSync };
    },
    set_claude_bundled: (args) => {
      state.claudeBundled = Boolean(args.on);
      return { claudeBundled: state.claudeBundled };
    },
    set_claude_builtin: (args) => {
      const name = String(args.name);
      if (args.on) state.claudeOff.delete(name);
      else state.claudeOff.add(name);
      return builtin(name);
    },
    set_codex_bundled: (args) => {
      state.codexBundled = Boolean(args.on);
      return { codexBundled: state.codexBundled };
    },
    skills_git_status: () => ({ git: { root: "/lib", branch: "main", changes: state.changes } }),
    commit_skills: () => {
      state.changes = [];
      return { commit: "abc", git: { root: "/lib", branch: "main", changes: [] } };
    },
    discard_staged: () => ({}),
    ...overrides,
  };
  const command = vi.fn(async (name: string, args: Record<string, unknown>) => {
    const handler = handlers[name];
    if (!handler) throw new Error(`unexpected command ${name}`);
    return handler(args);
  });
  return { command: command as unknown as PageCommand & typeof command, state };
}

function renderPage(command: PageCommand) {
  return render(<SkillsTab command={command} scope={{ kind: "personal" }} />);
}

const calls = (command: ReturnType<typeof vi.fn>, name: string) =>
  command.mock.calls.filter(([n]) => n === name).map(([, args]) => args as Record<string, unknown>);

async function openSkill(name: string) {
  fireEvent.click(await screen.findByRole("button", { name: new RegExp(`^${name}\\b`) }));
  await screen.findByRole("button", { name: "Back to skills" });
}

describe("the mode switch", () => {
  it("writes the chosen mode and shows what the server answered", async () => {
    const { command } = server();
    renderPage(command);
    await openSkill("alpha");

    fireEvent.click(screen.getByRole("radio", { name: "Manual" }));

    await waitFor(() => expect(screen.getByRole("radio", { name: "Manual" }).getAttribute("aria-checked")).toBe("true"));
    expect(calls(command, "set_skill_mode")).toEqual([{ dir: "/lib/alpha", mode: "manual" }]);
    expect(screen.getByRole("radio", { name: "On" }).getAttribute("aria-checked")).toBe("false");
  });

  it("goes back to the old mode and shows the error when the write fails", async () => {
    const { command } = server({
      set_skill_mode: () => {
        throw new Error("settings.json is not valid JSON");
      },
    });
    renderPage(command);
    await openSkill("alpha");

    fireEvent.click(screen.getByRole("radio", { name: "Off" }));

    expect((await screen.findByRole("alert")).textContent).toContain("settings.json is not valid JSON");
    expect(screen.getByRole("radio", { name: "On" }).getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("radio", { name: "Off" }).getAttribute("aria-checked")).toBe("false");
  });

  it("is not offered for a skill that is not ours to change", async () => {
    const { command } = server({}, [skill("wrangler", { scope: "plugin", plugin: "cloudflare", editable: false })]);
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: /Plugins/ }));
    await openSkill("wrangler");

    expect(screen.queryByRole("radiogroup")).toBeNull();
  });
});

describe("the section switches", () => {
  it("turns claude.ai sync off and reads the list again", async () => {
    const { command } = server();
    renderPage(command);
    const sw = await screen.findByRole("switch", { name: "Sync from claude.ai" });
    expect(sw.getAttribute("aria-checked")).toBe("true");
    const reads = calls(command, "list_skills").length;

    fireEvent.click(sw);

    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("false"));
    expect(calls(command, "set_claude_sync")).toEqual([{ on: false }]);
    await waitFor(() => expect(calls(command, "list_skills").length).toBeGreaterThan(reads));
  });

  it("turns Codex's built-in skills back on", async () => {
    const { command, state } = server();
    state.codexBundled = false;
    renderPage(command);
    const sw = await screen.findByRole("switch", { name: "Codex built-in skills" });
    expect(sw.getAttribute("aria-checked")).toBe("false");

    fireEvent.click(sw);

    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"));
    expect(calls(command, "set_codex_bundled")).toEqual([{ on: true }]);
  });

  it("puts the switch back and shows the error when the write fails", async () => {
    const { command } = server({
      set_codex_bundled: () => {
        throw new Error("config.toml is read-only");
      },
    });
    renderPage(command);
    const sw = await screen.findByRole("switch", { name: "Codex built-in skills" });

    fireEvent.click(sw);

    expect((await screen.findByRole("alert")).textContent).toContain("config.toml is read-only");
    expect(sw.getAttribute("aria-checked")).toBe("true");
  });
});

describe("built-in skills", () => {
  const codexSystem = () => [skill("alpha"), skill("imagegen", { dir: "/codex/.system/imagegen", scope: "system", editable: false })];

  it("turns one Claude Code built-in off, and back on", async () => {
    const { command } = server();
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: /Claude Code built-in/ }));
    const sw = screen.getByRole("switch", { name: "simplify" });
    expect(sw.getAttribute("aria-checked")).toBe("true");

    fireEvent.click(sw);
    await waitFor(() => expect(screen.getByRole("switch", { name: "simplify" }).getAttribute("aria-checked")).toBe("false"));
    fireEvent.click(screen.getByRole("switch", { name: "simplify" }));
    await waitFor(() => expect(screen.getByRole("switch", { name: "simplify" }).getAttribute("aria-checked")).toBe("true"));

    expect(calls(command, "set_claude_builtin")).toEqual([
      { name: "simplify", on: false },
      { name: "simplify", on: true },
    ]);
    expect(screen.getByRole("switch", { name: "loop" }).getAttribute("aria-checked")).toBe("true");
  });

  it("holds every row off while the group is off, and gives each its own back with the group", async () => {
    const { command, state } = server();
    state.claudeBundled = false;
    state.claudeOff.add("loop");
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: /Claude Code built-in/ }));
    for (const name of ["loop", "simplify"]) {
      const sw = screen.getByRole("switch", { name });
      expect(sw.getAttribute("aria-checked")).toBe("false");
      expect(sw.hasAttribute("disabled")).toBe(true);
    }

    fireEvent.click(screen.getByRole("switch", { name: "Claude Code built-in skills" }));

    await waitFor(() => expect(screen.getByRole("switch", { name: "simplify" }).getAttribute("aria-checked")).toBe("true"));
    expect(screen.getByRole("switch", { name: "loop" }).getAttribute("aria-checked")).toBe("false");
    expect(screen.getByRole("switch", { name: "loop" }).hasAttribute("disabled")).toBe(false);
    expect(calls(command, "set_claude_bundled")).toEqual([{ on: true }]);
  });

  it("puts a row's switch back and says why when the write fails", async () => {
    const { command } = server({
      set_claude_builtin: () => {
        throw new Error("settings.json is not valid JSON");
      },
    });
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: /Claude Code built-in/ }));

    fireEvent.click(screen.getByRole("switch", { name: "loop" }));

    expect(await screen.findByText(/settings.json is not valid JSON/)).toBeTruthy();
    expect(screen.getByRole("switch", { name: "loop" }).getAttribute("aria-checked")).toBe("true");
  });

  it("switches a Codex built-in off as a mode, and still opens it", async () => {
    const { command } = server({}, codexSystem());
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: /Codex built-in/ }));

    fireEvent.click(screen.getByRole("switch", { name: "imagegen" }));

    await waitFor(() => expect(screen.getByRole("switch", { name: "imagegen" }).getAttribute("aria-checked")).toBe("false"));
    expect(calls(command, "set_skill_mode")).toEqual([{ dir: "/codex/.system/imagegen", mode: "off" }]);
    await openSkill("imagegen");
  });

  it("finds Claude Code built-ins by name", async () => {
    const { command } = server();
    renderPage(command);
    await screen.findByRole("button", { name: /^alpha\b/ });

    fireEvent.change(screen.getByRole("searchbox", { name: "Search skills" }), { target: { value: "simp" } });

    expect(screen.getByRole("switch", { name: "simplify" })).toBeTruthy();
    expect(screen.queryByRole("switch", { name: "loop" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^alpha\b/ })).toBeNull();
  });
});

describe("the list", () => {
  it("keeps same-name copies as separate rows", async () => {
    const { command } = server({}, [skill("dup"), skill("dup", { dir: "/elsewhere/dup" })]);
    renderPage(command);
    await waitFor(() => expect(screen.getAllByRole("button", { name: /^dup\b/ })).toHaveLength(2));
  });

  it("shows only the matching rows while searching", async () => {
    const { command } = server();
    renderPage(command);
    await screen.findByRole("button", { name: /^alpha\b/ });

    fireEvent.change(screen.getByRole("searchbox", { name: "Search skills" }), { target: { value: "bet" } });

    expect(screen.queryByRole("button", { name: /^alpha\b/ })).toBeNull();
    expect(screen.getByRole("button", { name: /^beta\b/ })).toBeTruthy();
  });

  it("offers Try again after a failed load, and loads on it", async () => {
    let fail = true;
    const { command } = server();
    const real = command.getMockImplementation()!;
    command.mockImplementation(async (name, args) => {
      if (name === "list_skills" && fail) {
        fail = false;
        throw new Error("connection lost");
      }
      return real(name, args);
    });
    renderPage(command);

    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));

    expect(await screen.findByRole("button", { name: /^alpha\b/ })).toBeTruthy();
  });
});

describe("installing", () => {
  const staged = {
    id: "stage-1",
    repo: "acme/skills",
    skills: [
      { name: "pdf", description: "Reads PDFs", files: [{ path: "SKILL.md", size: 10 }], picked: true, installedIn: [] },
      { name: "alpha", description: "A newer alpha", files: [{ path: "SKILL.md", size: 10 }], picked: true, installedIn: [""] },
      {
        name: "xlsx",
        description: "Reads sheets",
        files: [{ path: "SKILL.md", size: 10 }],
        installedIn: ["/home/p"],
      },
    ],
  };

  async function fetchSource(command: PageCommand) {
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "acme/skills" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Fetch" }));
    await within(dialog).findByRole("checkbox", { name: "Install pdf" });
    return dialog;
  }

  it("starts a skill that would replace an installed one unticked", async () => {
    const { command } = server({ stage_skills: () => staged });
    const dialog = await fetchSource(command);

    expect(calls(command, "stage_skills")).toEqual([{ source: "acme/skills" }]);
    expect(within(dialog).getByRole("checkbox", { name: "Install pdf" }).getAttribute("aria-checked")).toBe("true");
    expect(within(dialog).getByRole("checkbox", { name: "Install alpha" }).getAttribute("aria-checked")).toBe("false");
    expect(within(dialog).getByRole("checkbox", { name: "Install xlsx" }).getAttribute("aria-checked")).toBe("false");
  });

  it("installs exactly the ticked skills and lists what came back", async () => {
    const { command, state } = server({
      stage_skills: () => staged,
      install_staged: (args) => {
        const added = (args.skills as string[]).map((n) => skill(n, { dir: `/lib/${n}-new` }));
        state.skills = [...state.skills, ...added];
        return { skills: added };
      },
    });
    const dialog = await fetchSource(command);

    fireEvent.click(within(dialog).getByRole("checkbox", { name: "Install alpha" }));
    fireEvent.click(within(dialog).getByRole("button", { name: /^Install \d/ }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls(command, "install_staged")).toEqual([{ id: "stage-1", skills: ["pdf", "alpha"], destination: "" }]);
    expect(await screen.findByRole("button", { name: /^pdf\b/ })).toBeTruthy();
    // Installing used the fetched copy up; there is nothing left to throw away.
    expect(calls(command, "discard_staged")).toEqual([]);
  });

  it("keeps the sheet open with the error when the install fails", async () => {
    const { command } = server({
      stage_skills: () => staged,
      install_staged: () => {
        throw new Error("disk full");
      },
    });
    const dialog = await fetchSource(command);

    fireEvent.click(within(dialog).getByRole("button", { name: /^Install \d/ }));

    expect((await within(dialog).findByRole("alert")).textContent).toContain("disk full");
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("shows a skill's SKILL.md when its name is tapped", async () => {
    const { command } = server({
      stage_skills: () => staged,
      read_staged_file: () => ({ content: "# PDF reader body", binary: false }),
    });
    const dialog = await fetchSource(command);

    fireEvent.click(within(dialog).getByRole("button", { name: /^pdf\b/ }));

    expect(await within(dialog).findByText("# PDF reader body")).toBeTruthy();
    expect(calls(command, "read_staged_file")).toEqual([{ id: "stage-1", skill: "pdf", path: "SKILL.md" }]);
  });

  it("installs into the server's default destination, or the one picked", async () => {
    const { command, state } = server({ stage_skills: () => staged, install_staged: () => ({ skills: [] }) });
    state.destinations = projectDestinations;
    state.defaultDestination = "/home/p";
    const dialog = await fetchSource(command);

    const picker = within(dialog).getByRole("radiogroup", { name: "Install into" });
    expect(within(picker).getByRole("radio", { name: /^This project/ }).getAttribute("aria-checked")).toBe("true");
    fireEvent.click(within(picker).getByRole("radio", { name: /^web repo/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: /^Install \d/ }));

    await waitFor(() => expect(calls(command, "install_staged")).toHaveLength(1));
    expect(calls(command, "install_staged")[0].destination).toBe("/code/web");
  });

  it("marks and holds back only what would replace a skill in the chosen destination", async () => {
    const { command, state } = server({ stage_skills: () => staged });
    state.destinations = projectDestinations;
    state.defaultDestination = "";
    const dialog = await fetchSource(command);
    const row = (name: string) => within(dialog).getByRole("button", { name: new RegExp(`^${name}\\b`) });
    const ticked = (name: string) =>
      within(dialog).getByRole("checkbox", { name: `Install ${name}` }).getAttribute("aria-checked");

    expect(row("alpha").textContent).toContain("replaces yours");
    expect(ticked("alpha")).toBe("false");
    expect(row("xlsx").textContent).not.toContain("replaces");

    fireEvent.click(within(dialog).getByRole("radio", { name: /^This project/ }));

    expect(row("alpha").textContent).not.toContain("replaces");
    expect(ticked("alpha")).toBe("true");
    expect(row("xlsx").textContent).toContain("replaces existing");
  });

  it("says a main checkout's install is not committed, and only there", async () => {
    const { command, state } = server({ stage_skills: () => staged });
    state.destinations = projectDestinations;
    state.defaultDestination = "/home/p";
    const dialog = await fetchSource(command);

    expect(within(dialog).queryByText(/^Not committed/)).toBeNull();
    fireEvent.click(within(dialog).getByRole("radio", { name: /^api repo/ }));
    expect(within(dialog).getByText(/^Not committed/)).toBeTruthy();
    fireEvent.click(within(dialog).getByRole("radio", { name: /^web repo/ }));
    expect(within(dialog).queryByText(/^Not committed/)).toBeNull();
  });

  it("has nothing to pick with only one destination", async () => {
    const { command } = server({ stage_skills: () => staged });
    const dialog = await fetchSource(command);
    expect(within(dialog).queryByRole("radiogroup")).toBeNull();
  });
});

describe("writing a new skill", () => {
  it("creates it and opens it in the editor", async () => {
    const { command, state } = server({
      create_skill: (args) => {
        const made = skill(String(args.name), { description: String(args.description) });
        state.skills = [...state.skills, made];
        return made;
      },
    });
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog");

    fireEvent.click(within(dialog).getByRole("button", { name: /new skill/ }));
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "review-migrations" } });
    fireEvent.change(within(dialog).getByLabelText("Description"), { target: { value: "Checks migrations" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create" }));

    const editor = (await screen.findByRole("textbox", { name: "SKILL.md source" })) as HTMLTextAreaElement;
    expect(editor.value).toBe("# review-migrations\n");
    expect(calls(command, "create_skill")).toEqual([
      { name: "review-migrations", description: "Checks migrations", destination: "" },
    ]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("creates it in the destination picked", async () => {
    const { command, state } = server({ create_skill: (args) => skill(String(args.name)) });
    state.destinations = projectDestinations;
    state.defaultDestination = "/home/p";
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog");

    fireEvent.click(within(dialog).getByRole("button", { name: /new skill/ }));
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "deploy" } });
    fireEvent.change(within(dialog).getByLabelText("Description"), { target: { value: "Ships it" } });
    fireEvent.click(within(dialog).getByRole("radio", { name: /^Personal/ }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Create" }));

    await waitFor(() => expect(calls(command, "create_skill")).toHaveLength(1));
    expect(calls(command, "create_skill")[0].destination).toBe("");
  });

  it("does not send a name the spec would reject", async () => {
    const { command } = server({ create_skill: () => skill("x") });
    renderPage(command);
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog");

    fireEvent.click(within(dialog).getByRole("button", { name: /new skill/ }));
    fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Bad Name" } });
    fireEvent.change(within(dialog).getByLabelText("Description"), { target: { value: "Something" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create" }));

    await waitFor(() => expect(within(dialog).getByLabelText("Name").getAttribute("aria-invalid")).toBe("true"));
    expect(calls(command, "create_skill")).toEqual([]);
  });
});

describe("checking for an update", () => {
  const sourced = skill("pdf", { source: { method: "git", repo: "acme/skills", managed: true } });
  const sibling = skill("xlsx", { source: { method: "git", repo: "acme/skills", managed: true } });

  it("shows only the opened skill's changes and applies just that skill", async () => {
    const { command } = server(
      {
        stage_update: () => ({
          id: "up-1",
          repo: "acme/skills",
          skills: [
            { name: "pdf", dir: "/lib/pdf", changed: true, files: [{ path: "SKILL.md", status: "modified" }] },
            { name: "xlsx", dir: "/lib/xlsx", changed: true, files: [{ path: "sheet.py", status: "added" }] },
          ],
        }),
        apply_update: () => ({ skills: [{ ...sourced, description: "Reads PDFs better" }] }),
      },
      [sourced, sibling],
    );
    renderPage(command);
    await openSkill("pdf");

    fireEvent.click(screen.getByRole("button", { name: "Check for update" }));

    const files = await screen.findByRole("list", { name: "Changed files" });
    expect(within(files).getAllByRole("button").map((b) => b.textContent)).toEqual([expect.stringContaining("SKILL.md")]);
    expect(calls(command, "stage_update")).toEqual([{ dir: "/lib/pdf" }]);

    fireEvent.click(screen.getByRole("button", { name: "Update" }));

    await waitFor(() => expect(calls(command, "apply_update")).toEqual([{ id: "up-1", dirs: ["/lib/pdf"] }]));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Update" })).toBeNull());
  });

  it("offers no update when only another skill from the same source changed", async () => {
    const { command } = server(
      {
        stage_update: () => ({
          id: "up-1",
          repo: "acme/skills",
          skills: [
            { name: "pdf", dir: "/lib/pdf", changed: false, files: [] },
            { name: "xlsx", dir: "/lib/xlsx", changed: true, files: [{ path: "sheet.py", status: "added" }] },
          ],
        }),
      },
      [sourced, sibling],
    );
    renderPage(command);
    await openSkill("pdf");

    fireEvent.click(screen.getByRole("button", { name: "Check for update" }));

    await waitFor(() => expect(calls(command, "stage_update")).toHaveLength(1));
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
    expect(screen.queryByRole("list", { name: "Changed files" })).toBeNull();
  });
});

describe("the commit strip", () => {
  it("commits every change and then goes away", async () => {
    const { command, state } = server();
    state.changes = [
      { name: "alpha", status: "modified", files: 1 },
      { name: "pdf", status: "added", files: 3 },
    ];
    renderPage(command);

    const strip = await screen.findByRole("region", { name: "Changes not committed" });
    fireEvent.click(within(strip).getByRole("button", { name: "Commit" }));

    await waitFor(() => expect(screen.queryByRole("region", { name: "Changes not committed" })).toBeNull());
    const [args] = calls(command, "commit_skills");
    expect(args.names).toEqual(["alpha", "pdf"]);
    expect(args.message).toBeTruthy();
  });

  it("is not there when nothing is uncommitted", async () => {
    const { command } = server();
    renderPage(command);
    await screen.findByRole("button", { name: /^alpha\b/ });
    await waitFor(() => expect(calls(command, "skills_git_status").length).toBeGreaterThan(0));
    expect(screen.queryByRole("region", { name: "Changes not committed" })).toBeNull();
  });

  it("asks git again after a write", async () => {
    const { command } = server();
    renderPage(command);
    await openSkill("alpha");
    await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(1));

    fireEvent.click(screen.getByRole("radio", { name: "Manual" }));

    await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(2));
  });
});

describe("project skills", () => {
  const ours = skill("brief", { scope: "project", private: true, folder: "/home/p", dir: "/home/p/.agents/skills/brief" });
  const api = skill("migrate", { scope: "project", folder: "/code/api", dir: "/code/api/.agents/skills/migrate" });
  const web = skill("lint", { scope: "project", folder: "/code/web", dir: "/code/web/.agents/skills/lint" });

  function renderProject(skills: Skill[]) {
    const { command, state } = server({}, skills);
    state.destinations = projectDestinations;
    state.defaultDestination = "/home/p";
    render(<SkillsTab command={command} scope={{ kind: "project", projectId: "p1", projectName: "p" }} />);
    return command;
  }

  it("lists the project's own apart from the repo's, titled by its folder", async () => {
    renderProject([ours, api]);

    const project = await screen.findByRole("region", { name: "This project" });
    expect(within(project).getByRole("button", { name: /^brief\b/ })).toBeTruthy();
    const repo = screen.getByRole("region", { name: "api repo" });
    expect(within(repo).getByRole("button", { name: /^migrate\b/ })).toBeTruthy();
    expect(within(repo).queryByRole("button", { name: /^brief\b/ })).toBeNull();
  });

  it("names each row's repo when the skills are in several", async () => {
    renderProject([api, web]);

    const repos = await screen.findByRole("region", { name: "Repos" });
    expect(within(repos).getByRole("button", { name: /^migrate\b/ }).textContent).toContain("api repo");
    expect(within(repos).getByRole("button", { name: /^lint\b/ }).textContent).toContain("web repo");
  });

  it("says a skill is not committed, on its row and in full when opened", async () => {
    renderProject([{ ...api, uncommitted: true }, web]);

    expect((await screen.findByRole("button", { name: /^migrate\b/ })).textContent).toContain("Not committed");
    expect(screen.getByRole("button", { name: /^lint\b/ }).textContent).not.toContain("Not committed");

    await openSkill("migrate");
    expect(screen.getByText(/won't see it until it is/).parentElement?.textContent).toContain("api repo");
  });
});
