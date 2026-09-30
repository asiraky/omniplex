// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import { ThreadDraft, type NewThreadInput } from "./ThreadDraft";
import type { ComposerItem, HarnessMeta, Project, Workspace } from "~/protocol";

const project = {
  id: "p1",
  name: "repo",
  defaults: { harness: "claude", harnesses: {}, workspace: "managed" },
  folders: [{ id: "f1", path: "/tmp/repo", git: true, copiesDir: ".worktrees", provisionTimeoutSeconds: 1800, deprovisionTimeoutSeconds: 600 }],
} as unknown as Project;

const harness = {
  id: "claude",
  name: "Claude Code",
  models: [],
  permissionModes: [],
  availability: { state: "ready" },
} as unknown as HarnessMeta;

type Props = React.ComponentProps<typeof ThreadDraft>;

// The draft text lives with the caller, as it does in the app. It starts with
// a message typed so Send is live as soon as nothing else holds it back.
function Draft(props: Omit<Props, "draft" | "onDraftChange">) {
  const [draft, setDraft] = useState("hi");
  return <ThreadDraft {...props} draft={draft} onDraftChange={setDraft} />;
}

function open(over: Partial<Props> & { onCreate?: Props["onStart"] } = {}) {
  const { onCreate, ...rest } = over;
  // Made once, so a re-render hands the draft the same callbacks the app would.
  const base: Omit<Props, "draft" | "onDraftChange"> = {
    projects: [project],
    harnesses: [harness],
    userConfig: null,
    status: "online",
    onStart: onCreate ?? vi.fn(async () => {}),
    onListWorkspaces: vi.fn(async () => [] as Workspace[]),
    onListIssues: vi.fn(async () => ({ issues: [], issuesError: "" })),
    onListComposerItems: vi.fn(async () => [] as ComposerItem[]),
    onAddProject: vi.fn(),
    onSettings: vi.fn(),
    onRecheck: vi.fn(),
    ...rest,
  };
  const view = render(<Draft {...base} />);
  return {
    rerender: (next: Partial<Props>) => view.rerender(wrap(<Draft {...base} {...next} />)),
  };
}

const chip = (label: string) => screen.getByRole("button", { name: new RegExp(`^${label}`) });
// Radix menus open on pointer down, not click.
const menu = (label: string) =>
  fireEvent.pointerDown(chip(label), { button: 0, ctrlKey: false });
async function openGit() {
  if (screen.queryByRole("radiogroup", { name: "Git" })) return;
  fireEvent.click(await waitFor(() => chip("Git")));
  await waitFor(() => screen.getByRole("radiogroup", { name: "Git" }));
}

beforeEach(() => localStorage.clear());
afterEach(() => vi.unstubAllGlobals());

// Radix Select drives its trigger with pointer capture and scrolls the chosen
// item into view, neither of which jsdom implements.
beforeAll(() => {
  const proto = window.HTMLElement.prototype;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
});

