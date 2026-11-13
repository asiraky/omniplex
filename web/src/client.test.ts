// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { emptyState } from "./apply";
import { Client, CommandTimeout, type ClientEvents, type ConnectionStatus } from "./client";
import type { Event, Job, ThreadState, TurnStatus } from "./protocol";

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
  onTurnStatus() {},
  onAccess() {},
  onLiveEvent() {},
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

  // A fetch the test answers by hand.
  function heldFetch() {
    let answer!: (state: ThreadState | null) => void;
    const fetch = vi.fn(
      (_url: string, init: { signal: AbortSignal }) =>
        new Promise<Response>((resolve, reject) => {
          init.signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
          answer = (state) =>
            resolve(state ? new Response(JSON.stringify(state)) : new Response("", { status: 404 }));
        }),
    );
    vi.stubGlobal("fetch", fetch);
    vi.spyOn(console, "warn").mockImplementation(() => {});
    return { fetch, answer: (state: ThreadState | null) => answer(state) };
  }
  const booted = (on: Partial<ClientEvents> = {}) => {
    const c = new Client("ws://test", { ...events, ...on });
    clients.push(c);
    return c;
  };

  it("starts before the socket, and attaches from it once the socket opens", async () => {
    const { fetch, answer } = heldFetch();
    const published: ThreadState[] = [];
    const c = booted({ onState: (_id, s) => published.push(s) });
    c.attach("t");
    c.connect();
    expect(fetch).toHaveBeenCalledWith("/api/threads/t", expect.anything());

    answer({ ...emptyState("t"), seq: 7 });
    await vi.advanceTimersByTimeAsync(0);
    expect(published.map((s) => s.seq)).toEqual([7]);

    latest().open();
    expect(attaches()).toEqual([{ type: "attach", threadId: "t", afterSeq: 7 }]);
  });

  it("holds the socket's attach until a slower fetch lands", async () => {
    const { answer } = heldFetch();
    const c = booted();
    c.attach("t");
    c.connect();
    latest().open();
    expect(attaches()).toEqual([]);

    answer({ ...emptyState("t"), seq: 7 });
    await vi.advanceTimersByTimeAsync(0);
    expect(attaches()).toEqual([{ type: "attach", threadId: "t", afterSeq: 7 }]);
  });

  it("asks the socket for the whole thread when the fetch fails before it opens", async () => {
    const { answer } = heldFetch();
    const c = booted();
    c.attach("gone");
    c.connect();
    answer(null);
    await vi.advanceTimersByTimeAsync(0);

    latest().open();
    expect(attaches()).toEqual([{ type: "attach", threadId: "gone", afterSeq: 0 }]);
  });

  it("publishes nothing once closed", async () => {
    const { answer } = heldFetch();
    const published: ThreadState[] = [];
    const c = booted({ onState: (_id, s) => published.push(s) });
    c.attach("t");
    c.connect();
    c.close();
    answer({ ...emptyState("t"), seq: 7 });
    await vi.advanceTimersByTimeAsync(0);
    expect(published).toEqual([]);
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

// A primed client on an open socket, recording every state it publishes.
function attached(state: Partial<ThreadState> = {}) {
  const published: ThreadState[] = [];
  const client = new Client("ws://test", { ...events, onState: (_id, s) => published.push(s) });
  client.prime({ ...emptyState("a"), ...state });
  client.connect();
  latest().open();
  const frame = (f: Record<string, unknown>) => latest().onmessage!({ data: JSON.stringify(f) });
  return { client, published, frame };
}

const job = (id: string, over: Partial<Job> = {}): Job => ({
  id,
  depth: 0,
  kind: "agent",
  status: "completed",
  usage: {},
  ...over,
});

const chunk = (seq: number, delta: string) => ({
  threadId: "a",
  seq,
  timestamp: 0,
  type: "message.chunk",
  payload: { blockId: "b1", role: "agent", kind: "text", turnId: "t1", delta },
});

function serve(body: unknown) {
  const fetch = vi.fn(async (_input: RequestInfo | URL) => new Response(JSON.stringify(body)));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

describe("reconnect", () => {
  it("applies a replay's events without publishing, then publishes once on synchronized", () => {
    const { published, frame } = attached({ seq: 3 });
    frame({ type: "replay", threadId: "a", seq: 6, events: [chunk(4, "a"), chunk(5, "b"), chunk(6, "c")] });
    expect(published).toHaveLength(0);

    frame({ type: "synchronized", threadId: "a", seq: 6 });
    expect(published).toHaveLength(1);
    expect(published[0].seq).toBe(6);
    expect(published[0].items[0].text).toBe("abc");

    // The cursor moved with the replay: the next attach asks after it.
    latest().close();
    vi.runOnlyPendingTimers();
    latest().open();
    expect(latest().sent.find((f) => f.type === "attach")).toMatchObject({ afterSeq: 6 });
  });

  it("holds a snapshot until synchronized", () => {
    const { published, frame } = attached({ seq: 3 });
    frame({ type: "snapshot", threadId: "a", seq: 9, state: { ...emptyState("a"), seq: 9, title: "fresh" } });
    expect(published).toHaveLength(0);
    frame({ type: "synchronized", threadId: "a", seq: 9 });
    expect(published.map((s) => s.title)).toEqual(["fresh"]);
  });

  it("still publishes each live event as it lands", () => {
    const { published, frame } = attached({ seq: 3 });
    frame({ type: "event", threadId: "a", event: chunk(4, "a") });
    frame({ type: "event", threadId: "a", event: chunk(5, "b") });
    expect(published.map((s) => s.seq)).toEqual([4, 5]);
  });
});

describe("loading older items", () => {
  it("merges the page's jobs, keeping the client's copy of one it already holds", async () => {
    const { client, published } = attached({
      itemsBefore: 5,
      jobs: [job("j2", { status: "running", activity: "live" })],
      jobsBefore: 3,
    });
    serve({
      items: [{ id: "c1", kind: "tool" }],
      itemsBefore: 0,
      jobs: [job("j1"), job("j2", { activity: "stale" })],
    });
    const next = await client.loadOlder();
    expect(next?.jobs.map((j) => [j.id, j.activity])).toEqual([
      ["j1", undefined],
      ["j2", "live"],
    ]);
    expect(next?.jobsBefore).toBe(2);
    expect(published).toHaveLength(1);
  });
});

describe("loading all jobs", () => {
  it("takes the server's list, prefers the client's copies, keeps jobs it did not list", async () => {
    const { client, published } = attached({
      jobs: [job("j2", { status: "running", activity: "live" }), job("j9", { status: "running" })],
      jobsBefore: 2,
    });
    const fetch = serve({ jobs: [job("j1"), job("j2", { activity: "stale" }), job("j3")] });
    const next = await client.loadAllJobs();
    expect(String(fetch.mock.calls[0][0])).toBe("/api/threads/a/jobs");
    expect(next?.jobs.map((j) => [j.id, j.activity])).toEqual([
      ["j1", undefined],
      ["j2", "live"],
      ["j3", undefined],
      ["j9", undefined],
    ]);
    expect(next?.jobsBefore).toBe(0);
    expect(published).toEqual([next]);
  });

  it("fetches the list when a job it does not hold comes back to life", async () => {
    const { frame, published } = attached({ jobs: [job("j9", { status: "running" })], jobsBefore: 2 });
    const fetch = serve({ jobs: [job("old", { status: "running", name: "reopened" }), job("j9", { status: "running" })] });
    const row = (seq: number, payload: Record<string, unknown>) => ({ threadId: "a", seq, timestamp: 0, type: "job.updated", payload });
    // Activity alone, or a finish, is history the list can wait for.
    frame({ type: "event", threadId: "a", event: row(1, { jobId: "old", activity: "Read" }) });
    frame({ type: "event", threadId: "a", event: row(2, { jobId: "old", status: "completed" }) });
    expect(fetch).not.toHaveBeenCalled();

    frame({ type: "event", threadId: "a", event: row(3, { jobId: "old", status: "running" }) });
    await vi.waitFor(() => expect(published.at(-1)?.jobs.map((j) => j.name)).toContain("reopened"));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("keeps a row for an unheld job that lands while the list is on its way", async () => {
    const { frame, published } = attached({ jobs: [], jobsBefore: 2 });
    let answer!: (r: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => (answer = resolve))));
    const row = (seq: number, payload: Record<string, unknown>) => ({ threadId: "a", seq, timestamp: 0, type: "job.updated", payload });
    frame({ type: "event", threadId: "a", event: row(1, { jobId: "old", status: "running" }) });
    // After the server read the list, before its answer arrives.
    frame({ type: "event", threadId: "a", event: row(2, { jobId: "old", status: "completed" }) });

    answer(new Response(JSON.stringify({ jobs: [job("old", { status: "running", name: "reopened" })] })));
    await vi.waitFor(() => expect(published.at(-1)?.jobsBefore).toBe(0));
    expect(published.at(-1)?.jobs.map((j) => [j.name, j.status])).toEqual([["reopened", "completed"]]);
  });

  it("drops the fetch and its held rows when the thread is left", async () => {
    const { client, frame, published } = attached({ jobs: [], jobsBefore: 2 });
    let answer!: (r: Response) => void;
    const jobsList = new Promise<Response>((resolve) => (answer = resolve));
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).endsWith("/jobs") ? jobsList : new Response(JSON.stringify({ ...emptyState("b"), jobsBefore: 2 })),
      ),
    );
    const request = client.loadAllJobs();

    // Over to b, where a row for a job b does not hold lands, and straight
    // back to a from the cache, before a's list arrives.
    client.attach("b");
    await vi.waitFor(() => expect(latest().sent.at(-1)).toMatchObject({ type: "attach", threadId: "b" }));
    const row = { threadId: "b", seq: 1, timestamp: 0, type: "job.updated", payload: { jobId: "b-job", status: "running" } };
    frame({ type: "event", threadId: "b", event: row });
    client.attach("a");
    answer(new Response(JSON.stringify({ jobs: [job("a-job")] })));

    expect(await request).toBeNull();
    expect(published.filter((s) => s.threadId === "a").flatMap((s) => s.jobs.map((j) => j.id))).not.toContain("b-job");
  });

  it("resolves null and publishes nothing when the fetch fails", async () => {
    const { client, published } = attached({ jobsBefore: 2 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 500 })));
    vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(await client.loadAllJobs()).toBeNull();
    expect(published).toHaveLength(0);
  });
});

