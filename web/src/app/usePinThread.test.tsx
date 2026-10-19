// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import { usePinThread } from "./usePinThread";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

const thread = (id: string, pinned?: boolean) => ({ id, pinned }) as ThreadMeta;

/** A server that answers each command only when the test says so. */
function setup(connected: null | "connected" = "connected") {
  const calls: { pinned: boolean; resolve: () => void; reject: (e: Error) => void }[] = [];
  const client = {
    command: vi.fn(
      (_: string, args: { pinned: boolean }) =>
        new Promise<void>((resolve, reject) => calls.push({ pinned: args.pinned, resolve, reject })),
    ),
  };
  const hook = renderHook(() => {
    const [threads, setThreads] = useState([thread("a"), thread("b")]);
    const wire = { clientRef: { current: connected && client }, threads } as unknown as Wire;
    return { ...usePinThread(wire, threads), setThreads };
  });
  /** The server's threads broadcast, carrying `b` with this flag. */
  const broadcast = (pinned?: boolean) =>
    act(() => hook.result.current.setThreads([thread("a"), thread("b", pinned)]));
  const pinned = () =>
    hook.result.current.threads
      .filter((s) => s.pinned)
      .map((s) => s.id)
      .join("");
  return { hook, calls, broadcast, pinned };
}

describe("usePinThread", () => {
  it("pins at once, and holds it over a stale broadcast", () => {
    const { hook, calls, broadcast, pinned } = setup();
    act(() => hook.result.current.setPinned("b", true));
    expect(pinned()).toBe("b");
    broadcast(undefined);
    expect(pinned()).toBe("b");
    expect(calls.map((c) => c.pinned)).toEqual([true]);
  });

  it("puts the flag back where the server has it once the command fails", async () => {
    const { hook, calls, pinned } = setup();
    act(() => hook.result.current.setPinned("b", true));
    await act(async () => calls[0].reject(new Error("offline")));
    expect(pinned()).toBe("");
    expect(toast.error).toHaveBeenCalledWith("Could not pin that thread", expect.anything());
  });

  it("reverts when there is no connection to send it on", async () => {
    const { hook, pinned } = setup(null);
    act(() => hook.result.current.setPinned("b", true));
    await waitFor(() => expect(pinned()).toBe(""));
  });

  it("lets the server's list speak again once it has answered", async () => {
    const { hook, calls, broadcast, pinned } = setup();
    act(() => hook.result.current.setPinned("b", true));
    await act(async () => calls[0].resolve());
    // The answer can beat the list that carries the flag.
    expect(pinned()).toBe("b");
    broadcast(true);
    expect(pinned()).toBe("b");
    // Another device unpins it afterwards.
    broadcast(undefined);
    expect(pinned()).toBe("");
  });

  it("sends one toggle per thread at a time, and only the latest waiting one", async () => {
    const { hook, calls, pinned } = setup();
    act(() => hook.result.current.setPinned("b", true));
    act(() => hook.result.current.setPinned("b", false));
    act(() => hook.result.current.setPinned("b", true));
    act(() => hook.result.current.setPinned("b", false));
    expect(calls.map((c) => c.pinned)).toEqual([true]);
    expect(pinned()).toBe("");

    await act(async () => calls[0].resolve());
    expect(calls.map((c) => c.pinned)).toEqual([true, false]);
    expect(pinned()).toBe("");
  });
});
