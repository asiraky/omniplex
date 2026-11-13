// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { createDraftStore } from "~/app/draftStore";
import { NEW_THREAD } from "~/app/threadKeys";
import type { Attachment } from "~/lib/attachments";
import { render } from "~/test/harness";
import { ThreadDraft, type CompareRun, type NewThreadInput } from "./ThreadDraft";
import type { ComposerItem, HarnessMeta, Project, Workspace } from "~/protocol";

const ready = { state: "ready" };

// Last used in the main checkout, so a forced worktree is visible.
const project = {
  id: "p1",
  name: "repo",
  defaults: { harness: "claude", harnesses: {}, workspace: "local" },
  folders: [
    {
      id: "f1",
      path: "/tmp/repo",
      git: true,
      copiesDir: ".worktrees",
      provisionTimeoutSeconds: 1800,
      deprovisionTimeoutSeconds: 600,
    },
  ],
} as unknown as Project;

const claude = {
  id: "claude",
  name: "Claude Code",
  models: [],
  permissionModes: [],
  availability: ready,
  instances: [
    {
      id: "claude",
      driver: "claude",
      displayName: "Claude Code",
      enabled: true,
      availability: ready,
      models: [
        { id: "opus", label: "Opus 5", version: "Opus 5", default: true },
        { id: "sonnet", label: "Sonnet 5", version: "Sonnet 5" },
        { id: "haiku", label: "Haiku 5", version: "Haiku 5" },
      ],
    },
  ],
} as unknown as HarnessMeta;

const codex = {
  id: "codex",
  name: "Codex",
  models: [],
  permissionModes: [],
  availability: ready,
  instances: [
    {
      id: "codex",
      driver: "codex",
      displayName: "Codex",
      enabled: true,
      availability: ready,
      models: [{ id: "gpt-5.6", label: "GPT-5.6", version: "5.6", default: true }],
    },
  ],
} as unknown as HarnessMeta;

type Props = React.ComponentProps<typeof ThreadDraft>;

function open(over: Partial<Props> = {}) {
  const drafts = createDraftStore();
  drafts.set(NEW_THREAD, "fix the login");
  const props: Props = {
    drafts,
    projects: [project],
    harnesses: [claude, codex],
    userConfig: null,
    status: "online",
    onStart: vi.fn(async () => {}),
    onCompare: vi.fn(async () => {}),
    onListWorkspaces: vi.fn(async () => [] as Workspace[]),
    onListIssues: vi.fn(async () => ({ issues: [], issuesError: "" })),
    onListComposerItems: vi.fn(async () => [] as ComposerItem[]),
    onAddProject: vi.fn(),
    onSettings: vi.fn(),
    onRecheck: vi.fn(),
    ...over,
  };
  render(<ThreadDraft {...props} />);
  return props;
}

const compareChip = () => screen.queryByRole("button", { name: "Compare with another model" });

/** Adds a model through the Compare chip's picker, by its row label. */
function add(label: string) {
  if (!document.querySelector("[cmdk-list]")) fireEvent.click(compareChip()!);
  const list = within(document.querySelector("[cmdk-list]") as HTMLElement);
  const hit = list
    .getAllByText(label)
    .map((el) => el.closest("[data-slot='command-item']"))
    .find(Boolean) as HTMLElement;
  fireEvent.click(hit);
}

/** Browses the picker over to another account's models. */
function browse(account: string) {
  fireEvent.click(compareChip()!);
  const picker = within(document.querySelector("[cmdk-root]") as HTMLElement);
  fireEvent.click(picker.getByRole("button", { name: new RegExp(`${account}$`) }));
}

async function send() {
  const button = screen.getByRole("button", { name: "Send" }) as HTMLButtonElement;
  await waitFor(() => expect(button.disabled).toBe(false));
  fireEvent.click(button);
}

const gitChip = () => screen.getByRole("button", { name: /^Git/ });

beforeEach(() => localStorage.clear());

beforeAll(() => {
  const proto = window.HTMLElement.prototype;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
});

