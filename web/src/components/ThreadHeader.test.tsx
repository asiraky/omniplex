// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import type { PanelControls } from "~/app/usePanel";
import type { ThreadHarness } from "~/app/useThreadHarness";
import { render, viewport } from "~/test/harness";
import { AgentNavContext, type AgentNav } from "~/lib/agentNav";
import type { Agent, ThreadMeta, ThreadState } from "~/protocol";

import { ThreadHeader } from "./ThreadHeader";

afterEach(() => vi.unstubAllGlobals());

const state = { threadId: "a", seq: 1, title: "One", jobs: [], items: [] } as unknown as ThreadState;
const meta = { id: "a", title: "One", headSeq: 1 } as ThreadMeta;

function renderHeader({
  isDesktop,
  activeId = "a",
  onFind = () => {},
  harness = {},
  parent,
  onOpenThread,
  nav = { openAgent: () => {}, openThread: () => {} },
}: {
  isDesktop: boolean;
  activeId?: string | null;
  onFind?: () => void;
  harness?: Partial<ThreadHarness>;
  parent?: ThreadMeta;
  onOpenThread?: (id: string) => void;
  nav?: AgentNav;
}) {
  const link = { copied: false, copy: vi.fn() };
  render(
    <AgentNavContext.Provider value={nav}>
      <ThreadStoreContext.Provider value={createThreadStore(state)}>
        <ThreadHeader
          sidebarOpen
          onShowSidebar={() => {}}
          activeId={activeId}
          meta={activeId ? meta : undefined}
          onRename={() => {}}
          creating={false}
          isDesktop={isDesktop}
          onSetPinned={() => {}}
          harness={{ modeOptions: [], ...harness } as unknown as ThreadHarness}
          copy={{ copied: false, copyAll: async () => {} }}
          link={link}
          panel={{ open: false, toggle: () => {}, show: () => {} } as unknown as PanelControls}
          onShowMcp={() => {}}
          onFind={onFind}
          parent={parent}
          onOpenThread={onOpenThread}
        />
      </ThreadStoreContext.Provider>
    </AgentNavContext.Provider>,
  );
  return link;
}

describe("ThreadHeader: copy thread link", () => {
  it("copies the open thread's link from the desktop bar", () => {
    const link = renderHeader({ isDesktop: true });
    fireEvent.click(screen.getByRole("button", { name: "Copy thread link" }));
    expect(link.copy).toHaveBeenCalledWith("a");
  });

  it("copies the open thread's link from the phone menu", async () => {
    viewport("phone");
    const link = renderHeader({ isDesktop: false });
    fireEvent.pointerDown(screen.getByRole("button", { name: "More thread actions" }), {
      button: 0,
      ctrlKey: false,
    });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Copy thread link" }));
    expect(link.copy).toHaveBeenCalledWith("a");
  });

  it("has no link to copy while nothing is selected", () => {
    renderHeader({ isDesktop: true, activeId: null });
    expect(screen.queryByRole("button", { name: "Copy thread link" })).toBeNull();
  });
});

describe("ThreadHeader: find in thread", () => {
  const openMenu = () => {
    const trigger = screen.getByRole("button", { name: "More thread actions" });
    fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
    return trigger;
  };

  it("opens find from the phone menu, and leaves focus off the menu's trigger", async () => {
    viewport("phone");
    const onFind = vi.fn();
    renderHeader({ isDesktop: false, onFind });
    const trigger = openMenu();

    fireEvent.click(await screen.findByRole("menuitem", { name: "Find in thread" }));
    expect(onFind).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    // The menu returns focus a tick after it closes; instead it asks again,
    // so the bar puts focus back in its field.
    await act(() => new Promise((r) => setTimeout(r, 10)));
    expect(document.activeElement).not.toBe(trigger);
    expect(onFind).toHaveBeenCalledTimes(2);
  });

  it("still returns focus to the trigger after any other item", async () => {
    viewport("phone");
    renderHeader({ isDesktop: false });
    const trigger = openMenu();

    fireEvent.click(await screen.findByRole("menuitem", { name: "Copy thread link" }));
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });
});

