import { afterEach, describe, expect, it, vi } from "vitest";

import { formatElapsed, pickMimeType, placeTranscript, transcribe, TRANSCRIBE_TIMEOUT_MS } from "./dictation";

const at = (draft: string, start = draft.length, end = start) => ({ draft, start, end });

describe("placeTranscript", () => {
  describe("when the draft is as it was when recording began", () => {
    it("fills the selection captured then", () => {
      const draft = "fix the old bug please";
      const out = placeTranscript(at(draft, 8, 11), { draft, cursor: 0 }, "new");
      expect(out).toEqual({ value: "fix the new bug please", cursor: 11 });
    });

    it("goes at the anchored caret, not wherever the cursor moved since", () => {
      const draft = "one three";
      const out = placeTranscript(at(draft, 4), { draft, cursor: draft.length }, "two");
      expect(out.value).toBe("one two three");
    });

    it("fills an empty draft", () => {
      expect(placeTranscript(at(""), { draft: "", cursor: 0 }, "Hello there.")).toEqual({
        value: "Hello there.",
        cursor: 12,
      });
    });

    it("takes a selection made right to left", () => {
      const draft = "a bad idea";
      const out = placeTranscript(at(draft, 5, 2), { draft, cursor: 0 }, "good");
      expect(out.value).toBe("a good idea");
    });
  });

  describe("when the draft changed during transcription", () => {
    it("inserts at the current cursor and replaces nothing", () => {
      const anchor = at("fix the old bug", 8, 11);
      const now = { draft: "fix the old bug today", cursor: 21 };
      expect(placeTranscript(anchor, now, "and tests")).toEqual({
        value: "fix the old bug today and tests",
        cursor: 31,
      });
    });

    it("keeps everything typed meanwhile, inserting mid-draft", () => {
      const anchor = at("");
      const now = { draft: "first second", cursor: 6 };
      const out = placeTranscript(anchor, now, "middle");
      expect(out.value).toBe("first middle second");
      expect(out.value.slice(0, out.cursor)).toBe("first middle ");
    });

    it("lands in a draft that was cleared by a send", () => {
      const out = placeTranscript(at("sent already", 12), { draft: "", cursor: 0 }, "next one");
      expect(out).toEqual({ value: "next one", cursor: 8 });
    });
  });

  describe("spacing", () => {
    it("adds a space before when it would run into the previous word", () => {
      expect(placeTranscript(at("Hello"), { draft: "Hello", cursor: 5 }, "world").value).toBe(
        "Hello world",
      );
    });

    it("adds none before after whitespace or a newline", () => {
      expect(placeTranscript(at("Hello "), { draft: "Hello ", cursor: 6 }, "world").value).toBe(
        "Hello world",
      );
      expect(placeTranscript(at("Hi\n"), { draft: "Hi\n", cursor: 3 }, "there").value).toBe(
        "Hi\nthere",
      );
    });

    it("adds a space after only when a word follows straight on", () => {
      expect(placeTranscript(at("abc", 0), { draft: "abc", cursor: 0 }, "x").value).toBe("x abc");
      expect(placeTranscript(at("?", 0), { draft: "?", cursor: 0 }, "why").value).toBe("why?");
      expect(placeTranscript(at(" b", 0), { draft: " b", cursor: 0 }, "a").value).toBe("a b");
    });

    it("treats letters beyond ASCII as words", () => {
      expect(placeTranscript(at("été", 0), { draft: "été", cursor: 0 }, "un").value).toBe("un été");
    });

    it("trims what the transcriber put around the text", () => {
      expect(placeTranscript(at("a"), { draft: "a", cursor: 1 }, "  b \n").value).toBe("a b");
    });

    it("puts the cursor after the inserted text and any space added after it", () => {
      const out = placeTranscript(at("ab cd", 2), { draft: "ab cd", cursor: 0 }, "x");
      // "ab" + " x" + "" + " cd": nothing follows straight on, so no trailing space.
      expect(out).toEqual({ value: "ab x cd", cursor: 4 });
      const glued = placeTranscript(at("abcd", 2), { draft: "abcd", cursor: 0 }, "x");
      expect(glued).toEqual({ value: "ab x cd", cursor: 5 });
    });
  });

  describe("out-of-range positions", () => {
    it("clamps a cursor past the end", () => {
      expect(placeTranscript(at("x"), { draft: "abc", cursor: 99 }, "d")).toEqual({
        value: "abc d",
        cursor: 5,
      });
    });

    it("clamps a negative or missing cursor to the start", () => {
      expect(placeTranscript(at("x"), { draft: "abc", cursor: -4 }, "z").value).toBe("z abc");
      expect(placeTranscript(at("x"), { draft: "abc", cursor: Number.NaN }, "z").value).toBe("z abc");
    });

    it("clamps an anchor that outran its draft", () => {
      expect(placeTranscript(at("ab", 1, 50), { draft: "ab", cursor: 0 }, "c").value).toBe("a c");
    });
  });

  it("leaves the draft alone for an empty transcript", () => {
    expect(placeTranscript(at("abc", 0, 3), { draft: "abc", cursor: 1 }, "  ")).toEqual({
      value: "abc",
      cursor: 0,
    });
  });
});