describe("ThreadDraft", () => {
  it("creates the thread carrying the message", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({ onCreate, projects: [{ ...project, folders: [] } as unknown as Project] });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((send as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(send);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ projectId: "p1", text: "hi" });
  });

  it("puts the message back when the thread could not be created", async () => {
    open({
      onCreate: vi.fn(async () => {
        throw new Error("no room");
      }),
      projects: [{ ...project, folders: [] } as unknown as Project],
    });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((send as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(send);
    await waitFor(() => screen.getByText("no room"));
    expect(document.querySelector("textarea")!.value).toBe("hi");
  });

  it("starts a bypass thread with no confirmation of any kind", async () => {
    // Bypass is a value in a dropdown, not a decision to re-litigate: picking
    // it once (here, as the project default) is the whole opt-in.
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    const onCreate = vi.fn(async () => {});
    open({
      projects: [
        {
          ...project,
          // "local" keeps the workspace choice out of it: this test is about
          // the permission mode, and the main checkout is the one choice
          // that needs nothing else named before Send is live.
          defaults: {
            ...project.defaults,
            harnesses: { claude: { mode: "bypassPermissions" } },
            workspace: "local",
          },
        } as unknown as Project,
      ],
      harnesses: [
        {
          ...harness,
          permissionModes: [
            { id: "default", label: "Default", default: true },
            { id: "bypassPermissions", label: "Bypass", description: "Skip all permission checks" },
          ],
        } as unknown as HarnessMeta,
      ],
      onCreate,
    });

    const start = await screen.findByRole("button", { name: "Send" });
    // The workspace listing lands a tick later; Send is disabled until it has.
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    await act(async () => {
      fireEvent.click(start);
    });

    expect(confirm).not.toHaveBeenCalled();
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "bypassPermissions" }), false,
    );
  });

  it("restores this project's settings for the harness selected", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const ready = { state: "ready" } as const;
    const claude = {
      ...harness,
      permissionModes: [
        { id: "default", label: "Manual", default: true },
        { id: "bypassPermissions", label: "Bypass" },
      ],
      instances: [
        {
          id: "claude",
          driver: "claude",
          displayName: "Claude Code",
          enabled: true,
          availability: ready,
          models: [{ id: "opus", label: "Opus", default: true }],
        },
      ],
    } as unknown as HarnessMeta;
    const codex = {
      id: "codex",
      name: "Codex",
      availability: ready,
      models: [],
      permissionModes: [
        { id: "on-request", label: "Ask when needed", default: true },
        { id: "full-access", label: "Bypass" },
      ],
      instances: [
        {
          id: "codex",
          driver: "codex",
          displayName: "Codex",
          enabled: true,
          availability: ready,
          models: [
            {
              id: "gpt-5.6-sol",
              label: "GPT-5.6-Sol",
              default: true,
              efforts: ["high", "xhigh"],
            },
          ],
        },
      ],
    } as unknown as HarnessMeta;
    open({
      projects: [
        {
          ...project,
          defaults: {
            ...project.defaults,
            harness: "claude",
            harnesses: {
              claude: { mode: "bypassPermissions" },
              codex: { model: "gpt-5.6-sol", mode: "full-access", effort: "xhigh" },
            },
            workspace: "local",
          },
        } as unknown as Project,
      ],
      harnesses: [claude, codex],
      onCreate,
    });

    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.change(screen.getByPlaceholderText(/Search models and accounts/), {
      target: { value: "gpt" },
    });
    const model = await waitFor(() => screen.getByText("GPT-5.6-Sol"));
    fireEvent.click(model.closest("[data-slot='command-item']")!);

    expect(chip("Permissions").textContent).toBe("Bypass");
    const start = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ harness: "codex", mode: "full-access", effort: "xhigh" }), false,
    );
  });

  // The 1M toggle follows the harness's own answer, so a model that is not
  // Opus gets it whenever the CLI offers a "[1m]" alias for it — and picking
  // 1M is sent as that tag on the model id, which is the whole mechanism.
  it("offers the 1M window for any model the harness flags, not just Opus", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const claude = {
      ...harness,
      instances: [
        {
          id: "claude",
          driver: "claude",
          displayName: "Claude Code",
          enabled: true,
          availability: { state: "ready" } as const,
          models: [{ id: "claude-sonnet-5", label: "Sonnet 5", default: true, supports1m: true }],
        },
      ],
    } as unknown as HarnessMeta;
    open({ harnesses: [claude], onCreate });

    fireEvent.click(screen.getByRole("button", { name: "1M context" }));
    const start = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ model: "claude-sonnet-5[1m]" }), false);
  });

  // The flag is the only thing that decides it: an Opus row the harness did
  // not flag gets no toggle, however Opus-shaped its name is.
  it("offers an existing copy only when there is one, a level below the copy choice", async () => {
    open();
    await openGit();
    await waitFor(() => screen.getByRole("radio", { name: /Work on a copy/ }));
    expect(screen.getByRole("radio", { name: /Work in the folder/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Continue on an existing copy" })).toBeNull();
    cleanup();

    const side = { path: "/tmp/repo/.worktrees/side", branch: "issue/1-side" } as Workspace;
    open({ onListWorkspaces: vi.fn(async () => [side]) });
    await openGit();
    await waitFor(() => screen.getByRole("button", { name: "Continue on an existing copy" }));
    fireEvent.click(screen.getByRole("radio", { name: /Work in the folder/ }));
    expect(screen.queryByRole("button", { name: "Continue on an existing copy" })).toBeNull();
  });

  it("still offers the folder itself when another thread is on it", async () => {
    const root = {
      path: "/tmp/repo",
      isRoot: true,
      busy: true,
      busyTitle: "the other one",
    } as Workspace;
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({
      onCreate,
      onListWorkspaces: vi.fn(async () => [root]),
    });

    await openGit();
    const choice = await waitFor(() => screen.getByRole("radio", { name: /Work in the folder/ }));
    expect(choice.getAttribute("disabled")).toBeNull();
    fireEvent.click(choice);

    // No warning copy is shown for a busy main checkout — it was removed.
    expect(screen.queryByText(/already on the main checkout/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(confirm).not.toHaveBeenCalled();
    expect(onCreate.mock.calls[0][0]).toMatchObject({ workspace: "local", branch: "" });
  });

  it("works on a copy with a made-up branch when none is named", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({ onCreate });

    // The managed default lands on the copy; the branch name is optional.
    await openGit();
    await waitFor(() => screen.getByRole("radio", { name: /Work on a copy/, checked: true }));
    const start = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      workspace: "managed",
      branch: "",
      workspacePath: "",
    });
  });

  it("sends a per-thread base ref picked from the branches on disk", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    // The Base dropdown offers the branches already checked out; stacking on
    // one of them is exactly what it exists for.
    const under = {
      path: "/tmp/repo/.worktrees/under",
      branch: "feature/underneath",
    } as Workspace;
    open({ onCreate, onListWorkspaces: vi.fn(async () => [under]) });

    await openGit();
    fireEvent.click(await waitFor(() => screen.getByRole("button", { name: "Name the branch" })));
    const field = await waitFor(() => screen.getByRole("combobox", { name: /Branch/ }));
    fireEvent.change(field, { target: { value: "issue/9-stack" } });

    const base = screen.getByRole("combobox", { name: "Base" });
    base.focus();
    fireEvent.keyDown(base, { key: "ArrowDown" });
    const option = await waitFor(() => screen.getByRole("option", { name: "feature/underneath" }));
    fireEvent.click(option);

    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      workspace: "managed",
      branch: "issue/9-stack",
      baseRef: "feature/underneath",
    });
  });

  it("continues on a copy another thread is already in", async () => {
    const side = {
      path: "/tmp/repo/.worktrees/side",
      branch: "issue/1-side",
      busy: true,
      busyTitle: "the other one",
    } as Workspace;
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({
      onCreate,
      onListWorkspaces: vi.fn(async () => [side]),
    });

    await openGit();
    fireEvent.click(
      await waitFor(() => screen.getByRole("button", { name: "Continue on an existing copy" })),
    );
    fireEvent.click(screen.getByRole("combobox", { name: /Existing copy/ }));
    const row = await waitFor(() => screen.getByRole("option", { name: /issue\/1-side/ }));
    expect(row.hasAttribute("disabled")).toBe(false);
    fireEvent.click(row);

    // No warning copy is shown for a busy worktree — it was removed.
    expect(screen.queryByText(/already in this worktree/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      workspace: "",
      workspacePath: side.path,
    });
  });

  it("will not start before the busy check has come back", async () => {
    let release: (w: Workspace[]) => void = () => {};
    const pending = new Promise<Workspace[]>((r) => {
      release = r;
    });
    open({ onListWorkspaces: vi.fn(() => pending) });

    // The managed default lands on a copy with no name, so nothing but the
    // outstanding check is holding Send back.
    await openGit();
    await waitFor(() => screen.getByRole("radio", { name: /Work on a copy/ }));
    expect(screen.getByRole("button", { name: "Send" }).hasAttribute("disabled")).toBe(true);

    release([]);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Send" }).hasAttribute("disabled")).toBe(false),
    );
  });
});

