// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { InstallDialog } from "./InstallDialog";
import type { SkillsCommand } from "./parts";
import type { Setup, Skill, Staged, StagedSkill } from "~/lib/skills";
import { render } from "~/test/harness";

const SETUP: Setup = {
  library: "~/.agents/skills",
  libraryDir: "/home/u/.agents/skills",
  exists: true,
  projectLibrary: ".agents/skills",
  cliVersion: "1.0.0",
  npx: true,
  links: [
    { harness: "claude", dir: "/home/u/.claude/skills", state: "per-skill" },
    { harness: "codex", dir: "/home/u/.agents/skills", state: "direct" },
    { harness: "pi", dir: "/home/u/.agents/skills", state: "direct" },
  ],
};

const staged = (name: string, extra: Partial<StagedSkill> = {}): StagedSkill => ({
  name,
  description: `${name} does things`,
  files: [
    { path: "SKILL.md", size: 10 },
    { path: "notes.md", size: 5 },
  ],
  manual: false,
  picked: false,
  inUser: false,
  inProject: false,
  ...extra,
});

const stage = (skills: StagedSkill[], extra: Partial<Staged> = {}): Staged => ({
  id: "stage-1",
  method: "git",
  repo: "acme/skills",
  skills,
  ...extra,
});

const placed = (name: string): Skill => ({
  name,
  description: "",
  dir: `/home/u/.agents/skills/${name}`,
  scope: "user",
  paths: [],
  harnesses: ["claude", "codex", "pi"],
  editable: true,
});

type Handler = (args: Record<string, unknown>) => unknown;

function mockCommand(handlers: Record<string, Handler>) {
  return vi.fn(async (name: string, args: Record<string, unknown>) => {
    const handler = handlers[name];
    if (!handler) throw new Error(`unexpected command ${name}`);
    return handler(args);
  }) as unknown as SkillsCommand & ReturnType<typeof vi.fn>;
}

const calls = (command: ReturnType<typeof vi.fn>, name: string) =>
  command.mock.calls.filter(([n]) => n === name).map(([, args]) => args as Record<string, unknown>);

function open(command: SkillsCommand, extra: Partial<Parameters<typeof InstallDialog>[0]> = {}) {
  const onOpenChange = vi.fn();
  const onInstalled = vi.fn();
  render(
    <InstallDialog
      open
      onOpenChange={onOpenChange}
      command={command}
      scopeArgs={{ threadId: "t1" }}
      setup={SETUP}
      projectRoot="/proj"
      projectAvailable
      onInstalled={onInstalled}
      {...extra}
    />,
  );
  return { onOpenChange, onInstalled };
}

const fetchSource = (text: string) => {
  fireEvent.change(screen.getByLabelText("Source"), { target: { value: text } });
  fireEvent.click(screen.getByRole("button", { name: "Fetch" }));
};

const installButton = () => screen.getByRole("button", { name: /^Install/ });
const tickBox = (name: RegExp | string) => screen.getByRole("checkbox", { name });

