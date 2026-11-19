// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentApi } from "~/lib/agentApi";
import type { Agent, HarnessMeta, Project } from "~/protocol";
import { render, viewport, wrap } from "~/test/harness";

import { AgentPage } from "./AgentPage";
import { AgentSettingsTab } from "./AgentSettingsTab";
import { NewAgent } from "./NewAgent";

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), info: vi.fn(), warning: vi.fn() }));
vi.mock("~/lib/toast", () => ({ toast }));
// The tabs are slice 4b's, tested there; here they only have to be told which agent.
vi.mock("./PromptTab", () => ({ PromptTab: ({ agent }: { agent: Agent }) => <p>prompt of {agent.name}</p> }));
vi.mock("./CompactionTab", () => ({ CompactionTab: () => <p>compaction tab</p> }));
vi.mock("./LogTab", () => ({ LogTab: () => <p>log tab</p> }));

const agent = (over: Partial<Agent> = {}): Agent => ({
  id: "a1",
  projectId: "p1",
  name: "backlog",
  harness: "claude",
  model: "opus",
  effort: "high",
  workspace: "local",
  threadId: "t1",
  promptRev: 2,
  compactRev: 1,
  epoch: 3,
  createdAt: 0,
  ...over,
});

const project = {
  id: "p1",
  name: "omniplex",
  defaults: { harness: "claude", harnesses: {} },
  folders: [{ id: "f1", path: "/code/omniplex", git: true }],
} as unknown as Project;
const other = { ...project, id: "p2", name: "other" } as Project;

const harnesses = [
  {
    id: "claude",
    name: "Claude Code",
    models: [
      { id: "opus", label: "Opus", default: true, efforts: ["low", "high", "max"] },
      { id: "haiku", label: "Haiku" },
    ],
    permissionModes: [
      { id: "default", label: "Ask", level: "ask", default: true },
      { id: "bypassPermissions", label: "Bypass", level: "all", description: "Never ask and no sandbox." },
    ],
    availability: { state: "ready" },
    instances: [{ id: "claude", driver: "claude", displayName: "Claude Code", enabled: true, availability: { state: "ready" } }],
  },
] as unknown as HarnessMeta[];

function mockApi(over: Partial<AgentApi> = {}): AgentApi {
  return {
    create: vi.fn(async () => ({ agent: agent({ id: "a9", threadId: undefined }) })),
    save: vi.fn(async () => ({ agent: agent() })),
    archive: vi.fn(async () => ({})),
    preamble: vi.fn(async () => "PREAMBLE"),
    ...over,
  } as unknown as AgentApi;
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});
afterEach(() => localStorage.clear());

