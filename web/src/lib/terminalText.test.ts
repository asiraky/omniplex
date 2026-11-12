import { describe, expect, it } from "vitest";

import { terminalLines } from "~/lib/terminalText";

describe("terminalLines", () => {
  it("splits on newlines and drops the empty segment after the last one", () => {
    expect(terminalLines("one\ntwo\n")).toEqual(["one", "two"]);
    expect(terminalLines("one\ntwo")).toEqual(["one", "two"]);
  });

  it("keeps blank lines between text but not after it", () => {
    expect(terminalLines("a\n\nb\n\n\n")).toEqual(["a", "", "b"]);
  });

  it("strips colour and cursor sequences", () => {
    expect(terminalLines("\x1b[32mstream line 2\x1b[0m\n")).toEqual(["stream line 2"]);
    expect(terminalLines("\x1b[1;38;5;196mred\x1b[K\x1b[2A up")).toEqual(["red up"]);
  });

  it("strips OSC sequences ended by BEL or ST, and two-character escapes", () => {
    expect(terminalLines("\x1b]0;title\x07a\x1b]8;;http://x\x1b\\link\x1b(Bb\x1b7c")).toEqual([
      "alinkbc",
    ]);
  });

  it("drops an escape cut off at the end of the tail", () => {
    expect(terminalLines("done\x1b[3")).toEqual(["done"]);
    expect(terminalLines("done\x1b")).toEqual(["done"]);
    expect(terminalLines("done\x1b]0;half a ti")).toEqual(["done"]);
  });

  it("shows a carriage-return redraw as its last frame", () => {
    expect(terminalLines("progress 50%\rprogress 100%\nnext\n")).toEqual(["progress 100%", "next"]);
  });

  it("keeps the text before a trailing carriage return, so CRLF reads as LF", () => {
    expect(terminalLines("abc\r")).toEqual(["abc"]);
    expect(terminalLines("one\r\ntwo\r\n")).toEqual(["one", "two"]);
  });

  it("strips stray control characters but keeps tabs", () => {
    expect(terminalLines("a\x07b\tc\x08")).toEqual(["ab\tc"]);
  });

  it("has nothing to show for escapes and whitespace alone", () => {
    expect(terminalLines("")).toEqual([]);
    expect(terminalLines("\x1b[0m\n  \r\n")).toEqual([]);
  });
});
