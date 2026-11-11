// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Client, CommandTimeout, type ClientEvents, type ConnectionStatus } from "./client";
import type { ThreadState } from "./protocol";

// A socket the test opens and closes by hand, recording what the client sends.
class FakeSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: Record<string, unknown>[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
  open() {
    this.readyState = FakeSocket.OPEN;
    this.onopen?.();
  }
  /** A frame from the server. */
  reply(frame: Record<string, unknown>) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

const events: ClientEvents = {
  onStatus() {},
  onThreads() {},
  onHarnesses() {},
  onComposerItemsChanged() {},
  onProjects() {},
  onQuotas() {},
  onState() {},
  onAccess() {},
};

const latest = () => FakeSocket.all[FakeSocket.all.length - 1];
const pings = (s: FakeSocket) => s.sent.filter((f) => f.type === "ping").length;

// Every client listens on the page for wake signals, so each test's clients
// are closed after it or they would answer the next test's events.
let clients: Client[] = [];
function newClient(): Client {
  const c = new Client("ws://test", events);
  clients.push(c);
  return c;
}

let visibility: DocumentVisibilityState = "visible";
function setVisibility(v: DocumentVisibilityState) {
  visibility = v;
  document.dispatchEvent(new Event("visibilitychange"));
}
const presence = (s: FakeSocket) => s.sent.filter((f) => f.type === "presence");
const commandFrames = (s: FakeSocket) => s.sent.filter((f) => f.type === "command");

// Connected and greeted, so nothing is probing yet.
function live(on: Partial<ClientEvents> = {}): { c: Client; s: FakeSocket } {
  const c = new Client("ws://test", { ...events, ...on });
  clients.push(c);
  c.connect();
  latest().open();
  latest().reply({ type: "welcome" });
  return { c, s: latest() };
}

// A fetch that never answers and rejects only when aborted, as fetch does.
function stallFetch() {
  vi.stubGlobal(
    "fetch",
    (_url: string, init: { signal: AbortSignal }) =>
      new Promise((_, reject) =>
        init.signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError"))),
      ),
  );
  vi.spyOn(console, "warn").mockImplementation(() => {});
}

beforeEach(() => {
  FakeSocket.all = [];
  clients = [];
  visibility = "visible";
  vi.useFakeTimers();
  vi.stubGlobal("WebSocket", FakeSocket);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
});
afterEach(() => {
  for (const c of clients) c.close();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("presence", () => {
  it("sends on change, and not when nothing changed", () => {
    const client = newClient();
    client.connect();
    latest().open();

    client.setPresence(true, "a");
    client.setPresence(true, "a");
    client.setPresence(false, "a");
    client.setPresence(false, "");

    expect(presence(latest())).toEqual([
      { type: "presence", visible: true, threadId: "a" },
      { type: "presence", visible: false, threadId: "a" },
      { type: "presence", visible: false, threadId: "" },
    ]);
  });

  it("says nothing until something was set", () => {
    const client = newClient();
    client.connect();
    latest().open();
    expect(presence(latest())).toEqual([]);
  });

  it("repeats the latest report after hello and attach on every new connection", () => {
    const client = newClient();
    client.prime({ threadId: "a", seq: 3 } as ThreadState);
    client.connect();
    // Set while the socket is still connecting: nothing can go yet.
    client.setPresence(true, "a");
    latest().open();
    expect(latest().sent.map((f) => f.type)).toEqual(["hello", "attach", "presence"]);

    // Changed while offline, then the reconnect carries the new value.
    latest().close();
    client.setPresence(true, "b");
    vi.runOnlyPendingTimers();
    const second = latest();
    expect(FakeSocket.all).toHaveLength(2);
    second.open();
    expect(second.sent.map((f) => f.type)).toEqual(["hello", "attach", "presence"]);
    expect(presence(second)).toEqual([{ type: "presence", visible: true, threadId: "b" }]);
  });
});

describe("status", () => {
  it("is online once the server has greeted, not when the socket opens", () => {
    const statuses: ConnectionStatus[] = [];
    const client = new Client("ws://test", { ...events, onStatus: (st) => statuses.push(st) });
    clients.push(client);
    client.connect();
    latest().open();
    expect(statuses).toEqual(["connecting"]);
    latest().reply({ type: "welcome" });
    expect(statuses).toEqual(["connecting", "online"]);
  });
});

describe("liveness probe", () => {
  for (const [name, fire] of [
    ["becoming visible", () => setVisibility("visible")],
    ["coming back online", () => window.dispatchEvent(new Event("online"))],
    ["a back-forward cache restore", () => window.dispatchEvent(new Event("pageshow"))],
  ] as const) {
    it(`pings on ${name}`, () => {
      const { s } = live();
      fire();
      expect(pings(s)).toBe(1);
    });
  }

  it("drops a silent socket and reconnects at once, without waiting on backoff", () => {
    const statuses: ConnectionStatus[] = [];
    live({ onStatus: (st) => statuses.push(st) });
    vi.spyOn(Math, "random").mockReturnValue(0.99);

    setVisibility("visible");
    vi.advanceTimersByTime(2_999);
    expect(FakeSocket.all).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all).toHaveLength(2);
    expect(statuses.slice(-2)).toEqual(["offline", "connecting"]);
  });

  it("takes any frame as an answer, not just the pong", () => {
    const { s } = live();
    setVisibility("visible");
    vi.advanceTimersByTime(2_000);
    s.reply({ type: "threads", threads: [] });
    vi.advanceTimersByTime(10_000);
    expect(FakeSocket.all).toHaveLength(1);
  });

  it("shares one ping between overlapping wake signals", () => {
    const { s } = live();
    setVisibility("visible");
    window.dispatchEvent(new Event("pageshow"));
    expect(pings(s)).toBe(1);
  });

  it("never lets a probe of a lost socket drop its replacement", () => {
    const { s } = live();
    vi.spyOn(Math, "random").mockReturnValue(0);
    setVisibility("visible");
    vi.advanceTimersByTime(2_800);
    s.close(); // went on its own, mid-probe; retried at once
    expect(FakeSocket.all).toHaveLength(2);
    latest().open();
    vi.advanceTimersByTime(1_000);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("pings on a heartbeat while visible, and stops while hidden", () => {
    const { s } = live();
    vi.advanceTimersByTime(25_000);
    expect(pings(s)).toBe(1);
    s.reply({ type: "pong" });

    setVisibility("hidden");
    vi.advanceTimersByTime(100_000);
    expect(pings(s)).toBe(1);

    setVisibility("visible"); // a probe of its own, then the heartbeat resumes
    s.reply({ type: "pong" });
    vi.advanceTimersByTime(25_000);
    expect(pings(s)).toBe(3);
  });

  it("cuts a backoff wait short on wake", () => {
    const { s } = live();
    vi.spyOn(Math, "random").mockReturnValue(0.99);
    s.close(); // the server went away
    expect(FakeSocket.all).toHaveLength(1);

    window.dispatchEvent(new Event("online"));
    expect(FakeSocket.all).toHaveLength(2);
    // And the abandoned wait does not open a third.
    vi.advanceTimersByTime(1_000);
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("replaces a stalled connection attempt on wake, but not a fresh one", () => {
    const c = newClient();
    c.connect();
    window.dispatchEvent(new Event("online"));
    expect(FakeSocket.all).toHaveLength(1);

    vi.advanceTimersByTime(5_000);
    window.dispatchEvent(new Event("online"));
    expect(FakeSocket.all).toHaveLength(2);
  });

  it("stops listening once closed", () => {
    const c = newClient();
    c.connect();
    latest().open();
    c.close();
    setVisibility("visible");
    vi.advanceTimersByTime(100_000);
    expect(FakeSocket.all).toHaveLength(1);
  });
});

describe("reconnect backoff", () => {
  // The wait before each of `n` successive retries, at a fixed draw.
  function waits(draw: number, n: number): number[] {
    vi.spyOn(Math, "random").mockReturnValue(draw);
    const c = newClient();
    c.connect();
    const out: number[] = [];
    for (let i = 0; i < n; i++) {
      const before = FakeSocket.all.length;
      latest().close();
      let waited = 0;
      while (FakeSocket.all.length === before) {
        vi.advanceTimersByTime(1);
        waited += 1;
      }
      out.push(waited);
    }
    c.close();
    return out;
  }

  it("scales each wait by the draw, so a lower draw always retries sooner", () => {
    const half = waits(0.5, 8);
    const quarter = waits(0.25, 8);
    expect(quarter.map((w) => w * 2)).toEqual(half);
  });

  it("climbs and then holds at its last step", () => {
    const w = waits(0.5, 8);
    for (let i = 1; i < w.length; i++) expect(w[i]).toBeGreaterThanOrEqual(w[i - 1]);
    expect(w[0]).toBeLessThan(w[w.length - 1]);
    expect(w[w.length - 1]).toBe(w[w.length - 2]);
  });

  it("can retry at once on a low draw", () => {
    vi.spyOn(Math, "random").mockReturnValue(0);
    const c = newClient();
    c.connect();
    latest().close();
    expect(FakeSocket.all).toHaveLength(2);
  });
});

describe("command timeout", () => {
  it("fails, carrying its id for a retry, when the socket never comes up", async () => {
    const c = newClient();
    c.connect();
    const caught = c.command("prompt", { text: "hi" }, "cmd-1").catch((e) => e);
    await vi.advanceTimersByTimeAsync(15_000);
    const err = await caught;
    expect(err).toBeInstanceOf(CommandTimeout);
    expect(err.command).toBe("prompt");
    expect(err.commandId).toBe("cmd-1");
  });

  it("keeps waiting on a connection that answers, however long the command takes", async () => {
    const { c, s } = live();
    let settled: unknown = "pending";
    c.command("create_thread", {}).then((v) => (settled = v), (e) => (settled = e));
    const id = commandFrames(s)[0].commandId;

    // A server that answers every ping, for three command deadlines.
    let answered = 0;
    for (let t = 0; t < 45_000; t += 1_000) {
      await vi.advanceTimersByTimeAsync(1_000);
      while (answered < pings(s)) {
        s.reply({ type: "pong" });
        answered++;
      }
    }
    expect(FakeSocket.all).toHaveLength(1);
    expect(settled).toBe("pending");
    s.reply({ type: "ack", commandId: id, result: { threadId: "t" } });
    await vi.advanceTimersByTimeAsync(0);
    expect(settled).toEqual({ threadId: "t" });
  });

  it("fails, and reconnects, when the deadline's probe goes unanswered", async () => {
    const { c } = live();
    const caught = c.command("cancel", {}).catch((e) => e);

    await vi.advanceTimersByTimeAsync(15_000 + 3_000);
    expect(await caught).toBeInstanceOf(CommandTimeout);
    expect(FakeSocket.all).toHaveLength(2);
    // Failed means given up: the replacement socket does not resend it.
    latest().open();
    expect(commandFrames(latest())).toEqual([]);
  });

  it("probes a quiet socket on send, and resends on the replacement with the same id", async () => {
    const { c, s } = live();
    await vi.advanceTimersByTimeAsync(10_000); // quiet since

    let settled: unknown = "pending";
    c.command("prompt", { text: "hi" }).then((v) => (settled = v));
    expect(pings(s)).toBe(1);
    const id = commandFrames(s)[0].commandId;

    await vi.advanceTimersByTimeAsync(3_000);
    const next = latest();
    expect(next).not.toBe(s);
    next.open();
    expect(commandFrames(next).map((f) => f.commandId)).toEqual([id]);
    next.reply({ type: "ack", commandId: id, result: "ok" });
    await vi.advanceTimersByTimeAsync(0);
    expect(settled).toBe("ok");
  });
});

describe("snapshot fetch", () => {
  const attaches = () => latest().sent.filter((f) => f.type === "attach");

  it("falls back to attaching over the socket when the fetch stalls", async () => {
    stallFetch();
    const { c } = live();
    c.attach("t");

    await vi.advanceTimersByTimeAsync(9_999);
    expect(attaches()).toEqual([]);
    await vi.advanceTimersByTimeAsync(1);
    expect(attaches()).toEqual([{ type: "attach", threadId: "t" }]);
  });

  it("does nothing for a fetch superseded by another attach", async () => {
    stallFetch();
    const { c } = live();
    c.attach("a");
    c.attach("b");
    await vi.advanceTimersByTimeAsync(10_000);
    expect(attaches()).toEqual([{ type: "attach", threadId: "b" }]);
  });
});