describe("scope", () => {
  const folder = (id: string, path: string, git: boolean) => ({
    id,
    path,
    git,
    copiesDir: ".worktrees",
    provisionTimeoutSeconds: 1800,
    deprovisionTimeoutSeconds: 600,
  });
  const bowerbird = {
    ...project,
    folders: [folder("f1", "/tmp/bowerbird/site", true), folder("f2", "/tmp/bowerbird/notes", false)],
  } as unknown as Project;

  const start = async () => {
    const button = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(button);
  };

  it("starts across the whole project with no git questions by default", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const onListWorkspaces = vi.fn(async () => [] as Workspace[]);
    open({ projects: [bowerbird], onCreate, onListWorkspaces });

    expect(chip("Scope").textContent).toBe("Everything");
    expect(screen.queryByRole("button", { name: /^Git/ })).toBeNull();
    await start();
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ folderId: "", workspace: "local", branch: "" });
    expect(onListWorkspaces).not.toHaveBeenCalled();
  });

  it("asks the git questions for a git folder and lists its copies", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const onListWorkspaces = vi.fn(async () => [] as Workspace[]);
    open({ projects: [bowerbird], onCreate, onListWorkspaces });

    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^site/ }));

    await openGit();
    await waitFor(() => screen.getByRole("radio", { name: /Work on a copy/, checked: true }));
    expect(onListWorkspaces).toHaveBeenCalledWith("p1", "f1");
    await start();
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ folderId: "f1", workspace: "managed" });
  });

  it("keeps a folder pick with its own project", async () => {
    const garden = {
      ...bowerbird,
      id: "p2",
      name: "garden",
      folders: [folder("g1", "/tmp/garden/app", true), folder("g2", "/tmp/garden/docs", false)],
    } as unknown as Project;
    open({ projects: [bowerbird, garden] });

    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^site/ }));
    expect(chip("Scope").textContent).toBe("site");

    menu("Project");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^garden/ }));
    expect(chip("Scope").textContent).toBe("Everything");

    menu("Project");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^repo/ }));
    expect(chip("Scope").textContent).toBe("site");
  });

  it("asks for a folder's copies afresh on coming back to it", async () => {
    const side = { path: "/tmp/bowerbird/.worktrees/side", branch: "issue/1-side" } as Workspace;
    const onListWorkspaces = vi
      .fn<Props["onListWorkspaces"]>()
      .mockResolvedValueOnce([side])
      .mockReturnValue(new Promise(() => {}));
    open({ projects: [bowerbird], onListWorkspaces });

    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^site/ }));
    await openGit();
    await waitFor(() => screen.getByRole("button", { name: "Continue on an existing copy" }));
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });

    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^Everything/ }));
    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^site/ }));

    // The copy listed last time may have gone since; until the folder
    // answers again, nothing from the old list is offered or sent on.
    await openGit();
    expect(onListWorkspaces).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("button", { name: "Continue on an existing copy" })).toBeNull();
    expect(screen.getByRole("button", { name: "Send" }).hasAttribute("disabled")).toBe(true);
  });

  it("asks nothing for a project whose one folder is plain", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const plain = { ...project, folders: [folder("f2", "/tmp/notes", false)] } as unknown as Project;
    open({ projects: [plain], onCreate });

    expect(screen.queryByRole("button", { name: /^Scope/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Git/ })).toBeNull();
    await start();
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ folderId: "f2", workspace: "local" });
  });
});

