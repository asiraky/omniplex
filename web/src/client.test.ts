// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Client, type ClientEvents } from "./client";
import type { ThreadState } from "./protocol";

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
