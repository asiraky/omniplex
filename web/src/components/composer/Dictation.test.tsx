// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Composer } from "~/components/Composer";
import { MAX_DICTATION_MS } from "~/lib/dictation";
import { toast } from "~/lib/toast";
import { render, wrap } from "~/test/harness";

import { HOLD_MS } from "./MicButton";

vi.mock("~/lib/toast", () => ({ toast: { success: vi.fn(), info: vi.fn(), error: vi.fn() } }));

// Enough of MediaRecorder for the composer: it starts, and on stop hands over
// one chunk and fires stop, as the browser does.
class FakeRecorder {
  static isTypeSupported = (type: string) => type === "audio/webm;codecs=opus";
  static last: FakeRecorder | undefined;
  state: "inactive" | "recording" = "inactive";
  mimeType: string;
  ondataavailable: ((e: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  constructor(_stream: unknown, opts?: { mimeType?: string }) {
    this.mimeType = opts?.mimeType ?? "";
    FakeRecorder.last = this;
  }
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    this.ondataavailable?.({ data: new Blob(["speech"], { type: this.mimeType }) });
    this.onstop?.();
  }
}

let track: { stop: ReturnType<typeof vi.fn> };
let getUserMedia: ReturnType<typeof vi.fn>;
let fetcher: ReturnType<typeof vi.fn>;
// The pending transcription, settled by the test when it wants the reply.
let reply: (body: unknown, status?: number) => void;

beforeEach(() => {
  track = { stop: vi.fn() };
  getUserMedia = vi.fn(async () => ({ getTracks: () => [track] }));
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  Object.defineProperty(navigator, "mediaDevices", {
    value: { getUserMedia },
    configurable: true,
  });
  fetcher = vi.fn(
    () =>
      new Promise<Response>((resolve) => {
        reply = (body, status = 200) => resolve(new Response(JSON.stringify(body), { status }));
      }),
  );
  vi.stubGlobal("fetch", fetcher);
  vi.mocked(toast.info).mockClear();
  vi.mocked(toast.error).mockClear();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "mediaDevices");
});

function Harness({ initial = "", thread = "t1" as string | undefined, dictate = true }) {
  const [draft, setDraft] = useState(initial);
  return (
    <Composer
      draft={draft}
      onDraftChange={setDraft}
      disabled={false}
      busy={false}
      onSend={vi.fn()}
      onCancel={vi.fn()}
      dictation={dictate ? { thread } : undefined}
    />
  );
}

const box = () => screen.getByLabelText<HTMLTextAreaElement>("Message");
const mic = () => screen.getByRole("button", { name: "Dictate" });
const stopButton = () => screen.getByRole("button", { name: "Stop and transcribe" });
const recordingNow = () => screen.queryByRole("timer");
// Lets the mic promise and React settle, under real or fake timers alike.
const settle = () => act(async () => {});

async function tap(el: HTMLElement) {
  fireEvent.pointerDown(el, { button: 0 });
  fireEvent.pointerUp(el, { button: 0 });
  await settle();
}

async function recordAndStop() {
  await tap(mic());
  expect(recordingNow()).not.toBeNull();
  await tap(stopButton());
  expect(fetcher).toHaveBeenCalledTimes(1);
}

async function answer(text: string) {
  reply({ text });
  await settle();
}