// The thread that would answer for its provider's commands does not exist
// yet, so the draft asks about the provider and folder it is set to.
describe("the command menu before the thread exists", () => {
  const skill = (name: string, inline = true): ComposerItem => ({
    id: `skill:${name}`,
    name,
    kind: "skill",
    trigger: "/",
    insertText: `/${name}`,
    behavior: "prompt",
    inline,
  });
  const box = () => document.querySelector("textarea")!;
  const type = (value: string) => {
    fireEvent.focus(box());
    fireEvent.change(box(), { target: { value, selectionStart: value.length } });
  };
  const twoFolders = {
    ...project,
    folders: [
      { ...project.folders[0], id: "f1", path: "/tmp/repo/site" },
      { ...project.folders[0], id: "f2", path: "/tmp/repo/notes", git: false },
    ],
  } as unknown as Project;

  it("completes the chosen provider's skills for the chosen folder", async () => {
    const onListComposerItems = vi.fn<Props["onListComposerItems"]>(async () => [skill("ship")]);
    open({ onListComposerItems });
    await waitFor(() =>
      expect(onListComposerItems).toHaveBeenCalledWith("claude", expect.any(String), "p1", "f1"),
    );

    type("/sh");
    await screen.findByRole("option", { name: /ship/ });
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(box().value).toBe("/ship ");
  });

  it("asks again for another folder and offers nothing of the last one meanwhile", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const onListComposerItems = vi
      .fn<Props["onListComposerItems"]>()
      .mockResolvedValueOnce([skill("ship")])
      .mockReturnValue(new Promise(() => {}));
    open({ projects: [twoFolders], onListComposerItems, onCreate });
    type("then /sh");
    await screen.findByRole("option", { name: /ship/ });

    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^notes/ }));
    await waitFor(() =>
      expect(onListComposerItems).toHaveBeenLastCalledWith("claude", expect.any(String), "p1", "f2"),
    );
    expect(screen.queryByRole("option", { name: /ship/ })).toBeNull();
  });

  it("completes a skill mid-sentence, replacing only its token", async () => {
    open({ onListComposerItems: vi.fn(async () => [skill("review")]) });
    type("do the thing then /");
    await screen.findByRole("option", { name: /review/ });
    type("do the thing then /rev");
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(box().value).toBe("do the thing then /review ");
  });

  it("sends a message ending in a path rather than completing it", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    const onListComposerItems = vi.fn(async () => [skill("review"), skill("closed", false)]);
    open({ onCreate, onListComposerItems, projects: [{ ...project, folders: [] } as unknown as Project] });
    await waitFor(() => expect(onListComposerItems).toHaveBeenCalled());
    await waitFor(() =>
      expect((screen.getByRole("button", { name: "Send" }) as HTMLButtonElement).disabled).toBe(false),
    );
    type("/");
    await screen.findByRole("option", { name: /closed/ });

    // Nothing the harness acts on mid-prompt is called tmp, or closed.
    for (const text of ["then run /closed", "see src/foo/bar", "see https://x/y", "clear out /tmp"]) {
      type(text);
      expect(screen.queryByRole("option")).toBeNull();
    }
    fireEvent.keyDown(box(), { key: "Enter" });
    await waitFor(() =>
      expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ text: "clear out /tmp" }), false),
    );
  });
});

