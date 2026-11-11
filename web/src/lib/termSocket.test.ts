// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { endTerminal, openTermSocket, type TermEnd, type TermStatus } from "./termSocket";

// A socket the test drives by hand, recording what the terminal sends.
class FakeSocket {
  static readonly OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  binaryType = "blob";
  sent: Record<string, unknown>[] = [];
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: unknown }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }
  get params() {
    return new URL(this.url).searchParams;
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close(code = 1006) {
    this.readyState = 3;
    this.onclose?.({ code });
  }
  open() {
    this.readyState = FakeSocket.OPEN;
    this.onopen?.();
  }
  sync(offset: number) {
    this.onmessage?.({ data: JSON.stringify({ type: "sync", offset }) });
  }
  bytes(text: string) {
    this.onmessage?.({ data: new TextEncoder().encode(text).buffer });
  }
}

const latest = () => FakeSocket.all[FakeSocket.all.length - 1];

function setup(target: { thread: string } | { login: string } = { thread: "t1" }) {
  const out: string[] = [];
  const log = { resets: 0, statuses: [] as TermStatus[], ended: [] as TermEnd[] };
  const term = openTermSocket("term-1", target, {
    onData: (b) => out.push(new TextDecoder().decode(b)),
    onReset: () => log.resets++,
    onStatus: (s) => log.statuses.push(s),
    onEnded: (r) => log.ended.push(r),
  });
  return { term, out, log };
}

beforeEach(() => {
  FakeSocket.all = [];
  vi.useFakeTimers();
  vi.stubGlobal("WebSocket", FakeSocket);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("first connect", () => {
  it("asks to attach-or-spawn under its id, with nothing to replay", () => {
    setup({ login: "inst" });
    const p = latest().params;
    expect(p.get("id")).toBe("term-1");
    expect(p.get("login")).toBe("inst");
    expect(p.has("attach")).toBe(false);
    expect(p.has("since")).toBe(false);
  });

  it("sends the latest size on open", () => {
    const { term, log } = setup();
    term.resize(80, 24);
    term.resize(100, 30);
    latest().open();
    expect(latest().sent).toEqual([{ type: "resize", cols: 100, rows: 30 }]);
    expect(log.statuses).toEqual(["connecting", "online"]);
  });
});

describe("sync", () => {
  it("does not clear the screen when the stream continues from it", () => {
    const { out, log } = setup();
    latest().open();
    latest().sync(0);
    latest().bytes("hello");
    expect(out).toEqual(["hello"]);
    expect(log.resets).toBe(0);
  });

  it("clears the screen when the stream jumps, and counts from the new offset", () => {
    const { log } = setup();
    latest().open();
    latest().sync(0);
    latest().bytes("abc");
    // The server dropped bytes we never saw: what is on screen is stale.
    latest().sync(500);
    expect(log.resets).toBe(1);
    latest().bytes("de");

    latest().close();
    vi.advanceTimersByTime(300);
    expect(latest().params.get("since")).toBe("502");
  });
});

describe("reconnect", () => {
  it("reattaches with the bytes it already has after a drop", () => {
    const { out, log } = setup();
    latest().open();
    latest().sync(0);
    latest().bytes("12345");
    latest().close();
    expect(log.statuses.at(-1)).toBe("reconnecting");

    vi.advanceTimersByTime(300);
    const p = latest().params;
    expect(p.get("attach")).toBe("1");
    expect(p.get("since")).toBe("5");
    expect(p.get("thread")).toBe("t1");

    latest().open();
    latest().sync(5);
    latest().bytes("6");
    expect(out.join("")).toBe("123456");
    expect(log.resets).toBe(0);
  });

  it("retries without attach when the first connect never reached the server", () => {
    setup();
    latest().close();
    vi.advanceTimersByTime(300);
    expect(FakeSocket.all).toHaveLength(2);
    expect(latest().params.has("attach")).toBe(false);
  });

  it("backs off further on each failure, and resets once a connection holds", () => {
    setup();
    latest().close();
    vi.advanceTimersByTime(300);
    latest().close();
    vi.advanceTimersByTime(999);
    expect(FakeSocket.all).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(3);

    latest().open();
    vi.advanceTimersByTime(30_000);
    latest().close();
    vi.advanceTimersByTime(300);
    expect(FakeSocket.all).toHaveLength(4);
  });

  it("retries at once on coming back online or to the foreground", () => {
    setup();
    latest().close();
    window.dispatchEvent(new Event("online"));
    expect(FakeSocket.all).toHaveLength(2);

    latest().close();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(FakeSocket.all).toHaveLength(3);
  });

  it("does not open a second socket on online while one is connected", () => {
    setup();
    latest().open();
    window.dispatchEvent(new Event("online"));
    expect(FakeSocket.all).toHaveLength(1);
  });

  it("drops keystrokes typed while disconnected", () => {
    const { term } = setup();
    latest().open();
    latest().sync(0);
    term.send("a");
    latest().close();
    term.send("rm -rf build\r");
    vi.advanceTimersByTime(300);
    latest().open();
    term.send("b");
    expect(FakeSocket.all[0].sent).toEqual([{ type: "input", data: "a" }]);
    expect(latest().sent).toEqual([{ type: "input", data: "b" }]);
  });
});

describe("ending", () => {
  it.each([
    [4000, "exited"],
    [4404, "gone"],
  ] as const)("close %i ends the terminal as %s, without a retry", (code, reason) => {
    const { log } = setup();
    latest().open();
    latest().sync(0);
    latest().close(code);
    vi.advanceTimersByTime(60_000);
    window.dispatchEvent(new Event("online"));
    expect(log.ended).toEqual([reason]);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it("restart clears the screen and asks for a fresh shell from the start", () => {
    const { term, log } = setup();
    latest().open();
    latest().sync(0);
    latest().bytes("bye");
    latest().close(4000);

    term.restart();
    expect(log.resets).toBe(1);
    const p = latest().params;
    expect(p.has("attach")).toBe(false);
    expect(p.has("since")).toBe(false);
    expect(log.statuses.at(-1)).toBe("connecting");
  });

  it("restart hangs up a live socket without it counting as a drop", () => {
    const { term, log } = setup();
    latest().open();
    const old = latest();
    term.restart();
    old.onclose?.({ code: 1006 });
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(2);
    expect(log.statuses).not.toContain("reconnecting");
  });
});

describe("dispose", () => {
  it("hangs up, and reports and retries nothing after", () => {
    const { term, out, log } = setup();
    const sock = latest();
    sock.open();
    sock.sync(0);
    const before = { ...log, statuses: [...log.statuses] };

    term.dispose();
    expect(sock.readyState).toBe(3);
    sock.bytes("late");
    sock.close();
    vi.advanceTimersByTime(60_000);
    window.dispatchEvent(new Event("online"));

    expect(out).toEqual([]);
    expect(log).toEqual(before);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it("cancels a pending retry", () => {
    const { term } = setup();
    latest().close();
    term.dispose();
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.all).toHaveLength(1);
  });
});

describe("endTerminal", () => {
  it("swallows a failed request", async () => {
    const fetch = vi.fn().mockRejectedValue(new Error("offline"));
    vi.stubGlobal("fetch", fetch);
    expect(() => endTerminal("a b")).not.toThrow();
    await Promise.resolve();
    expect(fetch).toHaveBeenCalledWith("/api/term?id=a%20b", { method: "DELETE" });
  });
});
