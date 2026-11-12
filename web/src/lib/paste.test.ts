import { describe, expect, it } from "vitest";

import { FILE_OVER_CHARS, FILE_OVER_LINES, pastedFile, pasteKind, pasteTakesText } from "./paste";

const lines = (n: number, line = "some words") => Array(n).fill(line).join("\n");

describe("pastedFile", () => {
  it("leaves a short paste in the box", () => {
    expect(pastedFile("fix the login bug")).toBeNull();
    expect(pastedFile(lines(FILE_OVER_LINES))).toBeNull();
    expect(pastedFile("x".repeat(FILE_OVER_CHARS))).toBeNull();
  });

  it("turns a paste past either limit into a file", () => {
    expect(pastedFile(lines(FILE_OVER_LINES + 1))?.name).toBe("pasted.txt");
    expect(pastedFile("x".repeat(FILE_OVER_CHARS + 1))?.name).toBe("pasted.txt");
  });

  it("keeps the text byte for byte", async () => {
    const body = lines(40, "  indented\ttab ✓");
    expect(await pastedFile(body)!.text()).toBe(body);
  });

  it("names markdown after its first heading", () => {
    const md = "intro\n\n## Release notes, v2!\n\n" + lines(30, "- item");
    const file = pastedFile(md)!;
    expect(file.name).toBe("release-notes-v2.md");
    expect(file.type).toBe("text/markdown");
  });

  it("falls back to a plain name for markdown without a heading", () => {
    expect(pastedFile("```ts\nconst a = 1;\n```\n" + lines(30))?.name).toBe("pasted.md");
  });

  it("gives JSON its own type", () => {
    const json = JSON.stringify({ items: Array.from({ length: 40 }, (_, i) => ({ i })) }, null, 2);
    const file = pastedFile(json)!;
    expect(file.name).toBe("pasted.json");
    expect(file.type).toBe("application/json");
  });
});

describe("pasteKind", () => {
  it("takes a fence or a table as markdown on its own", () => {
    expect(pasteKind("look:\n```\nls\n```")).toBe("markdown");
    expect(pasteKind("| a | b |\n| --- | --- |\n| 1 | 2 |")).toBe("markdown");
  });

  it("needs two looser signs, so a commented script stays text", () => {
    expect(pasteKind("# install deps\nnpm ci\n# build\nnpm run build")).toBe("text");
    expect(pasteKind("# Notes\n\n- one\n- two")).toBe("markdown");
    expect(pasteKind("See [the docs](https://x.dev) and run `make`.")).toBe("markdown");
  });

  it("treats something that only starts like JSON as text", () => {
    expect(pasteKind("{ not: json at all")).toBe("text");
    expect(pasteKind("[1, 2, 3]")).toBe("json");
  });

  it("calls a plain log text", () => {
    expect(pasteKind("2026-09-30 ERROR boom\n2026-09-30 INFO retry")).toBe("text");
  });
});

describe("pasteTakesText", () => {
  const png = (name = "image.png") => new File([new Uint8Array([1])], name, { type: "image/png" });
  // A clipboard as a paste event hands it over; getData throws when there is
  // no text, so a decision that reads text it was not offered fails here.
  const clip = (files: File[], text?: string) => ({
    files,
    types: text === undefined ? ["Files"] : ["Files", "text/plain", "text/html"],
    getData: (type: string) => {
      if (text === undefined) throw new Error("no text on this clipboard");
      return type === "text/plain" ? text : "<table></table>";
    },
  });

  it("takes the text of spreadsheet cells over the picture of them", () => {
    expect(pasteTakesText(clip([png()], "Q1\t1200\nQ2\t1350\n"))).toBe(true);
  });

  it("attaches a screenshot that came with no text", () => {
    expect(pasteTakesText(clip([png("clipboard.png")]))).toBe(false);
  });

  it("attaches a copied file whose name rode along as text", () => {
    expect(pasteTakesText(clip([png("diagram.png")], "diagram.png"))).toBe(false);
    expect(pasteTakesText(clip([png("my b.png")], "file:///home/a/my%20b.png"))).toBe(false);
    expect(pasteTakesText(clip([png("50%.png")], "/tmp/50%.png"))).toBe(false);
  });

  it("attaches files when the text is only whitespace", () => {
    expect(pasteTakesText(clip([png()], "  \n\t "))).toBe(false);
  });

  it("attaches several copied files listed one name per line", () => {
    const files = [png("a.png"), new File(["x"], "notes.txt", { type: "text/plain" })];
    expect(pasteTakesText(clip(files, "a.png\r\nnotes.txt\n"))).toBe(false);
    // macOS can separate the names with a lone carriage return.
    expect(pasteTakesText(clip(files, "a.png\rnotes.txt"))).toBe(false);
  });

  it("takes the text when it names a file but says more than that", () => {
    expect(pasteTakesText(clip([png("a.png")], "a.png\nand a sentence"))).toBe(true);
  });
});
