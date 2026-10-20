// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { useThreadOrder } from "./useThreadOrder";
import type { Wire } from "./useWire";

const thread = (id: string, position: number) => ({ id, position, createdAt: 0 }) as ThreadMeta;

// The pending lifecycle is useThreadOverlay's, and tested there.
describe("useThreadOrder", () => {
  it("moves the row at once", () => {
    const client = { command: vi.fn(() => new Promise(() => {})) };
    const threads = [thread("a", 0), thread("b", 1), thread("c", 2)];
    const wire = { clientRef: { current: client }, threads } as unknown as Wire;
    const hook = renderHook(() => useThreadOrder(wire, threads));
    const order = () => hook.result.current.threads.map((s) => s.id).join("");
    expect(order()).toBe("abc");

    act(() => hook.result.current.move("c", -1));
    expect(order()).toBe("cab");
    act(() => hook.result.current.move("c", 0.5));
    expect(order()).toBe("acb");
    expect(client.command).toHaveBeenCalledWith("set_thread_position", {
      threadId: "c",
      position: -1,
    });
  });
});
