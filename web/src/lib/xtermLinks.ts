// The glue between what a terminal buffer holds (cells in rows, some rows the
// soft-wrapped continuation of the one above) and the plain strings the link
// finder works on, and between a point on the screen and the cell under it.
// Typed against the slice of xterm's IBuffer it reads, so the tests can hand it
// a fake one.

import type { IBufferRange } from "@xterm/xterm";

import type { TerminalLink } from "~/lib/terminalLinks";

export interface LinkBufferCell {
  getChars(): string;
  getWidth(): number;
}

export interface LinkBufferLine {
  readonly isWrapped: boolean;
  readonly length: number;
  getCell(x: number): LinkBufferCell | undefined;
}

export interface LinkBuffer {
  /** 0-based, as xterm's is. */
  getLine(y: number): LinkBufferLine | undefined;
}

export interface BufferLink {
  link: TerminalLink;
  range: IBufferRange;
  text: string;
}

// How far a logical line is followed up and down from the hovered row. A
// minified bundle cat'ed to the terminal is one line over thousands of rows,
// and hover asks again on every row it crosses; a link longer than this many
// rows is not one anybody means to click.
const MAX_WRAP_ROWS = 32;

/**
 * The links that touch buffer row `y` (1-based, as xterm's link providers are
 * asked), as xterm ranges: 1-based cells, end inclusive. The rows soft-wrapped
 * together with `y` are read as the one line the program printed, so a URL
 * that wrapped is a single link spanning rows rather than two broken halves.
 *
 * The string is built cell by cell rather than with translateToString so each
 * UTF-16 offset knows its cell: a wide character (CJK, most emoji) is one or
 * two code units over two cells, and the cell after it is an empty spacer.
 * One limitation: when a wide character does not fit at the end of a row,
 * xterm leaves that last cell blank and wraps it, and the blank reads here as
 * a space, so a link broken at exactly that point ends there.
 */
export function linksAtRow(
  buffer: LinkBuffer,
  y: number,
  find: (line: string) => TerminalLink[],
): BufferLink[] {
  const row = y - 1;
  if (!buffer.getLine(row)) return [];

  let first = row;
  while (row - first < MAX_WRAP_ROWS && buffer.getLine(first)?.isWrapped && buffer.getLine(first - 1)) first--;
  let last = row;
  while (last - row < MAX_WRAP_ROWS && buffer.getLine(last + 1)?.isWrapped) last++;

  // For each code unit of the joined string: its row, its first cell, and how
  // many cells the character it belongs to covers.
  let text = "";
  const at: { y: number; x: number; width: number }[] = [];
  for (let r = first; r <= last; r++) {
    const line = buffer.getLine(r)!;
    for (let x = 0; x < line.length; x++) {
      const cell = line.getCell(x);
      const width = cell?.getWidth() ?? 1;
      if (width === 0) continue; // the spacer behind a wide character
      const chars = cell?.getChars() || " ";
      for (let i = 0; i < chars.length; i++) at.push({ y: r, x, width });
      text += chars;
    }
  }

  const links: BufferLink[] = [];
  for (const link of find(text)) {
    if (link.end <= link.start || link.end > at.length) continue;
    const start = at[link.start];
    const end = at[link.end - 1];
    if (start.y > row || end.y < row) continue;
    links.push({
      link,
      range: {
        start: { x: start.x + 1, y: start.y + 1 },
        end: { x: end.x + end.width, y: end.y + 1 },
      },
      text: text.slice(link.start, link.end),
    });
  }
  return links;
}

export interface Cell {
  /** 1-based column. */
  x: number;
  /** 1-based buffer row, scrollback included. */
  y: number;
}

/**
 * The cell under a point on the screen, or null off the grid. `screen` is the
 * rectangle of xterm's `.xterm-screen`, which is exactly cols × rows cells.
 * `viewportY` is the buffer row at the top of the viewport.
 */
export function cellAtPoint(
  screen: { left: number; top: number; width: number; height: number },
  grid: { cols: number; rows: number; viewportY: number },
  clientX: number,
  clientY: number,
): Cell | null {
  const col = Math.floor(((clientX - screen.left) / screen.width) * grid.cols);
  const row = Math.floor(((clientY - screen.top) / screen.height) * grid.rows);
  if (col < 0 || col >= grid.cols || row < 0 || row >= grid.rows) return null;
  return { x: col + 1, y: row + 1 + grid.viewportY };
}

/** Whether a range (end inclusive, possibly over several rows) covers a cell. */
export function rangeContains(range: IBufferRange, cell: Cell): boolean {
  const { start, end } = range;
  if (cell.y < start.y || cell.y > end.y) return false;
  if (cell.y === start.y && cell.x < start.x) return false;
  if (cell.y === end.y && cell.x > end.x) return false;
  return true;
}
