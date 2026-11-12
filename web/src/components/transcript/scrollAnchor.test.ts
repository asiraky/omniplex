// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { captureAnchor, restoreScroll } from "./scrollAnchor";

// jsdom lays nothing out, so the transcript's geometry is stated outright:
// rows stacked with no gap in a 400px window, each row a `display: contents`
// wrapper around one body whose layout box is what gets measured, and a tail
// of padding below the last row like the real one leaves for the composer.
const VIEW = 400;
const TAIL = 500;

function transcript(heights: number[]) {
  const sizes = [...heights];
  const topOf = (i: number) => sizes.slice(0, i).reduce((a, h) => a + h, 0);
  const scroller = document.createElement("div");
  const content = document.createElement("div");
  scroller.append(content);
  sizes.forEach((_, i) => {
    const row = document.createElement("div");
    row.setAttribute("data-row-id", `r${i}`);
    const body = document.createElement("div");
    Object.defineProperty(body, "offsetTop", { get: () => topOf(i) });
    Object.defineProperty(body, "offsetHeight", { get: () => sizes[i] });
    row.append(body);
    content.append(row);
  });
  const height = () => topOf(sizes.length) + TAIL;
  let top = 0;
  Object.defineProperty(scroller, "scrollHeight", { get: height });
  Object.defineProperty(scroller, "clientHeight", { value: VIEW });
  Object.defineProperty(scroller, "scrollTop", {
    get: () => top,
    set: (v: number) => {
      top = Math.max(0, Math.min(v, height() - VIEW));
    },
  });
  document.body.append(scroller);
  return {
    scroller,
    resize: (i: number, h: number) => {
      sizes[i] = h;
    },
    remove: (i: number) => scroller.querySelector(`[data-row-id="r${i}"]`)!.remove(),
    // The row stays; what it rendered goes, leaving it nothing to measure.
    empty: (i: number) => scroller.querySelector(`[data-row-id="r${i}"]`)!.replaceChildren(),
  };
}

// Frames run when the test says so, each moving the clock on by one frame's
// worth — 60Hz unless the test is standing in for a faster screen.
let frames: Map<number, FrameRequestCallback>;
let nextFrame: number;
let clock: number;
const frame = (ms = 16) => {
  clock += ms;
  const due = [...frames.values()];
  frames.clear();
  for (const cb of due) cb(clock);
};
const pending = () => frames.size;

