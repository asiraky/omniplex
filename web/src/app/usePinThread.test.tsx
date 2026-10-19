// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { toast } from "~/lib/toast";
import type { ThreadMeta } from "~/protocol";

import { usePinThread } from "./usePinThread";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

const thread = (id: string, pinned?: boolean) => ({ id, pinned }) as ThreadMeta;

/** A server that refuses each command when the test says so. */
function setup(threads: ThreadMeta[]) {
  const calls: { pinned: boolean; reject: (e: Error) => void }[] = [];
  const client = {
    command: vi.fn(
      (_: string, args: { pinned: boolean }) =>
        new Promise<void>((_resolve, reject) => calls.push({ pinned: args.pinned, reject })),
    ),
  };
  const wire = { clientRef: { current: client }, threads } as unknown as Wire;
  const hook = renderHook(() => usePinThread(wire, threads));
  const pinned = () =>
    hook.result.current.threads
      .filter((s) => s.pinned)
      .map((s) => s.id)
      .join("");
  return { hook, calls, pinned };
}

// The pending lifecycle is useThreadOverlay's, and tested there.
describe("usePinThread", () => {
  it("pins and unpins at once", () => {
    const { hook, pinned } = setup([thread("a", true), thread("b")]);
    act(() => hook.result.current.setPinned("b", true));
    expect(pinned()).toBe("ab");
    act(() => hook.result.current.setPinned("a", false));
    expect(pinned()).toBe("b");
  });

  it("names which way it failed", async () => {
    const { hook, calls } = setup([thread("a", true), thread("b")]);
    act(() => hook.result.current.setPinned("b", true));
    act(() => hook.result.current.setPinned("a", false));
    await act(async () => calls[0].reject(new Error("offline")));
    expect(toast.error).toHaveBeenLastCalledWith("Could not pin that thread", expect.anything());
    await act(async () => calls[1].reject(new Error("offline")));
    expect(toast.error).toHaveBeenLastCalledWith("Could not unpin that thread", expect.anything());
  });
});
