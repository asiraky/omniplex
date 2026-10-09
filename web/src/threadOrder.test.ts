import { describe, expect, it } from "vitest";

import { byPosition, dropPosition, positionBetween, withPositions } from "./threadOrder";
import type { ThreadMeta } from "~/protocol";

const thread = (id: string, position: number, createdAt = 0) =>
  ({ id, position, createdAt }) as ThreadMeta;
const ids = (list: ThreadMeta[]) => list.map((s) => s.id);

describe("byPosition", () => {
  it("sorts smallest position first", () => {
    expect(ids(byPosition([thread("b", 2), thread("a", -1), thread("c", 2.5)]))).toEqual([
      "a",
      "b",
      "c",
    ]);
  });

  it("hands back the same list when it is already in order", () => {
    const list = [thread("a", 0), thread("b", 1)];
    expect(byPosition(list)).toBe(list);
  });

  it("breaks ties the way the server does: newest created, then id", () => {
    expect(ids(byPosition([thread("old", 1, 1), thread("a", 1, 5), thread("b", 1, 5)]))).toEqual([
      "b",
      "a",
      "old",
    ]);
  });
});

describe("positionBetween", () => {
  it("takes the midpoint of two neighbours", () => {
    expect(positionBetween(1, 2)).toBe(1.5);
  });

  it("steps past the end when dropped at the top or the bottom", () => {
    expect(positionBetween(undefined, -3)).toBe(-4);
    expect(positionBetween(7, undefined)).toBe(8);
  });

  it("starts at zero in an empty list", () => {
    expect(positionBetween()).toBe(0);
  });
});

describe("dropPosition", () => {
  const rows = [thread("a", 0), thread("b", 1), thread("c", 2), thread("d", 3)];

  it("lands between the rows it was dropped between", () => {
    // d dropped at index 1 among a, b, c: between a and b.
    expect(dropPosition(rows, "d", 1)).toBe(0.5);
    // a dropped at index 2 among b, c, d: between c and d.
    expect(dropPosition(rows, "a", 2)).toBe(2.5);
  });

  it("goes above the top row and below the bottom one", () => {
    expect(dropPosition(rows, "c", 0)).toBe(-1);
    expect(dropPosition(rows, "a", 3)).toBe(4);
  });

  it("is no move at all when it goes back where it was", () => {
    expect(dropPosition(rows, "b", 1)).toBeNull();
  });

  it("is no move for a row that is not in the list", () => {
    expect(dropPosition(rows, "ghost", 0)).toBeNull();
  });

  it("only looks at the rows it was given, wherever they sit globally", () => {
    // One project's rows inside a list where other projects sit between them:
    // the midpoint of these neighbours is all that matters.
    const group = [thread("x", 0), thread("y", 10), thread("z", 20)];
    expect(dropPosition(group, "z", 1)).toBe(5);
  });

  it("orders a moved row where it was dropped once applied", () => {
    const position = dropPosition(rows, "d", 1)!;
    expect(ids(withPositions(rows, new Map([["d", position]])))).toEqual(["a", "d", "b", "c"]);
  });
});

describe("withPositions", () => {
  it("applies moves ahead of the server and sorts", () => {
    const list = [thread("a", 0), thread("b", 1), thread("c", 2)];
    expect(ids(withPositions(list, new Map([["c", -1]])))).toEqual(["c", "a", "b"]);
  });

  it("ignores moves for threads that are gone", () => {
    const list = [thread("a", 0), thread("b", 1)];
    expect(ids(withPositions(list, new Map([["ghost", -1]])))).toEqual(["a", "b"]);
  });

  it("puts a list that arrived out of order into position order", () => {
    expect(ids(withPositions([thread("b", 1), thread("a", 0)], new Map()))).toEqual(["a", "b"]);
  });
});
