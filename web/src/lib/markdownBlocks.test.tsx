// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import { describe, expect, it } from "vitest";

import { Markdown } from "~/components/Markdown";
import { closeOpenFence, splitMarkdownBlocks } from "./markdownBlocks";
import { CHAT_PLUGINS } from "./markdownPlugins";

const html = (source: string) =>
  renderToStaticMarkup(<ReactMarkdown remarkPlugins={CHAT_PLUGINS}>{source}</ReactMarkdown>)
    .replace(/>\s+</g, "><")
    .trim();

describe("splitMarkdownBlocks", () => {
  it.each([
    "# Title\n\nFirst paragraph with `code` and **bold**.\n\nSecond [link](https://example.com).",
    "- one\n- two\n\n- loose three\n\n  continued\n\nAfter the list.",
    "1. first\n\n2. second\n\nText\n\n3) restarted",
    "```ts\nconst a = 1;\n\n\nconst b = 2;\n```\n\nafter\n\n~~~\nx\n\n~~~~\n\ny\n~~~\n\nend",
    "> quote\n\n> another quote\n\n> [!NOTE]\n> alert",
    "| a | b |\n| - | - |\n| 1 | 2 |\n\nAfter the table.",
    "para\n\n    indented code\n\nnext",
    "Setext\n===\n\nnext\n---\n\n- [ ] task\n- [x] done",
    "line one\nline two\n\nline three\nline four",
    "``` a ` b\n\nnot a fence\n```\n\ninside the fence\n\nstill inside",
    "````\n```js\n\n```\n\n````\n\nafter",
  ])("renders each block as the whole text renders it, at every streamed prefix: %j", (text) => {
    for (let end = 0; end <= text.length; end++) {
      const source = text.slice(0, end);
      const blocks = splitMarkdownBlocks(source)!;
      expect(blocks.join("")).toBe(source);
      // Chat closes an open fence before parsing, on the last block only.
      const rendered = blocks.map((b, i) => html(i === blocks.length - 1 ? closeOpenFence(b) : b));
      expect(rendered.join("")).toBe(html(closeOpenFence(source)));
    }
  });

  it("splits code that only looks like document-wide syntax", () => {
    const text =
      "Code:\n\n```tsx\n<div>hi</div>\n\n[ref]: /x\n$$\n```\n\nA `/[^a-z]/` regex.\n\nend";
    const blocks = splitMarkdownBlocks(text)!;
    expect(blocks).toEqual([
      "Code:\n\n",
      "```tsx\n<div>hi</div>\n\n[ref]: /x\n$$\n```\n\n",
      "A `/[^a-z]/` regex.\n\n",
      "end",
    ]);
    expect(blocks.map(html).join("")).toBe(html(text));
  });

  it("splits only between closed blocks", () => {
    expect(splitMarkdownBlocks("a\n\n```\nb\n\nc\n```\n\n- d\n\n- e\n\nf")).toEqual([
      "a\n\n",
      "```\nb\n\nc\n```\n\n- d\n\n- e\n\n",
      "f",
    ]);
  });

  it.each([
    "[ref]\n\n[ref]: /later",
    "[ref]\n\n> [ref]: /quoted",
    "[ref]\n\n- item\n\n  [ref]: /listed",
    "[ref]\n\n1. [ref]: /numbered",
    "a[^x]\n\n[^x]: note",
    "<details>\n\nbody\n\n</details>",
    ":::note\n\ncontent\n\n:::",
    "$$\na\n\nb\n$$",
    "a\r\n\r\nb",
  ])("keeps text with document-wide syntax whole: %j", (text) => {
    expect(splitMarkdownBlocks(text)).toBeNull();
  });
});

describe("closeOpenFence", () => {
  it.each([
    ["```ts\nconst a", "```ts\nconst a\n```"],
    ["````\n```\nx", "````\n```\nx\n````"],
    ["~~~\nx\n~~~", "~~~\nx\n~~~"],
    ["``` a ` b\nx", "``` a ` b\nx"],
    ["plain", "plain"],
  ])("closes only a fence left open: %j", (text, closed) => {
    expect(closeOpenFence(text)).toBe(closed);
  });
});

describe("Markdown", () => {
  // The streaming caret hangs off the container's last child, and block
  // spacing off `first:` and `last:`, so split blocks must not be wrapped.
  it("renders split blocks as direct children of the container", () => {
    const markup = renderToStaticMarkup(<Markdown text={"# Title\n\npara\n\n- item\n\n```\ncode"} />);
    const root = new DOMParser().parseFromString(markup, "text/html").body.firstElementChild!;
    expect([...root.children].map((c) => c.tagName)).toEqual(["H1", "P", "UL", "DIV"]);
    expect(root.lastElementChild!.querySelector("pre")!.textContent).toBe("code");
  });
});
