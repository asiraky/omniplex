import { describe, expect, it } from "vitest";
import { RestartPolicy } from "./restart";

const opts = { maxRestarts: 3, baseDelayMs: 1000, maxDelayMs: 3000, stableAfterMs: 60000 };

function clock() {
  let t = 1_000_000;
  return { now: () => t, advance: (ms: number) => (t += ms) };
}

describe("RestartPolicy", () => {
  it("backs off exponentially up to the cap, then gives up", () => {
    const c = clock();
    const p = new RestartPolicy(opts, c.now);
    const delays = [];
    for (let i = 0; i < 4; i++) {
      p.started();
      c.advance(500);
      delays.push(p.onExit());
    }
    expect(delays).toEqual([1000, 2000, 3000, null]);
  });

  it("starts counting again after a run that lasted", () => {
    const c = clock();
    const p = new RestartPolicy(opts, c.now);
    p.started();
    p.onExit();
    p.started();
    p.onExit();
    p.started();
    c.advance(61000);
    expect(p.onExit()).toBe(1000);
  });

  it("reset gives a fresh budget", () => {
    const c = clock();
    const p = new RestartPolicy({ ...opts, maxRestarts: 1 }, c.now);
    p.started();
    expect(p.onExit()).toBe(1000);
    expect(p.onExit()).toBeNull();
    p.reset();
    expect(p.onExit()).toBe(1000);
  });
});