describe("thread cache", () => {
  const snapshot = (id: string, seq: number): ThreadState => ({ ...emptyState(id), seq });
  const event = (seq: number, title: string) =>
    ({ seq, type: "thread.config_changed", payload: { title } }) as unknown as Event;
  const deliver = (frame: Record<string, unknown>) => latest().onmessage?.({ data: JSON.stringify(frame) });
  const attaches = () => latest().sent.filter((f) => f.type === "attach");

  let fetchMock: ReturnType<typeof vi.fn>;
  let rendered: [string, ThreadState][];
  let client: Client;

  beforeEach(async () => {
    fetchMock = vi.fn(async (url: string) => {
      const id = decodeURIComponent(url.split("/").pop()!);
      return new Response(JSON.stringify(snapshot(id, 5)));
    });
    vi.stubGlobal("fetch", fetchMock);
    rendered = [];
    client = new Client("ws://test", { ...events, onState: (id, s) => rendered.push([id, s]) });
    client.connect();
    latest().open();
    // Visit a over HTTP, then move on to b, leaving a cached.
    client.attach("a");
    await vi.waitFor(() => expect(attaches()).toHaveLength(1));
    deliver({ type: "synchronized", threadId: "a", seq: 5 });
    client.attach("b");
    await vi.waitFor(() => expect(attaches()).toHaveLength(2));
    rendered = [];
    fetchMock.mockClear();
  });

  it("paints a recent thread from the cache and attaches after its seq", () => {
    client.attach("a");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(rendered).toEqual([["a", expect.objectContaining({ threadId: "a", seq: 5 })]]);
    expect(attaches().at(-1)).toEqual({ type: "attach", threadId: "a", afterSeq: 5 });
  });

  it("publishes a replayed catch-up once, then live events as they land", () => {
    client.attach("a");
    rendered = [];
    deliver({ type: "replay", threadId: "a", seq: 8, events: [event(6, "one"), event(7, "two"), event(8, "three")] });
    expect(rendered).toEqual([]);

    deliver({ type: "synchronized", threadId: "a", seq: 8 });
    expect(rendered).toHaveLength(1);
    expect(rendered[0][1]).toMatchObject({ seq: 8, title: "three" });

    deliver({ type: "event", threadId: "a", event: event(9, "live") });
    expect(rendered).toHaveLength(2);
    expect(rendered[1][1]).toMatchObject({ seq: 9, title: "live" });
  });

  it("replaces the cached state when the server answers with a snapshot", () => {
    client.attach("a");
    deliver({ type: "snapshot", threadId: "a", seq: 40, state: { ...snapshot("a", 40), title: "fresh" } });
    deliver({ type: "synchronized", threadId: "a", seq: 40 });
    expect(rendered.at(-1)![1]).toMatchObject({ seq: 40, title: "fresh" });

    // Leaving and coming back resumes from the snapshot, not the stale entry.
    client.attach("b");
    client.attach("a");
    expect(attaches().at(-1)).toEqual({ type: "attach", threadId: "a", afterSeq: 40 });
  });

  it("stops waiting on an attach that failed", () => {
    client.attach("a");
    deliver({ type: "error", threadId: "a", error: "gone" });
    // No replay is coming now, so a skipped seq is a loss to ask about again.
    deliver({ type: "event", threadId: "a", event: event(7, "after a hole") });
    expect(attaches().at(-1)).toEqual({ type: "attach", threadId: "a", afterSeq: 5 });
  });

  it("drops a straggler from an earlier attachment, and lets the replay bring what it skipped", () => {
    client.attach("a");
    const sent = attaches().length;
    // a's old live stream got as far as 7 before the detach; 6 was dropped
    // while we were on b.
    deliver({ type: "event", threadId: "a", event: event(7, "straggler") });
    expect(rendered.at(-1)![1]).toMatchObject({ seq: 5 });
    expect(attaches()).toHaveLength(sent); // no reattach: this one's replay is coming

    deliver({ type: "replay", threadId: "a", seq: 7, events: [event(6, "six"), event(7, "seven")] });
    deliver({ type: "synchronized", threadId: "a", seq: 7 });
    expect(rendered.at(-1)![1]).toMatchObject({ seq: 7, title: "seven" });
  });

  it("keeps waiting when an earlier attachment's marker arrives late", () => {
    // a → b → a before the server answered the first of those attaches.
    client.attach("a");
    client.attach("b");
    client.attach("a");
    const sent = attaches().length;
    deliver({ type: "synchronized", threadId: "a", seq: 5 }); // the first visit's
    // The second visit's replay is still coming, so this is a straggler.
    deliver({ type: "event", threadId: "a", event: event(7, "straggler") });
    expect(attaches()).toHaveLength(sent);

    deliver({ type: "replay", threadId: "a", seq: 7, events: [event(6, "one"), event(7, "two")] });
    deliver({ type: "synchronized", threadId: "a", seq: 7 });
    expect(rendered.at(-1)![1]).toMatchObject({ seq: 7, title: "two" });
  });

  it("reattaches from its cursor when the live stream skips a seq", () => {
    client.attach("a");
    deliver({ type: "synchronized", threadId: "a", seq: 5 });
    rendered = [];
    deliver({ type: "event", threadId: "a", event: event(7, "after a hole") });
    expect(rendered).toEqual([]);
    expect(attaches().at(-1)).toEqual({ type: "attach", threadId: "a", afterSeq: 5 });
  });
});