describe("comparing models from the draft", () => {
  it("sends one run per model, each in its own new worktree and titled with its model", async () => {
    const props = open();
    await waitFor(() => expect(gitChip().textContent).toContain("Main checkout"));
    add("Sonnet 5");

    expect(gitChip().textContent).toContain("New worktree");
    await waitFor(() => expect(document.querySelector("[cmdk-list]")).toBeNull());
    fireEvent.click(gitChip());
    const main = await screen.findByRole("radio", { name: /Main checkout/ });
    expect((main as HTMLButtonElement).disabled).toBe(true);
    await send();
    await waitFor(() => expect(props.onCompare).toHaveBeenCalled());
    const runs = vi.mocked(props.onCompare!).mock.calls[0][0] as CompareRun[];
    expect(runs.map((r) => r.label)).toEqual(["Opus 5", "Sonnet 5"]);
    for (const { input } of runs) {
      expect(input).toMatchObject({
        projectId: "p1",
        folderId: "f1",
        workspace: "managed",
        workspacePath: "",
        branch: "",
        text: "fix the login",
        agentSettingsExplicit: true,
      });
    }
    expect(runs[0].input).toMatchObject({ model: "opus", title: "fix the login · Opus 5" });
    expect(runs[1].input).toMatchObject({ model: "sonnet", title: "fix the login · Sonnet 5" });
    expect(props.onStart).not.toHaveBeenCalled();
  });

  it("runs a model under another harness with that harness, not the draft's", async () => {
    const props = open();
    await waitFor(() => compareChip());
    browse("Codex");
    add("GPT-5.6");
    await send();
    await waitFor(() => expect(props.onCompare).toHaveBeenCalled());
    const runs = vi.mocked(props.onCompare!).mock.calls[0][0] as CompareRun[];
    expect(runs[1].input).toMatchObject({ harness: "codex", instance: "codex", model: "gpt-5.6" });
  });

  it("names the account on chips and titles when the same model runs on two", async () => {
    const work = { ...claude.instances![0], id: "claude-work", displayName: "Work" };
    const twoAccounts = { ...claude, instances: [...claude.instances!, work] } as HarnessMeta;
    const props = open({ harnesses: [twoAccounts, codex] });
    await waitFor(() => compareChip());
    browse("Work");
    add("Opus 5");
    expect(screen.getByRole("button", { name: "Stop comparing with Opus 5 (Work)" }).textContent).toContain(
      "Opus 5 (Work)",
    );
    await send();
    await waitFor(() => expect(props.onCompare).toHaveBeenCalled());
    const runs = vi.mocked(props.onCompare!).mock.calls[0][0] as CompareRun[];
    expect(runs.map((r) => r.input.title)).toEqual([
      "fix the login · Opus 5 (Claude Code)",
      "fix the login · Opus 5 (Work)",
    ]);
  });

  it("adds a model once, never the draft's own, and stops offering more at three", async () => {
    open();
    await waitFor(() => compareChip());
    add("Opus 5");
    expect(screen.queryByRole("button", { name: /^Stop comparing/ })).toBeNull();
    add("Sonnet 5");
    add("Sonnet 5");
    expect(screen.getAllByRole("button", { name: /^Stop comparing/ })).toHaveLength(1);

    add("Haiku 5");
    browse("Codex");
    add("GPT-5.6");
    expect(screen.getAllByRole("button", { name: /^Stop comparing/ })).toHaveLength(3);
    expect(compareChip()).toBeNull();
  });

  it("goes back to a single thread where it was once the models are removed", async () => {
    const props = open();
    await waitFor(() => compareChip());
    add("Sonnet 5");
    fireEvent.click(screen.getByRole("button", { name: "Stop comparing with Sonnet 5" }));

    expect(gitChip().textContent).toContain("Main checkout");
    await send();
    await waitFor(() => expect(props.onStart).toHaveBeenCalled());
    const input = vi.mocked(props.onStart).mock.calls[0][0] as NewThreadInput;
    expect(input).toMatchObject({ workspace: "local", model: "opus" });
    expect(props.onCompare).not.toHaveBeenCalled();
  });

  it("holds send while files are attached", async () => {
    const file = { key: "a", kind: "file", status: "ready", name: "notes.txt" } as unknown as Attachment;
    open({ attachments: [file] });
    await waitFor(() => compareChip());
    add("Sonnet 5");
    expect((screen.getByRole("button", { name: "Send" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("will not schedule while comparing, and will once the models are removed", async () => {
    open();
    await waitFor(() => compareChip());
    add("Sonnet 5");
    const openOptions = () =>
      fireEvent.pointerDown(screen.getByRole("button", { name: "More send options" }), {
        button: 0,
        ctrlKey: false,
      });
    openOptions();
    const item = await screen.findByRole("menuitem", { name: /Schedule send/ });
    expect(item.getAttribute("aria-disabled")).toBe("true");
    fireEvent.keyDown(item, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menuitem")).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: "Stop comparing with Sonnet 5" }));
    openOptions();
    const again = await screen.findByRole("menuitem", { name: /Schedule send/ });
    expect(again.getAttribute("aria-disabled")).toBeNull();
  });

  it("puts the message back when no thread started", async () => {
    open({
      onCompare: vi.fn(async () => {
        throw new Error("none started");
      }),
    });
    await waitFor(() => compareChip());
    add("Sonnet 5");
    await send();
    await waitFor(() => screen.getByText("none started"));
    expect(document.querySelector("textarea")!.value).toBe("fix the login");
  });

  it("offers no comparing outside a git folder", () => {
    const plain = {
      ...project,
      folders: [{ ...project.folders[0], git: false }],
    } as unknown as Project;
    open({ projects: [plain] });
    expect(compareChip()).toBeNull();
  });
});
