// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

import { markInvited, shouldInvite } from "./pushInvite";

beforeEach(() => {
  localStorage.clear();
});

describe("shouldInvite", () => {
  it("asks a device that has never been asked", () => {
    expect(shouldInvite(true)).toBe(true);
  });

  // The whole point of recording the answer: an invitation that reappears on
  // every load is worse than never offering one, because the way to make it
  // stop is to deny the permission outright.
  it("does not ask twice", () => {
    markInvited();
    expect(shouldInvite(true)).toBe(false);
  });

  // A browser that cannot grant it, has already granted it, or has denied it
  // — none of those are questions worth raising.
  it("stays quiet when the browser has nothing to ask about", () => {
    expect(shouldInvite(false)).toBe(false);
  });

  // Private mode: reading storage throws. Offering notifications is the whole
  // feature, so a failure to remember must not silently disable the offer.
  it("still asks when storage is unavailable", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(shouldInvite(true)).toBe(true);
    vi.restoreAllMocks();
  });

  it("does not throw when storage refuses to record the answer", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => markInvited()).not.toThrow();
    vi.restoreAllMocks();
  });
});
