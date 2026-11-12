// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { createDraftStore } from "~/app/draftStore";
import { NEW_THREAD } from "~/app/threadKeys";
import { render, wrap } from "~/test/harness";
import type { ComposerHandle } from "./Composer";
import { ThreadDraft, type NewThreadInput } from "./ThreadDraft";
import type { ComposerItem, HarnessMeta, Project, Workspace } from "~/protocol";

const folder = (id: string, path: string, git: boolean) => ({
  id,
  path,
  git,
  copiesDir: ".worktrees",
  provisionTimeoutSeconds: 1800,
  deprovisionTimeoutSeconds: 600,
});
const projectWith = (id: string, name: string, workspace: string, folders: unknown[]) =>
  ({
    id,
    name,
    defaults: { harness: "claude", harnesses: {}, workspace },
    folders,
  }) as unknown as Project;

const bowerbird = projectWith("p1", "bowerbird", "managed", [
  folder("f1", "/tmp/bowerbird/site", true),
  folder("f2", "/tmp/bowerbird/notes", false),
]);
// Defaults to the main checkout, so a worktree only comes from the preset.
const garden = projectWith("p2", "garden", "local", [
  folder("g1", "/tmp/garden/app", true),
  folder("g2", "/tmp/garden/docs", false),
]);
const side = { path: "/tmp/garden/.worktrees/side", branch: "issue/1-side" } as Workspace;

const harness = {
  id: "claude",
  name: "Claude Code",
  models: [],
  permissionModes: [],
  availability: { state: "ready" },
  instances: [
    { id: "claude", driver: "claude", displayName: "Claude Code", enabled: true, availability: { state: "ready" } },
  ],
} as unknown as HarnessMeta;

type Props = React.ComponentProps<typeof ThreadDraft>;

function open(over: Partial<Props> = {}) {
  const drafts = createDraftStore();
  drafts.set(NEW_THREAD, "hi");
  const base: Props = {
    drafts,
    projects: [bowerbird, garden],
    harnesses: [harness],
    userConfig: null,
    status: "online",
    onStart: vi.fn(async () => {}),
    onListWorkspaces: vi.fn(async () => [side]),
    onListIssues: vi.fn(async () => ({ issues: [], issuesError: "" })),
    onListComposerItems: vi.fn(async () => [] as ComposerItem[]),
    onAddProject: vi.fn(),
    onSettings: vi.fn(),
    onRecheck: vi.fn(),
    ...over,
  };
  const view = render(<ThreadDraft {...base} />);
  return {
    rerender: (next: Partial<Props>) => view.rerender(wrap(<ThreadDraft {...base} {...next} />)),
  };
}

const chip = (label: string) => screen.getByRole("button", { name: new RegExp(`^${label}:`) });
const menu = (label: string) => fireEvent.pointerDown(chip(label), { button: 0, ctrlKey: false });
const send = () => screen.getByRole("button", { name: "Send" }) as HTMLButtonElement;
async function start(onStart: ReturnType<typeof vi.fn>) {
  await waitFor(() => expect(send().disabled).toBe(false));
  fireEvent.click(send());
  await waitFor(() => expect(onStart).toHaveBeenCalled());
  return onStart.mock.calls[0][0] as NewThreadInput;
}

beforeEach(() => localStorage.clear());

beforeAll(() => {
  const proto = window.HTMLElement.prototype;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
});