describe("ThreadHeader: restart", () => {
  const openMenu = () =>
    fireEvent.pointerDown(screen.getByRole("button", { name: "More thread actions" }), {
      button: 0,
      ctrlKey: false,
    });
  const restartable = (over: Partial<ThreadHarness> = {}) => ({
    canRestart: true,
    restartPending: false,
    restarting: false,
    restartThread: vi.fn(),
    cancelRestart: vi.fn(),
    ...over,
  });

  it("restarts from the phone menu", async () => {
    viewport("phone");
    const harness = restartable();
    renderHeader({ isDesktop: false, harness });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Restart thread" }));
    expect(harness.restartThread).toHaveBeenCalledOnce();
    expect(harness.cancelRestart).not.toHaveBeenCalled();
  });

  it("takes back a restart waiting on the turn from the phone menu", async () => {
    viewport("phone");
    const harness = restartable({ restartPending: true });
    renderHeader({ isDesktop: false, harness });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Cancel restart" }));
    expect(harness.cancelRestart).toHaveBeenCalledOnce();
    expect(harness.restartThread).not.toHaveBeenCalled();
  });

  it("is not offered where the thread cannot restart", async () => {
    viewport("phone");
    renderHeader({ isDesktop: false, harness: restartable({ canRestart: false }) });
    openMenu();
    await screen.findByRole("menuitem", { name: "Find in thread" });
    expect(screen.queryByRole("menuitem", { name: /restart/i })).toBeNull();
  });

  it("cannot be asked for twice while one is under way", async () => {
    viewport("phone");
    const harness = restartable({ restarting: true });
    renderHeader({ isDesktop: false, harness });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: /Restarting thread/ }));
    expect(harness.restartThread).not.toHaveBeenCalled();
  });

  it("restarts from the desktop bar", () => {
    const harness = restartable();
    renderHeader({ isDesktop: true, harness });
    fireEvent.click(screen.getByRole("button", { name: "Restart thread" }));
    expect(harness.restartThread).toHaveBeenCalledOnce();
  });

  it("disables the desktop button while a restart is under way", () => {
    renderHeader({ isDesktop: true, harness: restartable({ restarting: true }) });
    expect((screen.getByRole("button", { name: "Restarting thread" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("ThreadHeader: delegated by", () => {
  it("links back to the thread that delegated this one", () => {
    const onOpenThread = vi.fn();
    renderHeader({ isDesktop: false, parent: { id: "p", title: "Planner" } as ThreadMeta, onOpenThread });
    fireEvent.click(screen.getByRole("button", { name: /Delegated by Planner/ }));
    expect(onOpenThread).toHaveBeenCalledWith("p");
  });

  it("has no link when the parent is not listed", () => {
    renderHeader({ isDesktop: true, onOpenThread: vi.fn() });
    expect(screen.queryByRole("button", { name: /Delegated by/ })).toBeNull();
  });
});

describe("ThreadHeader: an agent's thread", () => {
  const agent: Agent = {
    id: "ag1",
    projectId: "p1",
    name: "backlog",
    harness: "claude",
    workspace: "local",
    kickoff: "Start the backlog loop.",
    threadId: "a",
    promptRev: 7,
    compactRev: 3,
    livePromptRev: 7,
    epoch: 46,
    createdAt: 0,
  };
  const navFor = (over: Partial<Agent> = {}) => ({
    current: { ...agent, ...over },
    openAgent: vi.fn(),
    openThread: vi.fn(),
  });
  const openMenu = () =>
    fireEvent.pointerDown(screen.getByRole("button", { name: "More thread actions" }), {
      button: 0,
      ctrlKey: false,
    });

  it("titles the thread with the agent, which opens the agent's page", () => {
    const nav = navFor();
    renderHeader({ isDesktop: true, nav });
    fireEvent.click(screen.getByRole("button", { name: /backlog/ }));
    expect(nav.openAgent).toHaveBeenCalledWith("ag1", "prompt");
    expect(screen.queryByRole("button", { name: "Rename thread" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Pin thread/ })).toBeNull();
  });

  it("shows the revision a restart is waiting to bring in", () => {
    renderHeader({ isDesktop: true, nav: navFor({ livePromptRev: 7, promptRev: 8, restartPending: true }) });
    expect(screen.getByRole("button", { name: /backlog/ }).textContent).toMatch(/r7.*r8/);
  });

  it("shows only the newest revision once no restart is pending", () => {
    renderHeader({ isDesktop: true, nav: navFor({ livePromptRev: 7, promptRev: 8 }) });
    expect(screen.getByRole("button", { name: /backlog/ }).textContent).not.toMatch(/r7/);
  });

  it("offers the agent's tabs, restart and new thread from the phone menu, but no pin", async () => {
    viewport("phone");
    const nav = navFor();
    renderHeader({ isDesktop: false, nav, harness: { canRestart: true, restartThread: vi.fn() } });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: /Log/ }));
    expect(nav.openAgent).toHaveBeenCalledWith("ag1", "log");
    openMenu();
    await screen.findByRole("menuitem", { name: /Restart thread/ });
    expect(screen.getByRole("menuitem", { name: /New thread/ })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: /Pin thread/ })).toBeNull();
  });

  it("starts a new thread on the kickoff and opens it", async () => {
    viewport("phone");
    const nav = navFor();
    const command = vi.fn(async () => ({ threadId: "b" }));
    renderHeader({ isDesktop: false, nav, harness: { command } });
    openMenu();
    fireEvent.click(await screen.findByRole("menuitem", { name: /New thread/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Archive and start" }));
    await waitFor(() => expect(nav.openThread).toHaveBeenCalledWith("b"));
    expect(command).toHaveBeenCalledWith("agent_new_thread", {
      agentId: "ag1",
      firstMessage: "Start the backlog loop.",
    });
  });

  it("sends the first message as edited for this start", async () => {
    const nav = navFor();
    const command = vi.fn(async () => ({ threadId: "b" }));
    renderHeader({ isDesktop: true, nav, harness: { command } });
    fireEvent.pointerDown(screen.getByRole("button", { name: "Agent actions" }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: /New thread/ }));
    fireEvent.change(await screen.findByLabelText("First message"), { target: { value: "Only #812 today." } });
    fireEvent.click(screen.getByRole("button", { name: "Archive and start" }));
    await waitFor(() => expect(nav.openThread).toHaveBeenCalledWith("b"));
    expect(command).toHaveBeenCalledWith("agent_new_thread", { agentId: "ag1", firstMessage: "Only #812 today." });
  });

  it("asks before restarting, then restarts the agent's thread", async () => {
    const restartThread = vi.fn();
    const command = vi.fn(async () => ({}));
    renderHeader({ isDesktop: true, nav: navFor(), harness: { canRestart: true, restartThread, command } });
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(command).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole("button", { name: "Restart" }));
    await waitFor(() => expect(command).toHaveBeenCalledWith("restart_thread", { threadId: "a" }));
    expect(restartThread).not.toHaveBeenCalled();
  });

  it("marks a thread the agent archived instead of claiming its current revision", () => {
    const nav = navFor({ threadId: "live", promptRev: 8, livePromptRev: 7, restartPending: true });
    renderHeader({ isDesktop: true, nav, harness: { canRestart: true, restartThread: vi.fn() } });
    const title = screen.getByRole("button", { name: /backlog/ });
    expect(title.textContent).toContain("archived");
    expect(title.textContent).not.toMatch(/r\d/);
    fireEvent.click(title);
    expect(nav.openAgent).toHaveBeenCalledWith("ag1", "prompt");
    expect(screen.queryByRole("button", { name: "Restart" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Restart thread/ })).toBeNull();
  });

  it("offers an archived thread the agent's tabs but no restart or new thread", async () => {
    viewport("phone");
    const nav = navFor({ threadId: "live" });
    renderHeader({ isDesktop: false, nav, harness: { canRestart: true, restartThread: vi.fn() } });
    openMenu();
    await screen.findByRole("menuitem", { name: /Log/ });
    expect(screen.queryByRole("menuitem", { name: /Restart thread/ })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /New thread/ })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /Pin thread/ })).toBeNull();
  });

  it("treats every thread of an agent with no live thread as archived", async () => {
    const nav = navFor({ threadId: undefined });
    renderHeader({ isDesktop: true, nav, harness: { canRestart: true, restartThread: vi.fn() } });
    expect(screen.getByRole("button", { name: /backlog/ }).textContent).toContain("archived");
    fireEvent.pointerDown(screen.getByRole("button", { name: "Agent actions" }), { button: 0, ctrlKey: false });
    await screen.findByRole("menuitem", { name: /Settings/ });
    expect(screen.queryByRole("menuitem", { name: /New thread/ })).toBeNull();
  });

  it("keeps the dialog and says why when the server refuses", async () => {
    const nav = navFor();
    const command = vi.fn(async () => {
      throw new Error("agent is archived");
    });
    renderHeader({ isDesktop: true, nav, harness: { command } });
    fireEvent.pointerDown(screen.getByRole("button", { name: "Agent actions" }), { button: 0, ctrlKey: false });
    fireEvent.click(await screen.findByRole("menuitem", { name: /New thread/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Archive and start" }));
    expect((await screen.findByRole("alert")).textContent).toContain("agent is archived");
    expect(nav.openThread).not.toHaveBeenCalled();
  });
});
