import { describe, expect, it } from "vitest";

import { dropIndex, edgeScroll, rowShift } from "./rowDrag";

// Four 50px rows: middles at 25, 75, 125, 175.
const centers = [25, 75, 125, 175];

describe("dropIndex", () => {
  it("stays put until the dragged row passes a neighbour's middle", () => {
    expect(dropIndex(centers, 1, 75)).toBe(1);
    expect(dropIndex(centers, 1, 120)).toBe(1);
    expect(dropIndex(centers, 1, 130)).toBe(2);
  });

  it("goes to the top above every row, and the bottom below every row", () => {
    expect(dropIndex(centers, 2, -40)).toBe(0);
    expect(dropIndex(centers, 0, 400)).toBe(3);
  });

  it("reaches either end slot when held level with the end row", () => {
    expect(dropIndex(centers, 0, 175)).toBe(3);
    expect(dropIndex(centers, 3, 25)).toBe(0);
  });

  it("moves up past a row whose middle it has crossed", () => {
    expect(dropIndex(centers, 3, 70)).toBe(1);
  });
});

describe("rowShift", () => {
  // Row 1 dragged down to index 2 among the others: row 2 slides up into
  // its slot, and rows 0 and 3 stay.
  it("slides the rows it passed going down up by one slot", () => {
    expect([0, 2, 3].map((i) => rowShift(i, 1, 2, 50))).toEqual([0, -50, 0]);
  });

  it("slides the rows it passed going up down by one slot", () => {
    expect([0, 1, 2].map((i) => rowShift(i, 3, 1, 50))).toEqual([0, 50, 50]);
  });

  it("moves nothing while the row is still in its own slot", () => {
    expect([0, 2, 3].map((i) => rowShift(i, 1, 1, 50))).toEqual([0, 0, 0]);
  });

  it("agrees with dropIndex on where the gap opens", () => {
    // Row 0 dragged to the bottom: everything else closes up.
    const to = dropIndex(centers, 0, 400);
    expect([1, 2, 3].map((i) => rowShift(i, 0, to, 50))).toEqual([-50, -50, -50]);
  });
});

describe("edgeScroll", () => {
  it("does nothing away from the edges", () => {
    expect(edgeScroll(300, 0, 600)).toBe(0);
  });

  it("scrolls up near the top and down near the bottom, faster the closer", () => {
    const near = edgeScroll(40, 0, 600);
    const nearer = edgeScroll(5, 0, 600);
    expect(near).toBeLessThan(0);
    expect(nearer).toBeLessThan(near);
    expect(edgeScroll(590, 0, 600)).toBeGreaterThan(0);
  });

  it("does not run away when the pointer leaves the scroller", () => {
    expect(edgeScroll(-500, 0, 600)).toBe(edgeScroll(0, 0, 600));
    expect(edgeScroll(5000, 0, 600)).toBe(edgeScroll(600, 0, 600));
  });
});
