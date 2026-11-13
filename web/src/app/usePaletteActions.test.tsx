// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ThemeProvider } from "~/components/ThemeProvider";
import type { ThreadMeta } from "~/protocol";
import "~/test/harness";

import { usePaletteActions } from "./usePaletteActions";

type Args = Parameters<typeof usePaletteActions>[0];

function actions(over: Partial<Args>) {
  const args = {
    meta: { id: "a", title: "One", headSeq: 1 } as ThreadMeta,
    threadOpen: true,
    turnRunning: false,
    composerOpen: false,
    composerRef: { current: null },
    onSetPinned: vi.fn(),
    onSetUnread: vi.fn(),
    onCopyTranscript: vi.fn(),
    onCopyLink: vi.fn(),
    onDelete: vi.fn(),
    onTogglePanel: vi.fn(),
    onOpenDiff: vi.fn(),
    onStopTurn: vi.fn(),
    onNewThreadHere: vi.fn(),
    onNewProject: vi.fn(),
    onSettings: vi.fn(),
    onUsage: vi.fn(),
    onSkills: vi.fn(),
    onToggleSidebar: vi.fn(),
    onShortcuts: vi.fn(),
    ...over,
  };
  const { result } = renderHook(() => usePaletteActions(args), { wrapper: ThemeProvider });
  return { args, list: result.current };
}

describe("usePaletteActions: copy thread link", () => {
  it("copies the open thread's link", () => {
    const { args, list } = actions({});
    list.find((a) => a.id === "copy-link")!.run();
    expect(args.onCopyLink).toHaveBeenCalledWith("a");
  });

  it("offers no link with no thread open", () => {
    const { list } = actions({ threadOpen: false, meta: undefined });
    expect(list.some((a) => a.id === "copy-link")).toBe(false);
  });
});
