// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import type { PanelControls } from "~/app/usePanel";
import type { ThreadHarness } from "~/app/useThreadHarness";
import { render, viewport } from "~/test/harness";
import type { ThreadMeta, ThreadState } from "~/protocol";

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
}: {
  isDesktop: boolean;
  activeId?: string | null;
  onFind?: () => void;
  harness?: Partial<ThreadHarness>;
  parent?: ThreadMeta;
  onOpenThread?: (id: string) => void;
}) {
  const link = { copied: false, copy: vi.fn() };
  render(
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
    </ThreadStoreContext.Provider>,
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
