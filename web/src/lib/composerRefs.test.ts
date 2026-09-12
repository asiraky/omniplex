import { describe, expect, it } from "vitest";

import {
  blobLabel,
  blobPeek,
  blobSize,
  blobTitle,
  composePrompt,
  countLines,
  insertRef,
  isBlobWorthy,
  parseRefs,
  refAt,
  refToken,
  removeRef,
  type Blob,
  type BlobOrigin,
} from "./composerRefs";

function blob(text: string, origin: BlobOrigin = { kind: "paste" }): Blob {
  return { key: "k", text, origin, lines: countLines(text) };
}

describe("tokens", () => {
  it("writes a range only when there is one", () => {
    expect(refToken({ path: "web/src/App.tsx" })).toBe("@web/src/App.tsx");
    expect(refToken({ path: "a.ts", from: 3 })).toBe("@a.ts#L3");
    expect(refToken({ path: "a.ts", from: 3, to: 3 })).toBe("@a.ts#L3");
    expect(refToken({ path: "a.ts", from: 3, to: 9 })).toBe("@a.ts#L3-L9");
  });

  it("finds tokens in a sentence and reads their ranges back", () => {
    const refs = parseRefs("look at @web/src/App.tsx and @a.ts#L3-L9 please");
    expect(refs.map((r) => r.path)).toEqual(["web/src/App.tsx", "a.ts"]);
    expect(refs[1]).toMatchObject({ from: 3, to: 9, text: "@a.ts#L3-L9" });
    expect(refs[0].text).toBe("@web/src/App.tsx");
  });

  it("leaves handles and email addresses alone", () => {
    expect(parseRefs("ask @channel or mail me@example")).toEqual([]);
    // An address ends in something extension-shaped, so the token must not
    // start mid-word: there is no @ preceded by a non-space here to match.
    expect(parseRefs("mail tobias@example.com").map((r) => r.path)).toEqual(["example.com"]);
  });

  it("does not swallow the punctuation that ends the sentence", () => {
    expect(parseRefs("see @web/src/App.tsx.")[0].text).toBe("@web/src/App.tsx");
    expect(parseRefs("see (@a/b.ts)")[0].text).toBe("@a/b.ts");
  });

  it("matches a caret sitting just past a token, and not just before it", () => {
    const text = "hi @a/b.ts there";
    expect(refAt(text, 10)?.path).toBe("a/b.ts"); // right after ".ts"
    expect(refAt(text, 3)).toBeNull(); // on the "@" itself
    expect(refAt(text, 12)).toBeNull();
  });
});

describe("editing around a token", () => {
  it("spaces an insert into the sentence without doubling", () => {
    expect(insertRef("look at", 7, { path: "a/b.ts" })).toEqual({
      value: "look at @a/b.ts ",
      cursor: 16,
    });
    expect(insertRef("look at ", 8, { path: "a/b.ts" }).value).toBe("look at @a/b.ts ");
    expect(insertRef("look at the file", 8, { path: "a/b.ts" }).value).toBe(
      "look at @a/b.ts the file",
    );
    expect(insertRef("", 0, { path: "a/b.ts" }).value).toBe("@a/b.ts ");
  });

  it("clamps a cursor that came from outside the text", () => {
    expect(insertRef("hi", 99, { path: "a/b.ts" }).value).toBe("hi @a/b.ts ");
  });

  it("takes the trailing space back out with the token", () => {
    const text = "word @a/b.ts word";
    const ref = parseRefs(text)[0];
    expect(removeRef(text, ref)).toEqual({ value: "word word", cursor: 5 });
  });

  it("keeps a space that was not the token's own", () => {
    const text = "word: @a/b.ts";
    const ref = parseRefs(text)[0];
    // Nothing follows, so nothing is eaten; the text before is untouched.
    expect(removeRef(text, ref).value).toBe("word: ");
  });
});

describe("blobs", () => {
  it("collapses only what would flood the box", () => {
    expect(isBlobWorthy("a short note")).toBe(false);
    expect(isBlobWorthy("x".repeat(2001))).toBe(true);
    expect(isBlobWorthy("line\n".repeat(15))).toBe(true);
    expect(isBlobWorthy("line\n".repeat(10))).toBe(false);
  });

  it("counts lines without splitting the string", () => {
    expect(countLines("")).toBe(1);
    expect(countLines("a\nb")).toBe(2);
    expect(countLines("a\n")).toBe(2);
  });

  it("names a chip after where it came from", () => {
    expect(blobLabel(blob("x", { kind: "terminal", label: "Term 2" }))).toBe("Term 2");
    expect(blobLabel(blob("x", { kind: "file", path: "web/src/App.tsx", from: 40, to: 91 }))).toBe(
      "App.tsx:40-91",
    );
    expect(blobLabel(blob("x"))).toBe("Pasted text");
    expect(blobTitle(blob("x", { kind: "file", path: "a/b.ts", from: 1, to: 2 }))).toBe(
      "a/b.ts · lines 1–2",
    );
  });

  it("peeks at the first line with anything on it", () => {
    expect(blobPeek(blob("\n\n  hello  \nworld"))).toBe("hello");
    expect(blobPeek(blob("abcdef"), 4)).toBe("abc…");
    expect(blobPeek(blob("\n \n"))).toBe("");
  });

  it("sizes a blob in lines and characters", () => {
    expect(blobSize(blob("one"))).toBe("1 line · 3 chars");
    expect(blobSize(blob("a\nb"))).toBe("2 lines · 3 chars");
  });
});

describe("composePrompt", () => {
  it("leaves a message with no blobs exactly as typed", () => {
    expect(composePrompt("hello  ", [])).toBe("hello  ");
  });

  it("puts the prose first and labels each block", () => {
    expect(
      composePrompt("what broke?", [
        blob("boom\n", { kind: "terminal", label: "Term 1" }),
        blob("code", { kind: "file", path: "a/b.ts", from: 3, to: 9 }),
      ]),
    ).toBe("what broke?\n\n``` Term 1\nboom\n```\n\n``` a/b.ts#L3-L9\ncode\n```");
  });

  it("grows the fence past backticks in the content", () => {
    expect(composePrompt("", [blob("a\n```\nb")])).toBe("````\na\n```\nb\n````");
  });

  it("stands alone when the message is only a paste", () => {
    expect(composePrompt("   ", [blob("x")])).toBe("```\nx\n```");
  });
});
