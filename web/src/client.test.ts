// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { emptyState } from "./apply";
import { Client, type ClientEvents } from "./client";
import type { Job, ThreadState } from "./protocol";

// A socket the test opens and closes by hand, recording what the client sends.
class FakeSocket {
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
const presence = (s: FakeSocket) => s.sent.filter((f) => f.type === "presence");

beforeEach(() => {
  FakeSocket.all = [];
  vi.useFakeTimers();
  vi.stubGlobal("WebSocket", FakeSocket);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("presence", () => {
  it("sends on change, and not when nothing changed", () => {
    const client = new Client("ws://test", events);
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
    const client = new Client("ws://test", events);
    client.connect();
    latest().open();
    expect(presence(latest())).toEqual([]);
  });

  it("repeats the latest report after hello and attach on every new connection", () => {
    const client = new Client("ws://test", events);
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

  it("resolves null and publishes nothing when the fetch fails", async () => {
    const { client, published } = attached({ jobsBefore: 2 });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 500 })));
    vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(await client.loadAllJobs()).toBeNull();
    expect(published).toHaveLength(0);
  });
});
