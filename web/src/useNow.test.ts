// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useNow } from "./useNow";

describe("useNow", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-11T10:00:30Z"));
  });
  afterEach(() => vi.useRealTimers());

  it("moves on at the next minute boundary, then every minute", () => {
    const { result, unmount } = renderHook(() => useNow());
    const start = result.current;
    act(() => vi.advanceTimersByTime(29_000));
    expect(result.current).toBe(start);
    act(() => vi.advanceTimersByTime(1_000));
    expect(result.current).toBe(Date.parse("2026-11-11T10:01:00Z"));
    act(() => vi.advanceTimersByTime(60_000));
    expect(result.current).toBe(Date.parse("2026-11-11T10:02:00Z"));
    unmount();
  });

  it("runs one timer however many read it, and none once they have gone", () => {
    const a = renderHook(() => useNow());
    const b = renderHook(() => useNow());
    expect(vi.getTimerCount()).toBe(1);
    act(() => vi.advanceTimersByTime(30_000));
    expect(a.result.current).toBe(b.result.current);
    a.unmount();
    expect(vi.getTimerCount()).toBe(1);
    b.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("catches up when the page comes back to the foreground", () => {
    const { result, unmount } = renderHook(() => useNow());
    // A background tab's timers are frozen: time passes, nothing fires.
    vi.setSystemTime(new Date("2026-11-11T10:20:10Z"));
    act(() => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(result.current).toBe(Date.parse("2026-11-11T10:20:10Z"));
    unmount();
  });
});
