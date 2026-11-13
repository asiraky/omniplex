// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDiffSplit, useDiffWrap } from "./diffPrefs";

beforeEach(() => localStorage.clear());
afterEach(() => vi.restoreAllMocks());

describe("diff preferences", () => {
  it("defaults off, and persists a change for the next reader", () => {
    const first = renderHook(() => useDiffSplit());
    expect(first.result.current[0]).toBe(false);
    act(() => first.result.current[1](true));
    expect(first.result.current[0]).toBe(true);
    first.unmount();

    const next = renderHook(() => useDiffSplit());
    expect(next.result.current[0]).toBe(true);
  });

  it("moves every mounted reader of a preference together, and only that preference", () => {
    const a = renderHook(() => useDiffWrap());
    const b = renderHook(() => useDiffWrap());
    const other = renderHook(() => useDiffSplit());
    act(() => a.result.current[1](true));
    expect(b.result.current[0]).toBe(true);
    expect(other.result.current[0]).toBe(false);
  });

  it("follows a change made in another tab", () => {
    const { result } = renderHook(() => useDiffWrap());
    act(() => {
      localStorage.setItem("omniplex.diffWrap", "1");
      window.dispatchEvent(new StorageEvent("storage", { key: "omniplex.diffWrap" }));
    });
    expect(result.current[0]).toBe(true);
  });

  it("still works for this page when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const a = renderHook(() => useDiffSplit());
    const b = renderHook(() => useDiffSplit());
    expect(a.result.current[0]).toBe(false);
    act(() => a.result.current[1](true));
    expect(a.result.current[0]).toBe(true);
    expect(b.result.current[0]).toBe(true);
  });
});
