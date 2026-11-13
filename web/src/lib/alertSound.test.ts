// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Mod = typeof import("./alertSound");

// A stand-in AudioContext: records what gets built, and lets a test decide
// whether the browser has unlocked audio yet.
let contexts: FakeContext[];
let resumeUnlocks: boolean;

class FakeContext {
  state: "running" | "suspended" | "closed" = initialState;
  currentTime = 0;
  destination = {};
  oscillators: { frequency: number }[] = [];
  resume = vi.fn(async () => {
    if (resumeUnlocks) this.state = "running";
  });
  constructor() {
    contexts.push(this);
  }
  createOscillator() {
    const record = { frequency: 0 };
    this.oscillators.push(record);
    return {
      type: "",
      frequency: { setValueAtTime: (f: number) => (record.frequency = f) },
      connect: vi.fn(),
      start: vi.fn(),
      stop: vi.fn(),
    };
  }
  createGain() {
    return {
      gain: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
      connect: vi.fn(),
    };
  }
}
let initialState: FakeContext["state"];

const KEY = "omniplex.alertSound";
const flush = () => new Promise((r) => setTimeout(r, 0));

// A fresh module each test: the context and the dedupe clock are module state.
// The gesture listeners it registers on load come off again after the test, so
// an earlier test's module never answers a later test's tap.
let registered: [string, EventListenerOrEventListenerObject, boolean | AddEventListenerOptions?][];
async function load(): Promise<Mod> {
  vi.resetModules();
  const add = window.addEventListener.bind(window);
  const spy = vi.spyOn(window, "addEventListener").mockImplementation((type, fn, opts) => {
    registered.push([type, fn, opts]);
    add(type, fn, opts);
  });
  const mod = await import("./alertSound");
  spy.mockRestore();
  return mod;
}

beforeEach(() => {
  localStorage.clear();
  contexts = [];
  initialState = "running";
  resumeUnlocks = false;
  registered = [];
  vi.stubGlobal("AudioContext", FakeContext);
});

afterEach(() => {
  for (const [type, fn, opts] of registered) window.removeEventListener(type, fn, opts);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("alert sound preference", () => {
  it("is off until turned on, and the setter persists and round-trips", async () => {
    const { alertSoundOn, useAlertSound } = await load();
    expect(alertSoundOn()).toBe(false);

    const a = renderHook(() => useAlertSound());
    const b = renderHook(() => useAlertSound());
    expect(a.result.current[0]).toBe(false);

    act(() => a.result.current[1](true));
    expect(alertSoundOn()).toBe(true);
    expect(b.result.current[0]).toBe(true);

    act(() => b.result.current[1](false));
    expect(alertSoundOn()).toBe(false);
    expect(a.result.current[0]).toBe(false);
  });

  it("follows a change made in another tab", async () => {
    const { useAlertSound } = await load();
    const { result } = renderHook(() => useAlertSound());
    localStorage.setItem(KEY, "1");
    act(() => {
      window.dispatchEvent(new StorageEvent("storage", { key: KEY }));
    });
    expect(result.current[0]).toBe(true);
  });

  it("falls back to off when storage is blocked", async () => {
    localStorage.setItem(KEY, "1");
    const { alertSoundOn, useAlertSound } = await load();
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(alertSoundOn()).toBe(false);
    const { result } = renderHook(() => useAlertSound());
    expect(() => act(() => result.current[1](true))).not.toThrow();
  });
});

describe("playAlertSound", () => {
  it("does nothing, not even build a context, while off", async () => {
    const { playAlertSound } = await load();
    playAlertSound("done");
    window.dispatchEvent(new Event("pointerdown"));
    expect(contexts).toHaveLength(0);
  });

  it("plays two notes when on, with a contour that differs by kind", async () => {
    localStorage.setItem(KEY, "1");
    const { playAlertSound } = await load();
    playAlertSound("done");
    playAlertSound("needs");
    expect(contexts).toHaveLength(1);
    const [d1, d2, n1, n2] = contexts[0]!.oscillators.map((o) => o.frequency);
    expect(d2).toBeGreaterThan(d1!);
    expect(n2).toBeLessThan(n1!);
  });

  it("chimes once for overlapping plays of one kind, and again once it has ended", async () => {
    localStorage.setItem(KEY, "1");
    const { playAlertSound } = await load();
    playAlertSound("done");
    playAlertSound("done");
    const c = contexts[0]!;
    expect(c.oscillators).toHaveLength(2);

    c.currentTime = 0.2;
    playAlertSound("done");
    expect(c.oscillators).toHaveLength(2);

    c.currentTime = 5;
    playAlertSound("done");
    expect(c.oscillators).toHaveLength(4);
  });

  it("plays nothing while the browser keeps audio locked", async () => {
    localStorage.setItem(KEY, "1");
    initialState = "suspended";
    const { playAlertSound } = await load();
    playAlertSound("needs");
    await flush();
    expect(contexts[0]!.oscillators).toHaveLength(0);
  });

  it("plays when called inside a gesture that unlocks audio", async () => {
    localStorage.setItem(KEY, "1");
    initialState = "suspended";
    resumeUnlocks = true;
    const { playAlertSound } = await load();
    playAlertSound("done");
    await flush();
    expect(contexts[0]!.oscillators).toHaveLength(2);
  });

  it("wakes a suspended context on a tap, so a later chime plays", async () => {
    localStorage.setItem(KEY, "1");
    initialState = "suspended";
    resumeUnlocks = true;
    const { playAlertSound } = await load();
    window.dispatchEvent(new Event("pointerdown"));
    await flush();
    expect(contexts).toHaveLength(1);
    const c = contexts[0]!;
    expect(c.state).toBe("running");
    playAlertSound("needs");
    expect(c.oscillators).toHaveLength(2);
  });

  it("never throws when the browser has no audio", async () => {
    localStorage.setItem(KEY, "1");
    vi.stubGlobal("AudioContext", undefined);
    const { playAlertSound } = await load();
    expect(() => playAlertSound("done")).not.toThrow();
  });
});
