// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { useRenameThread } from "./useRenameThread";
import type { Wire } from "./useWire";

const thread = (id: string, title: string) => ({ id, title }) as ThreadMeta;

// The pending lifecycle is useThreadOverlay's, and tested there.
describe("useRenameThread", () => {
  it("lays the new name over the wire's list, leaving other threads alone", () => {
    const client = { command: vi.fn(() => new Promise(() => {})) };
    const threads = [thread("a", "Old"), thread("b", "Other")];
    const wire = { clientRef: { current: client }, threads } as unknown as Wire;
    const hook = renderHook(() => useRenameThread(wire));
    expect(hook.result.current.threads).toBe(threads);

    act(() => hook.result.current.rename("a", "New"));
    expect(hook.result.current.threads.map((s) => s.title)).toEqual(["New", "Other"]);
    expect(hook.result.current.threads[1]).toBe(threads[1]);
    expect(client.command).toHaveBeenCalledWith("rename_thread", { threadId: "a", title: "New" });
  });
});
