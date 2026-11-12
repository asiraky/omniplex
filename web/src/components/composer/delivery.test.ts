import { describe, expect, it } from "vitest";

import { deliveryFor } from "./delivery";

describe("deliveryFor", () => {
  it.each([
    // A harness that only queues has no other way to send.
    { steers: false, inverted: false, want: undefined },
    { steers: false, inverted: true, want: undefined },
    // The default goes unsaid; only the other way is spelled out.
    { steers: true, inverted: false, want: undefined },
    { steers: true, inverted: true, want: "queue" },
  ])("steers=$steers inverted=$inverted → $want", ({ want, ...at }) => {
    expect(deliveryFor(at)).toBe(want);
  });
});
