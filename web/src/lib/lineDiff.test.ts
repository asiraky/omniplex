import { describe, expect, it } from "vitest";

import { diffLines } from "~/lib/lineDiff";

const SIGN = { add: "+", del: "-", hunk: "@", context: " ", meta: "?" } as const;

/** Each line with its kind in front, the way a patch reads. */
const kinds = (before: string, after: string, context?: number) =>
  diffLines(before, after, context).map((l) => `${SIGN[l.kind]}${l.text}`);

const lines = (n: number, prefix = "line") => Array.from({ length: n }, (_, i) => `${prefix} ${i + 1}`);

describe("diffLines", () => {
  it("is empty when the two read the same, trailing newline or not", () => {
    expect(diffLines("a\nb\n", "a\nb\n")).toEqual([]);
    expect(diffLines("a\nb", "a\nb\n")).toEqual([]);
    expect(diffLines("", "")).toEqual([]);
  });

  it("shows a changed line as the old one out and the new one in, numbered on its own side", () => {
    const out = diffLines("a\nb\nc\n", "a\nB\nc\n");
    expect(out).toEqual([
      { kind: "hunk", text: "@@ -1,3 +1,3 @@" },
      { kind: "context", text: "a", oldNo: 1, newNo: 1 },
      { kind: "del", text: "b", oldNo: 2 },
      { kind: "add", text: "B", newNo: 2 },
      { kind: "context", text: "c", oldNo: 3, newNo: 3 },
    ]);
  });

  it("keeps only the lines within reach of a change", () => {
    const before = lines(20);
    const after = [...before];
    after[9] = "changed";
    expect(kinds(before.join("\n"), after.join("\n"), 1)).toEqual([
      "@@@ -9,3 +9,3 @@",
      " line 9",
      "-line 10",
      "+changed",
      " line 11",
    ]);
  });

  it("cuts far-apart changes into separate hunks and joins near ones", () => {
    const before = lines(30);
    const far = [...before];
    far[2] = "x";
    far[25] = "y";
    expect(diffLines(before.join("\n"), far.join("\n"), 2).filter((l) => l.kind === "hunk")).toHaveLength(2);

    const near = [...before];
    near[2] = "x";
    near[5] = "y";
    expect(diffLines(before.join("\n"), near.join("\n"), 2).filter((l) => l.kind === "hunk")).toHaveLength(1);
  });

  it("numbers lines after an insertion by their new position", () => {
    const out = diffLines("a\nb\n", "a\nnew\nb\n");
    expect(out.find((l) => l.text === "new")).toEqual({ kind: "add", text: "new", newNo: 2 });
    expect(out.find((l) => l.text === "b")).toEqual({ kind: "context", text: "b", oldNo: 2, newNo: 3 });
  });

  it("shows a new file as all added and a removed one as all deleted", () => {
    expect(kinds("", "a\nb\n")).toEqual(["@@@ -0,0 +1,2 @@", "+a", "+b"]);
    expect(kinds("a\nb\n", "")).toEqual(["@@@ -1,2 +0,0 @@", "-a", "-b"]);
  });

  it("finds the lines two versions share in the middle, not only at the ends", () => {
    const out = kinds("a\nkeep\nb\n", "x\nkeep\ny\n");
    expect(out).toContain(" keep");
    expect(out.filter((l) => l.startsWith("-"))).toEqual(["-a", "-b"]);
    expect(out.filter((l) => l.startsWith("+"))).toEqual(["+x", "+y"]);
  });

  it("keeps lines that look like patch headers as the text they are", () => {
    const out = diffLines("--- old\n", "+++ new\n");
    expect(out.filter((l) => l.kind !== "hunk")).toEqual([
      { kind: "del", text: "--- old", oldNo: 1 },
      { kind: "add", text: "+++ new", newNo: 1 },
    ]);
  });

  it("still tells the truth about two versions too long to compare line by line", () => {
    const before = lines(1500, "old");
    const after = lines(1500, "new");
    const out = diffLines(before.join("\n"), after.join("\n"));
    expect(out.filter((l) => l.kind === "del").map((l) => l.text)).toEqual(before);
    expect(out.filter((l) => l.kind === "add").map((l) => l.text)).toEqual(after);
  });
});
