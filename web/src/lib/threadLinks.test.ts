import { describe, expect, it } from "vitest";

import { openThreadMessage, threadIdFromPath } from "./threadLinks";

describe("threadIdFromPath", () => {
  it("reads the thread from /thread/<id>, decoded", () => {
    expect(threadIdFromPath("/thread/abc-123")).toBe("abc-123");
    expect(threadIdFromPath("/thread/abc/")).toBe("abc");
    expect(threadIdFromPath("/thread/a%20b")).toBe("a b");
  });

  it("ignores every other path", () => {
    expect(threadIdFromPath("/")).toBeNull();
    expect(threadIdFromPath("/setup")).toBeNull();
    expect(threadIdFromPath("/thread/")).toBeNull();
    expect(threadIdFromPath("/thread/a/b")).toBeNull();
    expect(threadIdFromPath("/threads/a")).toBeNull();
  });

  it("ignores a malformed escape rather than throwing", () => {
    expect(threadIdFromPath("/thread/%E0%A4%A")).toBeNull();
  });
});

describe("openThreadMessage", () => {
  it("takes the thread from an open-thread message", () => {
    expect(openThreadMessage({ type: "open-thread", threadId: "t1" })).toBe("t1");
  });

  it("ignores anything else", () => {
    expect(openThreadMessage(null)).toBeNull();
    expect(openThreadMessage("open-thread")).toBeNull();
    expect(openThreadMessage({ type: "other", threadId: "t1" })).toBeNull();
    expect(openThreadMessage({ type: "open-thread", threadId: "" })).toBeNull();
    expect(openThreadMessage({ type: "open-thread", threadId: 7 })).toBeNull();
  });
});