describe("pickMimeType", () => {
  it("prefers Opus in WebM, then Ogg, then MP4", () => {
    const only = (...ok: string[]) => (t: string) => ok.includes(t);
    expect(pickMimeType(() => true)).toBe("audio/webm;codecs=opus");
    expect(pickMimeType(only("audio/mp4", "audio/ogg;codecs=opus"))).toBe("audio/ogg;codecs=opus");
    expect(pickMimeType(only("audio/mp4"))).toBe("audio/mp4");
  });

  it("leaves the choice to the browser when it supports none, or throws asking", () => {
    expect(pickMimeType(() => false)).toBeUndefined();
    expect(
      pickMimeType(() => {
        throw new Error("nope");
      }),
    ).toBeUndefined();
  });
});

describe("transcribe", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  const clip = new Blob([new Uint8Array([1, 2, 3])], { type: "audio/webm;codecs=opus" });
  const reply = (status: number, body: unknown) =>
    vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) =>
      new Response(typeof body === "string" ? body : JSON.stringify(body), { status }),
    );

  it("posts the clip with its type for the thread and returns the text", async () => {
    const fetcher = reply(200, { text: "  hello there " });
    await expect(transcribe(clip, "t 1", { fetcher })).resolves.toBe("hello there");
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe("/api/dictation?thread=t%201");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(clip);
    expect((init?.headers as Record<string, string>)["Content-Type"]).toBe(clip.type);
  });

  it("leaves the thread off when there is none", async () => {
    const fetcher = reply(200, { text: "x" });
    await transcribe(clip, undefined, { fetcher });
    expect(fetcher.mock.calls[0]![0]).toBe("/api/dictation");
  });

  it("returns empty for silence", async () => {
    await expect(transcribe(clip, "t", { fetcher: reply(200, { text: "" }) })).resolves.toBe("");
    await expect(transcribe(clip, "t", { fetcher: reply(200, {}) })).resolves.toBe("");
  });

  it("throws the server's message on failure", async () => {
    const fetcher = reply(402, { error: "ElevenLabs is out of credit." });
    await expect(transcribe(clip, "t", { fetcher })).rejects.toThrow("ElevenLabs is out of credit.");
  });

  it("falls back to the status when the error body is not JSON", async () => {
    await expect(transcribe(clip, "t", { fetcher: reply(502, "Bad Gateway") })).rejects.toThrow(
      "502",
    );
  });

  it("rewords a network failure", async () => {
    const fetcher = vi.fn(async () => {
      throw new TypeError("Failed to fetch");
    });
    await expect(transcribe(clip, "t", { fetcher })).rejects.toThrow(/reach the server/);
  });

  it("gives up after the timeout", async () => {
    vi.useFakeTimers();
    const fetcher = vi.fn(
      (_url: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((_, reject) =>
          init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))),
        ),
    );
    const out = transcribe(clip, "t", { fetcher });
    const settled = expect(out).rejects.toThrow(/too long/);
    await vi.advanceTimersByTimeAsync(TRANSCRIBE_TIMEOUT_MS);
    await settled;
  });

  it("passes the caller's abort through as it is", async () => {
    const controller = new AbortController();
    const fetcher = vi.fn(
      (_url: RequestInfo | URL, init?: RequestInit) =>
        new Promise<Response>((_, reject) =>
          init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))),
        ),
    );
    const out = transcribe(clip, "t", { signal: controller.signal, fetcher });
    controller.abort();
    await expect(out).rejects.toMatchObject({ name: "AbortError" });
  });
});

describe("formatElapsed", () => {
  it("shows minutes and zero-padded seconds", () => {
    expect(formatElapsed(0)).toBe("0:00");
    expect(formatElapsed(7_900)).toBe("0:07");
    expect(formatElapsed(65_000)).toBe("1:05");
  });
});
