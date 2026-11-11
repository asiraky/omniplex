import { describe, expect, it } from "vitest";

import { isComposing } from "./ime";

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
