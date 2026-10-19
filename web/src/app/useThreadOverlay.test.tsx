// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import { useThreadOverlay, type OverlayCommand } from "./useThreadOverlay";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

const thread = (id: string, title: string) => ({ id, title }) as ThreadMeta;

const COMMAND: OverlayCommand<string> = {
  send: (client, threadId, value) => client.command("test", { threadId, value }),
  errorTitle: (value) => `Could not set ${value}`,
};

/** A server that answers each command only when the test says so. */
function setup(connected: null | "connected" = "connected") {
  const calls: { value: string; resolve: () => void; reject: (e: Error) => void }[] = [];
  const client = {
    command: vi.fn(
      (_: string, args: { value: string }) =>
        new Promise<void>((resolve, reject) => calls.push({ value: args.value, resolve, reject })),
    ),
  };
  const hook = renderHook(() => {
    const [threads, setThreads] = useState([thread("a", "Old"), thread("b", "Other")]);
    const wire = { clientRef: { current: connected && client }, threads } as unknown as Wire;
    return { ...useThreadOverlay(wire, COMMAND), setThreads };
  });
  /** A threads broadcast from the server: a new list, whatever it carries. */
  const broadcast = () =>
    act(() => hook.result.current.setThreads([thread("a", "Old"), thread("b", "Other")]));
  const liveA = () => hook.result.current.live.get("a");
  return { hook, calls, broadcast, liveA };
}

describe("useThreadOverlay", () => {
  it("shows the value before the server answers, over a stale broadcast", () => {
    const { hook, calls, broadcast, liveA } = setup();
    act(() => hook.result.current.set("a", "New"));
    expect(liveA()).toBe("New");
    // A list the server sent before it saw the command.
    broadcast();
    expect(liveA()).toBe("New");
    expect(hook.result.current.live.has("b")).toBe(false);
    expect(calls.map((c) => c.value)).toEqual(["New"]);
  });

  it("drops the value and says so once the server refuses it", async () => {
    const { hook, calls, liveA } = setup();
    act(() => hook.result.current.set("a", "New"));
    await act(async () => calls[0].reject(new Error("offline")));
    expect(liveA()).toBeUndefined();
    expect(toast.error).toHaveBeenCalledWith("Could not set New", { description: "offline" });
  });

  it("drops the value when there is no connection to send it on", async () => {
    const { hook, liveA } = setup(null);
    act(() => hook.result.current.set("a", "New"));
    await waitFor(() => expect(liveA()).toBeUndefined());
  });

  it("lets the server's list speak again once it has answered", async () => {
    const { hook, calls, broadcast, liveA } = setup();
    act(() => hook.result.current.set("a", "New"));
    await act(async () => calls[0].resolve());
    // The answer can beat the list that carries the value.
    expect(liveA()).toBe("New");
    broadcast();
    expect(liveA()).toBeUndefined();
  });

  it("sends one command per thread at a time, and only the latest waiting one", async () => {
    const { hook, calls, broadcast, liveA } = setup();
    act(() => hook.result.current.set("a", "First"));
    act(() => hook.result.current.set("a", "Second"));
    act(() => hook.result.current.set("a", "Third"));
    expect(calls.map((c) => c.value)).toEqual(["First"]);
    expect(liveA()).toBe("Third");

    await act(async () => calls[0].resolve());
    expect(calls.map((c) => c.value)).toEqual(["First", "Third"]);
    // The server's confirmation of the first is not the value the user wants.
    broadcast();
    expect(liveA()).toBe("Third");

    await act(async () => calls[1].resolve());
    expect(liveA()).toBe("Third");
    broadcast();
    expect(liveA()).toBeUndefined();
  });
});
