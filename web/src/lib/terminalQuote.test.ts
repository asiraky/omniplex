import { describe, expect, it } from "vitest";

import { lastTerminalLines, quoteTerminalOutput, type TerminalRow } from "./terminalQuote";

describe("quoteTerminalOutput", () => {
  it("fences the output, tidying line ends and blank edges but not indentation", () => {
    expect(quoteTerminalOutput("\r\n  \r\n  $ ls   \r\n\r\n  a.ts\t\r\n\r\n   \n")).toBe(
      "```\n  $ ls\n\n  a.ts\n```",
    );
  });

  it("outruns the longest backtick run inside with its fence", () => {
    expect(quoteTerminalOutput("use `x` or ``y``")).toBe("```\nuse `x` or ``y``\n```");
    expect(quoteTerminalOutput("```js\nx\n```")).toBe("````\n```js\nx\n```\n````");
    expect(quoteTerminalOutput("a `````")).toBe("``````\na `````\n``````");
  });

  it("gives nothing for nothing but whitespace", () => {
    expect(quoteTerminalOutput("")).toBe("");
    expect(quoteTerminalOutput(" \r\n\t\n  ")).toBe("");
  });
});

const row = (text: string, wrapped = false): TerminalRow => ({ text, wrapped });

describe("lastTerminalLines", () => {
  it("takes the last n lines, skipping the empty screen below the prompt", () => {
    const rows = [row("one"), row("two"), row("three"), row("$"), row(""), row("  ")];
    expect(lastTerminalLines(rows, 2)).toBe("three\n$");
  });

  it("counts a soft-wrapped line once, rejoined without a separator", () => {
    const rows = [row("before"), row("aaaa"), row("bbbb", true), row("cc", true), row("after")];
    expect(lastTerminalLines(rows, 2)).toBe("aaaabbbbcc\nafter");
  });

  it("keeps a blank line inside the output", () => {
    expect(lastTerminalLines([row("a"), row(""), row("b")], 3)).toBe("a\n\nb");
  });

  it("starts a line at a wrapped first row whose start scrolled away", () => {
    expect(lastTerminalLines([row("tail", true), row("next")], 5)).toBe("tail\nnext");
  });

  it("gives everything when asked for more than there is, and nothing for none", () => {
    const rows = [row("a"), row("b")];
    expect(lastTerminalLines(rows, 10)).toBe("a\nb");
    expect(lastTerminalLines(rows, 0)).toBe("");
    expect(lastTerminalLines([], 3)).toBe("");
    expect(lastTerminalLines([row(""), row(" ")], 3)).toBe("");
  });
});
