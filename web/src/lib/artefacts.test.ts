// @vitest-environment jsdom
import { describe, expect, it } from "vitest";

import {
  badgeLabel,
  bundlePath,
  formatBytes,
  hasSourceView,
  parseAttachedFiles,
  rawUrl,
  readsText,
  retoken,
  typeFamily,
  viewerFor,
} from "~/lib/artefacts";

const trailer = (...lines: string[]) => `\n\n<attached-files>\n${lines.join("\n")}\n</attached-files>`;

describe("parseAttachedFiles", () => {
  it("splits the trailer off and reads every file in it", () => {
    const text =
      "Summarise these" +
      trailer(
        "- report.pdf (application/pdf, 1.2 MB, artefact 3f2a9c): /home/me/Omniplex/acme/uploads/report.pdf",
        "- data.csv (text/csv, 812 B, artefact 77b0): /tmp/x/data.csv",
      );
    expect(parseAttachedFiles(text)).toEqual({
      text: "Summarise these",
      files: [
        { name: "report.pdf", mediaType: "application/pdf", artefactId: "3f2a9c" },
        { name: "data.csv", mediaType: "text/csv", artefactId: "77b0" },
      ],
    });
  });

  it("keeps parentheses and spaces that belong to the name", () => {
    const text = trailer("- Q3 report (final) v2.pdf (application/pdf, 3 MB, artefact abc): /p/Q3 report (final) v2.pdf");
    expect(parseAttachedFiles(text)).toEqual({
      text: "",
      files: [{ name: "Q3 report (final) v2.pdf", mediaType: "application/pdf", artefactId: "abc" }],
    });
  });

  it("tolerates trailing whitespace after the block", () => {
    const text = "hi" + trailer("- a.txt (text/plain, 1 B, artefact x): /a.txt") + "\n  \n";
    expect(parseAttachedFiles(text).text).toBe("hi");
    expect(parseAttachedFiles(text).files).toHaveLength(1);
  });

  it("leaves text alone when the block is not at the end", () => {
    const text = "see" + trailer("- a.txt (text/plain, 1 B, artefact x): /a.txt") + "\nand then more words";
    expect(parseAttachedFiles(text)).toEqual({ text, files: [] });
  });

  it("leaves a message alone that only mentions the tag", () => {
    const text = "what does <attached-files></attached-files>";
    expect(parseAttachedFiles(text)).toEqual({ text, files: [] });
  });

  it("leaves a block with nothing it can read as the author's text", () => {
    const text = "look" + trailer("some notes of my own");
    expect(parseAttachedFiles(text)).toEqual({ text, files: [] });
  });

  it("skips lines it cannot read but keeps the ones it can", () => {
    const text = "x" + trailer("- garbage", "- b.md (text/markdown, 2 KB, artefact q): /b.md");
    expect(parseAttachedFiles(text)).toEqual({
      text: "x",
      files: [{ name: "b.md", mediaType: "text/markdown", artefactId: "q" }],
    });
  });

  it("takes the last block when the text quotes an earlier one", () => {
    const earlier = trailer("- old.txt (text/plain, 1 B, artefact o): /old.txt");
    const text = `quoting:${earlier}\nplease redo` + trailer("- new.txt (text/plain, 1 B, artefact n): /new.txt");
    const out = parseAttachedFiles(text);
    expect(out.files.map((f) => f.artefactId)).toEqual(["n"]);
    expect(out.text).toBe(`quoting:${earlier}\nplease redo`);
  });
});