describe("dictating into the composer", () => {
  it("records on a tap, stops on the next, and puts the transcript in", async () => {
    render(<Harness initial="Hello" />);
    await tap(mic());
    // A tap leaves it recording; the toolbar says so until it is stopped.
    expect(recordingNow()).not.toBeNull();
    await tap(stopButton());
    expect(recordingNow()).toBeNull();
    expect(track.stop).toHaveBeenCalled();
    const [url, init] = fetcher.mock.calls[0]!;
    expect(url).toBe("/api/dictation?thread=t1");
    expect((init.body as Blob).size).toBeGreaterThan(0);

    await answer("world.");
    expect(box().value).toBe("Hello world.");
    expect(mic()).toBeTruthy();
  });

  it("stops and sends when a held press is released", async () => {
    vi.useFakeTimers();
    render(<Harness />);
    fireEvent.pointerDown(mic(), { button: 0 });
    await settle();
    await act(() => vi.advanceTimersByTimeAsync(HOLD_MS + 50));
    expect(recordingNow()).not.toBeNull();
    fireEvent.pointerUp(stopButton(), { button: 0 });
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
    await answer("held");
    expect(box().value).toBe("held");
  });

  it("toggles from the keyboard", async () => {
    render(<Harness />);
    // A keyboard press arrives as a click with no pointer count.
    fireEvent.click(mic());
    await settle();
    expect(recordingNow()).not.toBeNull();
    fireEvent.click(stopButton());
    await settle();
    await answer("typed by voice");
    expect(box().value).toBe("typed by voice");
  });

  it("replaces the selection made before recording when nothing was typed since", async () => {
    render(<Harness initial="fix the old bug" />);
    box().focus();
    box().setSelectionRange(8, 11);
    await recordAndStop();
    await answer("new");
    expect(box().value).toBe("fix the new bug");
  });

  it("inserts at the cursor instead when the draft changed during transcription", async () => {
    render(<Harness initial="fix the old bug" />);
    box().focus();
    box().setSelectionRange(8, 11);
    await recordAndStop();

    fireEvent.change(box(), { target: { value: "fix the old bug in auth" } });
    box().setSelectionRange(15, 15);
    await answer("quickly");
    // The selection captured at the start no longer means anything: nothing
    // typed is replaced, and the transcript goes where the cursor is now.
    expect(box().value).toBe("fix the old bug quickly in auth");
  });

  it("goes on the end of the draft when the input was never focused", async () => {
    render(<Harness initial="one two" />);
    box().setSelectionRange(0, 0);
    await recordAndStop();
    await answer("three");
    expect(box().value).toBe("one two three");
  });

  it("discards a recording without uploading it", async () => {
    render(<Harness initial="keep" />);
    await tap(mic());
    fireEvent.click(screen.getByRole("button", { name: "Discard recording" }));
    await settle();
    expect(fetcher).not.toHaveBeenCalled();
    expect(track.stop).toHaveBeenCalled();
    expect(recordingNow()).toBeNull();
    expect(box().value).toBe("keep");
  });

  it("leaves the draft alone and says so when nothing was heard", async () => {
    render(<Harness initial="as typed" />);
    await recordAndStop();
    await answer("");
    expect(box().value).toBe("as typed");
    expect(toast.info).toHaveBeenCalledTimes(1);
    expect(mic()).toBeTruthy();
  });

  it("surfaces the server's error and leaves the draft alone", async () => {
    render(<Harness initial="as typed" />);
    await recordAndStop();
    reply({ error: "Add an ElevenLabs key in Settings." }, 409);
    await settle();
    expect(toast.error).toHaveBeenCalledWith("Add an ElevenLabs key in Settings.", expect.anything());
    expect(box().value).toBe("as typed");
    expect(mic()).toBeTruthy();
  });

  it("sends the same clip again from the failure's retry", async () => {
    render(<Harness initial="as typed" />);
    await recordAndStop();
    const sent = fetcher.mock.calls[0]![1].body as Blob;
    reply({ error: "connection dropped" }, 502);
    await settle();
    const retry = vi.mocked(toast.error).mock.calls[0]![1]!.action as { onClick: () => void };
    await act(async () => retry.onClick());
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(fetcher.mock.calls[1]![1].body).toBe(sent);
    await answer("said once");
    expect(box().value).toBe("as typed said once");
  });

  it("drops a retry once the thread has changed", async () => {
    const view = render(<Harness initial="draft" thread="t1" />);
    await recordAndStop();
    reply({ error: "connection dropped" }, 502);
    await settle();
    view.rerender(wrap(<Harness initial="draft" thread="t2" />));
    const retry = vi.mocked(toast.error).mock.calls[0]![1]!.action as { onClick: () => void };
    await act(async () => retry.onClick());
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("reports a blocked microphone and goes back to idle", async () => {
    getUserMedia.mockRejectedValueOnce(new DOMException("denied", "NotAllowedError"));
    render(<Harness />);
    await tap(mic());
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(recordingNow()).toBeNull();
    expect(mic()).toBeTruthy();
  });

  it("never lands a transcript after the thread changes", async () => {
    const view = render(<Harness initial="draft" thread="t1" />);
    await recordAndStop();
    view.rerender(wrap(<Harness initial="draft" thread="t2" />));
    await answer("for the old thread");
    expect(box().value).toBe("draft");
    expect(mic()).toBeTruthy();
  });

  it("stops by itself at the length cap", async () => {
    vi.useFakeTimers();
    render(<Harness />);
    await tap(mic());
    await act(() => vi.advanceTimersByTimeAsync(MAX_DICTATION_MS));
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(recordingNow()).toBeNull();
  });

  it("is not offered without a key, or in a browser that cannot record", () => {
    const view = render(<Harness dictate={false} />);
    expect(screen.queryByRole("button", { name: "Dictate" })).toBeNull();

    vi.stubGlobal("MediaRecorder", undefined);
    view.rerender(wrap(<Harness />));
    expect(screen.queryByRole("button", { name: "Dictate" })).toBeNull();
  });
});
