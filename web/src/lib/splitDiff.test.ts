import { describe, expect, it } from "vitest";

import type { DiffLine } from "./patch";
import { parsePatch } from "./patch";
import { splitRows } from "./splitDiff";

const ctx = (text: string, oldNo: number, newNo: number): DiffLine => ({ kind: "context", text, oldNo, newNo });
const del = (text: string, oldNo: number): DiffLine => ({ kind: "del", text, oldNo });
const add = (text: string, newNo: number): DiffLine => ({ kind: "add", text, newNo });

describe("splitRows", () => {
  it("puts a context line on both sides of one row", () => {
    const line = ctx("same", 3, 4);
    expect(splitRows([line])).toEqual([{ kind: "pair", left: line, right: line }]);
  });

  it("pairs deletions with the additions after them, in order, with leftovers alone", () => {
    const rows = splitRows([del("a", 1), del("b", 2), del("c", 3), add("A", 1), add("B", 2)]);
    expect(rows).toEqual([
      { kind: "pair", left: del("a", 1), right: add("A", 1) },
      { kind: "pair", left: del("b", 2), right: add("B", 2) },
      { kind: "pair", left: del("c", 3), right: undefined },
    ]);
  });

  it("runs extra additions on with nothing opposite", () => {
    const rows = splitRows([del("a", 1), add("A", 1), add("B", 2)]);
    expect(rows).toEqual([
      { kind: "pair", left: del("a", 1), right: add("A", 1) },
      { kind: "pair", left: undefined, right: add("B", 2) },
    ]);
  });

  it("shows additions with no deletion before them on the right only", () => {
    const rows = splitRows([ctx("x", 1, 1), add("new", 2), ctx("y", 2, 3)]);
    expect(rows[1]).toEqual({ kind: "pair", left: undefined, right: add("new", 2) });
  });

  it("starts a new change when a deletion follows additions", () => {
    const rows = splitRows([add("A", 1), del("b", 2), add("B", 2)]);
    expect(rows).toEqual([
      { kind: "pair", left: undefined, right: add("A", 1) },
      { kind: "pair", left: del("b", 2), right: add("B", 2) },
    ]);
  });

  it("does not pair across a context line", () => {
    const rows = splitRows([del("a", 1), ctx("x", 2, 1), add("A", 2)]);
    expect(rows.map((r) => (r.kind === "pair" ? [r.left?.text, r.right?.text] : r.kind))).toEqual([
      ["a", undefined],
      ["x", "x"],
      [undefined, "A"],
    ]);
  });

  it("gives hunk headers and notes their own rows, ending any open change", () => {
    const rows = splitRows([del("a", 1), { kind: "hunk", text: "@@ -9 +9 @@" }, add("A", 9)]);
    expect(rows).toEqual([
      { kind: "pair", left: del("a", 1), right: undefined },
      { kind: "hunk", text: "@@ -9 +9 @@" },
      { kind: "pair", left: undefined, right: add("A", 9) },
    ]);
  });

  it("keeps an edited last line on one row when neither version ends in a newline", () => {
    const patch = [
      "@@ -1,2 +1,2 @@",
      " keep",
      "-old",
      "\\ No newline at end of file",
      "+new",
      "\\ No newline at end of file",
    ].join("\n");
    const rows = splitRows(parsePatch(patch));
    expect(rows.map((r) => (r.kind === "pair" ? [r.left?.text, r.right?.text] : r.kind))).toEqual([
      "hunk",
      ["keep", "keep"],
      ["old", "new"],
      "meta",
      "meta",
    ]);
  });

  it("leaves a gap note where it stands", () => {
    const rows = splitRows([del("a", 1), { kind: "meta", text: "… 40 more lines" }, add("A", 1)]);
    expect(rows.map((r) => r.kind)).toEqual(["pair", "meta", "pair"]);
  });
});
