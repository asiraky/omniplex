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

describe("useScreens: agent pages", () => {
  const at = () => window.location.pathname + window.location.search;
  const back = () =>
    act(async () => {
      const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
      window.history.back();
      await popped;
    });

  it("opens on the page its URL names, so a reload lands on it", () => {
    window.history.replaceState(null, "", "/agents/a1/log?epoch=2");
    const { result } = renderHook(() => useScreens());
    expect(result.current.page).toEqual({ kind: "agent", agentId: "a1", tab: "log", focus: { epoch: 2 } });
    expect(result.current.covering).toBe(true);
  });

  it("gives an agent its own entry, but not each of its tabs", async () => {
    window.history.replaceState(null, "", "/t/x");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openPage({ kind: "agent", agentId: "a1", tab: "prompt" }));
    act(() => result.current.openPage({ kind: "agent", agentId: "a1", tab: "settings" }));
    expect(at()).toBe("/agents/a1/settings");
    await back();
    expect(at()).toBe("/t/x");
    await waitFor(() => expect(result.current.page).toBeNull());
  });

  it("comes back to the page on Forward", async () => {
    window.history.replaceState(null, "", "/t/x");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openPage({ kind: "agent", agentId: "a1", tab: "log" }));
    await back();
    await act(async () => {
      const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
      window.history.forward();
      await popped;
    });
    await waitFor(() => expect(result.current.page).toEqual({ kind: "agent", agentId: "a1", tab: "log" }));
  });

  it("leaves by Back to the place it was opened over, rather than a second entry for it", async () => {
    window.history.replaceState(null, "", "/t/x");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openPage({ kind: "agent", agentId: "a1", tab: "prompt" }));
    const depth = window.history.length;
    await act(async () => {
      const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
      result.current.closePage("/t/x");
      await popped;
    });
    expect(at()).toBe("/t/x");
    expect(result.current.page).toBeNull();
    // Forward still leads to the page: Back was taken, nothing was pushed.
    expect(window.history.length).toBe(depth);
  });

  it("pushes the way out when it goes somewhere else", () => {
    window.history.replaceState(null, "", "/t/x");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openPage({ kind: "agent", agentId: "a1", tab: "prompt" }));
    act(() => result.current.closePage("/t/y"));
    expect(at()).toBe("/t/y");
    expect(result.current.page).toBeNull();
  });

  it("lets the New agent form give its entry to the agent it made", async () => {
    window.history.replaceState(null, "", "/t/x");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.openPage({ kind: "new", projectId: "p1" }));
    expect(at()).toBe("/agents/new?project=p1");
    act(() => result.current.openPage({ kind: "agent", agentId: "a9", tab: "prompt" }, { replace: true }));
    expect(at()).toBe("/agents/a9/prompt");
    await back();
    expect(at()).toBe("/t/x");
  });

  it("gives way when a thread is chosen", () => {
    window.history.replaceState(null, "", "/agents/a1/prompt");
    const { result } = renderHook(() => useScreens());
    act(() => result.current.leaveAll());
    expect(result.current.page).toBeNull();
  });
});
