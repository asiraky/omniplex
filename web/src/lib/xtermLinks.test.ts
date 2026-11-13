import { describe, expect, it } from "vitest";

import type { TerminalLink } from "~/lib/terminalLinks";

import { cellAtPoint, linksAtRow, rangeContains, type LinkBufferLine } from "./xtermLinks";

// A row of cells as xterm lays them out: a wide character takes its cell and
// leaves an empty, zero-width spacer after it. Cells past the text are blank.
function row(text: string, cols: number, wrapped = false): LinkBufferLine {
  const cells: { chars: string; width: number }[] = [];
  for (const ch of text) {
    const wide = /[　-鿿\u{1f300}-\u{1faff}]/u.test(ch);
    cells.push({ chars: ch, width: wide ? 2 : 1 });
    if (wide) cells.push({ chars: "", width: 0 });
  }
  if (cells.length > cols) throw new Error(`row wider than ${cols}: ${text}`);
  while (cells.length < cols) cells.push({ chars: "", width: 1 });
  return {
    isWrapped: wrapped,
    length: cols,
    getCell: (x) => {
      const c = cells[x];
      return c && { getChars: () => c.chars, getWidth: () => c.width };
    },
  };
}

function buffer(...lines: LinkBufferLine[]) {
  return { getLine: (y: number) => lines[y] };
}

// Every run of "http…" up to a space, so the tests exercise the mapping and not
// the sibling finder's rules.
function findUrls(line: string): TerminalLink[] {
  return [...line.matchAll(/https?:\/\/\S+/g)].map((m) => ({
    kind: "url",
    start: m.index,
    end: m.index + m[0].length,
    url: m[0],
  }));
}

describe("linksAtRow", () => {
  it("maps a link on one row to 1-based cells with an inclusive end", () => {
    const buf = buffer(row("see http://a.io now", 30));
    const [hit] = linksAtRow(buf, 1, findUrls);
    expect(hit.range).toEqual({ start: { x: 5, y: 1 }, end: { x: 15, y: 1 } });
    expect(hit.text).toBe("http://a.io");
  });

  it("joins soft-wrapped rows so a wrapped URL is one link from either row", () => {
    const buf = buffer(row("x http://exa", 12), row("mple.com/p y", 12, true));
    const expected = { start: { x: 3, y: 1 }, end: { x: 10, y: 2 } };
    expect(linksAtRow(buf, 1, findUrls).map((l) => l.range)).toEqual([expected]);
    expect(linksAtRow(buf, 2, findUrls).map((l) => l.range)).toEqual([expected]);
    expect(linksAtRow(buf, 2, findUrls)[0].text).toBe("http://example.com/p");
  });

  it("leaves out links on other rows of the same logical line", () => {
    const buf = buffer(row("http://a.io ", 12), row("           ", 12, true), row("http://b.io", 12, true));
    expect(linksAtRow(buf, 3, findUrls).map((l) => l.text)).toEqual(["http://b.io"]);
    expect(linksAtRow(buf, 2, findUrls)).toEqual([]);
  });

  it("does not join a row that starts a new line", () => {
    const buf = buffer(row("http://a.io", 11), row("/more", 11));
    expect(linksAtRow(buf, 1, findUrls).map((l) => l.text)).toEqual(["http://a.io"]);
  });

  it("counts wide characters as two cells", () => {
    // 日本 takes cells 1-4, then a space, so the link starts in cell 6.
    const buf = buffer(row("日本 http://a.io", 20));
    expect(linksAtRow(buf, 1, findUrls)[0].range).toEqual({
      start: { x: 6, y: 1 },
      end: { x: 16, y: 1 },
    });
  });

  it("covers both cells of a wide character at the end of a link", () => {
    const buf = buffer(row("http://a.io/日 x", 20));
    expect(linksAtRow(buf, 1, findUrls)[0].range.end).toEqual({ x: 14, y: 1 });
  });

  it("keeps offsets right after an emoji, which is two code units", () => {
    const buf = buffer(row("🎉 http://a.io", 20));
    const [hit] = linksAtRow(buf, 1, findUrls);
    expect(hit.text).toBe("http://a.io");
    expect(hit.range.start).toEqual({ x: 4, y: 1 });
  });

  it("returns nothing for a row past the buffer", () => {
    expect(linksAtRow(buffer(row("http://a.io", 11)), 5, findUrls)).toEqual([]);
  });

  it("ignores a range the finder reports past the line", () => {
    const bogus = (): TerminalLink[] => [{ kind: "url", start: 0, end: 999, url: "x" }];
    expect(linksAtRow(buffer(row("abc", 5)), 1, bogus)).toEqual([]);
  });
});

describe("cellAtPoint", () => {
  const screen = { left: 10, top: 20, width: 100, height: 50 }; // 10 × 5 cells of 10px
  const grid = { cols: 10, rows: 5, viewportY: 40 };

  it("finds the cell under a point, offset by the scrollback above the viewport", () => {
    expect(cellAtPoint(screen, grid, 10, 20)).toEqual({ x: 1, y: 41 });
    expect(cellAtPoint(screen, grid, 35, 44)).toEqual({ x: 3, y: 43 });
    expect(cellAtPoint(screen, grid, 109.9, 69.9)).toEqual({ x: 10, y: 45 });
  });

  it("is null off the grid", () => {
    expect(cellAtPoint(screen, grid, 9, 30)).toBeNull();
    expect(cellAtPoint(screen, grid, 110, 30)).toBeNull();
    expect(cellAtPoint(screen, grid, 50, 70)).toBeNull();
  });
});

describe("rangeContains", () => {
  const range = { start: { x: 8, y: 2 }, end: { x: 3, y: 4 } };

  it("covers from the start cell to the end cell inclusive across rows", () => {
    expect(rangeContains(range, { x: 8, y: 2 })).toBe(true);
    expect(rangeContains(range, { x: 1, y: 3 })).toBe(true);
    expect(rangeContains(range, { x: 3, y: 4 })).toBe(true);
  });

  it("excludes cells before the start, after the end, and on other rows", () => {
    expect(rangeContains(range, { x: 7, y: 2 })).toBe(false);
    expect(rangeContains(range, { x: 4, y: 4 })).toBe(false);
    expect(rangeContains(range, { x: 5, y: 1 })).toBe(false);
    expect(rangeContains(range, { x: 5, y: 5 })).toBe(false);
  });
});
