// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useSmoothText } from "./useSmoothText";

// jsdom has no animation frames; drive the loop with fake timers instead.
// Fake timers also fake performance.now, so the hook's dt math stays real.
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) =>
    setTimeout(() => cb(performance.now()), 16) as unknown as number,
  );
  vi.stubGlobal("cancelAnimationFrame", (id: number) => clearTimeout(id));
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function frames(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("useSmoothText", () => {
  it("shows text present at mount immediately", () => {
    const { result } = renderHook(({ text, active }) => useSmoothText(text, active), {
      initialProps: { text: "already here", active: false },
    });
    expect(result.current).toBe("already here");
  });

  // The incident from issue #24: a block mounts with its first chunk while the
  // thread is (wrongly or rightly) not in a turn, then the text keeps
  // growing. The reveal loop had already exited and nothing restarted it, so
  // the block froze at the first chunk forever.
  it("catches up when text grows while inactive", async () => {
    const full = "The web" + " and then the remaining ~1,900 characters".repeat(8);
    const { result, rerender } = renderHook(
      ({ text, active }) => useSmoothText(text, active),
      { initialProps: { text: "The web", active: false } },
    );
    expect(result.current).toBe("The web");

    rerender({ text: full, active: false });
    await frames(5000);

    expect(result.current).toBe(full);
  });

  it("reveals arriving text progressively while active", async () => {
    const { result, rerender } = renderHook(
      ({ text, active }) => useSmoothText(text, active),
      { initialProps: { text: "", active: true } },
    );

    rerender({ text: "hello, this is a streamed line of text", active: true });
    await frames(48);
    const partial = result.current;
    expect(partial.length).toBeGreaterThan(0);
    expect(partial.length).toBeLessThan("hello, this is a streamed line of text".length);

    await frames(5000);
    expect(result.current).toBe("hello, this is a streamed line of text");
  });

  // Chunks can land faster than animation frames (codex streams a delta per
  // word). If each chunk restarted the reveal loop, the pending frame would be
  // cancelled before it ever fired and nothing would render until the stream
  // paused. The loop must keep its frame across re-renders.
  it("makes progress while chunks arrive faster than frames", async () => {
    let text = "";
    const { result, rerender } = renderHook(
      (p: { text: string; active: boolean }) => useSmoothText(p.text, p.active),
      { initialProps: { text, active: true } },
    );

    // 40 chunks, one every 8ms — twice per 16ms frame — for 320ms.
    for (let i = 0; i < 40; i++) {
      text += "word ";
      rerender({ text, active: true });
      await frames(8);
    }

    expect(result.current.length).toBeGreaterThan(0);
  });

  it("flushes the remainder after the turn ends", async () => {
    const { result, rerender } = renderHook(
      ({ text, active }) => useSmoothText(text, active),
      { initialProps: { text: "", active: true } },
    );
    rerender({ text: "a final message that ends mid-reveal", active: true });
    await frames(16);
    rerender({ text: "a final message that ends mid-reveal", active: false });
    await frames(5000);
    expect(result.current).toBe("a final message that ends mid-reveal");
  });
});
