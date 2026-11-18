// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { viewport } from "~/test/harness";

import { useScreens } from "./useScreens";

describe("useScreens: box terminal", () => {
  it("clears Usage and Skills to be seen, and covers the thread", () => {
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openUsage());
    act(() => result.current.openSkills());
    act(() => result.current.openBoxTerminal());
    expect(result.current.boxTerminal).toBe(true);
    expect(result.current.showUsage).toBe(false);
    expect(result.current.covering).toBe(true);
    expect(result.current.fullPage).toBe(true);
  });

  it("uncovers once closed", () => {
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openBoxTerminal());
    act(() => result.current.closeBoxTerminal());
    expect(result.current.boxTerminal).toBe(false);
    expect(result.current.covering).toBe(false);
  });

  it("gives way when a thread is chosen", () => {
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openBoxTerminal());
    act(() => result.current.leaveAll());
    expect(result.current.boxTerminal).toBe(false);
  });

  it("closes on Back on a phone", async () => {
    viewport("phone");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openBoxTerminal());
    await act(async () => {
      const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
      window.history.back();
      await popped;
    });
    await waitFor(() => expect(result.current.boxTerminal).toBe(false));
  });
});
