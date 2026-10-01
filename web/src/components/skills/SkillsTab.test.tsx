// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { GitChange, Skill, SkillDetail, SkillsList } from "~/lib/skills";
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

type Handler = (args: Record<string, unknown>) => unknown;

/** A server with just enough state that a write shows on the next read. */
function server(overrides: Record<string, Handler> = {}, initial: Skill[] = [skill("alpha"), skill("beta")]) {
  const state = {
    skills: initial,
    claudeSync: true,
    codexBundled: true,
    changes: [] as GitChange[],
  };
  const find = (dir: unknown) => state.skills.find((s) => s.dir === dir)!;
  const handlers: Record<string, Handler> = {
    list_skills: (): SkillsList => ({
      skills: state.skills,
      claudeSync: state.claudeSync,
      codexBundled: state.codexBundled,
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
      { name: "pdf", description: "Reads PDFs", files: [{ path: "SKILL.md", size: 10 }], picked: true, installed: false },
      { name: "alpha", description: "A newer alpha", files: [{ path: "SKILL.md", size: 10 }], picked: true, installed: true },
      { name: "xlsx", description: "Reads sheets", files: [{ path: "SKILL.md", size: 10 }], installed: false },
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
    expect(calls(command, "install_staged")).toEqual([{ id: "stage-1", skills: ["pdf", "alpha"] }]);
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
    expect(calls(command, "create_skill")).toEqual([{ name: "review-migrations", description: "Checks migrations" }]);
    expect(screen.queryByRole("dialog")).toBeNull();
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