describe("a draft asked for in a given place", () => {
  it("opens on the project, folder and existing worktree asked for, and starts there", async () => {
    const onStart = vi.fn(async (_input: NewThreadInput) => {});
    const onListWorkspaces = vi.fn(async () => [side]);
    open({
      onStart,
      onListWorkspaces,
      preset: { projectId: "p2", folderId: "g1", kind: "attach", attachPath: side.path },
    });

    expect(chip("Project").textContent).toBe("garden");
    expect(chip("Scope").textContent).toBe("app");
    await waitFor(() => expect(chip("Git").textContent).toBe("Worktree: issue/1-side"));
    expect(onListWorkspaces).toHaveBeenCalledWith("p2", "g1");
    expect(await start(onStart)).toMatchObject({
      projectId: "p2",
      folderId: "g1",
      workspace: "",
      workspacePath: side.path,
    });
  });

  it("waits for a pick when the worktree asked for is gone once the list lands", async () => {
    const onStart = vi.fn(async (_input: NewThreadInput) => {});
    open({
      onStart,
      onListWorkspaces: vi.fn(async () => [] as Workspace[]),
      preset: { projectId: "p2", folderId: "g1", kind: "attach", attachPath: side.path },
    });

    await waitFor(() =>
      expect(screen.getByPlaceholderText("Pick a worktree to continue on")).toBeTruthy(),
    );
    expect(chip("Git").textContent).toBe("Existing worktree");
    expect(send().disabled).toBe(true);
  });

  it("starts in a new worktree when asked, over a project that defaults to the main checkout", async () => {
    const onStart = vi.fn(async (_input: NewThreadInput) => {});
    open({ onStart, preset: { projectId: "p2", folderId: "g1", kind: "branch" } });

    expect(await start(onStart)).toMatchObject({
      projectId: "p2",
      folderId: "g1",
      workspace: "managed",
      workspacePath: "",
    });
  });

  it("starts in the main checkout when asked, over a project that defaults to worktrees", async () => {
    const onStart = vi.fn(async (_input: NewThreadInput) => {});
    open({ onStart, preset: { projectId: "p1", folderId: "f1", kind: "main" } });

    expect(await start(onStart)).toMatchObject({ folderId: "f1", workspace: "local" });
  });

  it("drops the worktree asked for once another folder is picked", async () => {
    open({ preset: { projectId: "p2", folderId: "g1", kind: "attach", attachPath: side.path } });
    await waitFor(() => expect(chip("Git").textContent).toBe("Worktree: issue/1-side"));

    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^docs/ }));
    menu("Scope");
    fireEvent.click(await screen.findByRole("menuitemradio", { name: /^app/ }));

    // Back on the same folder, but as a fresh choice: the project's default.
    await waitFor(() => expect(chip("Git").textContent).toBe("Main checkout"));
  });

  it("opens on the remembered folder when the request names none", async () => {
    localStorage.setItem(
      "omniplex.threadChoices.v1",
      JSON.stringify({ p1: { harness: "claude", byHarness: {}, folderId: "f2" } }),
    );
    open({ preset: { projectId: "p1" } });
    expect(chip("Scope").textContent).toBe("notes");
  });

  it("opens on the folder asked for over the remembered one, the whole project included", () => {
    localStorage.setItem(
      "omniplex.threadChoices.v1",
      JSON.stringify({ p1: { harness: "claude", byHarness: {}, folderId: "f2" } }),
    );
    open({ preset: { projectId: "p1", folderId: "" } });
    expect(chip("Scope").textContent).toBe("Everything");
  });

  it("applies the request once the project list lands after the draft", async () => {
    const onStart = vi.fn(async (_input: NewThreadInput) => {});
    const preset = { projectId: "p2", folderId: "g1", kind: "attach", attachPath: side.path } as const;
    const { rerender } = open({ onStart, projects: [], preset });
    rerender({ onStart, preset, projects: [bowerbird, garden] });

    expect(chip("Project").textContent).toBe("garden");
    await waitFor(() => expect(chip("Git").textContent).toBe("Worktree: issue/1-side"));
    expect(await start(onStart)).toMatchObject({ workspacePath: side.path });
  });
});

describe("the cursor on opening", () => {
  it("lands in the composer when the request asks for focus", async () => {
    open({ preset: { projectId: "p1", focus: true } });
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Message" })),
    );
  });

  it("stays put otherwise", () => {
    open({ preset: { projectId: "p1" } });
    expect(document.activeElement).not.toBe(screen.getByRole("textbox", { name: "Message" }));
  });

  it("hands the composer to the caller's ref", async () => {
    const composerRef = createRef<ComposerHandle>();
    open({ composerRef });
    composerRef.current!.focusEnd();
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "Message" })),
    );
  });
});
