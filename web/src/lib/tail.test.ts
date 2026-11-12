import { describe, expect, it } from "vitest";

import { appendTail } from "./tail";

function lines(from: number, to: number): string {
  let s = "";
  for (let i = from; i <= to; i++) s += `line ${i}\n`;
  return s;
}

function codePoints(s: string): number {
  return Array.from(s).length;
}

// These mirror internal/projection/tail_test.go: both sides must cut in the
// same place.
describe("appendTail", () => {
  it.each([
    ["short output is kept whole", "", "a\nb\n", "a\nb\n"],
    ["deltas join mid-line", "build: 4", "2%\n", "build: 42%\n"],
    ["exactly the cap is kept whole", "", lines(1, 20), lines(1, 20)],
    ["one terminated line over drops the oldest", "", lines(1, 21), lines(2, 21)],
    ["an unterminated partial line counts as one", "", lines(1, 20) + "partial", lines(2, 20) + "partial"],
    ["a blank line counts as one", lines(1, 19), "\n\n", lines(2, 19) + "\n\n"],
    ["many short lines", "", "x\n".repeat(500), "x\n".repeat(20)],
    ["carriage returns are not newlines", "", "10%\r20%\r30%", "10%\r20%\r30%"],
  ])("%s", (_name, tail, delta, want) => {
    expect(appendTail(tail, delta)).toBe(want);
  });

  it("bounds one runaway line by code points, cutting on a whole one", () => {
    // A progress bar that only ever redraws with "\r" is one line forever.
    const bar = "\r[=====>    ] é漢🙂".repeat(2000);
    const got = appendTail("", bar);
    expect(codePoints(got)).toBe(4000);
    expect(bar.endsWith(got)).toBe(true);
    // Astral characters are two UTF-16 units but one code point, as the
    // server counts them: text short enough in code points is not cut.
    const wide = "🙂".repeat(4000);
    expect(appendTail("", wide)).toBe(wide);
  });

  it("agrees appending in pieces with appending at once", () => {
    // The coalescer joins consecutive deltas, so folding them joined must
    // land on the same tail as folding them one by one. A seeded generator,
    // so a failure reproduces.
    let seed = 301;
    const rand = (n: number) => {
      seed = (seed * 48271) % 2147483647;
      return seed % n;
    };
    const text = ["a", "b", "é", "漢", "🙂", "\r", " "];
    // Lines of some length, from none at all to past the char cap, so both
    // caps get hit and the joins land mid-line, mid-run of newlines, and
    // after a newline.
    const piece = () => {
      let s = "";
      const width = [0, 3, 40, 1500][rand(4)];
      for (let n = rand(30); n > 0; n--) {
        for (let w = rand(width + 1); w > 0; w--) s += text[rand(text.length)];
        if (rand(4) > 0) s += "\n";
      }
      return s;
    };
    for (let i = 0; i < 1000; i++) {
      const tail = appendTail("", piece());
      const a = piece();
      const b = piece();
      expect(appendTail(appendTail(tail, a), b)).toBe(appendTail(tail, a + b));
    }
  });
});