// A signed-out harness is the commonest way a thread refuses to start, and
// the fix is the harness's own login. When the server can run that, the alert
// offers it; when it cannot, only "Check again" remains.
describe("the remembered project", () => {
  const other = {
    ...project,
    id: "p2",
    name: "other",
  } as unknown as Project;

  afterEach(() => localStorage.clear());

  it("prefers the open thread's project over the remembered project", async () => {
    localStorage.setItem("omniplex.lastProject.v1", "p2");
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({ projects: [project, other], activeProjectId: "p1", onCreate });
    expect(chip("Project").textContent).toBe("repo");
    const start = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ projectId: "p1" }), false));
  });

  it("falls back to the remembered project when the active project is unavailable", () => {
    localStorage.setItem("omniplex.lastProject.v1", "p2");
    open({ projects: [project, other], activeProjectId: "deleted" });
    expect(chip("Project").textContent).toBe("other");
  });

  it("opens on the project the last thread was started from", () => {
    localStorage.setItem("omniplex.lastProject.v1", "p2");
    open({ projects: [project, other] });
    expect(chip("Project").textContent).toBe("other");
  });

  it("opens on the open thread's project when the list lands after the draft", () => {
    const view = open({ projects: [], activeProjectId: "p2" });
    view.rerender({ projects: [project, other] });
    expect(chip("Project").textContent).toBe("other");
  });

  it("opens on the first project when the remembered one is gone", () => {
    localStorage.setItem("omniplex.lastProject.v1", "deleted");
    open({ projects: [project, other] });
    expect(chip("Project").textContent).toBe("repo");
  });

  it("remembers only a thread that actually started", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {
      throw new Error("no");
    });
    open({ projects: [project, other], onCreate });

    const start = await waitFor(() => screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(screen.getByText("no")).toBeTruthy());
    expect(localStorage.getItem("omniplex.lastProject.v1")).toBeNull();

    cleanup();
    const ok = vi.fn(async (_input: NewThreadInput) => {});
    open({ projects: [project, other], onCreate: ok });
    const go = await waitFor(() => screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect((go as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(go);
    await waitFor(() => expect(ok).toHaveBeenCalled());
    expect(localStorage.getItem("omniplex.lastProject.v1")).toBe("p1");
  });
});