describe("turn status", () => {
  const RETRYING: TurnStatus = { state: "retrying", attempt: 2, max: 10, reason: "overloaded", retryAt: 1 };

  // A client attached to thread "a" over a live socket, recording every
  // status it reports. The recording starts after the attach, which clears.
  function watching() {
    const seen: [string, TurnStatus | null][] = [];
    const client = new Client("ws://test", {
      ...events,
      onTurnStatus: (id, s) => seen.push([id, s]),
    });
    client.prime({ ...emptyState("a"), seq: 3 });
    client.connect();
    latest().open();
    seen.length = 0;
    const send = (frame: Record<string, unknown>) =>
      latest().onmessage?.({ data: JSON.stringify(frame) });
    return { client, seen, send };
  }

  it("hands the attached thread's status straight through", () => {
    const { seen, send } = watching();
    send({ type: "turn_status", threadId: "a", turnStatus: RETRYING });
    send({ type: "turn_status", threadId: "a", turnStatus: { state: "compacting" } });
    expect(seen).toEqual([
      ["a", RETRYING],
      ["a", { state: "compacting" }],
    ]);
  });

  it("clears when the frame carries no status, or one with no state", () => {
    const { seen, send } = watching();
    send({ type: "turn_status", threadId: "a" });
    send({ type: "turn_status", threadId: "a", turnStatus: {} });
    expect(seen).toEqual([
      ["a", null],
      ["a", null],
    ]);
  });

  it("ignores a status for a thread it is not attached to", () => {
    const { seen, send } = watching();
    send({ type: "turn_status", threadId: "b", turnStatus: RETRYING });
    expect(seen).toEqual([]);
  });

  it("clears on reconnect, before the server re-sends the live one", () => {
    const { seen } = watching();
    latest().close();
    vi.runOnlyPendingTimers();
    latest().open();
    expect(seen).toEqual([["a", null]]);
    expect(latest().sent.map((f) => f.type)).toContain("attach");
  });

  it("clears on a resync, whose clear the dropped queue may have lost", () => {
    const { seen, send } = watching();
    send({ type: "turn_status", threadId: "a", turnStatus: RETRYING });
    send({ type: "resync", threadId: "a" });
    expect(seen).toEqual([
      ["a", RETRYING],
      ["a", null],
    ]);
    expect(latest().sent.at(-1)).toMatchObject({ type: "attach", threadId: "a" });
  });

  it("clears on a snapshot", () => {
    const { seen, send } = watching();
    send({ type: "snapshot", threadId: "a", state: { ...emptyState("a"), seq: 9 } });
    expect(seen).toEqual([["a", null]]);
  });

  it("clears when leaving for another thread", () => {
    vi.stubGlobal("fetch", () => new Promise(() => {}));
    const { client, seen, send } = watching();
    send({ type: "turn_status", threadId: "a", turnStatus: RETRYING });
    client.attach("b");
    expect(seen.at(-1)).toEqual(["a", null]);
  });

  it("clears when returning to a cached thread, before the server re-sends the live one", () => {
    vi.stubGlobal("fetch", () => new Promise(() => {}));
    const { client, seen } = watching();
    client.attach("b");
    seen.length = 0;
    client.attach("a"); // cached when we left it: no fetch, straight to attach
    expect(seen.at(-1)).toEqual(["a", null]);
    expect(latest().sent.at(-1)).toEqual({ type: "attach", threadId: "a", afterSeq: 3 });
  });

  // Status frames and events travel separately, so the next turn's retry can
  // overtake the last turn's finish. The server owns the clear; a late
  // turn.finished must not wipe a status that is newer than it.
  it("keeps a status that overtook the previous turn's finish", () => {
    const { seen, send } = watching();
    send({ type: "turn_status", threadId: "a", turnStatus: RETRYING });
    send({
      type: "event",
      threadId: "a",
      event: { threadId: "a", seq: 4, timestamp: 1, type: "turn.finished", payload: { turnId: "t1", status: "done" } },
    });
    expect(seen).toEqual([["a", RETRYING]]);
  });
});

