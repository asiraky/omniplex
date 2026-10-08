// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { useRenameThread } from "./useRenameThread";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

const thread = (id: string, title: string) => ({ id, title }) as ThreadMeta;

/** A server that answers each command only when the test says so. */
function setup() {
  const calls: { title: string; resolve: () => void; reject: (e: Error) => void }[] = [];
  const client = {
    command: vi.fn(
      (_: string, args: { title: string }) =>
        new Promise<void>((resolve, reject) => calls.push({ title: args.title, resolve, reject })),
    ),
  };
  const hook = renderHook(() => {
    const [threads, setThreads] = useState([thread("a", "Old"), thread("b", "Other")]);
    const wire = { clientRef: { current: client }, threads } as unknown as Wire;
    return { ...useRenameThread(wire), setThreads };
  });
  /** The server's threads broadcast, carrying `a` under this name. */
  const broadcast = (title: string) =>
    act(() => hook.result.current.setThreads([thread("a", title), thread("b", "Other")]));
  const titleOfA = () => hook.result.current.threads.find((s) => s.id === "a")?.title;
  return { hook, calls, broadcast, titleOfA };
}

describe("useRenameThread", () => {
  it("shows the new name before the server answers, over a stale broadcast", () => {
    const { hook, calls, broadcast, titleOfA } = setup();
    act(() => hook.result.current.rename("a", "New"));
    expect(titleOfA()).toBe("New");
    // A list the server sent before it saw the rename.
    broadcast("Old");
    expect(titleOfA()).toBe("New");
    expect(hook.result.current.threads.find((s) => s.id === "b")?.title).toBe("Other");
    expect(calls.map((c) => c.title)).toEqual(["New"]);
  });

  it("shows what the server holds once a rename fails", async () => {
    const { hook, calls, titleOfA } = setup();
    act(() => hook.result.current.rename("a", "New"));
    await act(async () => calls[0].reject(new Error("offline")));
    expect(titleOfA()).toBe("Old");
  });

  it("lets the server's list speak again once it has answered", async () => {
    const { hook, calls, broadcast, titleOfA } = setup();
    act(() => hook.result.current.rename("a", "New"));
    await act(async () => calls[0].resolve());
    // Another device renames it afterwards.
    broadcast("Elsewhere");
    expect(titleOfA()).toBe("Elsewhere");
  });

  it("sends one rename per thread at a time, and only the latest waiting one", async () => {
    const { hook, calls, broadcast, titleOfA } = setup();
    act(() => hook.result.current.rename("a", "First"));
    act(() => hook.result.current.rename("a", "Second"));
    act(() => hook.result.current.rename("a", "Third"));
    expect(calls.map((c) => c.title)).toEqual(["First"]);
    expect(titleOfA()).toBe("Third");

    await act(async () => calls[0].resolve());
    expect(calls.map((c) => c.title)).toEqual(["First", "Third"]);
    // The server's confirmation of the first is not the name the user wants.
    broadcast("First");
    expect(titleOfA()).toBe("Third");

    broadcast("Third");
    await act(async () => calls[1].resolve());
    expect(titleOfA()).toBe("Third");
  });
});