beforeEach(() => {
  frames = new Map();
  nextFrame = 1;
  clock = 0;
  vi.spyOn(performance, "now").mockImplementation(() => clock);
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => {
    frames.set(nextFrame, cb);
    return nextFrame++;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

describe("capturing the row being read", () => {
  it("takes the first row still showing at the top edge, and how far into it the edge is", () => {
    const { scroller } = transcript([300, 300, 300, 300]);
    scroller.scrollTop = 450;
    expect(captureAnchor(scroller)).toEqual({ id: "r1", offset: 150 });
  });

  it("passes over a row whose bottom sits exactly on the edge — none of it is showing", () => {
    const { scroller } = transcript([300, 300, 300]);
    scroller.scrollTop = 300;
    expect(captureAnchor(scroller)).toEqual({ id: "r1", offset: 0 });
  });

  it("holds a view below every row relative to the last one", () => {
    const { scroller } = transcript([100, 100]);
    scroller.scrollTop = 250;
    expect(captureAnchor(scroller)).toEqual({ id: "r1", offset: 150 });
  });

  it("has nothing to say about a transcript with no rows", () => {
    const { scroller } = transcript([]);
    expect(captureAnchor(scroller)).toBeUndefined();
  });
});

describe("restoring by row", () => {
  it("lands on the same row and offset after a row above has grown, not the same pixel", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    scroller.scrollTop = 650;
    const position = { top: 650, atBottom: false, anchor: captureAnchor(scroller) };
    scroller.scrollTop = 0;

    // A diagram above the reader rendered in the meantime.
    resize(0, 500);
    restoreScroll(scroller, position);

    expect(scroller.scrollTop).toBe(850);
  });

  it("falls back to the raw offset when the row is gone", () => {
    const { scroller, remove } = transcript([300, 300, 300, 300]);
    remove(2);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });
    expect(scroller.scrollTop).toBe(650);
  });

  it("falls back to the raw offset when the row is there but rendered nothing to measure", () => {
    const { scroller, empty } = transcript([300, 300, 300, 300]);
    empty(2);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });
    expect(scroller.scrollTop).toBe(650);
    expect(pending()).toBe(0);
  });

  it("restores a position with no row to go by, such as an empty transcript's, by its offset", () => {
    const { scroller } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 420, atBottom: false });
    expect(scroller.scrollTop).toBe(420);
    expect(pending()).toBe(0);
  });

  it("follows the row while content above it is still arriving, then stops", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });

    resize(1, 450);
    frame();
    expect(scroller.scrollTop).toBe(800);

    // A diagram that waits a few frames before rendering is still caught.
    for (let i = 0; i < 12; i++) frame();
    resize(0, 400);
    frame();
    expect(scroller.scrollTop).toBe(900);

    // Then, once it has held still for a while, it lets go.
    for (let i = 0; i < 60 && pending(); i++) frame();
    expect(pending()).toBe(0);
  });

  it("waits as long on a fast screen as a slow one for content above to land", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });

    // At 120Hz, a diagram that renders a third of a second later — well past
    // the point a frame count tuned for 60Hz would have run out.
    while (clock < 330) frame(8);
    resize(0, 500);
    frame(8);
    expect(scroller.scrollTop).toBe(850);
  });

  it("holds still for the same stretch of time whatever the frame rate", () => {
    const settle = (ms: number) => {
      const { scroller } = transcript([300, 300, 300, 300]);
      clock = 0;
      restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });
      while (pending()) frame(ms);
      document.body.innerHTML = "";
      return clock;
    };
    const slow = settle(16);
    const fast = settle(8);
    expect(Math.abs(slow - fast)).toBeLessThanOrEqual(16);
  });

  it("gives up after the same stretch of time whatever the frame rate when the row never holds still", () => {
    const chase = (ms: number) => {
      const { scroller, resize } = transcript([300, 300, 300, 300]);
      clock = 0;
      restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });
      let grown = 300;
      for (let i = 0; i < 10_000 && pending(); i++) {
        resize(0, (grown += 1));
        frame(ms);
      }
      expect(pending()).toBe(0);
      document.body.innerHTML = "";
      return clock;
    };
    const slow = chase(16);
    const fast = chase(8);
    expect(Math.abs(slow - fast)).toBeLessThanOrEqual(16);
  });

  it("gives up after a bounded time when the row never holds still", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });
    let grown = 300;
    for (let i = 0; i < 1000 && pending(); i++) {
      resize(0, (grown += 10));
      frame();
    }
    expect(pending()).toBe(0);
  });

  it("lets go the moment a reader touches the view", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });

    scroller.dispatchEvent(new Event("touchstart"));
    resize(0, 500);
    frame();

    expect(scroller.scrollTop).toBe(650);
  });

  it("yields to anything else that moves the view, such as the pin taking the tail", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });

    const bottom = scroller.scrollHeight - VIEW;
    scroller.scrollTop = bottom;
    resize(0, 500);
    frame();

    expect(scroller.scrollTop).toBe(bottom);
    expect(pending()).toBe(0);
  });

  it("does not mistake an older page landing above for a rival", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });

    // The prepend correction moves the view by exactly what landed above it.
    resize(0, 700);
    scroller.scrollTop += 400;
    frame();
    // So the settle is still running, and still holds the row through what follows.
    resize(1, 400);
    frame();

    expect(scroller.scrollTop).toBe(1150);
  });

  it("stops when the row it is holding leaves", () => {
    const { scroller, resize, remove } = transcript([300, 300, 300, 300]);
    restoreScroll(scroller, { top: 650, atBottom: false, anchor: { id: "r2", offset: 50 } });

    remove(2);
    resize(0, 500);
    frame();

    expect(scroller.scrollTop).toBe(650);
    expect(pending()).toBe(0);
  });

  it("cancels on request", () => {
    const { scroller, resize } = transcript([300, 300, 300, 300]);
    const cancel = restoreScroll(scroller, {
      top: 650,
      atBottom: false,
      anchor: { id: "r2", offset: 50 },
    });

    cancel();
    resize(0, 500);
    frame();

    expect(scroller.scrollTop).toBe(650);
  });
});
