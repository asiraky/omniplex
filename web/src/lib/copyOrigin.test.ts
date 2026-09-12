import { beforeEach, describe, expect, it } from "vitest";

import { originOf, recordCopy, resetCopyOrigins } from "./copyOrigin";

describe("copyOrigin", () => {
  beforeEach(resetCopyOrigins);

  it("gives a paste back the origin of the copy that made it", () => {
    recordCopy("boom\nboom", { kind: "terminal", label: "Term 1" });
    expect(originOf("boom\nboom")).toEqual({ kind: "terminal", label: "Term 1" });
  });

  it("calls anything it never saw a plain paste", () => {
    expect(originOf("from a web page")).toEqual({ kind: "paste" });
    expect(originOf("")).toEqual({ kind: "paste" });
  });

  it("tells apart two long copies that differ only in the middle", () => {
    const a = `${"x".repeat(200)}A${"y".repeat(200)}`;
    const b = `${"x".repeat(200)}B${"y".repeat(200)}`;
    recordCopy(a, { kind: "file", path: "a.ts", from: 1, to: 9 });
    recordCopy(b, { kind: "terminal", label: "Term 2" });
    // Same length and same 128-character ends: the fingerprint cannot separate
    // these, and the most recent copy wins. Documented, not aspirational — the
    // cost of a miss is a subtitle, and holding the text would be a leak.
    expect(originOf(a)).toEqual({ kind: "terminal", label: "Term 2" });
  });

  it("remembers a few copies, not one", () => {
    recordCopy("one", { kind: "terminal", label: "Term 1" });
    recordCopy("two", { kind: "terminal", label: "Term 2" });
    expect(originOf("one")).toMatchObject({ label: "Term 1" });
    expect(originOf("two")).toMatchObject({ label: "Term 2" });
  });

  it("forgets the oldest once enough has been copied since", () => {
    recordCopy("first", { kind: "terminal", label: "Term 1" });
    for (const t of ["a", "b", "c", "d"]) recordCopy(t, { kind: "paste" });
    expect(originOf("first")).toEqual({ kind: "paste" });
  });

  it("moves a re-copy to the front instead of duplicating it", () => {
    recordCopy("same", { kind: "terminal", label: "Term 1" });
    for (const t of ["a", "b"]) recordCopy(t, { kind: "paste" });
    recordCopy("same", { kind: "terminal", label: "Term 1" });
    for (const t of ["c", "d"]) recordCopy(t, { kind: "paste" });
    expect(originOf("same")).toMatchObject({ label: "Term 1" });
  });
});
