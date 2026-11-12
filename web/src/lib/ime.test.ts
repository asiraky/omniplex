import { describe, expect, it, vi } from "vitest";

import { escapeUnlessComposing, isComposing } from "./ime";

describe("isComposing", () => {
  it("reads a React event's flag off its native event", () => {
    expect(isComposing({ keyCode: 13, nativeEvent: { isComposing: true } })).toBe(true);
  });

  it("reads a native event's own flag", () => {
    expect(isComposing({ isComposing: true, keyCode: 13 })).toBe(true);
  });

  it("catches Safari's confirming key, flagged only by keyCode 229", () => {
    expect(isComposing({ keyCode: 229, nativeEvent: { isComposing: false } })).toBe(true);
    expect(isComposing({ isComposing: false, keyCode: 229 })).toBe(true);
  });

  it("lets a plain Enter or Escape through", () => {
    expect(isComposing({ keyCode: 13, nativeEvent: { isComposing: false } })).toBe(false);
    expect(isComposing({ isComposing: false, keyCode: 27 })).toBe(false);
    expect(isComposing({})).toBe(false);
  });
});

describe("escapeUnlessComposing", () => {
  // The two fields Radix's event is read for, and the one it is told with.
  const escape = (init: { isComposing?: boolean; keyCode?: number }) => {
    const e = {
      key: "Escape",
      keyCode: 27,
      ...init,
      defaultPrevented: false,
      preventDefault: () => (e.defaultPrevented = true),
    };
    return e as unknown as KeyboardEvent;
  };

  it("keeps the surface open on an IME's Escape, without asking the caller", () => {
    const handler = vi.fn();
    for (const e of [escape({ isComposing: true }), escape({ keyCode: 229 })]) {
      escapeUnlessComposing(handler)(e);
      expect(e.defaultPrevented).toBe(true);
    }
    expect(handler).not.toHaveBeenCalled();
  });

  it("hands a plain Escape to the caller, and closes when there is none", () => {
    const handler = vi.fn();
    const e = escape({});
    escapeUnlessComposing(handler)(e);
    expect(handler).toHaveBeenCalledWith(e);
    const bare = escape({});
    escapeUnlessComposing()(bare);
    expect(bare.defaultPrevented).toBe(false);
  });
});
