// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { useRenameThread } from "./useRenameThread";
import type { Wire } from "./useWire";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

const thread = (id: string, title: string) => ({ id, title }) as ThreadMeta;

function setup(command: () => Promise<unknown>) {
  const client = { command: vi.fn(command) };
  const hook = renderHook(() => {
    const [threads, setThreads] = useState([thread("a", "Old"), thread("b", "Other")]);
    const wire = { clientRef: { current: client }, threads, setThreads } as unknown as Wire;
    return { rename: useRenameThread(wire), threads, setThreads };
  });
  return { hook, client };
}

const titleOf = (threads: ThreadMeta[], id: string) => threads.find((s) => s.id === id)?.title;

describe("useRenameThread", () => {
  it("shows the new name before the server answers", () => {
    const { hook, client } = setup(() => new Promise(() => {}));
    act(() => hook.result.current.rename("a", "New"));
    expect(titleOf(hook.result.current.threads, "a")).toBe("New");
    expect(titleOf(hook.result.current.threads, "b")).toBe("Other");
    expect(client.command).toHaveBeenCalledWith("rename_thread", { threadId: "a", title: "New" });
  });

  it("puts the old name back when the rename fails", async () => {
    const { hook } = setup(() => Promise.reject(new Error("offline")));
    act(() => hook.result.current.rename("a", "New"));
    await waitFor(() => expect(titleOf(hook.result.current.threads, "a")).toBe("Old"));
  });

  it("leaves a newer name alone when an older rename fails", async () => {
    let fail: (e: Error) => void = () => {};
    const { hook } = setup(() => new Promise((_, reject) => (fail = reject)));
    act(() => hook.result.current.rename("a", "New"));
    // Another device's rename lands through the threads broadcast first.
    act(() =>
      hook.result.current.setThreads((l) =>
        l.map((s) => (s.id === "a" ? { ...s, title: "Newer" } : s)),
      ),
    );
    await act(async () => fail(new Error("offline")));
    expect(titleOf(hook.result.current.threads, "a")).toBe("Newer");
  });
});
