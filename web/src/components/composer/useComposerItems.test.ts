// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ComposerItem } from "~/protocol";

import { useComposerItems } from "./useComposerItems";

const compact: ComposerItem = {
  id: "command:compact",
  name: "compact",
  description: "Compact the transcript",
  kind: "command",
  trigger: "/",
  insertText: "/compact",
  origin: "project",
  behavior: "adapter-action",
  action: "compact",
};

describe("useComposerItems", () => {
  it("keeps the catalogue it has when a refresh fails", async () => {
    const load = vi
      .fn<() => Promise<ComposerItem[]>>()
      .mockResolvedValueOnce([compact])
      .mockRejectedValueOnce(new Error("provider went away"));
    const { result, unmount } = renderHook(() => useComposerItems(load));
    await waitFor(() => expect(result.current.ready).toBe(true));

    act(() => result.current.reload());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(load).toHaveBeenCalledTimes(2);
    expect(result.current.items).toEqual([compact]);
    expect(result.current.ready).toBe(true);
    unmount();
  });

  it("fetches again when handed a new loader, which is how a catalogue change arrives", async () => {
    const first = vi.fn<() => Promise<ComposerItem[]>>().mockResolvedValue([]);
    const second = vi.fn<() => Promise<ComposerItem[]>>().mockResolvedValue([compact]);
    const { result, rerender, unmount } = renderHook(({ load }) => useComposerItems(load), {
      initialProps: { load: first },
    });
    await waitFor(() => expect(result.current.ready).toBe(true));
    rerender({ load: second });
    await waitFor(() => expect(result.current.items).toEqual([compact]));
    expect(first).toHaveBeenCalledTimes(1);
    unmount();
  });

  it("shows nothing loaded under another scope while this one's is on its way", async () => {
    let land!: (items: ComposerItem[]) => void;
    const first = vi.fn<() => Promise<ComposerItem[]>>().mockResolvedValue([compact]);
    const second = vi.fn(() => new Promise<ComposerItem[]>((resolve) => (land = resolve)));
    const { result, rerender, unmount } = renderHook(
      ({ load, scope }) => useComposerItems(load, scope),
      { initialProps: { load: first, scope: "claude" } },
    );
    await waitFor(() => expect(result.current.items).toEqual([compact]));

    rerender({ load: second, scope: "codex" });
    expect(result.current.items).toEqual([]);
    expect(result.current.ready).toBe(false);

    await act(async () => land([]));
    expect(result.current.ready).toBe(true);
    expect(result.current.items).toEqual([]);
    unmount();
  });

  it("keeps the old catalogue under the same scope until a new loader answers", async () => {
    const first = vi.fn<() => Promise<ComposerItem[]>>().mockResolvedValue([compact]);
    const second = vi.fn(() => new Promise<ComposerItem[]>(() => {}));
    const { result, rerender, unmount } = renderHook(({ load }) => useComposerItems(load), {
      initialProps: { load: first },
    });
    await waitFor(() => expect(result.current.ready).toBe(true));
    rerender({ load: second });
    await waitFor(() => expect(second).toHaveBeenCalled());
    expect(result.current.items).toEqual([compact]);
    expect(result.current.ready).toBe(true);
    unmount();
  });
});
