import { describe, expect, it } from "vitest";

import { parsePatch } from "./patch";

const kinds = (patch: string) => parsePatch(patch).map((l) => `${l.kind}:${l.text}`);

describe("parsePatch", () => {
  it("keeps a removed SQL comment and an added ++ line, which look like file headers", () => {
    const patch = [
      "--- a/q.sql",
      "+++ b/q.sql",
      "@@ -1,2 +1,2 @@",
      "--- old note",
      "+++ new note",
      " select 1;",
      "",
    ].join("\n");
    expect(kinds(patch)).toEqual([
      "hunk:@@ -1,2 +1,2 @@",
      "del:-- old note",
      "add:++ new note",
      "context:select 1;",
    ]);
  });

  it("reads headers again once a hunk's counts run out, across files", () => {
    const patch = [
      "diff --git a/x b/x",
      "--- a/x",
      "+++ b/x",
      "@@ -1 +1 @@",
      "-a",
      "+b",
      "diff --git a/y b/y",
      "--- a/y",
      "+++ b/y",
      "@@ -4,0 +5,1 @@",
      "+c",
      "",
    ].join("\n");
    expect(parsePatch(patch)).toEqual([
      { kind: "hunk", text: "@@ -1 +1 @@" },
      { kind: "del", text: "a", oldNo: 1 },
      { kind: "add", text: "b", newNo: 1 },
      { kind: "hunk", text: "@@ -4,0 +5,1 @@" },
      { kind: "add", text: "c", newNo: 5 },
    ]);
  });

  it("parses a hunks-only patch with no file header", () => {
    expect(kinds("@@ -2,2 +2,2 @@\n x\n-y\n+z\n")).toEqual(["hunk:@@ -2,2 +2,2 @@", "context:x", "del:y", "add:z"]);
  });

  it("treats a blank line inside a hunk as an empty context line", () => {
    expect(kinds("@@ -1,3 +1,3 @@\n a\n\n-b\n+c\n")).toEqual([
      "hunk:@@ -1,3 +1,3 @@",
      "context:a",
      "context:",
      "del:b",
      "add:c",
    ]);
  });

  it("still renders the lines of a hunk whose counts are short", () => {
    expect(kinds("@@ -1 +1 @@\n-a\n+b\n+c\n")).toEqual(["hunk:@@ -1 +1 @@", "del:a", "add:b", "add:c"]);
  });

  it("leaves the lines after a gap unnumbered, until the next hunk says where they are", () => {
    const patch = [
      "@@ -1,0 +1,2000 @@",
      "+line 1",
      "\\ [… 900 characters truncated …]",
      "+line 2000",
      "@@ -10 +2010 @@",
      "-x",
      "\\ No newline at end of file",
      "+y",
      "",
    ].join("\n");
    expect(parsePatch(patch)).toEqual([
      { kind: "hunk", text: "@@ -1,0 +1,2000 @@" },
      { kind: "add", text: "line 1", newNo: 1 },
      { kind: "meta", text: "[… 900 characters truncated …]" },
      { kind: "add", text: "line 2000", newNo: undefined },
      { kind: "hunk", text: "@@ -10 +2010 @@" },
      { kind: "del", text: "x", oldNo: 10 },
      { kind: "meta", text: "No newline at end of file" },
      { kind: "add", text: "y", newNo: 2010 },
    ]);
  });
});