describe("live events", () => {
  const notified = (seq: number): Event => ({
    threadId: "a",
    seq,
    timestamp: 0,
    type: "extension.notified",
    payload: { level: "info", message: `note ${seq}` },
  });
  const event = (seq: number) => latest().reply({ type: "event", threadId: "a", event: notified(seq) });
  const replay = (...seqs: number[]) =>
    latest().reply({ type: "replay", threadId: "a", seq: seqs.at(-1), events: seqs.map(notified) });

  // Attached to "a" at seq 3, with the socket up and the attach sent.
  const attached = () => {
    const onLiveEvent = vi.fn();
    const client = new Client("ws://test", { ...events, onLiveEvent });
    client.prime({ ...emptyState("a"), seq: 3 });
    client.connect();
    latest().open();
    return onLiveEvent;
  };
  const seqs = (fn: ReturnType<typeof vi.fn>) => fn.mock.calls.map(([, ev]) => (ev as Event).seq);

  it("holds back the catch-up replay and reports what follows synchronized", () => {
    const onLiveEvent = attached();
    replay(4);
    latest().reply({ type: "synchronized", threadId: "a", seq: 4 });
    expect(onLiveEvent).not.toHaveBeenCalled();

    event(5);
    expect(onLiveEvent).toHaveBeenCalledWith("a", notified(5));
  });

  it("does not report a straggler that lands while an attach is unanswered", () => {
    const onLiveEvent = attached();
    event(4);
    latest().reply({ type: "synchronized", threadId: "a", seq: 4 });
    event(5);
    expect(seqs(onLiveEvent)).toEqual([5]);
  });

  it("does not report a duplicate the reducer dropped", () => {
    const onLiveEvent = attached();
    latest().reply({ type: "synchronized", threadId: "a", seq: 3 });
    event(4);
    event(4);
    event(2);
    expect(seqs(onLiveEvent)).toEqual([4]);
  });

  it("treats the replay after a reconnect or a resync as catch-up again", () => {
    const onLiveEvent = attached();
    latest().reply({ type: "synchronized", threadId: "a", seq: 3 });

    latest().close();
    vi.runOnlyPendingTimers();
    latest().open();
    replay(4);
    latest().reply({ type: "synchronized", threadId: "a", seq: 4 });

    latest().reply({ type: "resync", threadId: "a" });
    replay(5);
    latest().reply({ type: "synchronized", threadId: "a", seq: 5 });
    event(6);

    expect(seqs(onLiveEvent)).toEqual([6]);
  });

  it("keeps catching up until the latest attach is answered, not an earlier one", () => {
    const onLiveEvent = attached();
    // A second attach goes out before the first is answered.
    latest().reply({ type: "resync", threadId: "a" });
    latest().reply({ type: "synchronized", threadId: "a", seq: 3 });
    event(4);
    expect(onLiveEvent).not.toHaveBeenCalled();

    latest().reply({ type: "synchronized", threadId: "a", seq: 4 });
    event(5);
    expect(seqs(onLiveEvent)).toEqual([5]);
  });

  it("counts a failed attach as answered", () => {
    const onLiveEvent = attached();
    latest().reply({ type: "resync", threadId: "a" });
    latest().reply({ type: "error", threadId: "a", error: "thread busy" });
    latest().reply({ type: "synchronized", threadId: "a", seq: 3 });
    event(4);
    expect(seqs(onLiveEvent)).toEqual([4]);
  });

  it("ignores another thread's synchronized", () => {
    const onLiveEvent = attached();
    latest().reply({ type: "synchronized", threadId: "b", seq: 9 });
    event(4);
    expect(onLiveEvent).not.toHaveBeenCalled();
  });
});
