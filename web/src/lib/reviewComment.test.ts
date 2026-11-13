import { describe, expect, it } from "vitest";

import { reviewComment } from "./reviewComment";

const path = "web/src/lib/patch.ts";

describe("reviewComment", () => {
  it("cites an added line by its new number and quotes it with its sign", () => {
    const line = { kind: "add" as const, text: "  const x = parse(line);", newNo: 42 };
    expect(reviewComment(path, line, "new", "  Rename this; it shadows the outer x.\n")).toBe(
      "`web/src/lib/patch.ts:42`\n```diff\n+  const x = parse(line);\n```\nRename this; it shadows the outer x.",
    );
  });

  it("cites a removed line by its old number and says it is gone", () => {
    const line = { kind: "del" as const, text: "return x;", oldNo: 40 };
    expect(reviewComment(path, line, "old", "Why?")).toBe(
      "`web/src/lib/patch.ts:40` (removed line)\n```diff\n-return x;\n```\nWhy?",
    );
  });

  it("cites a context line by its new number, quoted after a space", () => {
    const line = { kind: "context" as const, text: "}", oldNo: 40, newNo: 44 };
    expect(reviewComment(path, line, "new", "Here.")).toBe("`web/src/lib/patch.ts:44`\n```diff\n }\n```\nHere.");
  });

  it("marks a context line picked from the old side, whose number is not the file's now", () => {
    const line = { kind: "context" as const, text: "}", oldNo: 40, newNo: 44 };
    expect(reviewComment(path, line, "old", "Here.")).toBe(
      "`web/src/lib/patch.ts:40` (old line)\n```diff\n }\n```\nHere.",
    );
  });

  it("cites a line with no number by path alone", () => {
    const line = { kind: "add" as const, text: "x" };
    expect(reviewComment(path, line, "new", "Hm.")).toBe("`web/src/lib/patch.ts`\n```diff\n+x\n```\nHm.");
  });

  it("outruns a backtick run in the line with a longer fence", () => {
    const line = { kind: "add" as const, text: "const md = \"```ts\";", newNo: 3 };
    expect(reviewComment(path, line, "new", "Escape it.")).toBe(
      "`web/src/lib/patch.ts:3`\n````diff\n+const md = \"```ts\";\n````\nEscape it.",
    );
  });
});
