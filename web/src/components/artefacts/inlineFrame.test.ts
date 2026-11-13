import { describe, expect, it } from "vitest";

import { inlineFrameHeight } from "~/components/artefacts/inlineFrame";

const phone = { desktop: false, viewportHeight: 800 };
const desktop = { desktop: true, viewportHeight: 1200 };

describe("inlineFrameHeight", () => {
  it("starts at the floor before the page has said how tall it is", () => {
    expect(inlineFrameHeight(undefined, phone)).toEqual({ height: 120, cut: false });
  });

  it("never goes below the floor, and a short page is not cut", () => {
    expect(inlineFrameHeight(40, phone)).toEqual({ height: 120, cut: false });
    expect(inlineFrameHeight(Number.NaN, desktop)).toEqual({ height: 120, cut: false });
  });

  it("fits a page between the floor and the cap, rounding up so nothing is clipped", () => {
    expect(inlineFrameHeight(300.2, phone)).toEqual({ height: 301, cut: false });
    expect(inlineFrameHeight(640, desktop)).toEqual({ height: 640, cut: false });
  });

  it("caps a phone at 60% of its height and a desktop at 640px, and says the page was cut", () => {
    expect(inlineFrameHeight(2000, phone)).toEqual({ height: 480, cut: true });
    expect(inlineFrameHeight(2000, desktop)).toEqual({ height: 640, cut: true });
    expect(inlineFrameHeight(500, phone)).toEqual({ height: 480, cut: true });
    expect(inlineFrameHeight(500, desktop)).toEqual({ height: 500, cut: false });
  });

  it("keeps the floor on a screen too short for 60% of it to reach it", () => {
    expect(inlineFrameHeight(1000, { desktop: false, viewportHeight: 150 })).toEqual({ height: 120, cut: true });
  });
});