describe("a signed-out harness", () => {
  const signedOut = {
    ...harness,
    availability: {
      state: "unavailable",
      reason: "Claude is not signed in.",
      remedy: [{ text: "Sign in", command: "claude auth login", action: "login" }],
    },
    instances: [
      {
        id: "claude",
        driver: "claude",
        displayName: "Claude Code",
        enabled: true,
        availability: {
          state: "unavailable",
          reason: "Claude is not signed in.",
          remedy: [{ text: "Sign in", command: "claude auth login", action: "login" }],
        },
        models: [],
      },
    ],
  } as unknown as HarnessMeta;

  it("offers the harness's own sign-in", async () => {
    const onLogin = vi.fn();
    open({ harnesses: [signedOut], onLogin });
    expect(screen.getByText("Claude is not signed in.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(onLogin).toHaveBeenCalledWith("claude");
  });

  it("does not offer a sign-in the server cannot run", () => {
    open({ harnesses: [signedOut] });
    expect(screen.queryByRole("button", { name: /sign in/i })).toBeNull();
    expect(screen.getByRole("button", { name: /check again/i })).toBeTruthy();
  });
});


describe("remembered thread choices", () => {
  const ready = { state: "ready" } as const;
  const agents = ["claude", "codex"].map((id) => ({
    id,
    name: id,
    availability: ready,
    permissionModes: [
      { id: "ask", label: "Ask", default: true },
      { id: `${id}-bypass`, label: "Bypass" },
    ],
    instances: [
      {
        id,
        driver: id,
        displayName: id,
        enabled: true,
        availability: ready,
        models: [
          {
            id: `${id}-basic`,
            label: `${id} Basic`,
            default: true,
            efforts: ["low", "high"],
          },
          {
            id: `${id}-advanced`,
            label: `${id} Advanced`,
            efforts: ["low", "high", "ultra"],
          },
        ],
      },
    ],
  })) as unknown as HarnessMeta[];

  async function pickModel(label: string) {
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.change(screen.getByPlaceholderText(/Search models and accounts/), {
      target: { value: label },
    });
    fireEvent.click(await screen.findByRole("option", { name: new RegExp(label) }));
  }
  async function switchHarness(id: string) {
    await waitFor(() =>
      expect(screen.queryByPlaceholderText(/Search models and accounts/)).toBeNull(),
    );
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.click(await screen.findByRole("button", { name: new RegExp(id + "$", "i") }));
    fireEvent.keyDown(screen.getByPlaceholderText(/Search models and accounts/), { key: "Escape" });
  }
  async function bypass() {
    menu("Permissions");
    const advanced = await screen.findByRole("menuitem", { name: "Advanced" });
    if (advanced.getAttribute("aria-expanded") !== "true") fireEvent.click(advanced);
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^Bypass/ }));
  }
  async function highEffort() {
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.click(screen.getByRole("combobox", { name: "Reasoning effort" }));
    fireEvent.click(await screen.findByRole("option", { name: "High" }));
  }

  it("restores choices when switching away and back, cancelling and reopening, then starting", async () => {
    const onCreate = vi.fn(async () => {});
    const props = { harnesses: agents, onCreate };
    open(props);
    await pickModel("codex Advanced");
    await bypass();
    await highEffort();
    await switchHarness("claude");
    expect(chip("Permissions").textContent).toBe("Ask");
    await pickModel("claude Advanced");
    await bypass();
    await switchHarness("codex");
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain(
      "codex Advanced",
    );
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain(
      "High",
    );
    expect(chip("Permissions").textContent).toBe("Bypass");
    await switchHarness("claude");
    cleanup();
    open(props);
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain(
      "claude Advanced",
    );
    expect(chip("Permissions").textContent).toBe("Bypass");
    await switchHarness("codex");
    const start = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() =>
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          harness: "codex",
          model: "codex-advanced",
          mode: "codex-bypass",
          effort: "high",
        }), false,
      ),
    );
  });

  it("keeps explicit Auto over a project seed and keeps another project's choices separate", async () => {
    const seeded = {
      ...project,
      defaults: {
        ...project.defaults, harness: "codex",
        harnesses: { codex: { effort: "high" } },
      },
    } as Project;
    const other = { ...project, id: "p2", name: "other" };
    const props = { projects: [seeded, other], harnesses: agents };
    open(props);
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.click(screen.getByRole("combobox", { name: "Reasoning effort" }));
    fireEvent.click(await screen.findByRole("option", { name: /Auto/ }));
    await bypass();
    menu("Project");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: "other" }));
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain("claude Basic");
    expect(chip("Permissions").textContent).toBe("Ask");
    menu("Project");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: "repo" }));
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain("Auto");
    expect(chip("Permissions").textContent).toBe("Bypass");
    cleanup();
    open(props);
    expect(screen.getByRole("combobox", { name: "Harness and model" }).textContent).toContain("Auto");
  });


  it("drops to the strongest effort the newly picked model allows", async () => {
    const onCreate = vi.fn(async () => {});
    open({ harnesses: agents, onCreate });
    await pickModel("codex Advanced");
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.click(screen.getByRole("combobox", { name: "Reasoning effort" }));
    fireEvent.click(await screen.findByRole("option", { name: /Ultra/i }));
    await pickModel("codex Basic");
    await bypass();
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((send as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(send);
    await waitFor(() =>
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({ model: "codex-basic", effort: "high" }), false,
      ),
    );
  });


});

