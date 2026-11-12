import { describe, expect, it } from "vitest";

import { deliveryFor } from "./delivery";

describe("deliveryFor", () => {
  it.each([
    // An idle thread starts a turn whichever way the message was sent.
    { busy: false, steers: false, inverted: false, want: undefined },
    { busy: false, steers: false, inverted: true, want: undefined },
    { busy: false, steers: true, inverted: false, want: undefined },
    { busy: false, steers: true, inverted: true, want: undefined },
    // A harness that only queues has no other way to send.
    { busy: true, steers: false, inverted: false, want: undefined },
    { busy: true, steers: false, inverted: true, want: undefined },
    // The default goes unsaid; only the other way is spelled out.
    { busy: true, steers: true, inverted: false, want: undefined },
    { busy: true, steers: true, inverted: true, want: "queue" },
  ])("busy=$busy steers=$steers inverted=$inverted → $want", ({ want, ...at }) => {
    expect(deliveryFor(at)).toBe(want);
  });
});
