// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
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

function renderHeader({ isDesktop, activeId = "a" }: { isDesktop: boolean; activeId?: string | null }) {
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
        harness={{ modeOptions: [] } as unknown as ThreadHarness}
        copy={{ copied: false, copyAll: async () => {} }}
        link={link}
        panel={{ open: false, toggle: () => {}, show: () => {} } as unknown as PanelControls}
        onShowMcp={() => {}}
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
