import { describe, expect, it, vi } from "vitest";

import { createPlayer, explainFailure, speechURL, type AudioLike } from "./ttsPlayer";

// Enough of an <audio> element to drive the player: the attributes it reads,
// and events the test fires when the browser would.
class FakeAudio extends EventTarget {
  src = "";
  paused = true;
  ended = false;
  error: { code: number } | null = null;
  readyState = 0;
  playResult: Promise<void> = Promise.resolve();

  play() {
    this.paused = false;
    return this.playResult;
  }
  pause() {
    this.paused = true;
  }
  load() {
    this.readyState = 0;
    this.error = null;
  }
  removeAttribute(name: string) {
    if (name === "src") this.src = "";
  }
  set(over: Partial<FakeAudio>) {
    Object.assign(this, over);
  }
  fire(type: string) {
    this.dispatchEvent(new Event(type));
  }
}

function rig() {
  const audio = new FakeAudio();
  const explain = vi.fn(async (url: string) => `explained ${url}`);
  const notify = vi.fn();
  const p = createPlayer({ makeAudio: () => audio as unknown as AudioLike, explain, notify });
  return { audio, p, explain, notify };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("player", () => {
  it("goes loading, playing, then idle when the clip ends", () => {
    const { audio, p } = rig();
    p.play("m1", "hello", "t1");
    expect(p.getState()).toEqual({ playingKey: "m1", status: "loading" });
    expect(audio.src).toBe(speechURL("hello", "t1"));

    audio.fire("loadstart");
    expect(p.getState().status).toBe("loading");
    audio.set({ readyState: 4 });
    audio.fire("playing");
    expect(p.getState()).toEqual({ playingKey: "m1", status: "playing" });

    audio.set({ paused: true, ended: true });
    audio.fire("pause");
    audio.fire("ended");
    expect(p.getState()).toEqual({ playingKey: null, status: null });
  });

  it("stops the last clip when another starts, and ignores the old one's late events", () => {
    const { audio, p } = rig();
    p.play("m1", "first", "t1");
    audio.set({ readyState: 4 });
    audio.fire("playing");

    p.play("m2", "second", "t1");
    expect(p.getState()).toEqual({ playingKey: "m2", status: "loading" });
    expect(audio.src).toBe(speechURL("second", "t1"));

    // What the browser queued for the replaced clip lands after the switch.
    audio.set({ readyState: 0 });
    audio.fire("pause");
    audio.fire("playing");
    audio.fire("error");
    expect(p.getState()).toEqual({ playingKey: "m2", status: "loading" });
  });

  it("stops on request and drops the source, so the download stops too", () => {
    const { audio, p } = rig();
    p.play("m1", "hello", "t1");
    p.stop();
    expect(p.getState()).toEqual({ playingKey: null, status: null });
    expect(audio.src).toBe("");
    expect(audio.paused).toBe(true);
  });

  it("asks the server why a clip failed before any of it played", async () => {
    const { audio, p, explain, notify } = rig();
    p.play("m1", "hello", "t1");
    audio.set({ error: { code: 4 } });
    audio.fire("error");
    expect(p.getState().status).toBeNull();
    await flush();
    expect(explain).toHaveBeenCalledWith(speechURL("hello", "t1"));
    expect(notify).toHaveBeenCalledWith(`explained ${speechURL("hello", "t1")}`);
  });

  it("does not ask again for a clip that broke partway", () => {
    const { audio, p, explain, notify } = rig();
    p.play("m1", "hello", "t1");
    audio.set({ readyState: 4 });
    audio.fire("playing");
    audio.set({ error: { code: 2 } });
    audio.fire("error");
    expect(explain).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledTimes(1);
    expect(p.getState().status).toBeNull();
  });

  it("does not ask the server about a preview, whose audio is not its own", () => {
    const { audio, p, explain, notify } = rig();
    p.playURL("voice:v1", "https://cdn.example/v1.mp3");
    audio.set({ error: { code: 4 } });
    audio.fire("error");
    expect(explain).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it("goes idle and says so when the browser refuses to play", async () => {
    const { audio, p, notify } = rig();
    audio.playResult = Promise.reject(new DOMException("no", "NotAllowedError"));
    p.play("m1", "hello", "t1");
    await flush();
    expect(p.getState()).toEqual({ playingKey: null, status: null });
    expect(notify).toHaveBeenCalledTimes(1);
  });

  it("stays on the new clip when the old one's play is aborted", async () => {
    const { audio, p, notify } = rig();
    audio.playResult = Promise.reject(new DOMException("replaced", "AbortError"));
    p.play("m1", "first", "t1");
    audio.playResult = Promise.resolve();
    p.play("m2", "second", "t1");
    await flush();
    expect(p.getState()).toEqual({ playingKey: "m2", status: "loading" });
    expect(notify).not.toHaveBeenCalled();
  });

  it("resumes a clip paused from the lock screen rather than starting over", () => {
    const { audio, p } = rig();
    p.play("m1", "hello", "t1");
    audio.set({ readyState: 4 });
    audio.fire("playing");
    audio.pause();
    audio.fire("pause");
    expect(p.getState().status).toBeNull();

    const load = vi.spyOn(audio, "load");
    audio.src = "kept";
    p.play("m1", "hello", "t1");
    // Same clip: the element keeps its source and position.
    expect(audio.src).toBe("kept");
    expect(load).not.toHaveBeenCalled();
  });
});

describe("explainFailure", () => {
  const response = (status: number, body?: unknown) =>
    new Response(body === undefined ? null : JSON.stringify(body), { status });

  it("reads the server's message when the clip is refused", async () => {
    const fetcher = vi.fn(async (_url: string, init?: RequestInit) =>
      init?.method === "HEAD" ? response(409) : response(409, { error: "Pick a voice in Settings" }),
    );
    expect(await explainFailure("/api/tts?text=x", fetcher as typeof fetch)).toBe(
      "Pick a voice in Settings",
    );
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("does not GET a clip the server would serve, which could synthesise it again", async () => {
    const fetcher = vi.fn(async () => response(200));
    const msg = await explainFailure("/api/tts?text=x", fetcher as typeof fetch);
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]).toEqual([
      "/api/tts?text=x",
      { method: "HEAD", credentials: "same-origin" },
    ]);
    expect(msg).toBeTruthy();
  });
});