describe("viewerFor", () => {
  it.each([
    ["index.html", "text/html", "html"],
    ["README.md", "application/octet-stream", "markdown"],
    ["notes", "text/markdown", "markdown"],
    ["chart.svg", "image/svg+xml", "svg"],
    ["photo.jpg", "image/jpeg", "image"],
    ["frame.avif", "image/avif", "image"],
    ["shot.heic", "image/heic", "fallback"],
    ["clip.mp3", "audio/mpeg", "audio"],
    ["clip.webm", "video/webm", "video"],
    ["paper.pdf", "application/pdf", "pdf"],
    ["rows.csv", "text/csv", "csv"],
    ["rows.tsv", "text/plain", "csv"],
    ["data.json", "application/json", "json"],
    ["thing", "application/vnd.api+json", "json"],
    // Browsers call TypeScript an MPEG transport stream; the name knows better.
    ["main.ts", "video/mp2t", "text"],
    ["config.yaml", "application/octet-stream", "text"],
    ["build.log", "", "text"],
    ["noext", "text/plain; charset=utf-8", "text"],
    ["noext", "application/xml", "text"],
    ["deck.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "fallback"],
    ["archive.zip", "application/zip", "fallback"],
    ["blob", "application/octet-stream", "fallback"],
  ])("%s (%s) → %s", (entry, mediaType, kind) => {
    expect(viewerFor(entry, mediaType)).toBe(kind);
  });

  it("offers a source view only where a rendered form differs from the text", () => {
    expect(["html", "markdown", "svg", "csv", "json"].every((k) => hasSourceView(k as never))).toBe(true);
    expect(["image", "pdf", "text", "audio", "video", "fallback"].some((k) => hasSourceView(k as never))).toBe(false);
  });

  it("reads HTML and SVG as text only for their source", () => {
    expect(readsText("html", "preview")).toBe(false);
    expect(readsText("html", "source")).toBe(true);
    expect(readsText("svg", "preview")).toBe(false);
    expect(readsText("markdown", "preview")).toBe(true);
    expect(readsText("pdf", "source")).toBe(false);
  });
});

describe("typeFamily and badgeLabel", () => {
  it.each([
    ["site.html", "text/html", "html"],
    ["report.pdf", "application/pdf", "doc"],
    ["notes.md", "text/markdown", "doc"],
    ["pic.png", "image/png", "image"],
    ["logo.svg", "image/svg+xml", "image"],
    ["song.flac", "audio/flac", "media"],
    ["sheet.xlsx", "application/octet-stream", "data"],
    ["rows.csv", "text/csv", "data"],
    ["main.go", "text/x-go", "code"],
    ["archive.zip", "application/zip", "other"],
  ])("%s → %s", (name, mediaType, family) => {
    expect(typeFamily(name, mediaType)).toBe(family);
  });

  it("labels by extension, then by media subtype, then generically", () => {
    expect(badgeLabel("report.final.pdf", "application/pdf")).toBe("PDF");
    expect(badgeLabel("Makefile", "text/x-makefile")).toBe("MAKEF");
    expect(badgeLabel("blob", "application/octet-stream")).toBe("FILE");
    expect(badgeLabel("blob", "")).toBe("FILE");
  });
});

describe("urls", () => {
  it("encodes each path segment but keeps a folder's directories", () => {
    expect(rawUrl("s 1", "a/1", "sub dir/p#1.html")).toBe("/api/threads/s%201/artefacts/a%2F1/f/sub%20dir/p%231.html");
    expect(rawUrl("s", "a", "x.pdf", { rev: 5, download: true })).toBe("/api/threads/s/artefacts/a/f/x.pdf?m=5&download=1");
  });

  it("moves a page onto a fresh token and keeps where the reader was", () => {
    expect(retoken("http://h/p/OLD/docs/b.html?q=1#s", "/p/NEW/index.html")).toBe("http://h/p/NEW/docs/b.html?q=1#s");
    // Somewhere that is not a preview page: start over at the fresh one.
    expect(retoken("https://example.com/", "/p/NEW/index.html")).toBe("/p/NEW/index.html");
  });

  it("shows a preview page as its path inside the folder", () => {
    expect(bundlePath(`${window.location.origin}/p/tok123/docs/a%20b.html#top`)).toBe("/docs/a b.html#top");
    expect(bundlePath("/p/tok/index.html")).toBe("/index.html");
    expect(bundlePath("https://example.com/p/tok/x.html")).toBe("https://example.com/p/tok/x.html");
  });
});

describe("formatBytes", () => {
  it("scales through the units", () => {
    expect(formatBytes(812)).toBe("812 B");
    expect(formatBytes(1234)).toBe("1.2 KB");
    expect(formatBytes(45_600)).toBe("46 KB");
    expect(formatBytes(1_200_000)).toBe("1.2 MB");
    expect(formatBytes(3_000_000_000)).toBe("3 GB");
  });
});
