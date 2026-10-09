import { describe, expect, it } from "vitest";

import { newCount, rowStatus } from "./threadStatus";
import type { ThreadMeta } from "~/protocol";

const thread = (over: Partial<ThreadMeta>): ThreadMeta =>
  ({ id: "a", phase: "idle", headSeq: 10, lastViewedSeq: 10, ...over }) as ThreadMeta;

describe("rowStatus", () => {
  it("is quiet when idle and read", () => {
    expect(rowStatus(thread({ attention: "needs_prompt" }))).toBe("quiet");
  });

  it("is new when the agent stopped and nobody has read the end", () => {
    expect(rowStatus(thread({ attention: "needs_prompt", lastViewedSeq: 7 }))).toBe("new");
  });

  it("treats a pending question or permission like any other unread stop", () => {
    expect(rowStatus(thread({ attention: "needs_answer", lastViewedSeq: 7 }))).toBe("new");
    expect(rowStatus(thread({ attention: "needs_permission", lastViewedSeq: 7 }))).toBe("new");
    expect(rowStatus(thread({ attention: "needs_permission" }))).toBe("quiet");
  });

  it("keeps a working or background thread busy however far behind the reader is", () => {
    expect(rowStatus(thread({ attention: "working", lastViewedSeq: 1 }))).toBe("busy");
    expect(rowStatus(thread({ attention: "background", lastViewedSeq: 1 }))).toBe("busy");
  });

  it("reports a failed workspace over everything else", () => {
    expect(rowStatus(thread({ attention: "failed", lastViewedSeq: 1 }))).toBe("failed");
  });

  it("reads all as read when the server sends no read cursor", () => {
    expect(rowStatus(thread({ attention: "needs_prompt", lastViewedSeq: undefined }))).toBe(
      "quiet",
    );
  });

  it("falls back to phase when the server sends no attention", () => {
    expect(rowStatus(thread({ phase: "turn", lastViewedSeq: 1 }))).toBe("busy");
    expect(rowStatus(thread({ phase: "cleanup_failed" }))).toBe("failed");
    expect(rowStatus(thread({ phase: "idle", lastViewedSeq: 1 }))).toBe("new");
  });
});

describe("newCount", () => {
  it("counts unread stopped threads, leaving out the one on screen", () => {
    const threads = [
      thread({ id: "a", lastViewedSeq: 1 }),
      thread({ id: "b", lastViewedSeq: 1 }),
      thread({ id: "c", lastViewedSeq: 1, attention: "working" }),
      thread({ id: "d" }),
    ];
    expect(newCount(threads, "b")).toBe(1);
    expect(newCount(threads, null)).toBe(2);
  });
});
