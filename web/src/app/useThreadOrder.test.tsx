// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import { useThreadOrder } from "./useThreadOrder";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

const thread = (id: string, position: number) => ({ id, position, createdAt: 0 }) as ThreadMeta;

/** A server that answers each command only when the test says so. */
function setup(connected: null | "connected" = "connected") {
  const calls: { position: number; resolve: () => void; reject: (e: Error) => void }[] = [];
  const client = {
    command: vi.fn(
      (_: string, args: { position: number }) =>
        new Promise<void>((resolve, reject) =>
          calls.push({ position: args.position, resolve, reject }),
        ),
    ),
  };
  const hook = renderHook(() => {
    const [threads, setThreads] = useState([thread("a", 0), thread("b", 1), thread("c", 2)]);
    const wire = { clientRef: { current: connected && client }, threads } as unknown as Wire;
    return { ...useThreadOrder(wire, threads), setThreads };
  });
  /** The server's threads broadcast, carrying `c` at this position. */
  const broadcast = (c: number) =>
    act(() => hook.result.current.setThreads([thread("a", 0), thread("b", 1), thread("c", c)]));
  const order = () => hook.result.current.threads.map((s) => s.id).join("");
  return { hook, calls, broadcast, order };
}

describe("useThreadOrder", () => {
  it("moves the row at once, and holds it over a stale broadcast", () => {
    const { hook, calls, broadcast, order } = setup();
    act(() => hook.result.current.move("c", -1));
    expect(order()).toBe("cab");
    // A list the server sent before it saw the move.
    broadcast(2);
    expect(order()).toBe("cab");
    expect(calls.map((c) => c.position)).toEqual([-1]);
  });

  it("puts the row back where the server has it once the move fails", async () => {
    const { hook, calls, order } = setup();
    act(() => hook.result.current.move("c", -1));
    await act(async () => calls[0].reject(new Error("offline")));
    expect(order()).toBe("abc");
    expect(toast.error).toHaveBeenCalled();
  });

  it("says so when there is no connection to send it on", async () => {
    const { hook, order } = setup(null);
    act(() => hook.result.current.move("c", -1));
    await waitFor(() => expect(order()).toBe("abc"));
  });

  it("lets the server's list speak again once it has answered", async () => {
    const { hook, calls, broadcast, order } = setup();
    act(() => hook.result.current.move("c", -1));
    await act(async () => calls[0].resolve());
    // The answer can beat the list that carries the move.
    expect(order()).toBe("cab");
    broadcast(-1);
    expect(order()).toBe("cab");
    // Another device moves it afterwards.
    broadcast(0.5);
    expect(order()).toBe("acb");
  });

  it("sends one move per thread at a time, and only the latest waiting one", async () => {
    const { hook, calls, broadcast, order } = setup();
    act(() => hook.result.current.move("c", -1));
    act(() => hook.result.current.move("c", 3));
    act(() => hook.result.current.move("c", 0.5));
    expect(calls.map((c) => c.position)).toEqual([-1]);
    expect(order()).toBe("acb");

    await act(async () => calls[0].resolve());
    expect(calls.map((c) => c.position)).toEqual([-1, 0.5]);
    // The server's confirmation of the first is not where the user put it.
    broadcast(-1);
    expect(order()).toBe("acb");

    broadcast(0.5);
    await act(async () => calls[1].resolve());
    expect(order()).toBe("acb");
  });
});
