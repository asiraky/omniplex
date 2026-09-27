import { describe, expect, it } from "vitest";

import { UNLABELLED, visibleThreads } from "./labelFilter";
import type { Label, ThreadMeta } from "~/protocol";

const label = (id: string): Label => ({ id, name: id, color: "#000", position: 0, createdAt: 0 });
const thread = (id: string, labelId?: string) => ({ id, labelId }) as ThreadMeta;

const labels = [label("l1"), label("l2")];
const threads = [thread("a", "l1"), thread("b", "l2"), thread("c"), thread("d", "gone")];
const ids = (list: ThreadMeta[]) => list.map((s) => s.id);

describe("visibleThreads", () => {
  it("shows everything when nothing is switched off", () => {
    expect(visibleThreads(threads, labels, new Set())).toBe(threads);
  });

  it("drops the threads filed under a hidden label", () => {
    expect(ids(visibleThreads(threads, labels, new Set(["l1"])))).toEqual(["b", "c", "d"]);
  });

  it("treats unlabelled as its own switch, and a dangling label as unlabelled", () => {
    // "d" points at a label that no longer exists — the deletion broadcast can
    // land before the reassignment does, and it is unfiled in the meantime.
    expect(ids(visibleThreads(threads, labels, new Set([UNLABELLED])))).toEqual(["a", "b"]);
  });

  it("ignores hidden ids whose label is gone, rather than stranding threads", () => {
    expect(visibleThreads(threads, labels, new Set(["deleted-label"]))).toBe(threads);
  });

  it("filters by nothing once the last label is deleted", () => {
    // Deleting the last label unlabels every thread. A "No label" switched
    // off beforehand would empty the sidebar, and the menu no longer carries
    // the checkbox to switch it back on.
    expect(visibleThreads(threads, [], new Set([UNLABELLED, "l1"]))).toBe(threads);
  });

  it("can hide everything, which the sidebar renders as its own empty state", () => {
    expect(visibleThreads(threads, labels, new Set(["l1", "l2", UNLABELLED]))).toEqual([]);
  });
});