describe("InstallDialog", () => {
  it("shows a failed fetch and lets the same source be tried again", async () => {
    let attempts = 0;
    const command = mockCommand({
      stage_skills: () => {
        if (++attempts === 1) throw new Error("clone failed: no route to host");
        return stage([staged("pdf")]);
      },
      discard_staged: () => ({}),
    });
    open(command);

    fetchSource("acme/skills");
    expect(await screen.findByText(/no route to host/)).toBeTruthy();
    // Back at the paste box with what was typed, not at an empty one.
    expect((screen.getByLabelText("Source") as HTMLTextAreaElement).value).toBe("acme/skills");

    fireEvent.click(screen.getByRole("button", { name: "Fetch" }));
    expect(await screen.findByRole("checkbox", { name: /pdf/ })).toBeTruthy();
    expect(screen.queryByText(/no route to host/)).toBeNull();
    expect(calls(command, "stage_skills")).toEqual([
      { threadId: "t1", source: "acme/skills" },
      { threadId: "t1", source: "acme/skills" },
    ]);
  });

  it("treats a source with no skills as a failure and discards what was fetched", async () => {
    const command = mockCommand({ stage_skills: () => stage([]), discard_staged: () => ({}) });
    open(command);

    fetchSource("acme/empty");
    await waitFor(() => expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "stage-1" }]));
    expect(screen.getByLabelText("Source")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Install/ })).toBeNull();
  });

  it("ticks the named skills only, and installs just what is ticked", async () => {
    const command = mockCommand({
      stage_skills: () => stage([staged("a"), staged("b", { picked: true }), staged("c")]),
      install_staged: () => ({ skills: [placed("b"), placed("c")] }),
      discard_staged: () => ({}),
    });
    const { onInstalled, onOpenChange } = open(command);

    fetchSource("npx skills add acme/skills -s b");
    expect(((await screen.findByRole("checkbox", { name: /^b/ })) as HTMLElement).getAttribute("aria-checked")).toBe(
      "true",
    );
    expect(tickBox(/^a/).getAttribute("aria-checked")).toBe("false");

    fireEvent.click(tickBox(/^c/));
    fireEvent.click(installButton());

    await waitFor(() => expect(onInstalled).toHaveBeenCalled());
    expect(calls(command, "install_staged")).toEqual([
      {
        threadId: "t1",
        id: "stage-1",
        skills: ["b", "c"],
        scope: "user",
        link: ["claude", "codex", "pi"],
        replace: false,
      },
    ]);
    expect(onInstalled).toHaveBeenCalledWith([placed("b"), placed("c")], "user");
    expect(onOpenChange).toHaveBeenCalledWith(false);
    // The install used the holding folder up; there is nothing left to discard.
    expect(calls(command, "discard_staged")).toEqual([]);
  });

  it("cannot install with nothing ticked", async () => {
    const command = mockCommand({ stage_skills: () => stage([staged("a"), staged("b")]), discard_staged: () => ({}) });
    open(command);

    fetchSource("acme/skills");
    await screen.findByRole("checkbox", { name: /^a/ });
    expect((installButton() as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Select all" }));
    expect((installButton() as HTMLButtonElement).disabled).toBe(false);
  });

  it("blocks a clash until replace is ticked, then says so to the server", async () => {
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf", { inUser: true })]),
      install_staged: () => ({ skills: [placed("pdf")] }),
      discard_staged: () => ({}),
    });
    const { onInstalled } = open(command);

    fetchSource("acme/skills");
    const replace = await screen.findByRole("checkbox", { name: /Replace/ });
    expect((installButton() as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(installButton());
    expect(calls(command, "install_staged")).toEqual([]);

    fireEvent.click(replace);
    expect((installButton() as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(installButton());

    await waitFor(() => expect(onInstalled).toHaveBeenCalled());
    expect(calls(command, "install_staged")[0]).toMatchObject({ skills: ["pdf"], replace: true });
  });

  it("judges a clash by the library being installed into, and forgets the replace tick on a switch", async () => {
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf", { inUser: true })]),
      install_staged: () => ({ skills: [] }),
      discard_staged: () => ({}),
    });
    const { onInstalled } = open(command);

    fetchSource("acme/skills");
    fireEvent.click(await screen.findByRole("checkbox", { name: /Replace/ }));

    // Nothing of that name in the project, so nothing to replace there.
    fireEvent.click(screen.getByRole("radio", { name: /Project/ }));
    expect(screen.queryByRole("checkbox", { name: /Replace/ })).toBeNull();

    // Back in the personal library the question is asked afresh.
    fireEvent.click(screen.getByRole("radio", { name: /Personal/ }));
    expect(screen.getByRole("checkbox", { name: /Replace/ }).getAttribute("aria-checked")).toBe("false");
    expect((installButton() as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole("radio", { name: /Project/ }));
    fireEvent.click(installButton());
    await waitFor(() => expect(onInstalled).toHaveBeenCalled());
    expect(calls(command, "install_staged")[0]).toMatchObject({ scope: "project", replace: false });
    expect(onInstalled).toHaveBeenCalledWith([], "project");
  });

  it("leaves out a harness whose symlink was unticked, and keeps the ones that read the library", async () => {
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf")]),
      install_staged: () => ({ skills: [placed("pdf")] }),
      discard_staged: () => ({}),
    });
    const { onInstalled } = open(command);

    fetchSource("acme/skills");
    const harnesses = within(await screen.findByRole("region", { name: "Harnesses" }));
    const boxes = harnesses.getAllByRole("checkbox") as HTMLButtonElement[];
    // Claude needs a symlink and can be turned off; the other two read the library.
    expect(boxes.map((b) => b.disabled)).toEqual([false, true, true]);
    fireEvent.click(boxes[0]);
    fireEvent.click(installButton());

    await waitFor(() => expect(onInstalled).toHaveBeenCalled());
    expect(calls(command, "install_staged")[0].link).toEqual(["codex", "pi"]);
  });

  it("does not offer the project library without a project", async () => {
    const command = mockCommand({ stage_skills: () => stage([staged("pdf")]), discard_staged: () => ({}) });
    open(command, { projectAvailable: false, projectRoot: undefined });

    fetchSource("acme/skills");
    expect(((await screen.findByRole("radio", { name: /Project/ })) as HTMLButtonElement).disabled).toBe(true);
  });

  it("keeps the dialog open and the staged skills when the install fails", async () => {
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf")]),
      install_staged: () => {
        throw new Error("permission denied");
      },
      discard_staged: () => ({}),
    });
    const { onInstalled, onOpenChange } = open(command);

    fetchSource("acme/skills");
    await screen.findByRole("checkbox", { name: /pdf/ });
    fireEvent.click(installButton());

    expect(await screen.findByText(/permission denied/)).toBeTruthy();
    expect(onInstalled).not.toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalled();
    expect((installButton() as HTMLButtonElement).disabled).toBe(false);

    // Still holding the folder, so giving up now throws it away.
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "stage-1" }]);
  });

  it("discards the holding folder on close", async () => {
    const command = mockCommand({ stage_skills: () => stage([staged("pdf")]), discard_staged: () => ({}) });
    const { onOpenChange } = open(command);

    fetchSource("acme/skills");
    await screen.findByRole("checkbox", { name: /pdf/ });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "stage-1" }]);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("has nothing to discard when closed before anything was fetched", () => {
    const command = mockCommand({ discard_staged: () => ({}) });
    const { onOpenChange } = open(command);

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(command).not.toHaveBeenCalled();
  });

  it("discards a holding folder it is unmounted with", async () => {
    const command = mockCommand({ stage_skills: () => stage([staged("pdf")]), discard_staged: () => ({}) });
    const onOpenChange = vi.fn();
    const view = render(
      <InstallDialog
        open
        onOpenChange={onOpenChange}
        command={command}
        scopeArgs={{}}
        projectAvailable={false}
        onInstalled={vi.fn()}
      />,
    );

    fetchSource("acme/skills");
    await screen.findByRole("checkbox", { name: /pdf/ });
    view.unmount();
    expect(calls(command, "discard_staged")).toEqual([{ id: "stage-1" }]);
  });

  it("lets a slow fetch be cancelled, and throws away what it brings back late", async () => {
    let land: (staged: Staged) => void = () => {};
    const command = mockCommand({
      stage_skills: () => new Promise<Staged>((resolve) => (land = resolve)),
      discard_staged: () => ({}),
    });
    open(command);

    fetchSource("https://github.com/acme/skills");
    expect((await screen.findByRole("status")).textContent).toContain("acme/skills");

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    // Back at the paste box, and nothing to discard yet: the server has not answered.
    expect(screen.getByLabelText("Source")).toBeTruthy();
    expect(calls(command, "discard_staged")).toEqual([]);

    land(stage([staged("pdf")], { id: "late" }));
    await waitFor(() => expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "late" }]));
    expect(screen.queryByRole("checkbox", { name: /pdf/ })).toBeNull();
  });

  it("drops the first source's folder when another is fetched in its place", async () => {
    let n = 0;
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf")], { id: `stage-${++n}` }),
      install_staged: () => ({ skills: [] }),
      discard_staged: () => ({}),
    });
    const { onInstalled } = open(command);

    fetchSource("acme/skills");
    await screen.findByRole("checkbox", { name: /pdf/ });
    fireEvent.click(screen.getByRole("button", { name: "Change source" }));
    expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "stage-1" }]);

    fetchSource("acme/other");
    await screen.findByRole("checkbox", { name: /pdf/ });
    fireEvent.click(installButton());
    await waitFor(() => expect(onInstalled).toHaveBeenCalled());
    expect(calls(command, "install_staged")[0].id).toBe("stage-2");
  });

  it("previews SKILL.md first and reads another file only when it is tapped, once", async () => {
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf")]),
      read_staged_file: (args) => ({ content: `body of ${String(args.path)}`, binary: false }),
      discard_staged: () => ({}),
    });
    open(command);

    fetchSource("acme/skills");
    await screen.findByRole("checkbox", { name: /pdf/ });
    expect(calls(command, "read_staged_file")).toEqual([]);

    fireEvent.click(screen.getByRole("button", { name: "Preview pdf" }));
    expect(await screen.findByText("body of SKILL.md")).toBeTruthy();
    expect(calls(command, "read_staged_file")).toEqual([
      { threadId: "t1", id: "stage-1", skill: "pdf", path: "SKILL.md" },
    ]);

    fireEvent.click(screen.getByRole("button", { name: "notes.md" }));
    expect(await screen.findByText("body of notes.md")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "SKILL.md" }));
    expect(await screen.findByText("body of SKILL.md")).toBeTruthy();
    expect(calls(command, "read_staged_file").map((c) => c.path)).toEqual(["SKILL.md", "notes.md"]);
  });

  it("says so when a staged file cannot be read, and reads it again on request", async () => {
    let fail = true;
    const command = mockCommand({
      stage_skills: () => stage([staged("pdf")]),
      read_staged_file: () => {
        if (fail) throw new Error("staging expired");
        return { content: "the body", binary: false };
      },
      discard_staged: () => ({}),
    });
    open(command);

    fetchSource("acme/skills");
    fireEvent.click(await screen.findByRole("button", { name: "Preview pdf" }));
    expect(await screen.findByText(/staging expired/)).toBeTruthy();

    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("the body")).toBeTruthy();
  });
});