describe("AgentSettingsTab", () => {
  function open(a = agent(), api = mockApi()) {
    const props = {
      agent: a,
      api,
      projects: [project],
      harnesses,
      onOpenTab: vi.fn(),
      onNewThread: vi.fn(),
      onArchive: vi.fn(),
    };
    return { ...render(<AgentSettingsTab {...props} />), props, api };
  }

  it("saves a new name on Enter, trimmed, and says so", async () => {
    const { api } = open();
    const name = screen.getByRole("textbox", { name: "Name" });
    fireEvent.change(name, { target: { value: "  triage " } });
    fireEvent.keyDown(name, { key: "Enter" });
    expect(api.save).toHaveBeenCalledWith("a1", { name: "triage" });
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved"));
  });

  it("puts an emptied name back rather than saving it", () => {
    const { api } = open();
    const name = screen.getByRole("textbox", { name: "Name" });
    fireEvent.change(name, { target: { value: "  " } });
    fireEvent.blur(name);
    expect(api.save).not.toHaveBeenCalled();
    expect((name as HTMLInputElement).value).toBe("backlog");
  });

  it("saves the effort the model offers, and shows it before the server answers", () => {
    const { api } = open();
    fireEvent.click(screen.getByRole("radio", { name: "Max" }));
    expect(api.save).toHaveBeenCalledWith("a1", { effort: "max" });
    expect(screen.getByRole("radio", { name: "Max" }).getAttribute("aria-checked")).toBe("true");
  });

  it("goes back to the saved value when a save fails", async () => {
    const { api } = open(agent(), mockApi({ save: vi.fn(async () => Promise.reject(new Error("nope"))) }));
    fireEvent.click(screen.getByRole("radio", { name: "Low" }));
    expect(api.save).toHaveBeenCalled();
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("nope"));
    expect(screen.getByRole("radio", { name: "High" }).getAttribute("aria-checked")).toBe("true");
  });

  it("saves the first message when the box is left, only if it changed", () => {
    const { api } = open(agent({ kickoff: "Go." }));
    const box = screen.getByRole("textbox", { name: "First message" });
    fireEvent.blur(box);
    expect(api.save).not.toHaveBeenCalled();
    fireEvent.change(box, { target: { value: "Go now." } });
    fireEvent.blur(box);
    expect(api.save).toHaveBeenCalledWith("a1", { kickoff: "Go now." });
  });

  it("sends compact-at to its tab and archive to the page", () => {
    const { props } = open();
    fireEvent.click(screen.getByRole("button", { name: "Change in Compaction" }));
    expect(props.onOpenTab).toHaveBeenCalledWith("compact");
    fireEvent.click(screen.getByRole("button", { name: "Archive" }));
    expect(props.onArchive).toHaveBeenCalled();
  });
});

describe("NewAgent", () => {
  function open(over: Partial<React.ComponentProps<typeof NewAgent>> = {}) {
    const props = {
      projects: [project, other],
      harnesses,
      userConfig: null,
      projectId: "p1",
      api: mockApi(),
      sidebarOpen: true,
      onShowSidebar: vi.fn(),
      onCancel: vi.fn(),
      onCreated: vi.fn(),
      ...over,
    };
    return { ...render(<NewAgent {...props} />), props };
  }
  const type = (label: string, value: string) =>
    fireEvent.change(screen.getByRole("textbox", { name: label }), { target: { value } });

  it("cannot be created until it has a name and a prompt", () => {
    open();
    const create = screen.getByRole("button", { name: "Create agent" });
    expect(create).toHaveProperty("disabled", true);
    type("Name", "triage");
    expect(create).toHaveProperty("disabled", true);
    type("System prompt", "# Triage");
    expect(create).toHaveProperty("disabled", false);
  });

  it("creates the agent alone with no first message, and hands it back", async () => {
    const { props } = open();
    type("Name", " triage ");
    type("System prompt", "# Triage");
    fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(props.onCreated).toHaveBeenCalled());
    expect(props.api.create).toHaveBeenCalledWith(
      expect.objectContaining({
        projectId: "p1",
        name: "triage",
        harness: "claude",
        instance: "claude",
        model: "opus",
        mode: "bypassPermissions",
        workspace: "local",
        prompt: "# Triage",
        start: false,
      }),
    );
    const args = vi.mocked(props.api.create).mock.calls[0][0];
    expect(args.kickoff).toBeUndefined();
    expect(args.compactAt).toBeUndefined();
    expect(props.onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: "a9" }), undefined);
  });

  it("starts the thread with the first message, and hands back the thread", async () => {
    const api = mockApi({ create: vi.fn(async () => ({ agent: agent({ id: "a9" }), threadId: "t9" })) });
    const { props } = open({ api });
    type("Name", "triage");
    type("System prompt", "# Triage");
    type("First message", "  Poll now. ");
    fireEvent.click(screen.getByRole("radio", { name: "120k" }));
    fireEvent.click(screen.getByRole("radio", { name: /^New worktree/ }));
    fireEvent.click(screen.getByRole("button", { name: "Create and start" }));
    await waitFor(() => expect(props.onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: "a9" }), "t9"));
    expect(api.create).toHaveBeenCalledWith(
      expect.objectContaining({ kickoff: "Poll now.", start: true, compactAt: 120_000, workspace: "managed" }),
    );
  });

  it("keeps the form open with the error when creating fails", async () => {
    const api = mockApi({ create: vi.fn(async () => Promise.reject(new Error("name taken"))) });
    const { props } = open({ api });
    type("Name", "triage");
    type("System prompt", "# Triage");
    fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", "name taken");
    expect(props.onCreated).not.toHaveBeenCalled();
  });

  it("keeps what was typed across a reload, until it is created", async () => {
    const first = open();
    type("Name", "triage");
    type("System prompt", "# Triage");
    first.unmount();

    const second = open();
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveProperty("value", "triage");
    fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(second.props.onCreated).toHaveBeenCalled());
    second.unmount();

    open();
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveProperty("value", "");
  });

  it("moves the draft to the project whose + was pressed", async () => {
    const { rerender, props } = open();
    type("Name", "triage");
    type("System prompt", "# Triage");
    rerender(wrap(<NewAgent {...props} projectId="p2" />));
    fireEvent.click(screen.getByRole("button", { name: "Create agent" }));
    await waitFor(() => expect(props.api.create).toHaveBeenCalledWith(expect.objectContaining({ projectId: "p2", name: "triage" })));
  });

  it("shows the fixed preamble on asking", async () => {
    const { props } = open();
    fireEvent.click(screen.getByRole("button", { name: "Read the preamble" }));
    expect(await screen.findByText("PREAMBLE")).toBeTruthy();
    expect(props.api.preamble).toHaveBeenCalled();
  });
});

