// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { viewport } from "~/test/harness";
import { MOD_HOLD_MS, useModHeld, useShortcutLabel } from "./useShortcuts";

afterEach(() => {
  cleanup();
  delete window.omniplexDesktop;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const key = (type: "keydown" | "keyup", init: KeyboardEventInit) =>
  act(() => {
    window.dispatchEvent(new KeyboardEvent(type, { bubbles: true, ...init }));
  });

const wait = (ms: number) =>
  act(() => {
    vi.advanceTimersByTime(ms);
  });

describe.each([
  ["a Mac", "darwin", "Meta", "Control"],
  ["Linux", "linux", "Control", "Meta"],
])("useModHeld on %s", (_, platform, modKey, otherKey) => {
  const down = { key: modKey, metaKey: modKey === "Meta", ctrlKey: modKey === "Control" };

  beforeEach(() => {
    vi.useFakeTimers();
    window.omniplexDesktop = { platform };
  });

  it(`turns on once ${modKey} has been held alone for a moment`, () => {
    const { result } = renderHook(() => useModHeld(true));

    key("keydown", down);
    wait(MOD_HOLD_MS - 1);
    expect(result.current).toBe(false);
    wait(1);
    expect(result.current).toBe(true);
  });

  it("turns off when the key is let go", () => {
    const { result } = renderHook(() => useModHeld(true));
    key("keydown", down);
    wait(MOD_HOLD_MS);

    key("keyup", { key: modKey });

    expect(result.current).toBe(false);
  });

  it("turns off, and never turns on, when another key joins it", () => {
    const { result } = renderHook(() => useModHeld(true));
    key("keydown", down);
    wait(MOD_HOLD_MS);
    key("keydown", { ...down, key: "c" });
    expect(result.current).toBe(false);

    // A quick chord before the delay never flashes the hints.
    key("keyup", { key: modKey });
    key("keydown", down);
    key("keydown", { ...down, key: "c" });
    wait(MOD_HOLD_MS * 2);
    expect(result.current).toBe(false);
  });

  it(`does not count ${modKey} pressed with ${otherKey} already down`, () => {
    const { result } = renderHook(() => useModHeld(true));

    key("keydown", { ...down, metaKey: true, ctrlKey: true });
    wait(MOD_HOLD_MS);

    expect(result.current).toBe(false);
  });

  it(`ignores ${otherKey} held alone`, () => {
    const { result } = renderHook(() => useModHeld(true));

    key("keydown", { key: otherKey });
    wait(MOD_HOLD_MS);

    expect(result.current).toBe(false);
  });

  it("turns off when the window loses focus", () => {
    const { result } = renderHook(() => useModHeld(true));
    key("keydown", down);
    wait(MOD_HOLD_MS);

    act(() => {
      window.dispatchEvent(new Event("blur"));
    });

    expect(result.current).toBe(false);
  });

  it("keeps the delay from restarting on key repeat", () => {
    const { result } = renderHook(() => useModHeld(true));
    key("keydown", down);
    wait(MOD_HOLD_MS / 2);
    key("keydown", { ...down, repeat: true });
    wait(MOD_HOLD_MS / 2);

    expect(result.current).toBe(true);
  });

  it("stays off while disabled, and turns off when disabled", () => {
    const { result, rerender } = renderHook(({ on }) => useModHeld(on), { initialProps: { on: false } });
    key("keydown", down);
    wait(MOD_HOLD_MS);
    expect(result.current).toBe(false);

    rerender({ on: true });
    key("keydown", down);
    wait(MOD_HOLD_MS);
    expect(result.current).toBe(true);

    rerender({ on: false });
    expect(result.current).toBe(false);
  });
});

describe("useShortcutLabel", () => {
  /** A desktop with a mouse: every media query matches. */
  const mouse = () =>
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: true,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }));

  it("labels the command for a desktop with a mouse", () => {
    mouse();
    window.omniplexDesktop = { platform: "darwin" };
    expect(renderHook(() => useShortcutLabel("toggleSidebar")).result.current).toBe("⌘B");
  });

  it("is null where shortcuts are not advertised", () => {
    // Wide, but no pointer that can hover: a tablet.
    viewport("desktop");
    expect(renderHook(() => useShortcutLabel("toggleSidebar")).result.current).toBeNull();
    viewport("phone");
    expect(renderHook(() => useShortcutLabel("toggleSidebar")).result.current).toBeNull();
  });

  it("is null for a command with no chord in this shell", () => {
    mouse();
    expect(renderHook(() => useShortcutLabel("closePanel")).result.current).toBeNull();
  });
});
