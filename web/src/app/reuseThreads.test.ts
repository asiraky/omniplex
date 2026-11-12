import { describe, expect, it } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { reuseThreads } from "./reuseThreads";

// What a broadcast carries: every entry decoded afresh, so equal is never the
// same object.
const meta = (id: string, over: Partial<ThreadMeta> = {}) =>
  ({ id, title: id, phase: "idle", headSeq: 1, position: 1, ...over }) as ThreadMeta;

describe("reuseThreads", () => {
  it("keeps the previous list when nothing in it changed", () => {
    const prev = [meta("a"), meta("b")];
    expect(reuseThreads(prev, [meta("a"), meta("b")])).toBe(prev);
  });

  it("keeps each unchanged thread's object and takes the changed one", () => {
    const prev = [meta("a"), meta("b"), meta("c")];
    const moved = meta("b", { headSeq: 2 });
    const next = reuseThreads(prev, [meta("a"), moved, meta("c")]);

    expect(next).not.toBe(prev);
    expect(next[0]).toBe(prev[0]);
    expect(next[1]).toBe(moved);
    expect(next[2]).toBe(prev[2]);
  });

  it("reads a reorder as a change, keeping the objects", () => {
    const prev = [meta("a"), meta("b")];
    const next = reuseThreads(prev, [meta("b"), meta("a")]);

    expect(next).not.toBe(prev);
    expect(next).toEqual([prev[1], prev[0]]);
    expect(next[0]).toBe(prev[1]);
  });

  it("reads a thread added or gone as a change", () => {
    const prev = [meta("a"), meta("b")];

    const added = reuseThreads(prev, [meta("a"), meta("b"), meta("c")]);
    expect(added).toHaveLength(3);
    expect(added[0]).toBe(prev[0]);

    const gone = reuseThreads(prev, [meta("a")]);
    expect(gone).not.toBe(prev);
    expect(gone).toEqual([prev[0]]);
  });

  it("reads a field appearing as a change", () => {
    const prev = [meta("a")];
    const next = reuseThreads(prev, [meta("a", { pinned: true })]);
    expect(next[0]).not.toBe(prev[0]);
    expect(next[0].pinned).toBe(true);
  });
});