describe("permission levels", () => {
  const claude = {
    ...harness,
    permissionModes: [
      { id: "default", label: "Manual", default: true, level: "ask" },
      { id: "plan", label: "Plan" },
      { id: "acceptEdits", label: "Accept edits", level: "edits" },
      { id: "bypassPermissions", label: "Bypass", level: "all" },
    ],
  } as unknown as HarnessMeta;
  const plain = { ...project, folders: [] } as unknown as Project;

  const send = async () => {
    const button = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(button);
  };

  it("offers the three levels and sends the harness's mode for the one picked", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({ projects: [plain], harnesses: [claude], onCreate });
    expect(chip("Permissions").textContent).toBe("Ask first");
    menu("Permissions");
    const levels = await screen.findAllByRole("menuitemradio");
    expect(levels.map((l) => l.textContent?.split(":")[0])).toEqual([
      "Ask before changing anythingClaude Code",
      "Edit files, ask before commandsClaude Code",
      "Do everythingClaude Code",
    ]);
    fireEvent.click(screen.getByRole("menuitemradio", { name: /^Do everything/ }));
    expect(chip("Permissions").textContent).toBe("Do everything");
    await send();
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ mode: "bypassPermissions" });
  });

  it("keeps a mode outside the levels under Advanced", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({ projects: [plain], harnesses: [claude], onCreate });
    menu("Permissions");
    expect(screen.queryByRole("menuitemradio", { name: /^Plan/ })).toBeNull();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Advanced" }));
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^Plan/ }));
    expect(chip("Permissions").textContent).toBe("Plan");
    await send();
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ mode: "plan" });
  });

  it("starts a project's first thread on the default level from settings", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({
      projects: [plain],
      harnesses: [claude],
      userConfig: { version: 1, defaultLevel: "edits" },
      onCreate,
    });
    expect(chip("Permissions").textContent).toBe("Edit files");
    await send();
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({ mode: "acceptEdits" });
  });

  it("lets the project's own last choice win over the default from settings", async () => {
    open({ projects: [plain], harnesses: [claude], userConfig: { version: 1, defaultLevel: "edits" } });
    menu("Permissions");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^Ask before/ }));
    cleanup();
    open({ projects: [plain], harnesses: [claude], userConfig: { version: 1, defaultLevel: "edits" } });
    expect(chip("Permissions").textContent).toBe("Ask first");
  });
});

describe("the default model from settings", () => {
  const ready = { state: "ready" } as const;
  const agents = ["claude", "codex"].map((id) => ({
    id,
    name: id,
    availability: ready,
    permissionModes: [],
    instances: [
      {
        id,
        driver: id,
        displayName: id,
        enabled: true,
        availability: ready,
        models: [
          { id: `${id}-basic`, label: `${id} Basic`, default: true },
          { id: `${id}-advanced`, label: `${id} Advanced` },
        ],
      },
    ],
  })) as unknown as HarnessMeta[];
  const fresh = {
    ...project,
    defaults: { workspace: "local", harnesses: {} },
    folders: [],
  } as unknown as Project;

  it("starts a project with no habit of its own on the default model", async () => {
    const onCreate = vi.fn(async (_input: NewThreadInput) => {});
    open({
      projects: [fresh],
      harnesses: agents,
      userConfig: { version: 1, defaultInstance: "codex", defaultModel: "codex-advanced" },
      onCreate,
    });
    const button = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(button);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      harness: "codex",
      instance: "codex",
      model: "codex-advanced",
    });
  });
});