describe("AgentPage", () => {
  function open(over: Partial<React.ComponentProps<typeof AgentPage>> = {}) {
    const props = {
      agent: agent(),
      loaded: true,
      tab: "prompt" as const,
      api: mockApi(),
      projects: [project],
      harnesses,
      sidebarOpen: true,
      onShowSidebar: vi.fn(),
      onBack: vi.fn(),
      onOpenThread: vi.fn(),
      onTab: vi.fn(),
      onAction: vi.fn(),
      ...over,
    };
    return { ...render(<AgentPage {...props} />), props };
  }

  it("shows the tab it is on, and asks for another", () => {
    const { props } = open();
    expect(screen.getByText("prompt of backlog")).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "Log" }));
    expect(props.onTab).toHaveBeenCalledWith("log");
  });

  it("opens the live thread, or starts one when there is none", () => {
    const live = open();
    fireEvent.click(screen.getByRole("button", { name: "Open thread" }));
    expect(live.props.onOpenThread).toHaveBeenCalledWith("t1");
    live.unmount();

    const idle = open({ agent: agent({ threadId: undefined }) });
    fireEvent.click(screen.getByRole("button", { name: "Start thread" }));
    expect(idle.props.onAction).toHaveBeenCalledWith(expect.objectContaining({ id: "a1" }), "newThread");
  });

  it("offers restart only with a live thread, and archive always", async () => {
    viewport("desktop");
    const { props } = open({ agent: agent({ threadId: undefined }) });
    await act(async () => {
      fireEvent.pointerDown(screen.getByRole("button", { name: "More" }), { button: 0, ctrlKey: false });
    });
    expect(screen.queryByRole("menuitem", { name: /Restart thread/ })).toBeNull();
    fireEvent.click(await screen.findByRole("menuitem", { name: /Archive agent/ }));
    expect(props.onAction).toHaveBeenCalledWith(expect.objectContaining({ id: "a1" }), "archive");
  });

  it("says the agent is gone once the list is in, and waits before then", () => {
    const gone = open({ agent: undefined });
    expect(screen.getByText("No agent here")).toBeTruthy();
    gone.unmount();
    open({ agent: undefined, loaded: false });
    expect(screen.queryByText("No agent here")).toBeNull();
  });
});
